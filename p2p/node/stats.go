package node

import (
	"runtime/debug"
	"time"

	"github.com/dominant-strategies/go-quai/log"
)

// Returns the number of peers in the routing table, as well as how many active
// connections we currently have.
func (p *P2PNode) connectionStats() (int, int, int) {
	WANPeerNum := len(p.dht.WAN.RoutingTable().ListPeers())
	LANPeerNum := len(p.dht.LAN.RoutingTable().ListPeers())
	peers := p.Host.Network().Peers()
	numConnected := len(peers)

	return WANPeerNum, LANPeerNum, numConnected
}

func (p *P2PNode) statsLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-ticker.C:
			WANPeerNum, LANPeerNum, peersConnected := p.connectionStats()

			log.Global.Debugf("Number of peers connected: %d", peersConnected)
			log.Global.Debugf("Peers in WAN Routing table: %d, Peers in LAN Routing table: %d", WANPeerNum, LANPeerNum)
		case <-p.ctx.Done():
			log.Global.Warnf("Context cancelled. Stopping stats loop...")
			return
		}

	}
}
