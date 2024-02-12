package peerManager

import (
	"net"
	"sync"

	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/ipfs/go-datastore"
	basicConnGater "github.com/libp2p/go-libp2p/p2p/net/conngater"
	basicConnMgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"

	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// Represents the minimum ratio of positive to negative reports
	// 	e.g. live_reports / latent_reports = 0.8
	c_qualityThreshold = 0.8

	// The number of peers to return when querying for peers
	c_peerCount = 3
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
	PrunePeerConnection(p2p.PeerID)

	// Returns c_recipientCount of the highest quality peers: lively & resposnive
	GetBestPeers() []p2p.PeerID
	// Returns c_recipientCount responsive, but less lively peers
	GetResponsivePeers() []p2p.PeerID
	// Returns c_recipientCount peers regardless of status
	GetPeers() []p2p.PeerID

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
}

type BasicPeerManager struct {
	*basicConnGater.BasicConnectionGater
	*basicConnMgr.BasicConnMgr

	selfID p2p.PeerID

	bestPeers       map[p2p.PeerID]struct{}
	responsivePeers map[p2p.PeerID]struct{}
	peers           map[p2p.PeerID]struct{}

	mu sync.Mutex
}

func NewManager(selfID p2p.PeerID, low int, high int, datastore datastore.Datastore) (*BasicPeerManager, error) {
	mgr, err := basicConnMgr.NewConnManager(low, high)
	if err != nil {
		return nil, err
	}

	gater, err := basicConnGater.NewBasicConnectionGater(datastore)
	if err != nil {
		return nil, err
	}

	return &BasicPeerManager{
		selfID:               selfID,
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		bestPeers:            make(map[peer.ID]struct{}),
		responsivePeers:      make(map[peer.ID]struct{}),
		peers:                make(map[peer.ID]struct{}),
	}, nil
}

func (pm *BasicPeerManager) PrunePeerConnection(peer p2p.PeerID) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.bestPeers, peer)
	delete(pm.responsivePeers, peer)
	delete(pm.peers, peer)
}

// Returns a subset of the peers randomly. Go guarantees unstable iteration order every time.
func (pm *BasicPeerManager) getPeersHelper(peerList map[p2p.PeerID]struct{}, numPeers int) []p2p.PeerID {
	peerSubset := make([]p2p.PeerID, 0, numPeers)
	counter := 0

	pm.mu.Lock()
	defer pm.mu.Unlock()
	for peer := range peerList {
		peerSubset = append(peerSubset, peer)
		counter++
		if counter == numPeers {
			break
		}
	}
	return peerSubset
}

func (pm *BasicPeerManager) GetBestPeers() []p2p.PeerID {
	if len(pm.bestPeers) < c_peerCount {
		bestPeerList := pm.getPeersHelper(pm.bestPeers, len(pm.bestPeers))
		bestPeerList = append(bestPeerList, pm.GetResponsivePeers()...)
		return bestPeerList
	}
	return pm.getPeersHelper(pm.bestPeers, c_peerCount)
}

func (pm *BasicPeerManager) GetResponsivePeers() []p2p.PeerID {
	if len(pm.responsivePeers) < c_peerCount {
		responsivePeerList := pm.getPeersHelper(pm.responsivePeers, len(pm.responsivePeers))
		responsivePeerList = append(responsivePeerList, pm.GetPeers()...)
		return responsivePeerList
	}
	return pm.getPeersHelper(pm.responsivePeers, c_peerCount)
}

func (pm *BasicPeerManager) GetPeers() []p2p.PeerID {
	if len(pm.peers) < c_peerCount {
		return pm.getPeersHelper(pm.peers, len(pm.peers))
	}
	return pm.getPeersHelper(pm.peers, c_peerCount)
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
func (pm *BasicPeerManager) recategorizePeer(peer p2p.PeerID) {
	liveness := pm.calculatePeerLiveness(peer)
	responsiveness := pm.calculatePeerResponsiveness(peer)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Remove peer from all buckets first
	delete(pm.bestPeers, peer)
	delete(pm.responsivePeers, peer)
	delete(pm.peers, peer)

	if liveness >= c_qualityThreshold && responsiveness >= c_qualityThreshold {
		// Best peers: high liveness and responsiveness
		(pm.bestPeers)[peer] = struct{}{}
	} else if responsiveness >= c_qualityThreshold {
		// Responsive peers: high responsiveness, but low liveness
		(pm.responsivePeers)[peer] = struct{}{}
	} else {
		// All other peers
		(pm.peers)[peer] = struct{}{}
	}
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
