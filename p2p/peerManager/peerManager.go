package peerManager

import (
	"net"

	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core"
	basicConnGater "github.com/libp2p/go-libp2p/p2p/net/conngater"
	basicConnMgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"

	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/peer"
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

	// Increases the peer's liveliness score
	MarkLivelyPeer(core.PeerID)
	// Decreases the peer's liveliness score
	MarkLatentPeer(core.PeerID)

	// Increases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkResponsivePeer(core.PeerID)
	// Decreases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkUnresponsivePeer(core.PeerID)

	// Protects the peer's connection from being disconnected
	ProtectPeer(core.PeerID)
	// Remove protection from the peer's connection
	UnprotectPeer(p2p.PeerID)
	// Bans the peer's connection from being re-established
	BanPeer(core.PeerID)
}

type BasicPeerManager struct {
	*basicConnGater.BasicConnectionGater
	*basicConnMgr.BasicConnMgr
}

func NewManager(low int, high int, datastore datastore.Datastore) (*BasicPeerManager, error) {
	mgr, err := basicConnMgr.NewConnManager(low, high)
	if err != nil {
		return nil, err
	}

	gater, err := basicConnGater.NewBasicConnectionGater(datastore)
	if err != nil {
		return nil, err
	}

	return &BasicPeerManager{
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
	}, nil
}

func (pm *BasicPeerManager) MarkLivelyPeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "liveness_reports", 1)
}

func (pm *BasicPeerManager) MarkLatentPeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "latency_reports", 1)
}

func (pm *BasicPeerManager) MarkResponsivePeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "responses_served", 1)
}

func (pm *BasicPeerManager) MarkUnresponsivePeer(peer p2p.PeerID) {
	pm.TagPeer(peer, "responses_missed", 1)
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
