package node

import (
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	streamMetrics *prometheus.GaugeVec
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	streamMetrics = metrics_config.NewGaugeVec("StreamGauges", "Time spent doing state operations")
}