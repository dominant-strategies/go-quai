package gossipsub

import (
	gossip "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Handle incoming Gossipsub message
func HandleIncomingMessage(msg *gossip.Message) {
	// TODO: implement
}

// Handle logic when a peer joins the network
func HandlePeerJoin(peerID peer.ID) {
	// TODO: implement
}

// Handle logic when a peer leaves the network
func HandlePeerLeave(peerID peer.ID) {
	// TODO: implement
}
