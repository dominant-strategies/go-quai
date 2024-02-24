package peerManager

import (
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	peerMetrics   *prometheus.GaugeVec
	streamMetrics *prometheus.GaugeVec
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	peerMetrics = metrics_config.NewGaugeVec("PeerGauges", "Track the number of peers connected to this node")
	peerMetrics.WithLabelValues("numPeers")

	streamMetrics = metrics_config.NewGaugeVec("StreamGauges", "Time spent doing state operations")
}
