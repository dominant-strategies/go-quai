package p2p

import (
	"github.com/libp2p/go-libp2p/core"
)

const (
	// Prefix for all block topics
	C_blockTopicName = "block"
	C_transactionTopicName = "transaction"
)

// Multiaddr aliases the Multiaddr type from github.com/libp2p/core
//
// Refer to the docs on that type for more info.
type Multiaddr = core.Multiaddr

// PeerID aliases the PeerID type from github.com/libp2p/core
//
// Refer to the docs on that type for more info.
type PeerID = core.PeerID
