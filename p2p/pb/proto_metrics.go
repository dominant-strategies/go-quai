package pb

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
	messageMetrics = metrics_config.NewCounterVec("MessageCounters", "Counters to track messages sent over the P2P layer")
	messageMetrics.WithLabelValues("blocks")
	messageMetrics.WithLabelValues("headers")
	messageMetrics.WithLabelValues("transactions")
	messageMetrics.WithLabelValues("requests")
	messageMetrics.WithLabelValues("responses")
}
