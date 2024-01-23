package pubsubManager

import (
	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// Connection Gater is used to control which connectisons are allowed to and from peers.
type ConnGater struct {
	peerBlackList *map[peer.ID]struct{}
}

func NewConnGater(peerBlackList *map[peer.ID]struct{}) *ConnGater {
	return &ConnGater{
		peerBlackList: peerBlackList,
	}
}

// InterceptPeerDial tests whether we're permitted to Dial the specified peer.
//
// This is called by the network.Network implementation when dialling a peer.
func (cg *ConnGater) InterceptPeerDial(p peer.ID) (allow bool) {
	return cg.testPeer(p)
}

// InterceptAddrDial tests whether we're permitted to dial the specified
// multiaddr for the given peer.
//
// This is called by the network.Network implementation after it has
// resolved the peer's addrs, and prior to dialling each.
func (cg *ConnGater) InterceptAddrDial(p peer.ID, addr ma.Multiaddr) (allow bool) {
	return cg.testPeer(p)
}

// InterceptAccept tests whether an incipient inbound connection is allowed.
//
// This is called by the upgrader, or by the transport directly (e.g. QUIC,
// Bluetooth), straight after it has accepted a connection from its socket.
func (cg *ConnGater) InterceptAccept(network.ConnMultiaddrs) (allow bool) {
	return true
}

// InterceptSecured tests whether a given connection, now authenticated,
// is allowed.
//
// This is called by the upgrader, after it has performed the security
// handshake, and before it negotiates the muxer, or by the directly by the
// transport, at the exact same checkpoint.
func (cg *ConnGater) InterceptSecured(direction network.Direction, pid peer.ID, multiAddrs network.ConnMultiaddrs) (allow bool) {
	return cg.testPeer(pid)
}

// InterceptUpgraded tests whether a fully capable connection is allowed.
//
// At this point, the connection a multiplexer has been selected.
// When rejecting a connection, the gater can return a DisconnectReason.
// Refer to the godoc on the ConnectionGater type for more information.
//
// NOTE: the go-libp2p implementation currently IGNORES the disconnect reason.
func (cg *ConnGater) InterceptUpgraded(network.Conn) (allow bool, reason control.DisconnectReason) {
	return true, 0
}

func (cg *ConnGater) testPeer(p peer.ID) (ok bool) {
	_, ok = (*cg.peerBlackList)[p]
	return !ok
}

func (cg *ConnGater) ReportBadPeer(p peer.ID) {
	(*cg.peerBlackList)[p] = struct{}{}
}
