package peerManager

import (
	"context"
	"net"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	basicConnGater "github.com/libp2p/go-libp2p/p2p/net/conngater"
	basicConnMgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"

	"github.com/dominant-strategies/go-quai/p2p/peerManager/peerdb"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// Represents the minimum ratio of positive to negative reports
	// 	e.g. live_reports / latent_reports = 0.8
	c_qualityThreshold = 0.8

	// The number of peers to return when querying for peers
	c_peerCount = 3

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

	// Initializes a peer to the peer manager
	AddPeer(p2p.PeerID) error
	// Removes a peer from all the quality buckets
	RemovePeer(p2p.PeerID) error

	// Returns c_recipientCount of the highest quality peers: lively & resposnive
	GetBestPeersWithFallback() []p2p.PeerID
	// Returns c_recipientCount responsive, but less lively peers
	GetResponsivePeersWithFallback() []p2p.PeerID
	// Returns c_recipientCount peers regardless of status
	GetLastResortPeers() []p2p.PeerID

	// Increases the peer's liveliness score
	MarkLivelyPeer(p2p.PeerID)
	// Decreases the peer's liveliness score
	MarkLatentPeer(p2p.PeerID)

	// Increases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkResponsivePeer(p2p.PeerID)
	// Decreases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkUnresponsivePeer(p2p.PeerID)

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

	selfID p2p.PeerID

	bestPeersDB       *peerdb.PeerDB
	responsivePeersDB *peerdb.PeerDB
	lastResortPeers   *peerdb.PeerDB

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

	bestPeersDB, err := peerdb.NewPeerDB(c_bestDBName)
	if err != nil {
		return nil, err
	}

	responsivePeersDB, err := peerdb.NewPeerDB(c_responsiveDBName)
	if err != nil {
		return nil, err
	}

	lastResortPeers, err := peerdb.NewPeerDB(c_lastResortDBName)
	if err != nil {
		return nil, err
	}

	return &BasicPeerManager{
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		bestPeersDB:          bestPeersDB,
		responsivePeersDB:    responsivePeersDB,
		lastResortPeers:      lastResortPeers,
		ctx:                  ctx,
	}, nil
}

func (pm *BasicPeerManager) AddPeer(peerID p2p.PeerID) error {
	return pm.recategorizePeer(peerID)
}

// Removes peer from the bucket it is in. Does not return an error if the peer is not found
func (pm *BasicPeerManager) RemovePeer(peerID p2p.PeerID) error {
	key := datastore.NewKey(peerID.String())

	dbs := []*peerdb.PeerDB{pm.bestPeersDB, pm.responsivePeersDB, pm.lastResortPeers}
	for _, db := range dbs {
		exists, _ := db.Has(pm.ctx, key)
		if exists {
			return db.Delete(pm.ctx, key)
		}
	}

	return nil
}
func (pm *BasicPeerManager) SetP2PBackend(host quaiprotocol.QuaiP2PNode) {
	pm.p2pBackend = host
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

func (pm *BasicPeerManager) GetBestPeersWithFallback() []p2p.PeerID {
	bestPeersCount := pm.bestPeersDB.GetPeerCount()
	if bestPeersCount < c_peerCount {
		bestPeerList := pm.getPeersHelper(pm.bestPeersDB, bestPeersCount)
		bestPeerList = append(bestPeerList, pm.GetResponsivePeersWithFallback()...)
		return bestPeerList
	}
	return pm.getPeersHelper(pm.bestPeersDB, c_peerCount)
}

func (pm *BasicPeerManager) GetResponsivePeersWithFallback() []p2p.PeerID {
	responsivePeersCount := pm.responsivePeersDB.GetPeerCount()
	if responsivePeersCount < c_peerCount {
		responsivePeerList := pm.getPeersHelper(pm.responsivePeersDB, responsivePeersCount)
		responsivePeerList = append(responsivePeerList, pm.GetLastResortPeers()...)

		return responsivePeerList
	}
	return pm.getPeersHelper(pm.responsivePeersDB, c_peerCount)

}

func (pm *BasicPeerManager) GetLastResortPeers() []p2p.PeerID {
	return pm.getPeersHelper(pm.lastResortPeers, c_peerCount)
}

func (pm *BasicPeerManager) MarkLivelyPeer(peer p2p.PeerID) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "liveness_reports", 1)
	pm.recategorizePeer(peer)
}

func (pm *BasicPeerManager) MarkLatentPeer(peer p2p.PeerID) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "latency_reports", 1)
	pm.recategorizePeer(peer)
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

func (pm *BasicPeerManager) MarkResponsivePeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "responses_served", 1)
	pm.recategorizePeer(peer)
}

func (pm *BasicPeerManager) MarkUnresponsivePeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "responses_missed", 1)
	pm.recategorizePeer(peer)
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
func (pm *BasicPeerManager) recategorizePeer(peer p2p.PeerID) error {
	liveness := pm.calculatePeerLiveness(peer)
	responsiveness := pm.calculatePeerResponsiveness(peer)

	// remove peer from DB first
	err := pm.RemovePeer(peer)
	if err != nil {
		return err
	}

	key := datastore.NewKey(peer.String())
	// TODO: construct peerDB.PeerInfo and marshal it to bytes
	peerInfo := []byte{}

	if liveness >= c_qualityThreshold && responsiveness >= c_qualityThreshold {
		// Best peers: high liveness and responsiveness
		err := pm.bestPeersDB.Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in bestPeersDB")
		}

	} else if responsiveness >= c_qualityThreshold {
		// Responsive peers: high responsiveness, but low liveness
		err := pm.responsivePeersDB.Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in responsivePeersDB")
		}

	} else {
		// All other peers
		err := pm.lastResortPeers.Put(pm.ctx, key, peerInfo)
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
		pm.bestPeersDB.Close,
		pm.responsivePeersDB.Close,
		pm.lastResortPeers.Close,
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

	wg.Wait()

	if len(closeErrors) > 0 {
		return errors.New("failed to close some resources: " + strings.Join(closeErrors, "; "))
	}

	return nil
}
