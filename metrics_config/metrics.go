package metrics_config

import (
	"net/http"
	"os"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/prometheus/client_golang/prometheus"
	metrics "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// Enabled is checked by the constructor functions for all of the
// standard metrics. If it is true, the metric returned is a stub.
//
// This global kill-switch helps quantify the observer effect and makes
// for less cluttered pprof profiles.
var enabled = true

// Init enables or disables the metrics system. Since we need this to run before
// any other code gets to create meters and timers, we'll actually do an ugly hack
// and peek into the command line args for the metrics flag.
func init() {
}

func EnableMetrics() {
	enabled = true
}

func MetricsEnabled() bool {
	return enabled
}

func StartProcessMetrics() {
	// Short circuit if the metrics system is disabled
	if !enabled {
		return
	}

	// System usage metrics.
	gaugesMap := make(map[string]*prometheus.GaugeVec)

	gaugesMap["cpu"] = defineCPUMetrics()
	gaugesMap["mem"] = defineMemMetrics()
	gaugesMap["disk"] = defineDiskMetrics()
	gaugesMap["net"] = defineNetMetrics()

	go initializeHttpMetrics(gaugesMap)
}

func NewGaugeVec(name string, help string) *prometheus.GaugeVec {
	if !enabled {
		return nil
	}
	gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, []string{"label"})
	prometheus.MustRegister(gaugeVec)
	return gaugeVec
}

func NewGauge(name string, help string) *prometheus.Gauge {
	if !enabled {
		return nil
	}
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	})
	prometheus.MustRegister(gauge)
	return &gauge
}

func NewCounter(name string, help string) *prometheus.Counter {
	if !enabled {
		return nil
	}
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: name,
		Help: help,
	})
	prometheus.MustRegister(counter)
	return &counter
}

func NewTimer(name string, help string) *prometheus.Timer {
	if !enabled {
		return nil
	}
	timeHistogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: name,
		Help: help,
	})
	timer := prometheus.NewTimer(timeHistogram)
	prometheus.MustRegister(timeHistogram)

	return timer
	
}

func initializeHttpMetrics(metricsMap map[string]*prometheus.GaugeVec) {
	http.Handle("/metrics", promhttp.InstrumentMetricHandler(
		prometheus.DefaultRegisterer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			updateMetrics(metricsMap)
			promhttp.Handler().ServeHTTP(w, r)
		}),
	))
	http.ListenAndServe(":2112", nil)
}

func defineCPUMetrics() *metrics.GaugeVec {
	cpuUsageGauge := metrics.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cpu_usage",
			Help: "The average CPU usage over the last second",
		},
		[]string{"cpu_type"},
	)
	metrics.MustRegister(cpuUsageGauge)
	return cpuUsageGauge
}

func defineMemMetrics() *metrics.GaugeVec {
	memGauge := metrics.NewGaugeVec(
		metrics.GaugeOpts{
			Name: "mem_usage",
			Help: "The current memory usage",
		},
		[]string{"mem_type"},
	)
	metrics.MustRegister(memGauge)
	return memGauge
}

func defineDiskMetrics() *metrics.GaugeVec {
	diskGauge := metrics.NewGaugeVec(
		metrics.GaugeOpts{
			Name: "disk_usage",
			Help: "The current disk usage",
		},
		[]string{"usage_type"},
	)
	metrics.MustRegister(diskGauge)
	return diskGauge
}

func defineNetMetrics() *metrics.GaugeVec {
	netGauge := metrics.NewGaugeVec(
		metrics.GaugeOpts{
			Name: "net_usage",
			Help: "The current network usage",
		},
		[]string{"net_type"},
	)
	metrics.MustRegister(netGauge)
	return netGauge
}

func updateMetrics(metricsMap map[string]*prometheus.GaugeVec) {
	pid := os.Getpid()
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		log.Error("Failed to get process", "err", err)
	}

	collectCPUMetrics(metricsMap["cpu"], proc)
	collectMemoryMetrics(metricsMap["mem"], proc)
	collectDiskMetrics(metricsMap["disk"])
	collectNetworkingMetrics(metricsMap["net"], proc)

}

func collectCPUMetrics(cpuGaugeVec *metrics.GaugeVec, proc *process.Process) {
	percent, err := proc.CPUPercent()
	if err != nil {
		log.Error("Failed to get CPU percent", "err", err)
	} else {
		cpuGaugeVec.WithLabelValues("Go-quai").Set(percent)
	}

	usage, err := cpu.Percent(0, false)
	if err != nil {
		log.Error("Failed to get CPU percent", "err", err)
	} else {
		percent = usage[0]
		cpuGaugeVec.WithLabelValues("System").Set(percent)
	}

	cpuStats, err := cpu.Times(false)
	if err != nil {
		log.Error("Failed to get CPU stats", "err", err)
	} else {
		iowaits := cpuStats[0].Iowait / 1 * 100 // convert to percent
		cpuGaugeVec.WithLabelValues("Iowait").Set(iowaits)
	}

	threads, err := proc.NumThreads()
	if err != nil {
		log.Error("Failed to get threads", "err", err)
	} else {
		cpuGaugeVec.WithLabelValues("Go_routines").Set(float64(threads))
	}
}

func collectMemoryMetrics(memGaugeVec *metrics.GaugeVec, proc *process.Process) {
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		log.Error("Error while getting memory info", "err", err)
	} else {
		memGaugeVec.WithLabelValues("Used").Set(float64(memInfo.RSS))
		memGaugeVec.WithLabelValues("Swap").Set(float64(memInfo.Swap))
		memGaugeVec.WithLabelValues("Stack").Set(float64(memInfo.Stack))
	}
}

func collectDiskMetrics(diskGaugeVec *metrics.GaugeVec) {
	diskUse := disk.IOCountersStat{}
	diskGaugeVec.WithLabelValues("Iops").Set(float64(diskUse.IopsInProgress))
}

func collectNetworkingMetrics(netGaugeVec *metrics.GaugeVec, proc *process.Process) {
	tcpConnections, err := net.ConnectionsPid("tcp", proc.Pid)
	if err != nil {
		log.Error("Error while getting networking info", "err", err)
	} else {
		netGaugeVec.WithLabelValues("tcp").Set(float64(len(tcpConnections)))
	}

	udpConnections, err := net.ConnectionsPid("udp", proc.Pid)
	if err != nil {
		log.Error("Error while getting networking info", "err", err)
	} else {
		netGaugeVec.WithLabelValues("udp").Set(float64(len(udpConnections)))
	}
}
