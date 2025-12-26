package node

import (
	"fmt"

	"github.com/multiformats/go-multiaddr"
)

// PeerInfo returns:
// - this node's full multiaddrs (including /p2p/<peerID>)
// - connected peers keyed by peer ID, with their known multiaddrs
//
// This is intentionally exposed as a small, dependency-free API surface so
// higher layers (e.g. RPC) can introspect p2p state without importing libp2p.
func (p *P2PNode) PeerInfo() ([]string, map[string][]string, error) {
	selfP2P, err := p.p2pAddress()
	if err != nil {
		return nil, nil, err
	}

	self := make([]string, 0, len(p.host.Addrs()))
	for _, addr := range p.host.Addrs() {
		self = append(self, addr.Encapsulate(selfP2P).String())
	}

	peers := make(map[string][]string)
	for _, peerID := range p.host.Network().Peers() {
		// Ensure a /p2p/<peerID> suffix for each address.
		p2pComponent, err := multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", peerID.String()))
		if err != nil {
			return nil, nil, err
		}

		addrs := p.host.Peerstore().Addrs(peerID)
		out := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			out = append(out, addr.Encapsulate(p2pComponent).String())
		}
		peers[peerID.String()] = out
	}

	return self, peers, nil
}
