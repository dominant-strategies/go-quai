package peerManager

import (
	"net"

	"github.com/ipfs/go-datastore"
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
