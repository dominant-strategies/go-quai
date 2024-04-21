package peerManager

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.starlark.net/lib/proto"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/p2p/streamManager"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"

	"github.com/dominant-strategies/go-quai/p2p/peerManager/peerdb"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	basicConnGater "github.com/libp2p/go-libp2p/p2p/net/conngater"
	basicConnMgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"
)

const (
	// Represents the minimum ratio of positive to negative reports
	// 	e.g. live_reports / latent_reports = 0.8
	c_qualityThreshold = 0.8
)

const (
	// Peer DB positions in the peerDBs slice
	c_bestDBPos = iota
	c_responseiveDBPos
	c_lastResortDBPos

	// Dir names for the peerDBs
	c_bestDBName       = "bestPeersDB"
	c_responsiveDBName = "responsivePeersDB"
	c_lastResortDBName = "lastResortPeersDB"
)

// PeerManager is an interface that extends libp2p Connection Manager and Gater
type PeerManager interface {
	connmgr.ConnManager
	connmgr.ConnectionGater

	BlockAddr(ip net.IP) error
	BlockPeer(p peer.ID) error
	BlockSubnet(ipnet *net.IPNet) error
	ListBlockedAddrs() []net.IP
	ListBlockedPeers() []peer.ID
	ListBlockedSubnets() []*net.IPNet
	UnblockAddr(ip net.IP) error
	UnblockPeer(p peer.ID) error
	UnblockSubnet(ipnet *net.IPNet) error

	// Sets the ID for the node running the peer manager
	SetSelfID(p2p.PeerID)

	// Manages stream lifecycles
	streamManager.StreamManager

	// Removes a peer from all the quality buckets
	RemovePeer(p2p.PeerID) error
	// Returns an existing stream with that peer or opens a new one
	GetStream(p peer.ID) (network.Stream, error)

	// Returns c_recipientCount of the highest quality peers: lively & responsive
	GetBestPeersWithFallback(common.Location) []p2p.PeerID
	// Returns c_recipientCount responsive, but less lively peers
	GetResponsivePeersWithFallback(common.Location) []p2p.PeerID
	// Returns c_recipientCount peers regardless of status
	GetLastResortPeers(common.Location) []p2p.PeerID

	// Increases the peer's liveliness score
	MarkLivelyPeer(p2p.PeerID, common.Location)
	// Decreases the peer's liveliness score
	MarkLatentPeer(p2p.PeerID, common.Location)

	// Increases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkResponsivePeer(p2p.PeerID, common.Location)
	// Decreases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkUnresponsivePeer(p2p.PeerID, common.Location)

	// Protects the peer's connection from being disconnected
	ProtectPeer(p2p.PeerID)
	// Remove protection from the peer's connection
	UnprotectPeer(p2p.PeerID)
	// Bans the peer's connection from being re-established
	BanPeer(p2p.PeerID)

	// Stops the peer manager
	Stop() error
}

type BasicPeerManager struct {
	*basicConnGater.BasicConnectionGater
	*basicConnMgr.BasicConnMgr

	streamManager streamManager.StreamManager
	peerDBs       map[string][]*peerdb.PeerDB

	selfID p2p.PeerID

	ctx    context.Context
	cancel context.CancelFunc
	logger *log.Logger
}

func NewManager(ctx context.Context, low int, high int, datastore datastore.Datastore) (PeerManager, error) {
	mgr, err := basicConnMgr.NewConnManager(low, high)
	if err != nil {
		return nil, err
	}

	gater, err := basicConnGater.NewBasicConnectionGater(datastore)
	if err != nil {
		return nil, err
	}

	// Initialize the expected peerDBs
	peerDBs := make(map[string][]*peerdb.PeerDB)

	generateLocations := func() []common.Location {
		locations := make([]common.Location, 9)
		for region := byte(0); region <= 2; region++ {
			for zone := byte(0); zone <= 2; zone++ {
				locations = append(locations, common.Location{region, zone})
			}
		}
		return locations
	}

	for _, loc := range generateLocations() {
		domLocations := loc.GetDoms()
		for _, domLoc := range domLocations {
			domLocName := domLoc.Name()
			if peerDBs[domLocName] != nil {
				// This peerDB has already been initialized
				continue
			}
			peerDBs[domLocName] = make([]*peerdb.PeerDB, 3)
			peerDBs[domLocName][c_bestDBPos], err = peerdb.NewPeerDB(c_bestDBName, domLocName)
			if err != nil {
				return nil, err
			}
			peerDBs[domLocName][c_responseiveDBPos], err = peerdb.NewPeerDB(c_responsiveDBName, domLocName)
			if err != nil {
				return nil, err
			}
			peerDBs[domLocName][c_lastResortDBPos], err = peerdb.NewPeerDB(c_lastResortDBName, domLocName)
			if err != nil {
				return nil, err
			}
		}
	}

	streamManager, err := streamManager.NewStreamManager()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	logger := log.NewLogger("nodelogs/peers.log", "debug")

	go func() {
		q := query.Query{}
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Info("Reporting peer stats")
				for locName, location := range peerDBs {
					locPeer := []log.Fields{}
					for dbName, db := range location {
						results, err := db.Query(ctx, q)
						if err != nil {
							logger.Errorf("Error querying peerDB: %s", err)
							continue
						}
						for result := range results.Next() {
							peerID, err := peer.Decode(strings.TrimPrefix(result.Key, "/"))
							if err != nil {
								logger.Errorf("Error decoding peer ID: %s", err)
								continue
							}
							peerInfo := &peerdb.ProtoPeerInfo{}
							err = proto.Unmarshal(result.Value, peerInfo)
							if err != nil {
								logger.Errorf("Error unmarshaling peer info: %s", err)
								continue
							}
							locPeer = append(locPeer, log.Fields{
								"peerID": peerID,
								"info":   peerInfo.String(),
							})
						}
						logger.WithFields(log.Fields{
							"location":  locName,
							"peerCount": len(locPeer),
							"bucket":    dbName,
							"peers":     locPeer}).Info("Peer stats")
					}
				}
			}
		}
	}()

	return &BasicPeerManager{
		ctx:                  ctx,
		cancel:               cancel,
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		streamManager:        streamManager,
		peerDBs:              peerDBs,
		logger:               logger,
	}, nil
}

func (pm *BasicPeerManager) GetStream(peerID p2p.PeerID) (network.Stream, error) {
	return pm.streamManager.GetStream(peerID)
}

func (pm *BasicPeerManager) CloseStream(peerID p2p.PeerID) error {
	return pm.streamManager.CloseStream(peerID)
}

func (pm *BasicPeerManager) SetP2PBackend(p2pBackend quaiprotocol.QuaiP2PNode) {
	pm.streamManager.SetP2PBackend(p2pBackend)
}

func (pm *BasicPeerManager) RemovePeer(peerID p2p.PeerID) error {
	err := pm.removePeerFromAllDBs(peerID)
	if err != nil {
		return err
	}
	return pm.streamManager.CloseStream(peerID)
}

// Removes peer from the bucket it is in. Does not return an error if the peer is not found
func (pm *BasicPeerManager) removePeerFromAllDBs(peerID p2p.PeerID) error {
	for topic := range pm.peerDBs {
		err := pm.removePeerFromTopic(peerID, topic)
		if err != nil {
			return err
		}
	}
	return nil
}

// Removes peer from the bucket it is in. Does not return an error if the peer is not found
func (pm *BasicPeerManager) removePeerFromTopic(peerID p2p.PeerID, location string) error {
	key := datastore.NewKey(peerID.String())
	for _, db := range pm.peerDBs[location] {
		if exists, _ := db.Has(pm.ctx, key); exists {
			return db.Delete(pm.ctx, key)
		}
	}
	return nil
}

func (pm *BasicPeerManager) SetSelfID(selfID p2p.PeerID) {
	pm.selfID = selfID
}

func (pm *BasicPeerManager) getPeersHelper(peerDB *peerdb.PeerDB, numPeers int) []p2p.PeerID {
	peerSubset := make([]p2p.PeerID, 0, numPeers)
	q := query.Query{
		Limit: numPeers,
	}
	results, err := peerDB.Query(pm.ctx, q)
	if err != nil {
		return nil
	}

	for result := range results.Next() {
		peerID, err := peer.Decode(strings.TrimPrefix(result.Key, "/"))
		if err != nil {
			return nil
		}
		peerSubset = append(peerSubset, peerID)
	}

	return peerSubset
}

func (pm *BasicPeerManager) GetBestPeersWithFallback(location common.Location) []p2p.PeerID {
	locName := location.Name()
	if pm.peerDBs[locName] == nil {
		// There have not been any peers added to this topic
		return nil
	}

	bestPeersCount := pm.peerDBs[locName][c_bestDBPos].GetPeerCount()
	if bestPeersCount < streamManager.C_peerCount {
		bestPeerList := pm.getPeersHelper(pm.peerDBs[locName][c_bestDBPos], bestPeersCount)
		bestPeerList = append(bestPeerList, pm.GetResponsivePeersWithFallback(location)...)
		return bestPeerList
	}
	return pm.getPeersHelper(pm.peerDBs[locName][c_bestDBPos], streamManager.C_peerCount)
}

func (pm *BasicPeerManager) GetResponsivePeersWithFallback(location common.Location) []p2p.PeerID {
	locName := location.Name()

	responsivePeersCount := pm.peerDBs[locName][c_responseiveDBPos].GetPeerCount()
	if responsivePeersCount < streamManager.C_peerCount {
		responsivePeerList := pm.getPeersHelper(pm.peerDBs[locName][c_responseiveDBPos], responsivePeersCount)
		responsivePeerList = append(responsivePeerList, pm.GetLastResortPeers(location)...)

		return responsivePeerList
	}
	return pm.getPeersHelper(pm.peerDBs[locName][c_responseiveDBPos], streamManager.C_peerCount)

}

func (pm *BasicPeerManager) GetLastResortPeers(location common.Location) []p2p.PeerID {
	return pm.getPeersHelper(pm.peerDBs[location.Name()][c_lastResortDBPos], streamManager.C_peerCount)
}

func (pm *BasicPeerManager) MarkLivelyPeer(peer p2p.PeerID, location common.Location) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "liveness_reports", 1)
	pm.recategorizePeer(peer, location)
}

func (pm *BasicPeerManager) MarkLatentPeer(peer p2p.PeerID, location common.Location) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "latency_reports", 1)
	pm.recategorizePeer(peer, location)
}

func (pm *BasicPeerManager) calculatePeerLiveness(peer p2p.PeerID) float64 {
	peerTag := pm.GetTagInfo(peer)
	if peerTag == nil {
		return 0
	}

	liveness := peerTag.Tags["liveness_reports"]
	latents := peerTag.Tags["latency_reports"]
	return float64(liveness) / float64(latents)
}

func (pm *BasicPeerManager) MarkResponsivePeer(peer p2p.PeerID, location common.Location) {
	pm.TagPeer(peer, "responses_served", 1)
	pm.recategorizePeer(peer, location)
}

func (pm *BasicPeerManager) MarkUnresponsivePeer(peer p2p.PeerID, location common.Location) {
	pm.TagPeer(peer, "responses_missed", 1)
	pm.recategorizePeer(peer, location)
}

func (pm *BasicPeerManager) calculatePeerResponsiveness(peer p2p.PeerID) float64 {
	peerTag := pm.GetTagInfo(peer)
	if peerTag == nil {
		return 0
	}
	responses := peerTag.Tags["responses_served"]
	misses := peerTag.Tags["responses_missed"]
	return float64(responses) / float64(misses)
}

// Each peer can only be in one of the following buckets:
// 1. bestPeers
//   - peers with high liveness and responsiveness
//
// 2. responsivePeers
//   - peers with high responsiveness, but low liveness
//
// 3. peers
//   - all other peers
func (pm *BasicPeerManager) recategorizePeer(peer p2p.PeerID, location common.Location) error {
	liveness := pm.calculatePeerLiveness(peer)
	responsiveness := pm.calculatePeerResponsiveness(peer)

	// remove peer from DB first
	err := pm.removePeerFromTopic(peer, location.Name())
	if err != nil {
		return err
	}

	key := datastore.NewKey(peer.String())
	// TODO: construct peerDB.PeerInfo and marshal it to bytes
	peerInfo := []byte{}

	locationName := location.Name()
	if liveness >= c_qualityThreshold && responsiveness >= c_qualityThreshold {
		// Best peers: high liveness and responsiveness
		err := pm.peerDBs[locationName][c_bestDBPos].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in bestPeersDB")
		}

	} else if responsiveness >= c_qualityThreshold {
		// Responsive peers: high responsiveness, but low liveness
		err := pm.peerDBs[locationName][c_responseiveDBPos].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in responsivePeersDB")
		}

	} else {
		// All other peers
		err := pm.peerDBs[locationName][c_lastResortDBPos].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in allPeersDB")
		}
	}

	return nil
}

func (pm *BasicPeerManager) ProtectPeer(peer p2p.PeerID) {
	pm.Protect(peer, "gen_protection")
}

func (pm *BasicPeerManager) UnprotectPeer(peer p2p.PeerID) {
	pm.Unprotect(peer, "gen_protection")
}

func (pm *BasicPeerManager) BanPeer(peer p2p.PeerID) {
	pm.BlockPeer(peer)
}

func (pm *BasicPeerManager) Stop() error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var closeErrors []string

	closeFuncs := []func() error{
		pm.BasicConnMgr.Close,
	}

	wg.Add(len(closeFuncs))

	for _, closeFunc := range closeFuncs {
		go func(cf func() error) {
			defer wg.Done()
			if err := cf(); err != nil {
				mu.Lock()
				closeErrors = append(closeErrors, err.Error())
				mu.Unlock()
			}
		}(closeFunc)
	}

	for _, db := range pm.peerDBs {
		for _, peerDB := range db {
			wg.Add(1)
			go func(db *peerdb.PeerDB) {
				defer wg.Done()
				if err := db.Close(); err != nil {
					mu.Lock()
					closeErrors = append(closeErrors, err.Error())
					mu.Unlock()
				}
			}(peerDB)
		}
	}

	wg.Wait()

	if len(closeErrors) > 0 {
		return errors.New("failed to close some resources: " + strings.Join(closeErrors, "; "))
	}

	return nil
}
