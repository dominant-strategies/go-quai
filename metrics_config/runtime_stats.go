package metrics_config

import metrics "github.com/prometheus/client_golang/prometheus"

type runtimeStats struct {
	GCPauses     *metrics.Histogram
	GCAllocBytes uint64
	GCFreedBytes uint64

	MemTotal     uint64
	HeapObjects  uint64
	HeapFree     uint64
	HeapReleased uint64
	HeapUnused   uint64

	Goroutines   uint64
	SchedLatency *metrics.Histogram
}
