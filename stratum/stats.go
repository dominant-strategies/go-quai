package stratum

import (
	"fmt"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/log"
)

const (
	c_hashRateEmaSamples = 50
)

// formatAlgoHashrate returns a human-readable hashrate string with fixed units per algorithm
// SHA256: TH/s, Scrypt: GH/s, KawPoW: MH/s
func formatAlgoHashrate(hashrate float64, algorithm string) string {
	switch algorithm {
	case "scrypt":
		// Scrypt: display in GH/s
		gh := hashrate / 1e9
		return fmt.Sprintf("%.2f GH/s", gh)
	case "kawpow":
		// KawPoW: display in MH/s
		mh := hashrate / 1e6
		return fmt.Sprintf("%.2f MH/s", mh)
	default:
		// SHA256: display in TH/s
		th := hashrate / 1e12
		return fmt.Sprintf("%.2f TH/s", th)
	}
}

// WorkerStats tracks statistics for a connected miner
type WorkerStats struct {
	Address        string    `json:"address"`
	WorkerName     string    `json:"workerName"`
	Algorithm      string    `json:"algorithm"`
	ConnectedAt    time.Time `json:"connectedAt"`
	FirstShareAt   time.Time `json:"firstShareAt"`   // time of first valid share
	FirstShareDiff float64   `json:"firstShareDiff"` // difficulty of first valid share
	LastShareAt    time.Time `json:"lastShareAt"`
	LastShareDiff  float64   `json:"lastShareDiff"` // difficulty of last valid share
	SharesValid    uint64    `json:"sharesValid"`
	SharesStale    uint64    `json:"sharesStale"`
	SharesInvalid  uint64    `json:"sharesInvalid"`
	Difficulty     float64   `json:"difficulty"`
	Hashrate       float64   `json:"hashrate"` // estimated from share rate
	CumulativeWork float64   `json:"-"`        // sum of (difficulty) for each valid share
	IsConnected    bool      `json:"isConnected"`
}

// BlockFound represents a block discovered by the pool
type BlockFound struct {
	Height     uint64    `json:"height"`
	Hash       string    `json:"hash"`
	Worker     string    `json:"worker"`
	Algorithm  string    `json:"algorithm"`
	Difficulty string    `json:"difficulty"`
	FoundAt    time.Time `json:"foundAt"`
}

// ShareRecord tracks an individual share submission with difficulty info
type ShareRecord struct {
	Timestamp          time.Time `json:"timestamp"`
	Worker             string    `json:"worker"`
	Algorithm          string    `json:"algorithm"`
	AchievedDifficulty float64   `json:"achievedDifficulty"` // actual difficulty of the share
	WorkshareDiff      float64   `json:"workshareDiff"`      // target difficulty for a block
	LuckPercent        float64   `json:"luckPercent"`        // achievedDiff / workshareDiff * 100
	IsBlock            bool      `json:"isBlock"`            // true if this share found a block
}

// PoolStats aggregates all pool statistics
type PoolStats struct {
	workers     map[string]*WorkerStats
	blocks      []BlockFound
	totalShares uint64
	startedAt   time.Time
	mu          sync.RWMutex
	logger      *log.Logger

	// Hashrate calculation
	shareWindow    []shareEvent
	windowDuration time.Duration

	// Share history for solo mining luck tracking
	shareHistory []ShareRecord
}

type shareEvent struct {
	timestamp  time.Time
	difficulty float64
}

// NewPoolStats creates a new pool statistics tracker
func NewPoolStats(logger *log.Logger) *PoolStats {
	return &PoolStats{
		workers:        make(map[string]*WorkerStats),
		blocks:         make([]BlockFound, 0),
		startedAt:      time.Now(),
		shareWindow:    make([]shareEvent, 0),
		windowDuration: 10 * time.Minute,
		shareHistory:   make([]ShareRecord, 0),
		logger:         logger,
	}
}

// WorkerConnected registers a new worker connection
// workerName is optional (from address.workerName format)
func (ps *PoolStats) WorkerConnected(address, workerName, algorithm string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Use address.workerName as key to support multiple workers per address
	key := address
	if workerName != "" {
		key = address + "." + workerName + "." + algorithm
	}

	if worker, exists := ps.workers[key]; exists {
		// If worker is already connected, this is a redundant authorization.
		// Do nothing to prevent resetting stats for an active session.
		if worker.IsConnected {
			return
		}
	} else {
		ps.workers[key] = &WorkerStats{
			Address:     address,
			WorkerName:  workerName,
			Algorithm:   algorithm,
			ConnectedAt: time.Now(),
			IsConnected: true,
		}
	}
}

// WorkerDisconnected marks a worker as disconnected
func (ps *PoolStats) WorkerDisconnected(address, workerName, algorithm string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := address
	if workerName != "" {
		key = address + "." + workerName + "." + algorithm
	}

	if worker, exists := ps.workers[key]; exists {
		worker.IsConnected = false
	}
}

// ShareSubmitted records a share submission
func (ps *PoolStats) ShareSubmitted(address, workerName, algorithm string, difficulty float64, valid bool, stale bool) {
	ps.ShareSubmittedWithDiff(address, workerName, algorithm, 0, 0, 0, difficulty, 0, 0, valid, stale)
}

// ShareSubmittedWithDiff records a share with detailed difficulty information for solo mining
func (ps *PoolStats) ShareSubmittedWithDiff(address, workerName, algorithm string, totalShares, invalidShares, staleShares uint64, poolDiff, achievedDiff, workshareDiff float64, valid bool, stale bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.totalShares++

	// Record share for hashrate calculation
	ps.shareWindow = append(ps.shareWindow, shareEvent{
		timestamp:  time.Now(),
		difficulty: poolDiff,
	})
	ps.pruneShareWindow()

	key := address
	if workerName != "" {
		key = address + "." + workerName + "." + algorithm
	}

	now := time.Now()

	// Ensure worker exists and is connected (handles reconnects without authorize)
	worker, exists := ps.workers[key]
	if !exists {
		worker = &WorkerStats{
			Address:     address,
			WorkerName:  workerName,
			Algorithm:   algorithm,
			ConnectedAt: now,
			IsConnected: true,
		}
		ps.workers[key] = worker
	} else if !worker.IsConnected {
		worker.IsConnected = true
	}

	worker.LastShareAt = now
	worker.Difficulty = poolDiff
	if valid {
		// Track first share info
		isFirstShare := worker.SharesValid == 0
		if isFirstShare {
			worker.FirstShareAt = now
			worker.FirstShareDiff = poolDiff
		}
		worker.LastShareDiff = poolDiff
		worker.SharesValid++
		worker.CumulativeWork += poolDiff // Track actual work done at each share's difficulty
	} else if stale {
		worker.CumulativeWork += poolDiff // Track actual work done at each share's difficulty
		worker.SharesStale++
	} else {
		worker.SharesInvalid++
	}
	worker.Hashrate = ps.calculateWorkerHashrate(key)

	// Log worker hashrate with first and last share info
	if valid && ps.logger != nil {
		elapsed := now.Sub(worker.ConnectedAt).Seconds()
		ps.logger.WithFields(log.Fields{
			"worker":         key,
			"totalShares":    totalShares,
			"invalidShares":  invalidShares,
			"staleShares":    staleShares,
			"algorithm":      worker.Algorithm,
			"hashrate":       formatAlgoHashrate(worker.Hashrate, worker.Algorithm),
			"sharesValid":    worker.SharesValid,
			"cumulativeWork": worker.CumulativeWork,
			"elapsedSecs":    elapsed,
			"firstShareAt":   worker.FirstShareAt.Format(time.RFC3339),
			"firstShareDiff": worker.FirstShareDiff,
			"lastShareAt":    worker.LastShareAt.Format(time.RFC3339),
			"lastShareDiff":  worker.LastShareDiff,
			"poolDiff":       poolDiff,
		}).Info("Worker hashrate update")
	}

	// Record share in history for solo mining luck tracking (only valid shares)
	if valid && achievedDiff > 0 && workshareDiff > 0 {
		luckPercent := (achievedDiff / workshareDiff) * 100
		isBlock := achievedDiff >= workshareDiff

		record := ShareRecord{
			Timestamp:          time.Now(),
			Worker:             key,
			Algorithm:          algorithm,
			AchievedDifficulty: achievedDiff,
			WorkshareDiff:      workshareDiff,
			LuckPercent:        luckPercent,
			IsBlock:            isBlock,
		}
		ps.shareHistory = append(ps.shareHistory, record)

		// Keep only last 500 shares to avoid memory bloat
		if len(ps.shareHistory) > 500 {
			ps.shareHistory = ps.shareHistory[len(ps.shareHistory)-500:]
		}
	}
}

// BlockDiscovered records a found block
func (ps *PoolStats) BlockDiscovered(height uint64, hash, worker, algorithm, difficulty string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	block := BlockFound{
		Height:     height,
		Hash:       hash,
		Worker:     worker,
		Algorithm:  algorithm,
		Difficulty: difficulty,
		FoundAt:    time.Now(),
	}

	// Prepend to keep most recent first
	ps.blocks = append([]BlockFound{block}, ps.blocks...)

	// Keep only last 100 blocks
	if len(ps.blocks) > 100 {
		ps.blocks = ps.blocks[:100]
	}
}

// GetWorkers returns all worker stats
func (ps *PoolStats) GetWorkers() []WorkerStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	workers := make([]WorkerStats, 0, len(ps.workers))
	for _, w := range ps.workers {
		workers = append(workers, *w)
	}
	return workers
}

// GetConnectedWorkers returns only connected workers
func (ps *PoolStats) GetConnectedWorkers() []WorkerStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	workers := make([]WorkerStats, 0)
	for _, w := range ps.workers {
		if w.IsConnected {
			workers = append(workers, *w)
		}
	}
	return workers
}

// GetWorkersForAddress returns all workers for a specific address
func (ps *PoolStats) GetWorkersForAddress(address string) []WorkerStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	workers := make([]WorkerStats, 0)
	for _, w := range ps.workers {
		if w.Address == address {
			workers = append(workers, *w)
		}
	}
	return workers
}

// GetMinerAddresses returns unique miner addresses
func (ps *PoolStats) GetMinerAddresses() []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	addressMap := make(map[string]bool)
	for _, w := range ps.workers {
		addressMap[w.Address] = true
	}

	addresses := make([]string, 0, len(addressMap))
	for addr := range addressMap {
		addresses = append(addresses, addr)
	}
	return addresses
}

// GetBlocks returns found blocks
func (ps *PoolStats) GetBlocks() []BlockFound {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	blocks := make([]BlockFound, len(ps.blocks))
	copy(blocks, ps.blocks)
	return blocks
}

// GetShareHistory returns recent share records with difficulty info
func (ps *PoolStats) GetShareHistory() []ShareRecord {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	history := make([]ShareRecord, len(ps.shareHistory))
	copy(history, ps.shareHistory)
	return history
}

// GetShareHistoryByAlgorithm returns share history filtered by algorithm
func (ps *PoolStats) GetShareHistoryByAlgorithm(algorithm string) []ShareRecord {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	history := make([]ShareRecord, 0)
	for _, s := range ps.shareHistory {
		if s.Algorithm == algorithm {
			history = append(history, s)
		}
	}
	return history
}

// GetOverview returns summary statistics
func (ps *PoolStats) GetOverview() PoolOverview {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	connected := 0
	var totalHashrate float64
	var totalValidShares, totalStaleShares, totalInvalidShares uint64

	// Per-algorithm stats
	algoStats := map[string]*AlgorithmStats{
		"sha256": {},
		"scrypt": {},
		"kawpow": {},
	}

	for _, w := range ps.workers {
		algo := w.Algorithm
		if algo == "" {
			algo = "sha256" // default
		}
		if stats, ok := algoStats[algo]; ok {
			if w.IsConnected {
				stats.Hashrate += w.Hashrate
				stats.Workers++
			}
			stats.SharesValid += w.SharesValid
		}

		if w.IsConnected {
			connected++
			totalHashrate += w.Hashrate
		}
		totalValidShares += w.SharesValid
		totalStaleShares += w.SharesStale
		totalInvalidShares += w.SharesInvalid
	}

	// Network stats are fetched via RPC in the API layer, not calculated here

	return PoolOverview{
		WorkersTotal:     len(ps.workers),
		WorkersConnected: connected,
		Hashrate:         totalHashrate,
		SharesValid:      totalValidShares,
		SharesStale:      totalStaleShares,
		SharesInvalid:    totalInvalidShares,
		BlocksFound:      len(ps.blocks),
		Uptime:           time.Since(ps.startedAt).Seconds(),
		StartedAt:        ps.startedAt,
		SHA256:           *algoStats["sha256"],
		Scrypt:           *algoStats["scrypt"],
		KawPoW:           *algoStats["kawpow"],
	}
}

// AlgorithmStats contains per-algorithm statistics
type AlgorithmStats struct {
	Hashrate          float64 `json:"hashrate"`
	NetworkDifficulty float64 `json:"networkDifficulty"`
	NetworkHashrate   float64 `json:"networkHashrate"`
	Workers           int     `json:"workers"`
	SharesValid       uint64  `json:"sharesValid"`
}

// PoolOverview contains summary statistics
type PoolOverview struct {
	NodeName          string    `json:"nodeName,omitempty"` // Unique identifier for this node
	WorkersTotal      int       `json:"workersTotal"`
	WorkersConnected  int       `json:"workersConnected"`
	Hashrate          float64   `json:"hashrate"`
	SharesValid       uint64    `json:"sharesValid"`
	SharesStale       uint64    `json:"sharesStale"`
	SharesInvalid     uint64    `json:"sharesInvalid"`
	BlocksFound       int       `json:"blocksFound"`
	Uptime            float64   `json:"uptime"`
	StartedAt         time.Time `json:"startedAt"`
	NetworkHashrate   float64   `json:"networkHashrate,omitempty"`
	NetworkDifficulty float64   `json:"networkDifficulty,omitempty"`
	BlockHeight       uint64    `json:"blockHeight,omitempty"` // Current block height

	// Per-algorithm stats
	SHA256 AlgorithmStats `json:"sha256"`
	Scrypt AlgorithmStats `json:"scrypt"`
	KawPoW AlgorithmStats `json:"kawpow"`
}

// pruneShareWindow removes old share events outside the window
func (ps *PoolStats) pruneShareWindow() {
	cutoff := time.Now().Add(-ps.windowDuration)
	newWindow := make([]shareEvent, 0)
	for _, e := range ps.shareWindow {
		if e.timestamp.After(cutoff) {
			newWindow = append(newWindow, e)
		}
	}
	ps.shareWindow = newWindow
}

// calculateWorkerHashrate estimates hashrate based on cumulative work done
func (ps *PoolStats) calculateWorkerHashrate(address string) float64 {
	// Hashrate = (cumulative_work * scale) / time_window
	// CumulativeWork tracks the sum of difficulty for each valid share
	worker, exists := ps.workers[address]
	if !exists || worker.CumulativeWork == 0 {
		return 0
	}

	elapsed := time.Since(worker.ConnectedAt).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}

	// Use algorithm-specific scale factors
	// SHA: diff 1 = 2^32 hashes
	// Scrypt: diff 1 = 2^16 hashes (65536)
	// KawPoW: diff 1 = 2^32 hashes
	var scale float64
	switch worker.Algorithm {
	case "scrypt":
		scale = 65536 // 2^16
	default:
		scale = 4294967296 // 2^32
	}

	// Hashrate = (cumulative_work * scale) / time
	newHashRate := worker.CumulativeWork * scale / elapsed

	return (float64(c_hashRateEmaSamples-1)*worker.Hashrate + newHashRate) / float64(c_hashRateEmaSamples)
}

// GetTotalHashrate calculates pool-wide hashrate
func (ps *PoolStats) GetTotalHashrate() float64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var total float64
	for _, w := range ps.workers {
		if w.IsConnected {
			total += w.Hashrate
		}
	}
	return total
}
