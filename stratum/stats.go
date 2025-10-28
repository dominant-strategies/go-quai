package stratum

import (
	"sync"
	"time"
)

// WorkerStats tracks statistics for a connected miner
type WorkerStats struct {
	Address       string    `json:"address"`
	WorkerName    string    `json:"workerName"`
	Algorithm     string    `json:"algorithm"`
	ConnectedAt   time.Time `json:"connectedAt"`
	LastShareAt   time.Time `json:"lastShareAt"`
	SharesValid   uint64    `json:"sharesValid"`
	SharesStale   uint64    `json:"sharesStale"`
	SharesInvalid uint64    `json:"sharesInvalid"`
	Difficulty    float64   `json:"difficulty"`
	Hashrate      float64   `json:"hashrate"` // estimated from share rate
	IsConnected   bool      `json:"isConnected"`
}

// BlockFound represents a block discovered by the pool
type BlockFound struct {
	Height      uint64    `json:"height"`
	Hash        string    `json:"hash"`
	Worker      string    `json:"worker"`
	Algorithm   string    `json:"algorithm"`
	Difficulty  string    `json:"difficulty"`
	FoundAt     time.Time `json:"foundAt"`
}

// PoolStats aggregates all pool statistics
type PoolStats struct {
	workers     map[string]*WorkerStats
	blocks      []BlockFound
	totalShares uint64
	startedAt   time.Time
	mu          sync.RWMutex

	// Hashrate calculation
	shareWindow     []shareEvent
	windowDuration  time.Duration
}

type shareEvent struct {
	timestamp  time.Time
	difficulty float64
}

// NewPoolStats creates a new pool statistics tracker
func NewPoolStats() *PoolStats {
	return &PoolStats{
		workers:        make(map[string]*WorkerStats),
		blocks:         make([]BlockFound, 0),
		startedAt:      time.Now(),
		shareWindow:    make([]shareEvent, 0),
		windowDuration: 10 * time.Minute,
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
		key = address + "." + workerName
	}

	if worker, exists := ps.workers[key]; exists {
		worker.IsConnected = true
		worker.ConnectedAt = time.Now()
		if algorithm != "" {
			worker.Algorithm = algorithm
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
func (ps *PoolStats) WorkerDisconnected(address, workerName string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := address
	if workerName != "" {
		key = address + "." + workerName
	}

	if worker, exists := ps.workers[key]; exists {
		worker.IsConnected = false
	}
}

// ShareSubmitted records a share submission
func (ps *PoolStats) ShareSubmitted(address, workerName string, difficulty float64, valid bool, stale bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.totalShares++

	// Record share for hashrate calculation
	ps.shareWindow = append(ps.shareWindow, shareEvent{
		timestamp:  time.Now(),
		difficulty: difficulty,
	})
	ps.pruneShareWindow()

	key := address
	if workerName != "" {
		key = address + "." + workerName
	}

	if worker, exists := ps.workers[key]; exists {
		worker.LastShareAt = time.Now()
		worker.Difficulty = difficulty
		if valid {
			worker.SharesValid++
		} else if stale {
			worker.SharesStale++
		} else {
			worker.SharesInvalid++
		}
		worker.Hashrate = ps.calculateWorkerHashrate(key)
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

// GetBlocks returns found blocks
func (ps *PoolStats) GetBlocks() []BlockFound {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	blocks := make([]BlockFound, len(ps.blocks))
	copy(blocks, ps.blocks)
	return blocks
}

// GetOverview returns summary statistics
func (ps *PoolStats) GetOverview() PoolOverview {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	connected := 0
	var totalHashrate float64
	var totalValidShares, totalStaleShares, totalInvalidShares uint64

	for _, w := range ps.workers {
		if w.IsConnected {
			connected++
			totalHashrate += w.Hashrate
		}
		totalValidShares += w.SharesValid
		totalStaleShares += w.SharesStale
		totalInvalidShares += w.SharesInvalid
	}

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
	}
}

// PoolOverview contains summary statistics
type PoolOverview struct {
	WorkersTotal     int       `json:"workersTotal"`
	WorkersConnected int       `json:"workersConnected"`
	Hashrate         float64   `json:"hashrate"`
	SharesValid      uint64    `json:"sharesValid"`
	SharesStale      uint64    `json:"sharesStale"`
	SharesInvalid    uint64    `json:"sharesInvalid"`
	BlocksFound      int       `json:"blocksFound"`
	Uptime           float64   `json:"uptime"`
	StartedAt        time.Time `json:"startedAt"`
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

// calculateWorkerHashrate estimates hashrate based on recent shares
func (ps *PoolStats) calculateWorkerHashrate(address string) float64 {
	// Simple estimation: (shares * difficulty) / time_window
	// This is a rough estimate; real pools use more sophisticated methods
	worker, exists := ps.workers[address]
	if !exists || worker.SharesValid == 0 {
		return 0
	}

	elapsed := time.Since(worker.ConnectedAt).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}

	// Hashrate = (shares * difficulty * 2^32) / time
	// Simplified: just use difficulty * shares / time for relative comparison
	return float64(worker.SharesValid) * worker.Difficulty * 4294967296 / elapsed
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
