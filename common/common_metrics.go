package common

import (
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	messageMetrics *prometheus.CounterVec
	peerMetrics    *prometheus.GaugeVec
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	messageMetrics = metrics_config.NewCounterVec("MessageCounters", "Counters to track messages sent over the P2P layer")
	messageMetrics.WithLabelValues("sent")
	messageMetrics.WithLabelValues("received")

	peerMetrics = metrics_config.NewGaugeVec("PeerGauges", "Track the number of peers connected to this node")
	peerMetrics.WithLabelValues("numPeers")
}
