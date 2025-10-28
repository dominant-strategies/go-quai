package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/stratum"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

//go:embed ui/*
var uiFS embed.FS

// Dashboard serves the web UI and API for monitoring the node
type Dashboard struct {
	addr       string
	server     *http.Server
	logger     *logrus.Logger
	stratum    *stratum.Server
	p2pStats   P2PStatsProvider
	nodeStats  NodeStatsProvider
	blockchain BlockchainProvider

	// Connection URLs
	rpcAddr           string
	wsAddr            string
	stratumSHAAddr    string
	stratumScryptAddr string
	stratumKawpowAddr string

	// Node info
	version   string
	network   string
	location  string
	chainID   int64
	startTime time.Time

	// WebSocket clients
	wsClients  map[*websocket.Conn]bool
	wsMu       sync.RWMutex

	// Shutdown
	quit       chan struct{}
}

// P2PStatsProvider interface for P2P network statistics
type P2PStatsProvider interface {
	GetPeerCount() int
	GetPeers() []PeerInfo
}

// PeerInfo contains information about a connected peer
type PeerInfo struct {
	ID        string  `json:"id"`
	Address   string  `json:"address"`
	Latency   int64   `json:"latency"`
	Direction string  `json:"direction"` // inbound/outbound
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Country   string  `json:"country,omitempty"`
	City      string  `json:"city,omitempty"`
}

// NodeStatsProvider interface for node statistics
type NodeStatsProvider interface {
	GetSyncStatus() SyncStatus
	GetChainInfo() ChainInfo
}

// SyncStatus contains sync information
type SyncStatus struct {
	IsSyncing     bool   `json:"isSyncing"`
	CurrentBlock  uint64 `json:"currentBlock"`
	HighestBlock  uint64 `json:"highestBlock"`
	SyncProgress  float64 `json:"syncProgress"` // 0.0 to 1.0
}

// ChainInfo contains blockchain information
type ChainInfo struct {
	ChainID     string `json:"chainId"`
	NetworkName string `json:"networkName"`
	BlockHeight uint64 `json:"blockHeight"`
}

// BlockchainProvider interface for querying the blockchain
type BlockchainProvider interface {
	CurrentBlock() *types.WorkObject
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
}

// BlockInfo contains information about a mined block
type BlockInfo struct {
	Height    uint64    `json:"height"`
	Hash      string    `json:"hash"`
	Coinbase  string    `json:"coinbase"`
	Timestamp time.Time `json:"timestamp"`
	TxCount   int       `json:"txCount"`
}

// NodeInfo contains node information for the dashboard
type NodeInfo struct {
	Version     string `json:"version"`
	Network     string `json:"network"`
	Location    string `json:"location"`
	BlockHeight uint64 `json:"blockHeight"`
	ChainID     string `json:"chainId"`
	Uptime      int64  `json:"uptime"` // seconds
}

// ConnectionURLs contains the various connection endpoints
type ConnectionURLs struct {
	RPC           string `json:"rpc"`
	WS            string `json:"ws"`
	StratumSHA    string `json:"stratumSha,omitempty"`
	StratumScrypt string `json:"stratumScrypt,omitempty"`
	StratumKawpow string `json:"stratumKawpow,omitempty"`
}

// Config holds dashboard configuration
type Config struct {
	Addr            string
	Stratum         *stratum.Server
	P2P             P2PStatsProvider
	Node            NodeStatsProvider
	Blockchain      BlockchainProvider
	RPCAddr         string
	WSAddr          string
	StratumSHAAddr    string
	StratumScryptAddr string
	StratumKawpowAddr string
	Version         string
	Network         string
	Location        string
	ChainID         int64
}

// New creates a new dashboard server
func New(cfg Config) *Dashboard {
	logger := log.NewLogger("dashboard.log", viper.GetString(utils.LogLevelFlag.Name), viper.GetInt(utils.LogSizeFlag.Name))

	return &Dashboard{
		addr:              cfg.Addr,
		logger:            logger,
		stratum:           cfg.Stratum,
		p2pStats:          cfg.P2P,
		nodeStats:         cfg.Node,
		blockchain:        cfg.Blockchain,
		rpcAddr:           cfg.RPCAddr,
		wsAddr:            cfg.WSAddr,
		stratumSHAAddr:    cfg.StratumSHAAddr,
		stratumScryptAddr: cfg.StratumScryptAddr,
		stratumKawpowAddr: cfg.StratumKawpowAddr,
		version:           cfg.Version,
		network:           cfg.Network,
		location:          cfg.Location,
		chainID:           cfg.ChainID,
		startTime:         time.Now(),
		wsClients:         make(map[*websocket.Conn]bool),
		quit:              make(chan struct{}),
	}
}

// Start begins serving the dashboard
func (d *Dashboard) Start() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/overview", d.handleOverview)
	mux.HandleFunc("/api/workers", d.handleWorkers)
	mux.HandleFunc("/api/blocks", d.handleBlocks)
	mux.HandleFunc("/api/peers", d.handlePeers)
	mux.HandleFunc("/api/sync", d.handleSync)
	mux.HandleFunc("/api/ws", d.handleWebSocket)

	// Serve static UI files
	uiContent, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(uiContent)))

	d.server = &http.Server{
		Addr:         d.addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start background broadcast loop
	go d.broadcastLoop()

	d.logger.WithField("addr", d.addr).Info("Dashboard server starting")

	go func() {
		if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			d.logger.WithField("error", err).Error("Dashboard server error")
		}
	}()

	return nil
}

// Stop gracefully shuts down the dashboard
func (d *Dashboard) Stop() error {
	close(d.quit)

	// Close all WebSocket connections
	d.wsMu.Lock()
	for conn := range d.wsClients {
		conn.Close()
	}
	d.wsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return d.server.Shutdown(ctx)
}

// API Handlers

func (d *Dashboard) handleOverview(w http.ResponseWriter, r *http.Request) {
	overview := make(map[string]interface{})

	// Stratum stats
	if d.stratum != nil {
		stats := d.stratum.Stats()
		overview["stratum"] = stats.GetOverview()
	}

	// P2P stats
	if d.p2pStats != nil {
		overview["peerCount"] = d.p2pStats.GetPeerCount()
	}

	// Node stats
	if d.nodeStats != nil {
		overview["sync"] = d.nodeStats.GetSyncStatus()
		overview["chain"] = d.nodeStats.GetChainInfo()
	}

	// Node info and URLs
	overview["nodeInfo"] = d.getNodeInfo()
	overview["urls"] = d.getConnectionURLs()

	writeJSON(w, overview)
}

func (d *Dashboard) handleWorkers(w http.ResponseWriter, r *http.Request) {
	if d.stratum == nil {
		writeJSON(w, []interface{}{})
		return
	}

	workers := d.stratum.Stats().GetConnectedWorkers()
	writeJSON(w, workers)
}

func (d *Dashboard) handleBlocks(w http.ResponseWriter, r *http.Request) {
	blocks := []BlockInfo{}

	// Query recent blocks from the blockchain
	if d.blockchain != nil {
		ctx := context.Background()
		current := d.blockchain.CurrentBlock()
		if current != nil {
			currentHeight := current.NumberU64(2) // ZONE_CTX = 2
			// Get last 20 blocks
			limit := 20
			for i := 0; i < limit && currentHeight >= uint64(i); i++ {
				height := currentHeight - uint64(i)
				block, err := d.blockchain.HeaderByNumber(ctx, rpc.BlockNumber(height))
				if err != nil || block == nil {
					continue
				}
				coinbase := block.PrimaryCoinbase().Hex()
				blockInfo := BlockInfo{
					Height:    height,
					Hash:      block.Hash().Hex(),
					Coinbase:  coinbase,
					Timestamp: time.Unix(int64(block.Time()), 0),
					TxCount:   len(block.Transactions()),
				}
				blocks = append(blocks, blockInfo)
			}
		}
	}

	writeJSON(w, blocks)
}

func (d *Dashboard) handlePeers(w http.ResponseWriter, r *http.Request) {
	if d.p2pStats == nil {
		writeJSON(w, []interface{}{})
		return
	}

	peers := d.p2pStats.GetPeers()
	writeJSON(w, peers)
}

func (d *Dashboard) handleSync(w http.ResponseWriter, r *http.Request) {
	if d.nodeStats == nil {
		writeJSON(w, SyncStatus{})
		return
	}

	status := d.nodeStats.GetSyncStatus()
	writeJSON(w, status)
}

// WebSocket handling

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local dashboard
	},
}

func (d *Dashboard) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		d.logger.WithField("error", err).Error("WebSocket upgrade failed")
		return
	}

	d.wsMu.Lock()
	d.wsClients[conn] = true
	d.wsMu.Unlock()

	d.logger.Debug("WebSocket client connected")

	// Read loop (for ping/pong and client messages)
	go func() {
		defer func() {
			d.wsMu.Lock()
			delete(d.wsClients, conn)
			d.wsMu.Unlock()
			conn.Close()
			d.logger.Debug("WebSocket client disconnected")
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
func (d *Dashboard) broadcastLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.quit:
			return
		case <-ticker.C:
			d.broadcastUpdate()
		}
	}
}

func (d *Dashboard) broadcastUpdate() {
	update := make(map[string]interface{})
	update["type"] = "update"
	update["timestamp"] = time.Now().Unix()

	if d.stratum != nil {
		stats := d.stratum.Stats()
		update["stratum"] = map[string]interface{}{
			"overview": stats.GetOverview(),
			"workers":  stats.GetConnectedWorkers(),
		}
	}

	if d.p2pStats != nil {
		update["peerCount"] = d.p2pStats.GetPeerCount()
	}

	if d.nodeStats != nil {
		update["sync"] = d.nodeStats.GetSyncStatus()
	}

	// Include node info (updates uptime)
	update["nodeInfo"] = d.getNodeInfo()

	data, err := json.Marshal(update)
	if err != nil {
		return
	}

	d.wsMu.RLock()
	defer d.wsMu.RUnlock()

	for conn := range d.wsClients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			conn.Close()
			delete(d.wsClients, conn)
		}
	}
}

// Node Info helpers

func (d *Dashboard) getNodeInfo() NodeInfo {
	info := NodeInfo{
		Version:  d.version,
		Network:  d.network,
		Location: d.location,
		ChainID:  fmt.Sprintf("%d", d.chainID),
		Uptime:   int64(time.Since(d.startTime).Seconds()),
	}

	// Get current block height from blockchain
	if d.blockchain != nil {
		current := d.blockchain.CurrentBlock()
		if current != nil {
			info.BlockHeight = current.NumberU64(2) // ZONE_CTX = 2
		}
	}

	return info
}

func (d *Dashboard) getConnectionURLs() ConnectionURLs {
	return ConnectionURLs{
		RPC:           d.rpcAddr,
		WS:            d.wsAddr,
		StratumSHA:    d.stratumSHAAddr,
		StratumScrypt: d.stratumScryptAddr,
		StratumKawpow: d.stratumKawpowAddr,
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
