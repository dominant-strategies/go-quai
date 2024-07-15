package node

import (
	"runtime/debug"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
)

var (
	bandwidthMetrics       = metrics_config.NewGaugeVec("Bandwidth", "Bandwidth guages")
	inRateTotal            = bandwidthMetrics.WithLabelValues("total bytes/s in")
	outRateTotal           = bandwidthMetrics.WithLabelValues("total bytes/s out")
	inRateGossipsub        = bandwidthMetrics.WithLabelValues("gossipsub bytes/s in")
	outRateGossipsub       = bandwidthMetrics.WithLabelValues("gossipsub bytes/s out")
	inRateRelay            = bandwidthMetrics.WithLabelValues("relay bytes/s in")
	outRateRelay           = bandwidthMetrics.WithLabelValues("relay bytes/s out")
	inRateRequestResponse  = bandwidthMetrics.WithLabelValues("request-response bytes/s in")
	outRateRequestResponse = bandwidthMetrics.WithLabelValues("request-response bytes/s out")
)

// Returns the number of peers in the routing table, as well as how many active
// connections we currently have.
func (p *P2PNode) connectionStats() int {
	peers := p.peerManager.GetHost().Network().Peers()
	numConnected := len(peers)

	return numConnected
}

func (p *P2PNode) statsLoop() {
	defer func() {
		if r := recover(); r != nil {
			p.quitCh <- struct{}{}
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
			// Collect peer stats
			peersConnected := p.connectionStats()
			common.PeerMetrics.WithLabelValues("numPeers").Set(float64(peersConnected))
			log.Global.Debugf("Number of peers connected: %d", peersConnected)

			// Collect bandwidth stats
			bandwidth := p.bandwidthCounter.GetBandwidthTotals()
			inRateTotal.Set(float64(bandwidth.RateIn))
			outRateTotal.Set(float64(bandwidth.RateOut))
			pubsubBw := p.bandwidthCounter.GetBandwidthForProtocol("/meshsub/1.1.0")
			inRateGossipsub.Set(float64(pubsubBw.RateIn))
			outRateGossipsub.Set(float64(pubsubBw.RateOut))
			relayBw := p.bandwidthCounter.GetBandwidthForProtocol("/libp2p/circuit/relay/0.2.0/hop")
			inRateRelay.Set(float64(relayBw.RateIn))
			outRateRelay.Set(float64(relayBw.RateOut))
			reqResBw := p.bandwidthCounter.GetBandwidthForProtocol("/quai/1.0.0")
			inRateRequestResponse.Set(float64(reqResBw.RateIn))
			outRateRequestResponse.Set(float64(reqResBw.RateOut))
		case <-p.ctx.Done():
			log.Global.Warnf("Context cancelled. Stopping stats loop...")
			return
		}
	}
}
