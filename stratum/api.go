package stratum

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/gorilla/websocket"
)

// API serves HTTP endpoints for the pool dashboard
type API struct {
	addr      string
	nodeName  string
	server    *http.Server
	stats     *PoolStats
	backend   quaiapi.Backend
	wsClients map[*websocket.Conn]bool
	wsMu      sync.RWMutex
	quit      chan struct{}
}

// NewAPI creates a new API server
func NewAPI(addr string, nodeName string, stats *PoolStats, backend quaiapi.Backend) *API {
	return &API{
		addr:      addr,
		nodeName:  nodeName,
		stats:     stats,
		backend:   backend,
		wsClients: make(map[*websocket.Conn]bool),
		quit:      make(chan struct{}),
	}
}

// Start begins serving the HTTP API
func (a *API) Start() error {
	mux := http.NewServeMux()

	// Standard pool API endpoints (for aggregators like miningpoolstats)
	mux.HandleFunc("/api/stats", a.handleStats)
	mux.HandleFunc("/api/miners", a.handleMiners)
	mux.HandleFunc("/api/blocks", a.handleBlocks)
	mux.HandleFunc("/api/payments", a.handlePayments)

	// Pool-wide endpoints (dashboard)
	mux.HandleFunc("/api/pool/stats", a.handlePoolStats)
	mux.HandleFunc("/api/pool/blocks", a.handlePoolBlocks)
	mux.HandleFunc("/api/pool/shares", a.handlePoolShares)
	mux.HandleFunc("/api/pool/workers", a.handlePoolWorkers)

	// Miner-specific endpoints (address-scoped)
	mux.HandleFunc("/api/miner/", a.handleMiner)

	// WebSocket for real-time updates
	mux.HandleFunc("/api/ws", a.handleWebSocket)

	// Node info endpoint (for multi-node aggregation)
	mux.HandleFunc("/api/node/info", a.handleNodeInfo)

	// Health check
	mux.HandleFunc("/health", a.handleHealth)

	a.server = &http.Server{
		Addr:         a.addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start background broadcast loop for WebSocket clients
	go a.broadcastLoop()

	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
		}
	}()

	return nil
}

// Stop gracefully shuts down the API server
func (a *API) Stop() error {
	close(a.quit)

	// Close all WebSocket connections
	a.wsMu.Lock()
	for conn := range a.wsClients {
		conn.Close()
	}
	a.wsMu.Unlock()

	if a.server != nil {
		return a.server.Close()
	}
	return nil
}

// handlePoolStats returns pool-wide statistics
func (a *API) handlePoolStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	overview := a.getExtendedOverview()
	writeJSON(w, overview)
}

// getExtendedOverview returns pool stats with network info and top miners
func (a *API) getExtendedOverview() PoolOverview {
	overview := a.stats.GetOverview()

	// Add node name for multi-node aggregation
	overview.NodeName = a.nodeName

	// Add current block height from backend
	if a.backend != nil {
		if block := a.backend.CurrentBlock(); block != nil {
			overview.BlockHeight = block.NumberU64(common.ZONE_CTX) // Zone 0
		}
	}

	return overview
}

// handlePoolBlocks returns blocks found by the pool
func (a *API) handlePoolBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	blocks := a.stats.GetBlocks()
	writeJSON(w, blocks)
}

// ShareHistoryResponse contains share history with difficulty info for solo mining
type ShareHistoryResponse struct {
	Shares         []ShareRecord `json:"shares"`
	WorkshareDiff  float64       `json:"workshareDiff"` // Current workshare difficulty
	AverageLuck    float64       `json:"averageLuck"`   // Average luck % across all shares
	BestShareLuck  float64       `json:"bestShareLuck"` // Best share luck %
	TotalShares    int           `json:"totalShares"`
	BlocksFound    int           `json:"blocksFound"`    // Shares that met workshare diff
	ExpectedShares float64       `json:"expectedShares"` // Expected shares to find a block
}

// handlePoolShares returns share history with difficulty info for solo mining luck tracking
func (a *API) handlePoolShares(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get optional algorithm filter
	algorithm := r.URL.Query().Get("algorithm")

	var shares []ShareRecord
	if algorithm != "" {
		shares = a.stats.GetShareHistoryByAlgorithm(algorithm)
	} else {
		shares = a.stats.GetShareHistory()
	}

	// Calculate aggregate stats
	var totalLuck, bestLuck, currentWorkDiff float64
	var blocksFound int

	for _, s := range shares {
		totalLuck += s.LuckPercent
		if s.LuckPercent > bestLuck {
			bestLuck = s.LuckPercent
		}
		if s.IsBlock {
			blocksFound++
		}
		currentWorkDiff = s.WorkshareDiff // Use latest workshare diff
	}

	avgLuck := 0.0
	if len(shares) > 0 {
		avgLuck = totalLuck / float64(len(shares))
	}

	// Expected shares = workshare_diff / pool_diff
	// This tells you how many shares you'd expect before finding a block
	expectedShares := 0.0
	if len(shares) > 0 && currentWorkDiff > 0 {
		// Use the average achieved difficulty to estimate expected shares
		avgAchieved := 0.0
		for _, s := range shares {
			avgAchieved += s.AchievedDifficulty
		}
		avgAchieved /= float64(len(shares))
		if avgAchieved > 0 {
			expectedShares = currentWorkDiff / avgAchieved
		}
	}

	writeJSON(w, ShareHistoryResponse{
		Shares:         shares,
		WorkshareDiff:  currentWorkDiff,
		AverageLuck:    avgLuck,
		BestShareLuck:  bestLuck,
		TotalShares:    len(shares),
		BlocksFound:    blocksFound,
		ExpectedShares: expectedShares,
	})
}

// handlePoolWorkers returns all connected workers
func (a *API) handlePoolWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workers := a.stats.GetConnectedWorkers()
	writeJSON(w, workers)
}

// handleMiner routes miner-specific requests
// Routes: /api/miner/{address}/stats, /api/miner/{address}/workers
func (a *API) handleMiner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/miner/{address}/{endpoint}
	path := strings.TrimPrefix(r.URL.Path, "/api/miner/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "invalid path, expected /api/miner/{address}/{stats|workers}", http.StatusBadRequest)
		return
	}

	address := parts[0]
	endpoint := parts[1]

	switch endpoint {
	case "stats":
		a.handleMinerStats(w, address)
	case "workers":
		a.handleMinerWorkers(w, address)
	default:
		http.Error(w, "unknown endpoint", http.StatusNotFound)
	}
}

// handleMinerStats returns stats for a specific miner address
func (a *API) handleMinerStats(w http.ResponseWriter, address string) {
	workers := a.stats.GetWorkersForAddress(address)

	var hashrateSHA, hashrateScrypt, hashrateKawPoW float64
	var totalValid, totalStale, totalInvalid uint64
	var connected int

	for _, worker := range workers {
		totalValid += worker.SharesValid
		totalStale += worker.SharesStale
		totalInvalid += worker.SharesInvalid
		if worker.IsConnected {
			connected++
		}

		// Aggregate hashrate by algorithm
		switch worker.Algorithm {
		case "sha256":
			hashrateSHA += worker.Hashrate
		case "scrypt":
			hashrateScrypt += worker.Hashrate
		case "kawpow":
			hashrateKawPoW += worker.Hashrate
		}
	}

	stats := MinerStats{
		Address:          address,
		WorkersTotal:     len(workers),
		WorkersConnected: connected,
		HashrateSHA:      hashrateSHA,
		HashrateScrypt:   hashrateScrypt,
		HashrateKawPoW:   hashrateKawPoW,
		SharesValid:      totalValid,
		SharesStale:      totalStale,
		SharesInvalid:    totalInvalid,
	}

	writeJSON(w, stats)
}

// handleMinerWorkers returns workers for a specific miner address
func (a *API) handleMinerWorkers(w http.ResponseWriter, address string) {
	workers := a.stats.GetWorkersForAddress(address)
	writeJSON(w, workers)
}

// handleHealth returns a simple health check
func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNodeInfo returns node identification info for multi-node aggregation
func (a *API) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, map[string]interface{}{
		"nodeName":  a.nodeName,
		"version":   "1.0.0",
		"timestamp": time.Now().Unix(),
	})
}

// =============================================================================
// Standard Pool API Endpoints (for aggregators like miningpoolstats.stream)
// =============================================================================

// PoolStatsResponse is the standard format expected by mining pool aggregators
type PoolStatsResponse struct {
	// Pool info
	Name       string  `json:"name"`
	NodeName   string  `json:"nodeName"` // Unique node identifier for multi-node pools
	Symbol     string  `json:"symbol"`
	Algorithm  string  `json:"algorithm"`
	PoolFee    float64 `json:"poolFee"`
	PoolFeeStr string  `json:"poolFeeStr"`
	MinPayout  float64 `json:"minPayout"`

	// Current stats
	Hashrate        float64 `json:"hashrate"`
	HashrateStr     string  `json:"hashrateStr"`
	Workers         int     `json:"workers"`
	Miners          int     `json:"miners"`
	BlocksFound     int     `json:"blocksFound"`
	BlocksPending   int     `json:"blocksPending"`
	BlocksConfirmed int     `json:"blocksConfirmed"`

	// Network stats
	NetworkHashrate    float64 `json:"networkHashrate"`
	NetworkDifficulty  float64 `json:"networkDifficulty"`
	NetworkBlockHeight uint64  `json:"networkBlockHeight"`

	// Timestamps
	LastBlockFound int64 `json:"lastBlockFound"`
	UpdatedAt      int64 `json:"updatedAt"`
}

// handleStats returns pool stats in standard format for aggregators
func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	overview := a.getExtendedOverview()
	miners := a.stats.GetMinerAddresses()
	blocks := a.stats.GetBlocks()

	// Get last block timestamp
	var lastBlockFound int64
	if len(blocks) > 0 {
		lastBlockFound = blocks[0].FoundAt.Unix()
	}

	// Get block height from backend
	var blockHeight uint64
	if a.backend != nil {
		if block := a.backend.CurrentBlock(); block != nil {
			blockHeight = block.NumberU64(0) // Zone 0
		}
	}

	response := PoolStatsResponse{
		// Pool info - customize these
		Name:       "Quai DePool",
		NodeName:   a.nodeName,
		Symbol:     "QUAI",
		Algorithm:  "ProgPoW/SHA3/Scrypt",
		PoolFee:    0.0, // Non-custodial = no fee
		PoolFeeStr: "0%",
		MinPayout:  0.0, // Non-custodial = direct payouts

		// Current stats
		Hashrate:        overview.Hashrate,
		HashrateStr:     formatHashrate(overview.Hashrate),
		Workers:         overview.WorkersConnected,
		Miners:          len(miners),
		BlocksFound:     overview.BlocksFound,
		BlocksPending:   0, // Non-custodial pools don't track pending
		BlocksConfirmed: overview.BlocksFound,

		// Network stats
		NetworkHashrate:    overview.NetworkHashrate,
		NetworkDifficulty:  overview.NetworkDifficulty,
		NetworkBlockHeight: blockHeight,

		// Timestamps
		LastBlockFound: lastBlockFound,
		UpdatedAt:      time.Now().Unix(),
	}

	writeJSON(w, response)
}

// MinerListEntry represents a miner in the miners list
type MinerListEntry struct {
	Address   string  `json:"address"`
	Hashrate  float64 `json:"hashrate"`
	Workers   int     `json:"workers"`
	LastShare int64   `json:"lastShare"`
}

// MinersResponse is the response format for /api/miners
type MinersResponse struct {
	Now         int64            `json:"now"`
	Miners      []MinerListEntry `json:"miners"`
	Hashrate    float64          `json:"hashrate"`
	MinersTotal int              `json:"minersTotal"`
}

// handleMiners returns list of all miners
func (a *API) handleMiners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	minerAddrs := a.stats.GetMinerAddresses()
	var miners []MinerListEntry
	var totalHashrate float64

	for _, addr := range minerAddrs {
		workers := a.stats.GetWorkersForAddress(addr)
		var hashrate float64
		var lastShare int64

		for _, w := range workers {
			hashrate += w.Hashrate
			wsTime := w.LastShareAt.Unix()
			if wsTime > lastShare {
				lastShare = wsTime
			}
		}

		miners = append(miners, MinerListEntry{
			Address:   addr,
			Hashrate:  hashrate,
			Workers:   len(workers),
			LastShare: lastShare,
		})
		totalHashrate += hashrate
	}

	writeJSON(w, MinersResponse{
		Now:         time.Now().Unix(),
		Miners:      miners,
		Hashrate:    totalHashrate,
		MinersTotal: len(miners),
	})
}

// BlockEntry represents a block in the blocks list
type BlockEntry struct {
	Height     uint64 `json:"height"`
	Hash       string `json:"hash"`
	Miner      string `json:"miner"`
	Worker     string `json:"worker"`
	Difficulty string `json:"difficulty"`
	Reward     string `json:"reward"`
	Timestamp  int64  `json:"timestamp"`
	Confirmed  bool   `json:"confirmed"`
}

// BlocksResponse is the response format for /api/blocks
type BlocksResponse struct {
	Matured         []BlockEntry       `json:"matured"`
	MaturedTotal    int                `json:"maturedTotal"`
	Immature        []BlockEntry       `json:"immature"`
	ImmatureTotal   int                `json:"immatureTotal"`
	Candidates      []BlockEntry       `json:"candidates"`
	CandidatesTotal int                `json:"candidatesTotal"`
	Luck            map[string]float64 `json:"luck"`
}

// handleBlocks returns block history
func (a *API) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	blocks := a.stats.GetBlocks()
	var matured []BlockEntry

	for _, b := range blocks {
		// Calculate an estimate of the block reward
		reward := a.calculateBlockReward()

		matured = append(matured, BlockEntry{
			Height:     b.Height,
			Hash:       b.Hash,
			Miner:      b.Worker, // Worker address is the miner
			Worker:     b.Worker,
			Difficulty: b.Difficulty,
			Reward:     reward,
			Timestamp:  b.FoundAt.Unix(),
			Confirmed:  true, // Non-custodial = immediate
		})
	}

	// Calculate simple luck (blocks found / expected based on shares)
	luck := make(map[string]float64)
	luck["64"] = 100.0 // Placeholder
	luck["128"] = 100.0
	luck["256"] = 100.0

	writeJSON(w, BlocksResponse{
		Matured:         matured,
		MaturedTotal:    len(matured),
		Immature:        []BlockEntry{},
		ImmatureTotal:   0,
		Candidates:      []BlockEntry{},
		CandidatesTotal: 0,
		Luck:            luck,
	})
}

// calculateBlockReward calculates the estimated workshare reward for a block
func (a *API) calculateBlockReward() string {
	if a.backend == nil {
		return "0"
	}

	// Get the block
	ctx := context.Background()
	currentHeader := a.backend.CurrentHeader()
	if currentHeader == nil {
		return "0"
	}

	// Get the prime terminus to get the exchange rate
	primeTerminus, err := a.backend.BlockByHash(ctx, currentHeader.PrimeTerminusHash())
	if err != nil || primeTerminus == nil {
		return "0"
	}

	exchangeRate := primeTerminus.ExchangeRate()
	difficulty := currentHeader.Difficulty()

	// Calculate the base block reward
	baseBlockReward := misc.CalculateReward(currentHeader.WorkObjectHeader(), difficulty, exchangeRate)

	// Calculate workshare reward = baseBlockReward / (ExpectedWorksharesPerBlock + 1)
	workshareReward := new(big.Int).Div(baseBlockReward, big.NewInt(int64(params.ExpectedWorksharesPerBlock+1)))

	// Convert from "its" (10^-18) to human readable QUAI
	// reward is in wei-equivalent, divide by 10^18 for display
	rewardFloat := new(big.Float).SetInt(workshareReward)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	rewardFloat.Quo(rewardFloat, divisor)

	return rewardFloat.Text('f', 6)
}

// PaymentEntry represents a payment
type PaymentEntry struct {
	TxHash    string `json:"txHash"`
	Address   string `json:"address"`
	Amount    string `json:"amount"`
	Timestamp int64  `json:"timestamp"`
}

// PaymentsResponse is the response format for /api/payments
type PaymentsResponse struct {
	Payments      []PaymentEntry `json:"payments"`
	PaymentsTotal int            `json:"paymentsTotal"`
}

// handlePayments returns payment history (for non-custodial, this is empty as payments are direct)
func (a *API) handlePayments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Non-custodial pool: block rewards go directly to miners
	// No separate payment tracking needed
	writeJSON(w, PaymentsResponse{
		Payments:      []PaymentEntry{},
		PaymentsTotal: 0,
	})
}

// formatHashrate formats hashrate for display
func formatHashrate(h float64) string {
	if h >= 1e15 {
		return fmt.Sprintf("%.2f PH/s", h/1e15)
	}
	if h >= 1e12 {
		return fmt.Sprintf("%.2f TH/s", h/1e12)
	}
	if h >= 1e9 {
		return fmt.Sprintf("%.2f GH/s", h/1e9)
	}
	if h >= 1e6 {
		return fmt.Sprintf("%.2f MH/s", h/1e6)
	}
	if h >= 1e3 {
		return fmt.Sprintf("%.2f KH/s", h/1e3)
	}
	return fmt.Sprintf("%.2f H/s", h)
}

// MinerStats contains aggregated stats for a miner address
type MinerStats struct {
	Address          string  `json:"address"`
	WorkersTotal     int     `json:"workersTotal"`
	WorkersConnected int     `json:"workersConnected"`
	HashrateSHA      float64 `json:"hashrateSha"`
	HashrateScrypt   float64 `json:"hashrateScrypt"`
	HashrateKawPoW   float64 `json:"hashrateKawpow"`
	SharesValid      uint64  `json:"sharesValid"`
	SharesStale      uint64  `json:"sharesStale"`
	SharesInvalid    uint64  `json:"sharesInvalid"`
}

// WebSocket handling

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dashboard
	},
}

func (a *API) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	a.wsMu.Lock()
	a.wsClients[conn] = true
	a.wsMu.Unlock()

	// Read loop (handles ping/pong and client disconnect)
	go func() {
		defer func() {
			a.wsMu.Lock()
			delete(a.wsClients, conn)
			a.wsMu.Unlock()
			conn.Close()
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()
}

// broadcastLoop sends periodic updates to all WebSocket clients
func (a *API) broadcastLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
			a.broadcastUpdate()
		}
	}
}

func (a *API) broadcastUpdate() {
	update := map[string]interface{}{
		"type":      "update",
		"timestamp": time.Now().Unix(),
		"pool":      a.getExtendedOverview(),
		"workers":   a.stats.GetConnectedWorkers(),
	}

	data, err := json.Marshal(update)
	if err != nil {
		return
	}

	// Collect failed connections while holding read lock
	a.wsMu.RLock()
	var failedConns []*websocket.Conn
	for conn := range a.wsClients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			conn.Close()
			failedConns = append(failedConns, conn)
		}
	}
	a.wsMu.RUnlock()

	// Delete failed connections with write lock
	if len(failedConns) > 0 {
		a.wsMu.Lock()
		for _, conn := range failedConns {
			delete(a.wsClients, conn)
		}
		a.wsMu.Unlock()
	}
}

// Helpers

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
