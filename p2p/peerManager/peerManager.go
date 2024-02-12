package peerManager

import (
	"context"
	"net"
	"strings"

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
	c_peersDBName      = "allPeersDB"
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
	PrunePeerConnection(p2p.PeerID) error

	// Returns c_recipientCount of the highest quality peers: lively & resposnive
	GetBestPeers() ([]p2p.PeerID, error)
	// Returns c_recipientCount responsive, but less lively peers
	GetResponsivePeers() ([]p2p.PeerID, error)
	// Returns c_recipientCount peers regardless of status
	GetPeers() ([]p2p.PeerID, error)

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

	selfID p2p.PeerID

	bestPeersDB       *peerdb.PeerDB
	responsivePeersDB *peerdb.PeerDB
	allPeersDB        *peerdb.PeerDB

	ctx context.Context
}

func NewManager(ctx context.Context, selfID p2p.PeerID, low int, high int, datastore datastore.Datastore) (*BasicPeerManager, error) {
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

	allPeersDB, err := peerdb.NewPeerDB(c_peersDBName)
	if err != nil {
		return nil, err
	}

	return &BasicPeerManager{
		selfID:               selfID,
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		bestPeersDB:          bestPeersDB,
		responsivePeersDB:    responsivePeersDB,
		allPeersDB:           allPeersDB,
		ctx:                  ctx,
	}, nil
}

func (pm *BasicPeerManager) PrunePeerConnection(peerID p2p.PeerID) error {
	return pm.deletePeerFromDB(peerID)
}

func (pm *BasicPeerManager) getPeersHelper(peerDB *peerdb.PeerDB, numPeers int) ([]p2p.PeerID, error) {
	peerSubset := make([]p2p.PeerID, 0, numPeers)
	q := query.Query{
		Limit: numPeers,
	}
	results, err := peerDB.Query(pm.ctx, q)
	if err != nil {
		return nil, err
	}

	for result := range results.Next() {
		peerID := p2p.PeerID(result.Key)
		peerSubset = append(peerSubset, peerID)
	}

	return peerSubset, nil
}

func (pm *BasicPeerManager) GetBestPeers() ([]p2p.PeerID, error) {
	bestPeersCount := pm.bestPeersDB.GetPeerCount()
	if bestPeersCount < c_peerCount {
		bestPeerList, err := pm.getPeersHelper(pm.bestPeersDB, bestPeersCount)
		if err != nil {
			return nil, err
		}

		responsivePeerList, err := pm.getPeersHelper(pm.responsivePeersDB, c_peerCount-bestPeersCount)
		if err != nil {
			return nil, err
		}

		bestPeerList = append(bestPeerList, responsivePeerList...)
		return bestPeerList, nil
	}
	return pm.getPeersHelper(pm.bestPeersDB, c_peerCount)
}

func (pm *BasicPeerManager) GetResponsivePeers() ([]p2p.PeerID, error) {
	responsivePeersCount := pm.responsivePeersDB.GetPeerCount()
	if responsivePeersCount < c_peerCount {
		responsivePeerList, err := pm.getPeersHelper(pm.responsivePeersDB, responsivePeersCount)
		if err != nil {
			return nil, err
		}

		allPeersList, err := pm.getPeersHelper(pm.allPeersDB, c_peerCount-responsivePeersCount)
		if err != nil {
			return nil, err
		}

		responsivePeerList = append(responsivePeerList, allPeersList...)

		return responsivePeerList, nil
	}
	return pm.getPeersHelper(pm.responsivePeersDB, c_peerCount)

}

func (pm *BasicPeerManager) GetPeers() ([]p2p.PeerID, error) {
	return pm.getPeersHelper(pm.allPeersDB, c_peerCount)
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
	err := pm.deletePeerFromDB(peer)
	if err != nil {
		return err
	}

	key := peerdb.NewKey(peer)
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
		err := pm.allPeersDB.Put(pm.ctx, key, peerInfo)
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
	var closeErrors []string

	if err := pm.BasicConnMgr.Close(); err != nil {
		closeErrors = append(closeErrors, err.Error())
	}
	if err := pm.bestPeersDB.Close(); err != nil {
		closeErrors = append(closeErrors, err.Error())
	}
	if err := pm.responsivePeersDB.Close(); err != nil {
		closeErrors = append(closeErrors, err.Error())
	}
	if err := pm.allPeersDB.Close(); err != nil {
		closeErrors = append(closeErrors, err.Error())
	}

	if len(closeErrors) > 0 {
		return errors.New("failed to close some resources: " + strings.Join(closeErrors, "; "))
	}

	return nil
}

// Deletes the peer from the first DB it is found in.
// Returns error 'ErrPeerNotFound' if the peer is not found in any of the DBs.
func (pm *BasicPeerManager) deletePeerFromDB(peer p2p.PeerID) error {
	key := peerdb.NewKey(peer)

	dbs := []*peerdb.PeerDB{pm.bestPeersDB, pm.responsivePeersDB, pm.allPeersDB}

	for _, db := range dbs {
		exists, err := db.Has(pm.ctx, key)
		if err != nil {
			return err
		}
		if exists {
			return db.Delete(pm.ctx, key)
		}
	}

	return peerdb.ErrPeerNotFound
}
