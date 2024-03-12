package peerManager

import (
	"context"
	"net"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru"
	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"

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

	// The number of peers to return when querying for peers
	c_peerCount = 3
	// The amount of redundancy for open streams
	// c_peerCount * c_streamReplicationFactor = total number of open streams
	c_streamReplicationFactor = 3
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

var (
	errStreamNotFound = errors.New("stream not found")
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

	p2pBackend  quaiprotocol.QuaiP2PNode
	streamCache *lru.Cache

	selfID p2p.PeerID

	peerDBs map[string][]*peerdb.PeerDB

	ctx context.Context
}

func NewManager(ctx context.Context, low int, high int, datastore datastore.Datastore) (*BasicPeerManager, error) {
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
	for _, loc := range utils.GetRunningZones() {
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

	lruCache, err := lru.NewWithEvict(
		c_peerCount*c_streamReplicationFactor,
		severStream,
	)
	if err != nil {
		return nil, err
	}

	return &BasicPeerManager{
		ctx:                  ctx,
		streamCache:          lruCache,
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		peerDBs:              peerDBs,
	}, nil
}

func (pm *BasicPeerManager) RemovePeer(peerID p2p.PeerID) error {
	err := pm.removePeerFromAllDBs(peerID)
	if err != nil {
		return err
	}
	return pm.prunePeerConnection(peerID)
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

func (pm *BasicPeerManager) prunePeerConnection(peerID p2p.PeerID) error {
	stream, ok := pm.streamCache.Get(peerID)
	if ok {
		log.Global.WithField("peerID", peerID).Debug("Pruned connection with peer")
		severStream(peerID, stream)
		return nil
	}
	return errStreamNotFound
}

func severStream(key interface{}, value interface{}) {
	stream := value.(network.Stream)
	err := stream.Close()
	if err != nil {
		log.Global.WithField("err", err).Error("Failed to close stream")
	}
}

func (pm *BasicPeerManager) SetP2PBackend(host quaiprotocol.QuaiP2PNode) {
	pm.p2pBackend = host
}

func (pm *BasicPeerManager) GetStream(peerID p2p.PeerID) (network.Stream, error) {
	stream, ok := pm.streamCache.Get(peerID)
	var err error
	if !ok {
		// Create a new stream to the peer and register it in the cache
		stream, err = pm.p2pBackend.GetHostBackend().NewStream(pm.ctx, peerID, quaiprotocol.ProtocolVersion)
		if err != nil {
			// Explicitly return nil here to avoid casting a nil later
			return nil, err
		}
		pm.streamCache.Add(peerID, stream)
		go quaiprotocol.QuaiProtocolHandler(stream.(network.Stream), pm.p2pBackend)
		log.Global.Debug("Had to create new stream")
	} else {
		log.Global.Trace("Requested stream was found in cache")
	}

	return stream.(network.Stream), err
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
	if bestPeersCount < c_peerCount {
		bestPeerList := pm.getPeersHelper(pm.peerDBs[locName][c_bestDBPos], bestPeersCount)
		bestPeerList = append(bestPeerList, pm.GetResponsivePeersWithFallback(location)...)
		return bestPeerList
	}
	return pm.getPeersHelper(pm.peerDBs[locName][c_bestDBPos], c_peerCount)
}

func (pm *BasicPeerManager) GetResponsivePeersWithFallback(location common.Location) []p2p.PeerID {
	locName := location.Name()

	responsivePeersCount := pm.peerDBs[locName][c_responseiveDBPos].GetPeerCount()
	if responsivePeersCount < c_peerCount {
		responsivePeerList := pm.getPeersHelper(pm.peerDBs[locName][c_responseiveDBPos], responsivePeersCount)
		responsivePeerList = append(responsivePeerList, pm.GetLastResortPeers(location)...)

		return responsivePeerList
	}
	return pm.getPeersHelper(pm.peerDBs[locName][c_responseiveDBPos], c_peerCount)

}

func (pm *BasicPeerManager) GetLastResortPeers(location common.Location) []p2p.PeerID {
	return pm.getPeersHelper(pm.peerDBs[location.Name()][c_lastResortDBPos], c_peerCount)
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

	// Need to add the peer to all locations that it is running
	// This is an important optimization to not have to wait for a
	// prime block before adding a peer to the prime DB
	locationContexts := location.GetDoms()
	for _, location := range locationContexts {
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
