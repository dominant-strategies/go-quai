package protocol

import (
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	streamMetrics  *prometheus.GaugeVec
	messageMetrics *prometheus.CounterVec
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	streamMetrics = metrics_config.NewGaugeVec("StreamGauges", "Track the number of streams opened by this node")
	streamMetrics.WithLabelValues("NumStreams")

	messageMetrics = metrics_config.NewCounterVec("MessageCounters", "Counters to track messages sent over the P2P layer")
	messageMetrics.WithLabelValues("blocks")
	messageMetrics.WithLabelValues("headers")
	messageMetrics.WithLabelValues("transactions")
	messageMetrics.WithLabelValues("requests")
	messageMetrics.WithLabelValues("responses")
}
