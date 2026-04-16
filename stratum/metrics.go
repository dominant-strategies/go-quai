package stratum

import (
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/prometheus/client_golang/prometheus"
)

// Stratum server Prometheus metrics
// All metrics are guarded by metrics_config.MetricsEnabled() check
var (
	// Aggregate metrics (low cardinality)
	stratumWorkersConnected *prometheus.GaugeVec
	stratumSharesTotal      *prometheus.CounterVec
	stratumBlocksFound      *prometheus.CounterVec
	stratumWorksharesFound  *prometheus.CounterVec
	stratumHashrate         *prometheus.GaugeVec
	stratumActiveConns      prometheus.Gauge
	stratumConnectionsTotal prometheus.Counter

	// Per-worker metrics (higher cardinality - use with caution)
	stratumWorkerHashrate    *prometheus.GaugeVec
	stratumWorkerShares      *prometheus.CounterVec
	stratumWorkerWorkshares  *prometheus.CounterVec
	stratumWorkerOnline      *prometheus.GaugeVec
)

func init() {
	// Aggregate metrics
	stratumWorkersConnected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stratum_workers_connected",
			Help: "Number of currently connected stratum workers",
		},
		[]string{"algorithm"},
	)

	stratumSharesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stratum_shares_total",
			Help: "Total number of shares submitted",
		},
		[]string{"algorithm", "status"}, // status: valid, stale, invalid
	)

	stratumBlocksFound = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stratum_blocks_found_total",
			Help: "Total number of blocks found",
		},
		[]string{"algorithm"},
	)

	stratumWorksharesFound = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stratum_workshares_found_total",
			Help: "Total number of workshares found",
		},
		[]string{"algorithm"},
	)

	stratumHashrate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stratum_hashrate",
			Help: "Aggregate pool hashrate in H/s",
		},
		[]string{"algorithm"},
	)

	stratumActiveConns = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "stratum_active_connections",
			Help: "Number of active TCP connections",
		},
	)

	stratumConnectionsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "stratum_connections_total",
			Help: "Total number of TCP connections accepted",
		},
	)

	// Per-worker metrics
	stratumWorkerHashrate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stratum_worker_hashrate",
			Help: "Per-worker hashrate in H/s",
		},
		[]string{"address", "worker", "algorithm"},
	)

	stratumWorkerShares = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stratum_worker_shares_total",
			Help: "Per-worker share count",
		},
		[]string{"address", "worker", "algorithm", "status"},
	)

	stratumWorkerWorkshares = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stratum_worker_workshares_total",
			Help: "Per-worker workshare count",
		},
		[]string{"address", "worker", "algorithm"},
	)

	stratumWorkerOnline = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stratum_worker_online",
			Help: "Whether worker is currently connected (1=online, 0=offline)",
		},
		[]string{"address", "worker", "algorithm"},
	)

	// Register all metrics
	prometheus.MustRegister(
		stratumWorkersConnected,
		stratumSharesTotal,
		stratumBlocksFound,
		stratumWorksharesFound,
		stratumHashrate,
		stratumActiveConns,
		stratumConnectionsTotal,
		stratumWorkerHashrate,
		stratumWorkerShares,
		stratumWorkerWorkshares,
		stratumWorkerOnline,
	)
}

// =============================================================================
// Wrapper functions - guarded by MetricsEnabled() check
// =============================================================================

// RecordWorkerConnected increments the connected worker count for an algorithm
func RecordWorkerConnected(algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkersConnected.WithLabelValues(algorithm).Inc()
}

// RecordWorkerDisconnected decrements the connected worker count for an algorithm
func RecordWorkerDisconnected(algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkersConnected.WithLabelValues(algorithm).Dec()
}

// RecordShareSubmitted records a share submission
// status should be "valid", "stale", or "invalid"
func RecordShareSubmitted(algorithm, status string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumSharesTotal.WithLabelValues(algorithm, status).Inc()
}

// RecordBlockFound increments the block found counter
func RecordBlockFound(algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumBlocksFound.WithLabelValues(algorithm).Inc()
}

// RecordWorkshareFound increments the workshare found counter
func RecordWorkshareFound(algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorksharesFound.WithLabelValues(algorithm).Inc()
}

// RecordHashrate updates the aggregate hashrate for an algorithm
func RecordHashrate(algorithm string, hashrate float64) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumHashrate.WithLabelValues(algorithm).Set(hashrate)
}

// RecordConnectionAccepted increments total connections and active connections
func RecordConnectionAccepted() {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumConnectionsTotal.Inc()
	stratumActiveConns.Inc()
}

// RecordConnectionClosed decrements active connections
func RecordConnectionClosed() {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumActiveConns.Dec()
}

// =============================================================================
// Per-worker metrics wrappers
// =============================================================================

// RecordWorkerOnline sets worker online status (1=online, 0=offline)
func RecordWorkerOnline(address, worker, algorithm string, online bool) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	val := 0.0
	if online {
		val = 1.0
	}
	stratumWorkerOnline.WithLabelValues(address, worker, algorithm).Set(val)
}

// RecordWorkerHashrate updates a specific worker's hashrate
func RecordWorkerHashrate(address, worker, algorithm string, hashrate float64) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkerHashrate.WithLabelValues(address, worker, algorithm).Set(hashrate)
}

// RecordWorkerShare records a share for a specific worker
// status should be "valid", "stale", or "invalid"
func RecordWorkerShare(address, worker, algorithm, status string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkerShares.WithLabelValues(address, worker, algorithm, status).Inc()
}

// RecordWorkerWorkshare records a workshare found by a specific worker
func RecordWorkerWorkshare(address, worker, algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkerWorkshares.WithLabelValues(address, worker, algorithm).Inc()
}

// DeleteWorkerMetrics removes metrics for a disconnected worker
// Call this when a worker disconnects to prevent stale metrics
func DeleteWorkerMetrics(address, worker, algorithm string) {
	if !metrics_config.MetricsEnabled() {
		return
	}
	stratumWorkerOnline.DeleteLabelValues(address, worker, algorithm)
	stratumWorkerHashrate.DeleteLabelValues(address, worker, algorithm)
	// Note: We don't delete share counters - they should persist for historical tracking
}
