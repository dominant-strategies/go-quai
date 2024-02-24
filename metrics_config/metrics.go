package metrics_config

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metrics "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"

	"github.com/dominant-strategies/go-quai/log"
)

// Enabled is checked by the constructor functions for all of the
// standard metrics. If it is true, the metric returned is a stub.
//
// This global kill-switch helps quantify the observer effect and makes
// for less cluttered pprof profiles.
var enabled bool

var registeredGauges = make(map[string]*prometheus.GaugeVec)
var registeredCounters = make(map[string]*prometheus.CounterVec)

// Init enables or disables the metrics system. Since we need this to run before
// any other code gets to create meters and timers, we'll actually do an ugly hack
// and peek into the command line args for the metrics flag.
func init() {
	enabled = false
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
	if gaugeVec, exists := registeredGauges[name]; exists {
		return gaugeVec
	}
	gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, []string{"label"})
	prometheus.Register(gaugeVec)
	registeredGauges[name] = gaugeVec
	return gaugeVec
}

func NewGauge(name string, help string) *prometheus.Gauge {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	})
	prometheus.Register(gauge)
	return &gauge
}

func NewCounter(name string, help string) *prometheus.Counter {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: name,
		Help: help,
	})
	prometheus.Register(counter)
	return &counter
}

func NewCounterVec(name string, help string) *prometheus.CounterVec {
	if counterVec, exists := registeredCounters[name]; exists {
		return counterVec
	}
	counterVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: help,
	}, []string{"label"})
	prometheus.Register(counterVec)
	registeredCounters[name] = counterVec
	return counterVec
}

func NewTimer(name string, help string) *prometheus.Timer {
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
		[]string{"usage_type"},
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
		[]string{"usage_type"},
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
		log.Global.Error("Failed to get process", "err", err)
	}

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		collectCPUMetrics(metricsMap["cpu"], proc)
		wg.Done()
	}()

	go func() {
		collectMemoryMetrics(metricsMap["mem"], proc)
		wg.Done()
	}()

	go func() {
		collectDiskMetrics(metricsMap["disk"])
		wg.Done()
	}()

	go func() {
		collectNetworkingMetrics(metricsMap["net"], proc)
		wg.Done()
	}()
}

func collectCPUMetrics(cpuGaugeVec *metrics.GaugeVec, proc *process.Process) {
	percent, err := proc.CPUPercent()
	if err != nil {
		log.Global.Error("Failed to get CPU percent", "err", err)
	} else {
		cpuGaugeVec.WithLabelValues("Go-quai").Set(percent)
	}

	usage, err := cpu.Percent(0, false)
	if err != nil {
		log.Global.Error("Failed to get CPU percent", "err", err)
	} else {
		percent = usage[0]
		cpuGaugeVec.WithLabelValues("System").Set(percent)
	}

	cpuStats, err := cpu.Times(false)
	if err != nil {
		log.Global.Error("Failed to get CPU stats", "err", err)
	} else {
		iowaits := cpuStats[0].Iowait / 1 * 100 // convert to percent
		cpuGaugeVec.WithLabelValues("Iowait").Set(iowaits)
	}

	threads, err := proc.NumThreads()
	if err != nil {
		log.Global.Error("Failed to get threads", "err", err)
	} else {
		cpuGaugeVec.WithLabelValues("Go_routines").Set(float64(threads))
	}
}

func collectMemoryMetrics(memGaugeVec *metrics.GaugeVec, proc *process.Process) {
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		log.Global.Error("Error while getting memory info", "err", err)
	} else {
		memGaugeVec.WithLabelValues("Used").Set(float64(memInfo.RSS))
		memGaugeVec.WithLabelValues("Swap").Set(float64(memInfo.Swap))
		memGaugeVec.WithLabelValues("Stack").Set(float64(memInfo.Stack))
	}
}

func collectDiskMetrics(diskGaugeVec *metrics.GaugeVec) {
	ioCounters, err := disk.IOCounters()
	if err != nil {
		log.Global.Error("Unable to get disk usage", "err", err)
		return
	}

	time.Sleep(1 * time.Second)

	var diskUse disk.IOCountersStat
	for _, stat := range ioCounters {
		diskUse = stat
		break
	}
	diskGaugeVec.WithLabelValues("Iops").Set(float64(diskUse.IopsInProgress))

	ioCountersEnd, err := disk.IOCounters()
	if err != nil {
		log.Global.Error("Unable to get disk usage", "err", err)
	}
	var readDiff uint64 = 0
	var writeDiff uint64 = 0

	for diskName, startStats := range ioCounters {
		if endStats, exists := ioCountersEnd[diskName]; exists {
			readDiff += (endStats.ReadBytes - startStats.ReadBytes)
			writeDiff += (endStats.WriteBytes - startStats.WriteBytes)

		}
	}
	diskGaugeVec.WithLabelValues("Read").Set(float64(readDiff))
	diskGaugeVec.WithLabelValues("Write").Set(float64(writeDiff))
}

func collectNetworkingMetrics(netGaugeVec *metrics.GaugeVec, proc *process.Process) {
	tcpConnections, err := net.ConnectionsPid("tcp", proc.Pid)
	if err != nil {
		log.Global.Error("Error while getting networking info", "err", err)
	} else {
		netGaugeVec.WithLabelValues("tcp").Set(float64(len(tcpConnections)))
	}

	udpConnections, err := net.ConnectionsPid("udp", proc.Pid)
	if err != nil {
		log.Global.Error("Error while getting networking info", "err", err)
	} else {
		netGaugeVec.WithLabelValues("udp").Set(float64(len(udpConnections)))
	}
}
