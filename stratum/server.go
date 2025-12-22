package stratum

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/kawpow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	seenSharesSize = 1024 // Size of LRU cache for seen shares per session
	jobsCacheSize  = 32
)

// StratumConfig holds configuration for the stratum server
type StratumConfig struct {
	SHAAddr        string // Address for SHA miners (e.g., ":3333")
	ScryptAddr     string // Address for Scrypt miners (e.g., ":4444")
	KawpowAddr     string // Address for Kawpow miners (e.g., ":5555")
	VarDiffEnabled bool   // Enable automatic variable difficulty for liveness (default true)
	JobWorkerCount int    // Number of workers in the job broadcast pool (default 4)
	MaxConnections int    // Maximum concurrent connections (default 10000, 0 = unlimited)
}

// Connection limits and liveness timeout constants
const (
	defaultMaxConnections = 10000           // Default max concurrent connections
	livenessTimeout       = 5 * time.Minute // Close connection if no share within this time
)

// templateState tracks the last template sent for change detection
type templateState struct {
	sealHash   common.Hash // For sha256/scrypt: header seal hash
	parentHash common.Hash // For change detection (new block)
	height     uint64      // Block height
	quaiHeight int64       // Quai chain height (for kawpow stale detection)
}

// jobTask represents a task to send a job to a session
type jobTask struct {
	sess  *session
	clean bool
}

// jobWorkerPool manages a fixed pool of workers for sending jobs to miners.
// This prevents unbounded goroutine growth with thousands of concurrent miners.
type jobWorkerPool struct {
	tasks   chan jobTask
	server  *Server
	workers int
	wg      sync.WaitGroup
	stopped chan struct{}
}

const (
	defaultJobWorkerCount  = 4
	jobWorkerPoolQueueSize = 10000 // Buffered channel size
)

// newJobWorkerPool creates a new worker pool for job broadcasting.
func newJobWorkerPool(server *Server, workers int) *jobWorkerPool {
	if workers <= 0 {
		workers = defaultJobWorkerCount
	}
	return &jobWorkerPool{
		tasks:   make(chan jobTask, jobWorkerPoolQueueSize),
		server:  server,
		workers: workers,
		stopped: make(chan struct{}),
	}
}

// start launches the worker goroutines.
func (p *jobWorkerPool) start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	p.server.logger.WithField("workers", p.workers).Info("Job worker pool started")
}

// worker processes job tasks from the queue.
func (p *jobWorkerPool) worker(_ int) {
	defer p.wg.Done()
	for {
		select {
		case task, ok := <-p.tasks:
			if !ok {
				return // Channel closed
			}
			if err := p.server.sendJobAndNotify(task.sess, task.clean); err != nil {
				p.server.logger.WithFields(log.Fields{
					"user":   task.sess.user,
					"worker": task.sess.workerName,
					"error":  err,
				}).Debug("Worker pool: failed to send job")
			}
		case <-p.stopped:
			return
		}
	}
}

// submit queues a job task for a worker to process.
// Returns true if the task was queued, false if the queue is full or pool is stopped.
func (p *jobWorkerPool) submit(sess *session, clean bool) bool {
	select {
	case <-p.stopped:
		// Pool is stopped, don't attempt to send (would panic on closed channel)
		return false
	default:
	}

	select {
	case p.tasks <- jobTask{sess: sess, clean: clean}:
		return true
	case <-p.stopped:
		// Pool stopped while we were trying to send
		return false
	default:
		// Queue full - log and drop to prevent blocking
		p.server.logger.WithFields(log.Fields{
			"user":      sess.user,
			"worker":    sess.workerName,
			"queueSize": len(p.tasks),
		}).Warn("Job worker pool queue full, dropping task")
		return false
	}
}

// stop gracefully shuts down the worker pool.
func (p *jobWorkerPool) stop() {
	close(p.stopped)
	close(p.tasks)
	p.wg.Wait()
	p.server.logger.Info("Job worker pool stopped")
}

// Stratum v1 server implementing subscribe/authorize/notify/submit using AuxPow from getPendingHeader.
type Server struct {
	config  StratumConfig
	backend quaiapi.Backend
	// Per-algorithm listeners
	lnSHA    net.Listener
	lnScrypt net.Listener
	lnKawpow net.Listener
	logger   *logrus.Logger
	stats    *PoolStats
	// kawpow engine for share verification
	kawpowEngine *kawpow.Kawpow
	// Template tracking for change detection (per algorithm)
	templateMu         sync.RWMutex
	lastTemplateSHA    *templateState
	lastTemplateScrypt *templateState
	lastTemplateKawpow *templateState
	// Connected sessions for broadcasting (per algorithm)
	sessionsMu     sync.RWMutex
	sessionsSHA    map[*session]struct{}
	sessionsScrypt map[*session]struct{}
	sessionsKawpow map[*session]struct{}
	// Worker pool for job broadcasting (prevents unbounded goroutine growth)
	jobPool *jobWorkerPool
	// Connection limit tracking (DDoS protection)
	activeConns    atomic.Int64
	maxConnections int
	// Shutdown signaling
	stopped chan struct{}
	wg      sync.WaitGroup // tracks background goroutines (template polling, force broadcast)
}

// NewServer creates a new stratum server with the given configuration.
// For backward compatibility, if config has empty addresses, it uses the legacy single-port mode.
func NewServer(addr string, backend quaiapi.Backend) *Server {
	// Legacy single-port mode - default to SHA
	return NewServerWithConfig(StratumConfig{SHAAddr: addr}, backend)
}

// NewServerWithConfig creates a new stratum server with per-algorithm ports
func NewServerWithConfig(config StratumConfig, backend quaiapi.Backend) *Server {
	logger := log.NewLogger("stratum.log", viper.GetString(utils.LogLevelFlag.Name), viper.GetInt(utils.LogSizeFlag.Name))
	// Initialize kawpow engine for share verification
	kawpowLogger := log.NewLogger("stratum-kawpow.log", viper.GetString(utils.LogLevelFlag.Name), viper.GetInt(utils.LogSizeFlag.Name))
	kawpowConfig := params.PowConfig{
		PowMode:        params.ModeNormal,
		CachesInMem:    3,
		CachesOnDisk:   0,
		CachesLockMmap: false,
	}
	kawpowEngine := kawpow.New(kawpowConfig, nil, false, kawpowLogger)
	// Set max connections with default
	maxConns := config.MaxConnections
	if maxConns <= 0 {
		maxConns = defaultMaxConnections
	}
	s := &Server{
		config:         config,
		backend:        backend,
		logger:         logger,
		stats:          NewPoolStats(logger),
		kawpowEngine:   kawpowEngine,
		sessionsSHA:    make(map[*session]struct{}),
		sessionsScrypt: make(map[*session]struct{}),
		sessionsKawpow: make(map[*session]struct{}),
		maxConnections: maxConns,
		stopped:        make(chan struct{}),
	}
	// Initialize worker pool for job broadcasting
	s.jobPool = newJobWorkerPool(s, config.JobWorkerCount)
	return s
}

// Stats returns the pool statistics tracker
func (s *Server) Stats() *PoolStats {
	return s.stats
}

// registerSession adds a session to the appropriate algorithm's session map
func (s *Server) registerSession(sess *session) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	switch sess.chain {
	case "sha256":
		s.sessionsSHA[sess] = struct{}{}
	case "scrypt":
		s.sessionsScrypt[sess] = struct{}{}
	case "kawpow":
		s.sessionsKawpow[sess] = struct{}{}
	}
}

// unregisterSession removes a session from the appropriate algorithm's session map
func (s *Server) unregisterSession(sess *session) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	switch sess.chain {
	case "sha256":
		delete(s.sessionsSHA, sess)
	case "scrypt":
		delete(s.sessionsScrypt, sess)
	case "kawpow":
		delete(s.sessionsKawpow, sess)
	}
}

func (s *Server) Start() error {
	if s.backend == nil {
		return fmt.Errorf("nil backend")
	}

	// Start the job worker pool
	s.jobPool.start()

	// Start SHA listener if configured
	if s.config.SHAAddr != "" {
		ln, err := net.Listen("tcp", s.config.SHAAddr)
		if err != nil {
			return fmt.Errorf("failed to start SHA listener on %s: %v", s.config.SHAAddr, err)
		}
		s.lnSHA = ln
		s.logger.WithField("addr", s.config.SHAAddr).Info("sha256 stratum listener started")
		go s.acceptLoop(ln, "sha256")
	}

	// Start Scrypt listener if configured
	if s.config.ScryptAddr != "" {
		ln, err := net.Listen("tcp", s.config.ScryptAddr)
		if err != nil {
			return fmt.Errorf("failed to start Scrypt listener on %s: %v", s.config.ScryptAddr, err)
		}
		s.lnScrypt = ln
		s.logger.WithField("addr", s.config.ScryptAddr).Info("Scrypt stratum listener started")
		go s.acceptLoop(ln, "scrypt")
	}

	// Start Kawpow listener if configured
	if s.config.KawpowAddr != "" {
		ln, err := net.Listen("tcp", s.config.KawpowAddr)
		if err != nil {
			return fmt.Errorf("failed to start Kawpow listener on %s: %v", s.config.KawpowAddr, err)
		}
		s.lnKawpow = ln
		s.logger.WithField("addr", s.config.KawpowAddr).Info("Kawpow stratum listener started")
		go s.acceptLoop(ln, "kawpow")
	}

	// Start template polling loops for each algorithm (check every second)
	if s.config.SHAAddr != "" {
		s.wg.Add(1)
		go s.templatePollingLoop("sha256")
	}
	if s.config.ScryptAddr != "" {
		s.wg.Add(1)
		go s.templatePollingLoop("scrypt")
	}
	if s.config.KawpowAddr != "" {
		s.wg.Add(1)
		go s.templatePollingLoop("kawpow")
	}

	// Start force broadcast loops to ensure miners always have work.
	// Runs every 1 second (minimum frequency granularity) and checks each session's
	// individual jobFrequency setting. Miners can configure their frequency via username:
	// address.workername.frequency=X (1-10 seconds, supports decimals, default 5s)
	if s.config.SHAAddr != "" {
		s.wg.Add(1)
		go s.forceBroadcastLoop("sha256")
	}
	if s.config.ScryptAddr != "" {
		s.wg.Add(1)
		go s.forceBroadcastLoop("scrypt")
	}
	if s.config.KawpowAddr != "" {
		s.wg.Add(1)
		go s.forceBroadcastLoop("kawpow")
	}

	return nil
}

// templatePollingLoop polls for new templates and broadcasts to connected miners
func (s *Server) templatePollingLoop(algorithm string) {
	defer s.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopped:
			return
		case <-ticker.C:
			changed, clean, isNewBlock := s.checkTemplateChanged(algorithm)
			if changed {
				s.broadcastJob(algorithm, clean, isNewBlock)
			}
		}
	}
}

// checkTemplateChanged checks if the template has changed for the given algorithm.
// Returns (changed bool, clean bool, isNewBlock bool):
//   - changed: true if template changed and broadcast is needed
//   - clean: true if miners should abandon current work
//   - isNewBlock: true if parentHash changed (new block on donor chain), false if only quaiHeight changed
func (s *Server) checkTemplateChanged(algorithm string) (bool, bool, bool) {
	powID := powIDFromChain(algorithm)

	// Get pending header (using a dummy address since we just need to check for changes)
	pending, err := s.backend.GetPendingHeader(types.PowID(powID), common.Address{})
	if err != nil || pending == nil || pending.WorkObjectHeader() == nil {
		return false, false, false
	}

	header := pending.WorkObjectHeader()
	auxPow := header.AuxPow()
	if auxPow == nil || auxPow.Header() == nil {
		return false, false, false
	}

	// Use AuxPow header's PrevBlock (donor chain parent) for change detection,
	// not Quai's ParentHash. Miners need new work when the donor chain advances.
	auxParentHash := auxPow.Header().PrevBlock()
	newState := &templateState{
		sealHash:   header.SealHash(),
		parentHash: common.BytesToHash(auxParentHash[:]),
		height:     header.NumberU64(),
		quaiHeight: int64(pending.NumberU64(common.ZONE_CTX)),
	}

	s.templateMu.Lock()
	defer s.templateMu.Unlock()

	var lastState *templateState
	switch algorithm {
	case "sha256":
		lastState = s.lastTemplateSHA
		s.lastTemplateSHA = newState
	case "scrypt":
		lastState = s.lastTemplateScrypt
		s.lastTemplateScrypt = newState
	case "kawpow":
		lastState = s.lastTemplateKawpow
		s.lastTemplateKawpow = newState
	}

	// First template - no change to broadcast (miners get job on authorize)
	if lastState == nil {
		return false, false, false
	}

	if lastState.parentHash != newState.parentHash {
		s.logger.WithFields(log.Fields{
			"algo":      algorithm,
			"oldHeight": lastState.height,
			"newHeight": newState.height,
		}).Info("New block detected")
		return true, true, true // isNewBlock=true: parentHash changed
	}

	if lastState.quaiHeight != newState.quaiHeight {
		s.logger.WithFields(log.Fields{
			"algo":          algorithm,
			"oldQuaiHeight": lastState.quaiHeight,
			"newQuaiHeight": newState.quaiHeight,
		}).Info("QuaiHeight changed")
		return true, true, false // isNewBlock=false: only quaiHeight changed
	}

	if algorithm == "kawpow" && lastState.sealHash != newState.sealHash {
		s.logger.WithFields(log.Fields{
			"algo":   algorithm,
			"height": newState.height,
		}).Trace("Template updated (sealhash changed)")
		return true, true, false
	}

	return false, false, false
}

// broadcastJob sends a new job to all connected miners for the given algorithm.
// isNewBlock indicates if this is a new block (parentHash changed) vs just a quaiHeight change.
// Miners with skip=true receive every other quaiHeight change, but always receive new blocks.
func (s *Server) broadcastJob(algorithm string, clean bool, isNewBlock bool) {
	s.sessionsMu.RLock()
	var sessions map[*session]struct{}
	switch algorithm {
	case "sha256":
		sessions = s.sessionsSHA
	case "scrypt":
		sessions = s.sessionsScrypt
	case "kawpow":
		sessions = s.sessionsKawpow
	}
	// Copy session list to avoid holding lock during broadcast
	sessionList := make([]*session, 0, len(sessions))
	for sess := range sessions {
		if sess.authorized {
			sessionList = append(sessionList, sess)
		}
	}
	s.sessionsMu.RUnlock()

	if len(sessionList) == 0 {
		return
	}

	s.logger.WithFields(log.Fields{
		"algo":       algorithm,
		"miners":     len(sessionList),
		"clean":      clean,
		"isNewBlock": isNewBlock,
	}).Info("Broadcasting new job")

	// Broadcast to all sessions via worker pool
	for _, sess := range sessionList {
		// Handle skip blocks mode - skip every other quaiHeight change,
		// but always send new blocks (parentHash changes)
		if sess.skipBlocks && !isNewBlock {
			sess.mu.Lock()
			sess.skipBlockCounter++
			counter := sess.skipBlockCounter
			// Skip when counter is EVEN (2, 4, 6...) so first broadcast (counter=1) is SENT
			// Pattern: SEND, SKIP, SEND, SKIP...
			shouldSkip := counter%2 == 0
			sess.mu.Unlock()
			if shouldSkip {
				s.logger.WithFields(log.Fields{
					"user":    sess.user,
					"worker":  sess.workerName,
					"counter": counter,
				}).Debug("Skipping quaiHeight change for miner (skip=true mode)")
				continue
			}
			s.logger.WithFields(log.Fields{
				"user":    sess.user,
				"worker":  sess.workerName,
				"counter": counter,
			}).Debug("Sending quaiHeight change to miner (skip=true mode)")
		}
		s.jobPool.submit(sess, clean)
	}
}

// forceBroadcastLoop runs the force broadcast check every second until shutdown
func (s *Server) forceBroadcastLoop(algorithm string) {
	defer s.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopped:
			return
		case <-ticker.C:
			s.forceBroadcastStale(algorithm)
		}
	}
}

// forceBroadcastStale checks all sessions for an algorithm and force-broadcasts
// the latest template to any session that hasn't received a job within its configured
// jobFrequency. Each miner can configure their own frequency via username format:
// address.workername.frequency=X (1-10 seconds, supports decimals, default 5s)
// Also handles vardiff timeout: decreases difficulty if no share within 60 seconds.
// Also handles liveness timeout: closes connections with no share within 5 minutes.
func (s *Server) forceBroadcastStale(algorithm string) {
	// Copy session pointers under sessionsMu, then release before per-session locking
	// to avoid blocking registerSession/unregisterSession during the scan
	s.sessionsMu.RLock()
	var sessions map[*session]struct{}
	switch algorithm {
	case "sha256":
		sessions = s.sessionsSHA
	case "scrypt":
		sessions = s.sessionsScrypt
	case "kawpow":
		sessions = s.sessionsKawpow
	}
	sessionsCopy := make([]*session, 0, len(sessions))
	for sess := range sessions {
		sessionsCopy = append(sessionsCopy, sess)
	}
	s.sessionsMu.RUnlock()

	// Now iterate without holding sessionsMu
	staleSessions := make([]*session, 0)
	varDiffTimeoutSessions := make([]*session, 0)
	livenessTimeoutSessions := make([]*session, 0)
	now := time.Now()
	for _, sess := range sessionsCopy {
		if !sess.authorized {
			continue
		}
		sess.mu.Lock()
		timeSinceLastJob := now.Sub(sess.lastJobSent)
		timeSinceLastShare := now.Sub(sess.lastShareTime)
		threshold := sess.jobFrequency
		// Check for vardiff timeout
		varDiffTimeout := sess.varDiffEnabled && timeSinceLastShare >= varDiffRetargetAfter
		// Check for liveness timeout (no share in 5 minutes)
		// Only applies when miner has a liveness target (vardiff or d=X specified)
		// In workshare-only mode, shares could take hours/days - don't timeout
		hasLivenessTarget := sess.varDiffEnabled || sess.minerDifficulty != nil
		livenessExpired := hasLivenessTarget && timeSinceLastShare >= livenessTimeout
		sess.mu.Unlock()
		// Use per-session threshold (configured via username)
		if timeSinceLastJob >= threshold {
			staleSessions = append(staleSessions, sess)
		}
		if varDiffTimeout {
			varDiffTimeoutSessions = append(varDiffTimeoutSessions, sess)
		}
		if livenessExpired {
			livenessTimeoutSessions = append(livenessTimeoutSessions, sess)
		}
	}

	// Close connections that have been idle too long (no shares in 5 minutes)
	for _, sess := range livenessTimeoutSessions {
		s.logger.WithFields(log.Fields{
			"user":   sess.user,
			"worker": sess.workerName,
			"algo":   algorithm,
		}).Warn("Closing connection due to liveness timeout (no share in 5 minutes)")
		sess.conn.Close() // This will cause handleConn to exit and clean up
	}

	// Handle vardiff timeout adjustments (decrease difficulty)
	for _, sess := range varDiffTimeoutSessions {
		sess.mu.Lock()
		// Only decrease if still past the timeout (double-check after lock)
		if now.Sub(sess.lastShareTime) >= varDiffRetargetAfter {
			oldDiff := sess.varDiff
			sess.varDiff *= varDiffAdjustDown
			// Enforce algorithm-specific minimum
			_, minDiff := getVarDiffDefaults(sess.chain)
			if sess.varDiff < minDiff {
				sess.varDiff = minDiff
			}
			// Reset lastShareTime to prevent repeated decreases every second
			// Also set flag so next share doesn't immediately increase difficulty
			sess.lastShareTime = now
			sess.varDiffTimeoutReset = true
			if sess.varDiff != oldDiff {
				s.logger.WithFields(log.Fields{
					"user":    sess.user,
					"worker":  sess.workerName,
					"oldDiff": oldDiff,
					"newDiff": sess.varDiff,
					"reason":  "timeout",
				}).Info("vardiff decreased (no share within 60s)")
			}
		}
		sess.mu.Unlock()
	}

	if len(staleSessions) == 0 {
		return
	}

	s.logger.WithFields(log.Fields{
		"algo":   algorithm,
		"miners": len(staleSessions),
	}).Debug("Force broadcasting to stale sessions")

	// Broadcast with clean=true to stale sessions via worker pool
	for _, sess := range staleSessions {
		s.jobPool.submit(sess, true)
	}
}

func (s *Server) Stop() error {
	// Signal all background goroutines to stop
	close(s.stopped)

	// Wait for template polling and force broadcast loops to exit
	// This must happen BEFORE closing the job pool to prevent panic on send
	s.wg.Wait()

	var errs []error
	if s.lnSHA != nil {
		if err := s.lnSHA.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.lnScrypt != nil {
		if err := s.lnScrypt.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.lnKawpow != nil {
		if err := s.lnKawpow.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	// Stop the job worker pool (safe now that no goroutines are submitting)
	if s.jobPool != nil {
		s.jobPool.stop()
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors stopping listeners: %v", errs)
	}
	return nil
}

// acceptLoop accepts connections on a listener and assigns them the specified algorithm
func (s *Server) acceptLoop(ln net.Listener, algorithm string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		// Check connection limit (DDoS protection)
		currentConns := s.activeConns.Add(1)
		if s.maxConnections > 0 && currentConns > int64(s.maxConnections) {
			s.activeConns.Add(-1)
			s.logger.WithFields(log.Fields{
				"algo":        algorithm,
				"current":     currentConns - 1,
				"max":         s.maxConnections,
				"remote_addr": conn.RemoteAddr().String(),
			}).Warn("Connection limit reached, rejecting new connection")
			conn.Close()
			continue
		}

		// Enable TCP keepalive to detect dead connections faster than OS default (2hrs)
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(5 * time.Minute) // Probe every 30 min after idle
		}
		go s.handleConn(conn, algorithm)
	}
}

type stratumReq struct {
	ID     interface{}   `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}
type stratumResp struct {
	ID     interface{} `json:"id"`
	Result interface{} `json:"result"`
	Error  interface{} `json:"error"`
}

// Default and bounds for job frequency (force broadcast interval)
const (
	defaultJobFrequency = 5 * time.Second
	minJobFrequency     = 1 * time.Second
	maxJobFrequency     = 10 * time.Second
)

// Vardiff constants for automatic difficulty adjustment
// When miner doesn't specify d=X, we use vardiff to target ~30s per share for liveness
// Algorithm-specific defaults based on expected hashrates:
// - Kawpow: ~4.3 billion hashes per stratum diff 1
// - SHA256: ~4.3 billion hashes per stratum diff 1 (miners typically at 1+ petahash = 230k diff)
// - Scrypt: ~65k hashes per stratum diff 1 (ASIC miners at high hashrate)
const (
	// Kawpow vardiff (diff1 = ~2^32 hashes)
	varDiffDefaultKawpow = 0.1  // Starting difficulty
	varDiffMinKawpow     = 0.01 // Minimum difficulty

	// SHA256 vardiff (diff1 = 2^32 hashes)
	// default=230,000 (one petahash), min=2300 (ten terahash)
	varDiffDefaultSHA = 230000.0 // Starting difficulty
	varDiffMinSHA     = 2300.0   // Minimum difficulty

	// Scrypt vardiff (diff1 = 2^16 hashes)
	varDiffDefaultScrypt = 15000.0 // Starting difficulty
	varDiffMinScrypt     = 1500.0  // Minimum difficulty

	// Common vardiff timing constants
	varDiffTargetTime    = 30 * time.Second // Target time between shares
	varDiffRetargetAfter = 60 * time.Second // Decrease diff if no share within this time
	varDiffAdjustUp      = 1.25             // Multiply difficulty by this when shares come too fast
	varDiffAdjustDown    = 0.8              // Multiply difficulty by this when shares come too slow
)

// getVarDiffDefaults returns (default, min) vardiff values for an algorithm.
// Max vardiff is dynamically capped at workshare difficulty (never set vardiff > workshare diff).
func getVarDiffDefaults(chain string) (defaultDiff, minDiff float64) {
	switch strings.ToLower(chain) {
	case "kawpow":
		return varDiffDefaultKawpow, varDiffMinKawpow
	case "scrypt":
		return varDiffDefaultScrypt, varDiffMinScrypt
	default: // sha256
		return varDiffDefaultSHA, varDiffMinSHA
	}
}

// adjustVarDiff adjusts vardiff based on share timing and returns true if difficulty changed.
// Called after receiving a valid share. Adjusts up if shares come faster than target.
// workshareStratumDiff is the workshare difficulty in stratum units - vardiff will never exceed this.
func (s *Server) adjustVarDiff(sess *session, workshareStratumDiff float64) bool {
	if !sess.varDiffEnabled {
		return false
	}

	sess.mu.Lock()
	now := time.Now()
	timeSinceLastShare := now.Sub(sess.lastShareTime)
	oldDiff := sess.varDiff

	// If this is the first share after a timeout reset, don't increase difficulty.
	// The timeout decreased difficulty and reset lastShareTime, so the next share
	// would appear to come "too fast" - but it's actually the first real share
	// at the new lower difficulty. Just reset the baseline.
	if sess.varDiffTimeoutReset {
		sess.varDiffTimeoutReset = false
		sess.lastShareTime = now
		sess.mu.Unlock()
		s.logger.WithFields(log.Fields{
			"user":     sess.user,
			"worker":   sess.workerName,
			"varDiff":  sess.varDiff,
			"shareGap": timeSinceLastShare.Seconds(),
		}).Debug("first share after timeout reset, resetting baseline")
		return false
	}

	// If share came faster than target, increase difficulty
	if timeSinceLastShare < varDiffTargetTime {
		// Calculate how much faster (e.g., 15s vs 30s target = 2x faster)
		ratio := float64(varDiffTargetTime) / float64(timeSinceLastShare)
		// Adjust difficulty proportionally, capped at 2x per adjustment
		if ratio > varDiffAdjustUp {
			ratio = varDiffAdjustUp
		}
		sess.varDiff *= ratio
		// Cap vardiff at workshare difficulty - never set vardiff > workshare diff
		if workshareStratumDiff > 0 && sess.varDiff > workshareStratumDiff {
			sess.varDiff = workshareStratumDiff
		}
	}
	// Note: We don't decrease here - that's handled by the timeout in forceBroadcastStale

	sess.lastShareTime = now
	newDiff := sess.varDiff
	sess.mu.Unlock()

	if newDiff != oldDiff {
		s.logger.WithFields(log.Fields{
			"user":          sess.user,
			"worker":        sess.workerName,
			"oldDiff":       oldDiff,
			"newDiff":       newDiff,
			"workshareDiff": workshareStratumDiff,
			"shareGap":      timeSinceLastShare.Seconds(),
		}).Info("vardiff adjusted")
		return true
	}
	return false
}

type session struct {
	conn       net.Conn
	enc        *json.Encoder
	dec        *json.Decoder
	encMu      sync.Mutex // protects enc writes (broadcasts + responses can race)
	authorized bool
	user       string // payout address
	workerName string // worker name (from user.workerName format)
	chain      string // sha256|scrypt
	job        *job
	kawJob     *kawpowJob // kawpow-specific job data
	xnonce1    []byte
	// vardiff state (per-connection)
	difficulty         float64 // workshare stratum difficulty
	lastSentDifficulty float64 // last difficulty actually sent to miner (to avoid redundant set_difficulty)
	lastSentTarget     string  // last target sent to kawpow miner (to avoid redundant set_target)
	// version rolling state
	versionRolling bool
	versionMask    uint32
	// job tracking
	mu      sync.Mutex // protects job, jobs, jobSeq, jobHistory, difficulty
	jobs    *lru.Cache[string, *job]
	kawJobs *lru.Cache[string, *kawpowJob] // kawpow job tracking
	jobSeq  uint64
	// share de-duplication (per-connection LRU)
	seenShares *lru.Cache[string, struct{}]
	// cleanup
	done      chan struct{}
	jobTicker *time.Ticker
	// last job sent time for stale broadcast detection
	lastJobSent time.Time
	// jobFrequency controls how often force broadcasts are sent to this miner
	// Configured via username: address.workername.frequency=X (1-10 seconds, supports decimals)
	jobFrequency time.Duration
	// jobSendMu serializes sendJobAndNotify calls to prevent concurrent job sends
	// This avoids race conditions between post-submit job refresh, forceBroadcastStale, and broadcastJob
	jobSendMu sync.Mutex
	// minerDifficulty is the custom difficulty set by the miner via password field (d=<difficulty>)
	// If set, shares meeting this difficulty are accepted for liveness tracking, but only
	// shares meeting the workshare difficulty are submitted to the node.
	// nil means use workshare difficulty as the minimum.
	minerDifficulty *float64
	// Vardiff state for automatic difficulty adjustment (when minerDifficulty is nil)
	varDiffEnabled      bool      // true if using automatic vardiff (no d=X specified)
	varDiff             float64   // current vardiff value
	lastShareTime       time.Time // when last valid share was received (for vardiff timing)
	varDiffTimeoutReset bool      // true if lastShareTime was reset due to timeout (not a real share)
	// Skip blocks mode - only send every other block (for debugging fast block times)
	skipBlocks       bool // true if miner wants to skip every other block
	skipBlockCounter int  // counter to track which blocks to skip
}

// sendJSON safely encodes and sends JSON to the miner (thread-safe).
// Uses a 10-second write deadline to prevent goroutine buildup from slow/stalled clients.
func (s *session) sendJSON(v interface{}) error {
	s.encMu.Lock()
	defer s.encMu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := s.enc.Encode(v)
	s.conn.SetWriteDeadline(time.Time{}) // clear deadline
	return err
}

type job struct {
	id           string
	version      uint32
	prevHashLE   string
	nBits        uint32
	nTime        uint32
	merkleBranch []string
	coinb1       string
	coinb2       string
	// the exact pending header used to construct this job
	pending *types.WorkObject
}

// handleConn handles a stratum connection for a specific algorithm.
// The algorithm is determined by which port the miner connected to.
func (s *Server) handleConn(c net.Conn, algorithm string) {
	defer c.Close()
	defer s.activeConns.Add(-1) // Decrement connection counter on exit
	// Set a longer initial read deadline - will be extended on each message
	// Use SetReadDeadline (not SetDeadline) to avoid overriding the 10s write deadline in sendJSON
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
	dec := json.NewDecoder(bufio.NewReader(c))
	enc := json.NewEncoder(c)

	seenShares, _ := lru.New[string, struct{}](seenSharesSize)
	jobsCache, _ := lru.New[string, *job](jobsCacheSize)
	kawJobsCache, _ := lru.New[string, *kawpowJob](jobsCacheSize)

	sess := &session{
		conn:         c,
		enc:          enc,
		dec:          dec,
		chain:        algorithm, // Set algorithm from the port they connected to
		jobs:         jobsCache,
		kawJobs:      kawJobsCache,
		seenShares:   seenShares,
		done:         make(chan struct{}),
		jobFrequency: defaultJobFrequency, // Default 5 seconds, can be overridden via username
	}

	// Cleanup function for goroutines
	defer func() {
		s.unregisterSession(sess)
		if sess.authorized && sess.user != "" {
			s.stats.WorkerDisconnected(sess.user, sess.workerName)
		}
		close(sess.done)
		if sess.jobTicker != nil {
			sess.jobTicker.Stop()
		}
	}()

	for {

		var req stratumReq
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				if sess.authorized {
					s.logger.WithFields(log.Fields{
						"user":   sess.user,
						"worker": sess.workerName,
						"algo":   algorithm,
					}).Debug("Miner disconnected (EOF)")
				}
				return
			}
			if sess.authorized {
				s.logger.WithFields(log.Fields{
					"user":   sess.user,
					"worker": sess.workerName,
					"algo":   algorithm,
					"error":  err.Error(),
				}).Debug("Miner disconnected (read error)")
			}
			return
		}
		// Extend read deadline on each message received
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))

		switch req.Method {
		case "mining.subscribe":
			// Response differs by algorithm:
			// - Kawpow: [nil, "00"] - extranonce not used (64-bit header nonce is sufficient)
			// - sha256/scrypt: [[subscriptions], extranonce1, extranonce2_size]
			if sess.chain == "kawpow" {
				// Kawpow doesn't use extranonce - the 64-bit header nonce provides enough search space
				result := []interface{}{
					nil,  // Session ID (not used for resuming)
					"00", // extranonce1 - placeholder, not used
				}
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: result, Error: nil}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "remoteAddr": sess.conn.RemoteAddr().String()}).Error("failed to send mining.subscribe response (kawpow)")
					sess.conn.Close()
					return
				}
			} else {
				// sha256/scrypt use extranonce for coinbase modification
				x1 := []byte{0x01, 0x01, 0x01, 0x01} // All 1s instead of random
				sess.xnonce1 = append([]byte{}, x1...)
				result := []interface{}{
					[]interface{}{
						[]interface{}{"mining.set_difficulty", "1"},
						[]interface{}{"mining.notify", "1"},
					},
					hex.EncodeToString(x1), // Will send "01010101"
					4,                      // extranonce2_size (4 bytes for wider miner compatibility)
				}
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: result, Error: nil}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "remoteAddr": sess.conn.RemoteAddr().String()}).Error("failed to send mining.subscribe response (sha256/scrypt)")
					sess.conn.Close()
					return
				}
			}
		case "mining.extranonce.subscribe":
			if err := sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil}); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.extranonce.subscribe response")
			}
		case "mining.configure":
			// BIP 310: params = [extensions_array, params_map]
			// extensions_array: ["version-rolling", "minimum-difficulty", ...]
			// params_map: {"version-rolling.mask": "1fffe000", ...}
			//
			// Always accept version-rolling with a fixed mask.
			// This is simpler and compatible with all miners.
			wantsVersionRolling := false

			// Check if miner requested version-rolling
			if len(req.Params) >= 1 {
				// First param can be either:
				// - array of strings (BIP 310 compliant): ["version-rolling", ...]
				// - array of interfaces (some miners): [["version-rolling", {...}], ...]
				switch extensions := req.Params[0].(type) {
				case []interface{}:
					for _, ext := range extensions {
						if extStr, ok := ext.(string); ok && extStr == "version-rolling" {
							wantsVersionRolling = true
							break
						}
						// Some miners send nested arrays like [["version-rolling", params]]
						if extArr, ok := ext.([]interface{}); ok && len(extArr) > 0 {
							if extStr, ok := extArr[0].(string); ok && extStr == "version-rolling" {
								wantsVersionRolling = true
								break
							}
						}
					}
				}
			}

			// Build response - always indicate support for version-rolling if requested
			resp := map[string]interface{}{}
			if wantsVersionRolling {
				sess.mu.Lock()
				sess.versionRolling = true
				sess.versionMask = 0x1fffe000 // Standard mask (bits 13-28)
				sess.mu.Unlock()
				resp["version-rolling"] = true
				resp["version-rolling.mask"] = fmt.Sprintf("%08x", sess.versionMask)
				resp["version-rolling.min-bit-count"] = 0
				s.logger.WithField("mask", fmt.Sprintf("0x%08x", sess.versionMask)).Info("VERSION-ROLLING enabled")
			}
			if err := sess.sendJSON(stratumResp{ID: req.ID, Result: resp, Error: nil}); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.configure response")
			}

			// Send mining.set_version_mask for miners that expect it after configure
			sess.mu.Lock()
			vr := sess.versionRolling
			sess.mu.Unlock()
			if vr {
				note := map[string]interface{}{"id": nil, "method": "mining.set_version_mask", "params": []interface{}{fmt.Sprintf("%08x", sess.versionMask)}}
				if err := sess.sendJSON(note); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.set_version_mask")
				}
			}
		case "mining.authorize":
			// Track old worker name to handle re-authorization
			oldUser, oldWorkerName := sess.user, sess.workerName
			wasAuthorized := sess.authorized

			if len(req.Params) >= 1 {
				if u, ok := req.Params[0].(string); ok {
					// Parse username format: address.workername.frequency=X
					// Examples:
					//   0x1234...          -> user=0x1234..., worker=default, freq=5s
					//   0x1234....rig1     -> user=0x1234..., worker=rig1, freq=5s
					//   0x1234....rig1.frequency=2.5 -> user=0x1234..., worker=rig1, freq=2.5s
					sess.user, sess.workerName, sess.jobFrequency = parseUsername(u)
				}
			}

			// If re-authorizing with different worker name, disconnect old worker first
			if wasAuthorized && (oldUser != sess.user || oldWorkerName != sess.workerName) {
				s.stats.WorkerDisconnected(oldUser, oldWorkerName)
			}
			// Parse password for optional difficulty, frequency, and skip settings
			// Format: d=<difficulty>,frequency=<seconds>,skip=<true|false>
			if len(req.Params) >= 2 {
				if p, ok := req.Params[1].(string); ok {
					var pwFreq time.Duration
					sess.minerDifficulty, pwFreq, sess.skipBlocks = parsePassword(p)
					if pwFreq > 0 {
						sess.jobFrequency = pwFreq // Password frequency overrides username frequency
					}
				}
			}
			// Initialize lastShareTime for liveness tracking (all sessions, not just vardiff)
			sess.lastShareTime = time.Now()
			// Initialize vardiff if enabled and miner didn't specify a static difficulty
			// If vardiff is disabled and no d=X, workshare difficulty will be used (handled in sendJobAndNotify)
			if sess.minerDifficulty == nil && s.config.VarDiffEnabled {
				sess.varDiffEnabled = true
				defaultDiff, _ := getVarDiffDefaults(sess.chain)
				sess.varDiff = defaultDiff
			}
			// Validate that the address is internal (belongs to this zone)
			address := common.HexToAddress(sess.user, s.backend.NodeLocation())
			if _, err := address.InternalAddress(); err != nil {
				s.logger.WithFields(log.Fields{
					"user":  sess.user,
					"error": err,
				}).Warn("miner address is not internal to this zone")
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "address is not internal to this zone"}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.authorize rejection")
					sess.conn.Close()
					return
				}
				continue
			}
			// Algorithm is determined by which port the miner connected to (set in handleConn)
			sess.authorized = true
			s.registerSession(sess)                                         // Register for broadcasts
			s.stats.WorkerConnected(sess.user, sess.workerName, sess.chain) // Track worker in stats
			logFields := log.Fields{
				"user":         sess.user,
				"worker":       sess.workerName,
				"chain":        sess.chain,
				"powID":        powIDFromChain(sess.chain),
				"jobFrequency": sess.jobFrequency.Seconds(),
				"remoteAddr":   sess.conn.RemoteAddr().String(),
			}
			if sess.minerDifficulty != nil {
				logFields["minerDifficulty"] = *sess.minerDifficulty
			} else if sess.varDiffEnabled {
				logFields["varDiff"] = sess.varDiff
				logFields["varDiffEnabled"] = true
			}
			if sess.skipBlocks {
				logFields["skipBlocks"] = true
			}
			s.logger.WithFields(logFields).Info("miner authorized")
			if err := sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil}); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.authorize success")
				sess.conn.Close()
				return
			}
			// Send a fresh job with miner difficulty based on workshare diff
			if err := s.sendJobAndNotify(sess, true); err != nil {
				s.logger.WithField("error", err).Error("makeJob error")
				// Keep trying to send a job every few seconds if it fails
				go func() {
					for i := 0; i < 10; i++ {
						select {
						case <-sess.done:
							return
						case <-time.After(2 * time.Second):
							// Check if job was created successfully (under lock to avoid race)
							sess.mu.Lock()
							hasJob := sess.job != nil || sess.kawJobs.Len() > 0
							sess.mu.Unlock()
							if hasJob {
								return // job was created successfully
							}
							s.logger.WithField("attempt", i+1).Info("retrying makeJob")
							if err := s.sendJobAndNotify(sess, true); err == nil {
								s.logger.Info("makeJob retry successful")
								return
							}
						}
					}
				}()
			}
			// Note: Job updates are now handled by the central template polling loop
			// started in Server.Start(), not per-session tickers
		case "mining.submit":
			// Kawpow uses different submit format: [worker, job_id, nonce, header_hash, mix_hash]
			// sha256/scrypt uses: [worker, job_id, ex2, ntime, nonce, version_bits?]
			if powIDFromChain(sess.chain) == types.Kawpow {
				if len(req.Params) < 5 {
					if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "bad kawpow params"}); err != nil {
						s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit bad params (kawpow)")
					}
					continue
				}
				// Kawpow params: [worker, job_id, nonce, header_hash, mix_hash]
				jobID, _ := req.Params[1].(string)
				nonceHex, _ := req.Params[2].(string)
				headerHashHex, _ := req.Params[3].(string)
				mixHashHex, _ := req.Params[4].(string)
				kawJob, ok := sess.kawJobs.Peek(jobID)
				knownJobs := sess.kawJobs.Len() // snapshot for logging (avoid race)
				sess.mu.Lock()
				difficulty := sess.difficulty // snapshot for stats
				sess.mu.Unlock()
				if !ok {
					s.logger.WithFields(log.Fields{"jobID": jobID, "known": knownJobs, "worker": sess.user + "." + sess.workerName}).Error("unknown kawpow jobID")
					s.stats.ShareSubmitted(sess.user, sess.workerName, difficulty, false, true) // stale share
					if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "nojob"}); err != nil {
						s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit stale job (kawpow)")
					}
					continue
				}

				// Submit kawpow share
				if err := s.submitKawpowShare(sess, kawJob, nonceHex, headerHashHex, mixHashHex); err != nil {
					s.stats.ShareSubmitted(sess.user, sess.workerName, difficulty, false, false) // invalid share
					if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: err.Error()}); err != nil {
						s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit invalid share (kawpow)")
					}
				} else {
					// Note: valid share is already recorded in submitKawpowShare with difficulty info
					s.logger.WithFields(log.Fields{"addr": sess.user, "nonce": nonceHex, "mixHash": mixHashHex}).Info("kawpow submit accepted")
					sess.kawJobs.Remove(jobID)
					if err := sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil}); err != nil {
						s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.submit accepted (kawpow)")
						sess.conn.Close()
						return
					}
					// Send fresh job via worker pool
					s.jobPool.submit(sess, true)
				}
				continue
			}

			// sha256/scrypt submit handling
			if len(req.Params) < 5 {
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "bad params"}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit bad params (sha256/scrypt)")
				}
				continue
			}
			// params: [user, job_id, ex2, ntime, nonce, version_bits (optional)]
			jobID, _ := req.Params[1].(string)
			ex2hex, _ := req.Params[2].(string)
			ntimeHex, _ := req.Params[3].(string)
			nonceHex, _ := req.Params[4].(string)
			var versionBits string
			if len(req.Params) >= 6 {
				versionBits, _ = req.Params[5].(string)
			}

			// Look up the submitted job by ID under lock
			sess.mu.Lock()
			difficulty := sess.difficulty // snapshot for stats
			sess.mu.Unlock()
			j, ok := sess.jobs.Peek(jobID)
			if !ok {
				s.logger.WithFields(log.Fields{"jobID": jobID, "known": sess.jobs.Len(), "worker": sess.user + "." + sess.workerName}).Error("unknown or stale jobID")
				sess.sendJSON(stratumResp{
					ID:     req.ID,
					Result: false,
					Error:  fmt.Errorf("no such jobID %s", jobID).Error(),
				})
				continue
			}
			sess.mu.Lock()
			sess.job = j
			sess.mu.Unlock()
			// Look up the submitted job by ID to avoid stale/current mismatches
			sess.mu.Lock()
			if j2, ok2 := sess.jobs.Peek(jobID); ok2 {
				sess.job = j2
				sess.mu.Unlock()
			} else {
				known := sess.jobs.Len()
				sess.mu.Unlock()
				s.logger.WithFields(log.Fields{"jobID": jobID, "known": known}).Error("unknown or stale jobID")
				s.stats.ShareSubmitted(sess.user, sess.workerName, difficulty, false, true) // stale share
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "nojob"}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit stale job (sha256/scrypt)")
				}
				continue
			}
			// apply nonce into AuxPow header and submit as workshare
			// Pass j directly to avoid TOCTOU race where sess.job could be replaced by concurrent sendJobAndNotify
			refreshJob, err := s.submitAsWorkShare(sess, j, ex2hex, ntimeHex, nonceHex, versionBits)
			if err != nil {
				s.stats.ShareSubmitted(sess.user, sess.workerName, difficulty, false, false) // invalid share
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: err.Error()}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.submit invalid share (sha256/scrypt)")
				}
			} else {
				// Note: valid share is already recorded in submitAsWorkShare with difficulty info
				s.logger.WithFields(log.Fields{"addr": sess.user, "chain": sess.chain, "nonce": nonceHex}).Info("submit accepted")
				// Mark this job ID as consumed to prevent duplicate submissions
				sess.mu.Lock()
				sess.jobs.Remove(jobID)
				sess.mu.Unlock()
				if err := sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil}); err != nil {
					s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.submit accepted (sha256/scrypt)")
					sess.conn.Close()
					return
				}
				// Send a fresh job after successful workshare to keep miner on latest work
				// Skip for sub-shares (met miner difficulty but not workshare difficulty)
				if refreshJob {
					s.jobPool.submit(sess, true)
				}
			}
		default:
			s.logger.WithFields(log.Fields{
				"user":   sess.user,
				"worker": sess.workerName,
				"method": req.Method,
			}).Debug("Unknown stratum method")
			if err := sess.sendJSON(stratumResp{ID: req.ID, Result: nil, Error: nil}); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "method": req.Method, "worker": sess.user + "." + sess.workerName}).Debug("failed to send response for unknown method")
			}
		}
	}
}

// sendJobAndNotify creates a new job, sets miner difficulty using SHA workshare diff,
// and sends set_difficulty followed by mining.notify.
// The clean parameter indicates whether the miner should abandon current work (new block).
// This function is serialized per-session via jobSendMu to prevent race conditions
// between post-submit job refresh, forceBroadcastStale, and broadcastJob.
func (s *Server) sendJobAndNotify(sess *session, clean bool) error {
	// Serialize job sends per session to avoid concurrent job sends
	sess.jobSendMu.Lock()
	defer sess.jobSendMu.Unlock()

	// Kawpow uses a different stratum format
	if powIDFromChain(sess.chain) == types.Kawpow {
		s.logger.WithField("chain", sess.chain).Debug("routing to kawpow stratum")
		return s.sendKawpowJob(sess, clean)
	}
	s.logger.WithField("chain", sess.chain).Debug("routing to sha256/scrypt stratum")

	j, err := s.makeJob(sess)
	if err != nil {
		return err
	}
	// Assign a unique job ID and track it (protected by session mutex)
	sess.mu.Lock()
	j.id = s.newJobID(sess)
	sess.job = j
	sess.jobs.Add(j.id, j)
	// Maintain job history to allow stale shares; keep 32 jobs for solo mining
	sess.mu.Unlock()
	s.logger.WithFields(log.Fields{"jobID": j.id, "chain": sess.chain}).Info("notify job")

	// Stratum difficulty mapping:
	// - SHA256: stratumDiff = workshareDiff / 2^32 (diff1 = 4294967296 hashes)
	// - Scrypt: stratumDiff = workshareDiff / 2^16 (diff1 = 65536 hashes)
	const (
		sha256Diff1 = 4294967296.0 // 2^32
		scryptDiff1 = 65536.0      // 2^16
	)

	// Calculate workshare stratum difficulty from pending header
	var workshareStratumDiff float64 = 1e-10 // fallback
	switch powIDFromChain(sess.chain) {
	case types.SHA_BTC, types.SHA_BCH:
		if j.pending != nil && j.pending.WorkObjectHeader() != nil && j.pending.WorkObjectHeader().ShaDiffAndCount() != nil && j.pending.WorkObjectHeader().ShaDiffAndCount().Difficulty() != nil {
			sd := j.pending.WorkObjectHeader().ShaDiffAndCount().Difficulty()
			diffF, _ := new(big.Float).Quo(new(big.Float).SetInt(sd), big.NewFloat(sha256Diff1)).Float64()
			if diffF > 0 {
				workshareStratumDiff = diffF
			}
			s.logger.WithFields(log.Fields{"sha256Diff": sd.String(), "minerDiff": workshareStratumDiff}).Debug("sendJobAndNotify sha256 diff")
		} else {
			s.logger.WithField("fallback", workshareStratumDiff).Debug("sendJobAndNotify: no sha256 diff available, using fallback")
		}
	case types.Scrypt:
		if j.pending != nil && j.pending.WorkObjectHeader() != nil && j.pending.WorkObjectHeader().ScryptDiffAndCount() != nil && j.pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty() != nil {
			sd := j.pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
			diffF, _ := new(big.Float).Quo(new(big.Float).SetInt(sd), big.NewFloat(scryptDiff1)).Float64()
			if diffF > 0 {
				workshareStratumDiff = diffF
			}
			s.logger.WithFields(log.Fields{"ScryptDiff": sd.String(), "minerDiff": workshareStratumDiff}).Debug("sendJobAndNotify Scrypt diff")
		} else {
			s.logger.WithField("fallback", workshareStratumDiff).Debug("sendJobAndNotify: No Scrypt diff available, using fallback")
		}
	}

	// Lock once for all session field access
	sess.mu.Lock()

	// Store workshare difficulty
	sess.difficulty = workshareStratumDiff

	// Determine miner difficulty - Priority: 1) miner-specified d=X, 2) vardiff, 3) workshare difficulty
	minerDiff := workshareStratumDiff
	var diffSource string
	if sess.minerDifficulty != nil && *sess.minerDifficulty > 0 {
		minerDiff = *sess.minerDifficulty
		diffSource = "miner-specified"
	} else if sess.varDiffEnabled {
		minerDiff = sess.varDiff
		diffSource = "vardiff"
	} else {
		diffSource = "workshare"
	}

	// Check if difficulty changed since last send
	diffChanged := sess.lastSentDifficulty != minerDiff
	if diffChanged {
		sess.lastSentDifficulty = minerDiff
	}

	sess.mu.Unlock()

	// Only send set_difficulty if the difficulty actually changed
	if diffChanged {
		s.logger.WithFields(log.Fields{diffSource: minerDiff, "workshareDiff": workshareStratumDiff}).Debug("Sending set_difficulty")
		diffNote := map[string]interface{}{"id": nil, "method": "mining.set_difficulty", "params": []interface{}{minerDiff}}
		if err := sess.sendJSON(diffNote); err != nil {
			s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.set_difficulty (sha256/scrypt)")
		}
	}

	// Prepend 4 zero bytes to coinb2 - miner sees this as part of coinb2, but we use it as ex2 padding
	// This allows us to send 4-byte extranonce2 to miners while the node expects 8-byte extranonce2
	coinb2WithPadding := "00000000" + j.coinb2
	params := []interface{}{j.id, j.prevHashLE, j.coinb1, coinb2WithPadding, j.merkleBranch, fmt.Sprintf("%08x", j.version), fmt.Sprintf("%08x", j.nBits), fmt.Sprintf("%08x", j.nTime), clean}

	note := map[string]interface{}{"id": nil, "method": "mining.notify", "params": params}

	err = sess.sendJSON(note)
	if err != nil {
		s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.notify (sha256/scrypt)")
		sess.conn.Close() // can't send jobs, session is dead
		return err
	}
	sess.mu.Lock()
	sess.lastJobSent = time.Now()
	sess.mu.Unlock()
	return nil
}

// sendKawpowJob creates and sends a kawpow-specific job to the miner.
// Kawpow stratum notify format: [job_id, header_hash, seed_hash, target, clean, height, bits]
func (s *Server) sendKawpowJob(sess *session, clean bool) error {
	address := common.HexToAddress(sess.user, s.backend.NodeLocation())
	pending, err := s.backend.GetPendingHeader(types.Kawpow, address)
	if err != nil || pending == nil || pending.WorkObjectHeader() == nil {
		if err == nil {
			err = fmt.Errorf("no pending header for kawpow")
		}
		s.logger.WithField("error", err).Error("sendKawpowJob error")
		return err
	}
	pending.WorkObjectHeader().SetPrimaryCoinbase(address)

	// Get block height from Ravencoin header for epoch calculation
	// IMPORTANT: Must use the kawpow height from the AuxPow header, NOT the Quai block height!
	// ComputePowLight uses ravencoinHeader.Height() to calculate the epoch and DAG,
	// so the stratum must use the same value for VerifyKawpowShare to produce matching results.
	auxPow := pending.WorkObjectHeader().AuxPow()
	if auxPow == nil || auxPow.Header() == nil {
		return fmt.Errorf("no AuxPow header for kawpow")
	}
	height := uint64(auxPow.Header().Height())

	// Calculate epoch and seed hash from height
	epoch := calculateEpoch(height)
	seedHash := calculateSeedHash(epoch)

	// Get nBits and header hash from AuxPow header
	// For kawpow, the header hash is the double SHA256 of the Ravencoin-style header
	// (version, prevHash, merkleRoot, time, bits, height) - excludes nonce and mixhash
	auxPowHeader := pending.WorkObjectHeader().AuxPow().Header()
	if auxPowHeader == nil {
		s.logger.Error("AuxPow header is nil for kawpow job")
		return fmt.Errorf("AuxPow header is nil")
	}
	nBits := auxPowHeader.Bits()
	// SealHash() returns GetKAWPOWHeaderHash() for RavencoinBlockHeader
	// which is the double SHA256 of the 80-byte header input fields
	sealHash := auxPowHeader.SealHash().Reverse()
	headerHash := hex.EncodeToString(sealHash[:])

	// Lock once for all session field access
	sess.mu.Lock()

	// Get kawpow difficulty and convert to target
	// Priority: 1) miner-specified d=X, 2) vardiff, 3) workshare difficulty
	var targetHex string
	var usedDiff float64
	var diffSource string

	if sess.minerDifficulty != nil && *sess.minerDifficulty > 0 {
		usedDiff = *sess.minerDifficulty
		diffSource = "miner-specified"
	} else if sess.varDiffEnabled {
		usedDiff = sess.varDiff
		diffSource = "vardiff"
	}

	if usedDiff > 0 {
		// target = KawpowDiff1 / difficulty
		minerDiffBig := new(big.Float).SetFloat64(usedDiff)
		diff1Float := new(big.Float).SetInt(KawpowDiff1)
		targetFloat := new(big.Float).Quo(diff1Float, minerDiffBig)
		targetInt, _ := targetFloat.Int(nil)
		// Convert to 32-byte hex string, zero-padded
		targetBytes := targetInt.Bytes()
		result := make([]byte, 32)
		copy(result[32-len(targetBytes):], targetBytes)
		targetHex = hex.EncodeToString(result)
	} else if pending.WorkObjectHeader().KawpowDifficulty() != nil {
		kawpowDiff := core.CalculateKawpowShareDiff(pending.WorkObjectHeader())
		targetHex = common.GetTargetInHex(kawpowDiff)
		diffSource = "workshare"
	} else {
		// Fallback to max target
		targetHex = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		diffSource = "fallback"
		s.logger.Warn("No kawpow difficulty available, using max target")
	}

	// Create kawpow job and assign ID
	sess.jobSeq++
	jobID := fmt.Sprintf("%x%04x", uint64(time.Now().UnixNano()), sess.jobSeq&0xffff)

	kawJob := &kawpowJob{
		id:         jobID,
		headerHash: headerHash,
		seedHash:   seedHash,
		target:     targetHex,
		height:     height,
		bits:       nBits,
		pending:    types.CopyWorkObject(pending),
	}
	sess.kawJob = kawJob
	sess.kawJobs.Add(jobID, kawJob)

	// Check if target changed since last send
	targetChanged := sess.lastSentTarget != targetHex
	if targetChanged {
		sess.lastSentTarget = targetHex
	}

	sess.mu.Unlock()

	// Log job info
	s.logger.WithFields(log.Fields{"jobID": jobID, "height": height, "epoch": epoch, "diffSource": diffSource}).Info("notify kawpow job")

	// Only send set_target if the target actually changed
	if targetChanged {
		s.logger.WithFields(log.Fields{"target": targetHex}).Debug("Sending set_target")
		targetNote := map[string]interface{}{"id": nil, "method": "mining.set_target", "params": []interface{}{targetHex}}
		if err := sess.sendJSON(targetNote); err != nil {
			s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send mining.set_target (kawpow)")
		}
	}

	// Kawpow mining.notify format: [job_id, header_hash, seed_hash, target, clean, height, bits]
	// Note: height is sent as integer (not hex), bits as hex string
	params := []interface{}{
		jobID,
		headerHash,
		seedHash,
		targetHex,
		clean,  // clean_jobs - true when new block, false for same-block updates
		height, // Block height as integer (miner uses .asInt64())
		fmt.Sprintf("%08x", nBits),
	}

	note := map[string]interface{}{"id": nil, "method": "mining.notify", "params": params}
	err = sess.sendJSON(note)
	if err != nil {
		s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Error("failed to send mining.notify (kawpow)")
		sess.conn.Close() // can't send jobs, session is dead
		return err
	}
	sess.mu.Lock()
	sess.lastJobSent = time.Now()
	sess.mu.Unlock()
	return nil
}

func (s *Server) makeJob(sess *session) (*job, error) {
	powID := powIDFromChain(sess.chain)
	address := common.HexToAddress(sess.user, s.backend.NodeLocation())
	pending, err := s.backend.GetPendingHeader(types.PowID(powID), address)

	if err != nil || pending == nil || pending.WorkObjectHeader() == nil || pending.WorkObjectHeader().AuxPow() == nil {
		if err == nil {
			err = fmt.Errorf("no pending header")
		}
		s.logger.WithField("error", err).Error("makeJob error")
		return nil, err
	}
	pending.WorkObjectHeader().SetPrimaryCoinbase(address)

	aux := pending.WorkObjectHeader().AuxPow()

	// Keep the existing SHA workshare difficulty - don't override with BCH difficulty
	// The workshare system uses its own difficulty separate from the BCH block difficulty
	if pending.WorkObjectHeader().ShaDiffAndCount() != nil {
		currentDiff := pending.WorkObjectHeader().ShaDiffAndCount().Difficulty()
		s.logger.WithField("ShaDiff", currentDiff.String()).Debug("Using existing workshare ShaDiff")
	}

	// Also log existing Scrypt workshare difficulty if present
	if pending.WorkObjectHeader().ScryptDiffAndCount() != nil {
		currentDiff := pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
		s.logger.WithField("ScryptDiff", currentDiff.String()).Debug("Using existing workshare ScryptDiff")
	}

	// Merkle branch
	mb := make([]string, len(aux.MerkleBranch()))
	for i, h := range aux.MerkleBranch() {
		mb[i] = hex.EncodeToString(h)
	}
	// header fields
	version := aux.Header().Version()
	prev := aux.Header().PrevBlock()
	nBits := aux.Header().Bits()
	nTime := aux.Header().Timestamp()

	coinb1, coinb2, err := types.ExtractCoinb1AndCoinb2FromAuxPowTx(aux.Transaction())
	if err != nil {
		return nil, err
	}

	// Send fully reversed and word-swapped prevhash to miner (to match validation)
	// prevReversed := fullReverse(prev[:])
	var prevLE [32]byte
	copy(prevLE[:], prev[:])
	prevLESwapped := swapWords32x32(prevLE)

	return &job{
		id:           "", // assigned in sendJobAndNotify
		version:      uint32(version),
		prevHashLE:   hex.EncodeToString(prevLESwapped[:]),
		nBits:        nBits,
		nTime:        nTime,
		merkleBranch: mb,
		coinb1:       hex.EncodeToString(coinb1),
		coinb2:       hex.EncodeToString(coinb2),
		pending:      types.CopyWorkObject(pending),
	}, nil
}

// newJobID returns a per-session unique job ID string
func (s *Server) newJobID(sess *session) string {
	// Caller should hold sess.mu
	sess.jobSeq++
	// 4-byte hex ID (8 chars) - just a counter, unique per session
	return fmt.Sprintf("%08x", sess.jobSeq)
}

// submitAsWorkShare validates and submits a sha256/scrypt share.
// Returns (refreshJob, error) where refreshJob indicates whether to send a new job after acceptance.
// refreshJob is false for sub-shares (met miner difficulty but not workshare difficulty).
// The job parameter is the looked-up job from the caller, avoiding TOCTOU races with sess.job.
func (s *Server) submitAsWorkShare(sess *session, curJob *job, ex2hex, ntimeHex, nonceHex, versionBits string) (bool, error) {
	if curJob == nil {
		return false, fmt.Errorf("no current job")
	}

	powID := powIDFromChain(sess.chain)

	// Snapshot all needed session fields under one lock
	sess.mu.Lock()
	xnonce1 := sess.xnonce1
	versionRolling := sess.versionRolling
	versionMask := sess.versionMask
	minerDifficulty := sess.minerDifficulty
	varDiffEnabled := sess.varDiffEnabled
	varDiff := sess.varDiff
	sess.mu.Unlock()
	pending := curJob.pending
	if pending == nil || pending.WorkObjectHeader() == nil || pending.WorkObjectHeader().AuxPow() == nil {
		return false, fmt.Errorf("no pending header for job")
	}

	// Rebuild donor header for SHA chains with updated merkle root and nTime
	templateHeader := pending.AuxPow().Header()

	// Reconstruct coinbase from coinb1 + ex1 + ex2 + coinb2
	ex2, _ := hex.DecodeString(ex2hex)
	if len(ex2) != 4 {
		return false, fmt.Errorf("bad extranonce2 length: expected 4, got %d", len(ex2))
	}
	// Pad ex2 to 8 bytes - append 4 zero bytes (these match the padding we prepended to coinb2)
	ex2 = append(ex2, 0, 0, 0, 0)

	// Reconstruct full coinbase: coinb1 + ex1(4) + ex2(8) + coinb2
	coinb1Bytes, _ := hex.DecodeString(curJob.coinb1)
	coinb2Bytes, _ := hex.DecodeString(curJob.coinb2)

	fullCoinb := make([]byte, 0, len(coinb1Bytes)+12+len(coinb2Bytes))
	fullCoinb = append(fullCoinb, coinb1Bytes...)
	fullCoinb = append(fullCoinb, xnonce1...) // use the exact extranonce1 you sent in subscribe
	fullCoinb = append(fullCoinb, ex2...)     // miner's extranonce2
	fullCoinb = append(fullCoinb, coinb2Bytes...)

	merkleRoot := types.CalculateMerkleRoot(powID, fullCoinb, pending.AuxPow().MerkleBranch())

	ntime, err := strconv.ParseUint(ntimeHex, 16, 32)
	if err != nil {
		return false, fmt.Errorf("invalid ntime: %v", err)
	}

	// Parse nonce from hex string
	nonce, err := strconv.ParseUint(nonceHex, 16, 32)
	if err != nil {
		return false, fmt.Errorf("invalid nonce: %v", err)
	}

	// Parse version bits (if provided)
	var finalVersion uint32
	if versionRolling && versionBits != "" {
		vb, _ := strconv.ParseUint(versionBits, 16, 32)
		finalVersion = (uint32(curJob.version) & ^versionMask) | (uint32(vb) & versionMask)
	} else {
		finalVersion = uint32(curJob.version)
	}

	templateHeader = types.NewBlockHeader(
		types.PowID(powID),
		int32(finalVersion),
		templateHeader.PrevBlock(),
		merkleRoot,
		uint32(ntime),
		templateHeader.Bits(),
		uint32(nonce),
		0,
	)

	hashBytes := templateHeader.PowHash().Bytes()
	powHashBigInt := new(big.Int).SetBytes(hashBytes)

	// Get workshare target
	var workShareTarget *big.Int
	switch pending.AuxPow().PowID() {
	case types.Scrypt:
		workShareTarget = new(big.Int).Div(common.Big2e256, pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty())
	default:
		workShareTarget = new(big.Int).Div(common.Big2e256, pending.WorkObjectHeader().ShaDiffAndCount().Difficulty())
	}
	if workShareTarget == nil {
		return false, fmt.Errorf("missing workshare difficulty")
	}

	// Calculate achieved difficulty
	var achievedDiff *big.Int
	if powHashBigInt.Sign() > 0 {
		achievedDiff = new(big.Int).Div(common.Big2e256, powHashBigInt)
	}

	if pending.WorkObjectHeader().AuxPow() == nil {
		return false, fmt.Errorf("work object missing auxpow")
	}

	pending.WorkObjectHeader().AuxPow().SetHeader(templateHeader)
	pending.WorkObjectHeader().AuxPow().SetTransaction(fullCoinb)

	// Check if share meets workshare target (hash must be <= target)
	meetsWorkshareTarget := powHashBigInt.Cmp(workShareTarget) <= 0

	// Calculate stratum difficulties for comparison
	var diff1 float64
	var workshareRawDiff *big.Int
	switch powID {
	case types.Scrypt:
		diff1 = 65536.0 // 2^16
		if pending.WorkObjectHeader().ScryptDiffAndCount() != nil {
			workshareRawDiff = pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
		}
	default:
		diff1 = 4294967296.0 // 2^32 for SHA
		if pending.WorkObjectHeader().ShaDiffAndCount() != nil {
			workshareRawDiff = pending.WorkObjectHeader().ShaDiffAndCount().Difficulty()
		}
	}

	var workshareStratumDiff float64
	if workshareRawDiff != nil {
		workshareStratumDiff, _ = new(big.Float).Quo(
			new(big.Float).SetInt(workshareRawDiff),
			new(big.Float).SetFloat64(diff1),
		).Float64()
	}

	var achievedStratumDiff float64
	if achievedDiff != nil {
		if achievedDiff.BitLen() > 63 {
			achievedStratumDiff, _ = new(big.Float).Quo(
				new(big.Float).SetInt(achievedDiff),
				new(big.Float).SetFloat64(diff1),
			).Float64()
		} else {
			achievedStratumDiff = float64(achievedDiff.Uint64()) / diff1
		}
	}

	// Determine liveness target - Priority: 1) miner-specified d=X, 2) vardiff, 3) workshare only
	var livenessTarget float64
	var usingVarDiff bool
	if minerDifficulty != nil {
		livenessTarget = *minerDifficulty
	} else if varDiffEnabled {
		livenessTarget = varDiff
		usingVarDiff = true
	}

	if livenessTarget > 0 && achievedDiff != nil {
		// Check if share meets liveness difficulty
		if achievedStratumDiff < livenessTarget {
			s.logger.WithFields(log.Fields{
				"achievedDiff": achievedStratumDiff,
				"targetDiff":   livenessTarget,
				"vardiff":      usingVarDiff,
			}).Debug("share did not meet liveness difficulty")
			return false, fmt.Errorf("share difficulty %.4f below target %.4f", achievedStratumDiff, livenessTarget)
		}

		// Share meets liveness difficulty - update lastShareTime for liveness tracking
		// For vardiff, adjustVarDiff also updates lastShareTime internally
		if usingVarDiff && s.adjustVarDiff(sess, workshareStratumDiff) {
			sess.mu.Lock()
			newDiff := sess.varDiff
			sess.lastSentDifficulty = newDiff // Track that we're sending this difficulty
			sess.mu.Unlock()
			diffNote := map[string]interface{}{"id": nil, "method": "mining.set_difficulty", "params": []interface{}{newDiff}}
			if err := sess.sendJSON(diffNote); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send vardiff mining.set_difficulty (sha256/scrypt)")
			}
		} else if !usingVarDiff {
			// For non-vardiff sessions, update lastShareTime explicitly for liveness tracking
			sess.mu.Lock()
			sess.lastShareTime = time.Now()
			sess.mu.Unlock()
		}

		// If it doesn't meet workshare target, accept for liveness but don't submit
		if !meetsWorkshareTarget {
			s.logger.WithFields(log.Fields{
				"powID":        powID,
				"achievedDiff": achievedStratumDiff,
				"targetDiff":   livenessTarget,
				"vardiff":      usingVarDiff,
				"workerName":   sess.workerName,
			}).Debug("share accepted for liveness (below workshare difficulty)")

			// Record the share for hashrate calculation even though it won't be submitted to network
			var algoName string
			switch powID {
			case types.Scrypt:
				algoName = "scrypt"
			case types.Kawpow:
				algoName = "kawpow"
			default:
				algoName = "sha256"
			}
			workshareDiffFloat, _ := new(big.Float).SetInt(new(big.Int).Div(common.Big2e256, workShareTarget)).Float64()
			achievedDiffFloat, _ := new(big.Float).SetInt(achievedDiff).Float64()
			minerDiff := float64(1)
			if sess.minerDifficulty != nil {
				minerDiff = *sess.minerDifficulty
			}
			s.stats.ShareSubmittedWithDiff(sess.user, sess.workerName, algoName, minerDiff, achievedDiffFloat, workshareDiffFloat, true, false)

			return false, nil // Accept share but don't submit to node
		}
	}

	// LRU de-dup and submit workshare
	// Note: seenShares LRU is thread-safe, no lock needed
	shareKey := hex.EncodeToString(hashBytes)
	if _, seen := sess.seenShares.Peek(shareKey); seen {
		return false, fmt.Errorf("duplicate share")
	}
	sess.seenShares.Add(shareKey, struct{}{})

	s.logger.WithFields(log.Fields{"powID": pending.AuxPow().PowID(), "achievedDiff": achievedStratumDiff, "workshareDiff": workshareStratumDiff}).Info("submitting workshare to node")

	// Get algorithm name for stats
	var algoName string
	switch pending.AuxPow().PowID() {
	case types.Scrypt:
		algoName = "scrypt"
	case types.Kawpow:
		algoName = "kawpow"
	default:
		algoName = "sha256"
	}

	// Get workshare difficulty as float
	workshareDiff := new(big.Float).SetInt(new(big.Int).Div(common.Big2e256, workShareTarget))
	workshareDiffFloat, _ := workshareDiff.Float64()
	achievedDiffFloat, _ := new(big.Float).SetInt(achievedDiff).Float64()

	// Use the effective pool difficulty (minerDifficulty if set, otherwise workshare-based)
	sess.mu.Lock()
	effectiveDiff := sess.difficulty
	if sess.minerDifficulty != nil && *sess.minerDifficulty < sess.difficulty {
		effectiveDiff = *sess.minerDifficulty
	}
	sess.mu.Unlock()

	// Record the share with difficulty info for solo mining luck tracking
	s.stats.ShareSubmittedWithDiff(sess.user, sess.workerName, algoName, effectiveDiff, achievedDiffFloat, workshareDiffFloat, true, false)

	// Update lastShareTime for workshare-only mode (livenessTarget was 0)
	// In liveness mode, lastShareTime was already updated above
	if livenessTarget == 0 {
		sess.mu.Lock()
		sess.lastShareTime = time.Now()
		sess.mu.Unlock()
	}

	// Second check: does it also meet workshare target? If so, submit to network
	if powHashBigInt.Cmp(workShareTarget) <= 0 {
		s.logger.WithFields(log.Fields{"powID": pending.AuxPow().PowID(), "achievedDiff": achievedDiff.String(), "hashBytes": hex.EncodeToString(hashBytes), "workshareHash": pending.Hash().Hex()}).Info("workshare received - submitting to network")
		return true, s.backend.ReceiveMinedHeader(pending)
	}

	return true, nil
}

// submitKawpowShare handles kawpow share submissions
// Kawpow submit params: [worker, job_id, nonce, header_hash, mix_hash]
// Note: headerHashHex from miner is validated against kawJob.headerHash but the job's hash is used for computation
func (s *Server) submitKawpowShare(sess *session, kawJob *kawpowJob, nonceHex, _ /* headerHashHex */, mixHashHex string) error {
	if kawJob == nil || kawJob.pending == nil {
		return fmt.Errorf("no kawpow job")
	}

	pending, ok := kawJob.pending.(*types.WorkObject)
	if !ok || pending == nil {
		return fmt.Errorf("invalid kawpow pending work")
	}

	// Snapshot needed session fields under one lock
	sess.mu.Lock()
	minerDifficulty := sess.minerDifficulty
	varDiffEnabled := sess.varDiffEnabled
	varDiff := sess.varDiff
	sess.mu.Unlock()

	// Parse nonce (8 bytes for kawpow)
	nonceHex = strings.TrimPrefix(nonceHex, "0x")
	nonce, err := strconv.ParseUint(nonceHex, 16, 64)
	if err != nil {
		return fmt.Errorf("invalid nonce: %v", err)
	}

	// Parse mix hash (32 bytes)
	mixHashHex = strings.TrimPrefix(mixHashHex, "0x")
	mixHashBytes, err := hex.DecodeString(mixHashHex)
	if err != nil || len(mixHashBytes) != 32 {
		return fmt.Errorf("invalid mix hash: length=%d", len(mixHashBytes))
	}

	// Parse header hash from job (this is the seal hash used for kawpow input)
	headerHashBytes, err := hex.DecodeString(kawJob.headerHash)
	if err != nil || len(headerHashBytes) != 32 {
		return fmt.Errorf("invalid header hash in job: %v", err)
	}
	kawpowHeaderHash := common.BytesToHash(headerHashBytes)

	// Get block height for epoch calculation
	blockNumber := kawJob.height

	// Compute Kawpow hash using the engine's VerifyKawpowShare
	calculatedMixHash, powHash, err := s.kawpowEngine.VerifyKawpowShare(kawpowHeaderHash, nonce, blockNumber)
	if err != nil {
		return fmt.Errorf("failed to compute kawpow hash: %v", err)
	}

	// Miner submits mixhash in big-endian, but VerifyKawpowShare returns little-endian
	mixHashLE := make([]byte, 32)
	for i := 0; i < 32; i++ {
		mixHashLE[i] = mixHashBytes[31-i]
	}
	submittedMixHash := common.BytesToHash(mixHashLE)

	// Verify the mix hash matches
	if !bytes.Equal(calculatedMixHash.Bytes(), submittedMixHash.Bytes()) {
		s.logger.WithFields(log.Fields{
			"calculated": hex.EncodeToString(calculatedMixHash.Bytes()),
			"submitted":  hex.EncodeToString(submittedMixHash.Bytes()),
		}).Error("kawpow mixhash mismatch")
		return fmt.Errorf("mixhash mismatch: calculated %x, submitted %x",
			calculatedMixHash.Bytes(), submittedMixHash.Bytes())
	}

	// Get workshare target from the pending header
	kawpowShareDiff := core.CalculateKawpowShareDiff(pending.WorkObjectHeader())
	workShareTarget := new(big.Int).Div(common.Big2e256, kawpowShareDiff)
	powHashInt := new(big.Int).SetBytes(powHash.Bytes())

	// Check if share meets workshare target (hash must be <= target)
	meetsWorkshareTarget := powHashInt.Cmp(workShareTarget) <= 0

	// Calculate stratum difficulties
	achievedStratumDiff, _ := new(big.Float).Quo(
		new(big.Float).SetInt(KawpowDiff1),
		new(big.Float).SetInt(powHashInt),
	).Float64()

	workshareStratumDiff, _ := new(big.Float).Quo(
		new(big.Float).SetInt(KawpowDiff1),
		new(big.Float).SetInt(workShareTarget),
	).Float64()

	// Determine liveness target - Priority: 1) miner-specified d=X, 2) vardiff, 3) workshare only
	var livenessTarget float64
	var usingVarDiff bool
	if minerDifficulty != nil {
		livenessTarget = *minerDifficulty
	} else if varDiffEnabled {
		livenessTarget = varDiff
		usingVarDiff = true
	}

	if livenessTarget > 0 {
		// Check if share meets liveness difficulty
		if achievedStratumDiff < livenessTarget {
			s.logger.WithFields(log.Fields{
				"achievedDiff": achievedStratumDiff,
				"targetDiff":   livenessTarget,
				"vardiff":      usingVarDiff,
			}).Debug("share did not meet liveness difficulty")
			return fmt.Errorf("share difficulty %.4f below target %.4f", achievedStratumDiff, livenessTarget)
		}

		// Share meets liveness difficulty - update lastShareTime for liveness tracking
		// For vardiff, adjustVarDiff also updates lastShareTime internally
		if usingVarDiff && s.adjustVarDiff(sess, workshareStratumDiff) {
			sess.mu.Lock()
			newDiff := sess.varDiff
			// For kawpow, convert difficulty to target: target = KawpowDiff1 / difficulty
			minerDiffBig := new(big.Float).SetFloat64(newDiff)
			diff1Float := new(big.Float).SetInt(KawpowDiff1)
			targetFloat := new(big.Float).Quo(diff1Float, minerDiffBig)
			targetInt, _ := targetFloat.Int(nil)
			targetBytes := targetInt.Bytes()
			result := make([]byte, 32)
			copy(result[32-len(targetBytes):], targetBytes)
			targetHex := hex.EncodeToString(result)
			sess.lastSentTarget = targetHex // Track that we're sending this target
			sess.mu.Unlock()
			targetNote := map[string]interface{}{"id": nil, "method": "mining.set_target", "params": []interface{}{targetHex}}
			if err := sess.sendJSON(targetNote); err != nil {
				s.logger.WithFields(log.Fields{"error": err, "worker": sess.user + "." + sess.workerName}).Debug("failed to send vardiff mining.set_target (kawpow)")
			}
		} else if !usingVarDiff {
			// For non-vardiff sessions, update lastShareTime explicitly for liveness tracking
			sess.mu.Lock()
			sess.lastShareTime = time.Now()
			sess.mu.Unlock()
		}

		// If it doesn't meet workshare target, accept for liveness but don't submit
		if !meetsWorkshareTarget {
			s.logger.WithFields(log.Fields{
				"powID":         types.Kawpow,
				"height":        kawJob.height,
				"achievedDiff":  achievedStratumDiff,
				"targetDiff":    livenessTarget,
				"workshareDiff": kawpowShareDiff.String(),
				"vardiff":       usingVarDiff,
			}).Debug("share accepted for liveness (below workshare difficulty)")
			s.stats.ShareSubmittedWithDiff(sess.user, sess.workerName, "kawpow", sess.difficulty, achievedStratumDiff, workshareStratumDiff, true, false)
			return nil
		}
	} else if !meetsWorkshareTarget {
		s.logger.WithFields(log.Fields{
			"powHash": hex.EncodeToString(powHash.Bytes()),
			"target":  workShareTarget.String(),
		}).Debug("share did not meet workshare target")
		return fmt.Errorf("share did not meet target")
	}

	// LRU de-dup and submit workshare
	// Note: seenShares LRU is thread-safe, no lock needed
	shareKey := hex.EncodeToString(powHash.Bytes())
	if _, seen := sess.seenShares.Peek(shareKey); seen {
		return fmt.Errorf("duplicate share")
	}
	sess.seenShares.Add(shareKey, struct{}{})

	// Set the nonce and mix hash on the AuxPow's Ravencoin header for submission.
	// The kawpow validation path (ComputePowLight/ComputePowHash) reads from:
	// - header.AuxPow().Header().Nonce64()
	// - header.AuxPow().Header().MixHash()
	// See consensus/kawpow/kawpow.go:549-557 and :491
	auxPowHeader := pending.WorkObjectHeader().AuxPow().Header()
	if auxPowHeader == nil {
		return fmt.Errorf("AuxPow header is nil")
	}

	// Set nonce on Ravencoin header
	auxPowHeader.SetNonce64(nonce)

	// Set mixHash on Ravencoin header
	// Miner submits mixhash in big-endian, but RavencoinBlockHeader stores it in little-endian
	// (same as what kawpowLight returns).
	// Note: mixHashLE was already computed above for verification, reuse it here
	auxPowHeader.SetMixHash(common.BytesToHash(mixHashLE))

	// Calculate achieved difficulty for logging
	achievedDiff := new(big.Int).Div(common.Big2e256, powHashInt)

	// Get workshare difficulty for stats
	workshareDiffFloat, _ := new(big.Float).SetInt(pending.WorkObjectHeader().KawpowDifficulty()).Float64()
	achievedDiffFloat, _ := new(big.Float).SetInt(achievedDiff).Float64()

	// Record the share with difficulty info
	s.stats.ShareSubmittedWithDiff(sess.user, sess.workerName, "kawpow", sess.difficulty, achievedDiffFloat, workshareDiffFloat, true, false)

	// Update lastShareTime for workshare-only mode (livenessTarget was 0)
	// In liveness mode, lastShareTime was already updated above
	if livenessTarget == 0 {
		sess.mu.Lock()
		sess.lastShareTime = time.Now()
		sess.mu.Unlock()
	}

	s.logger.WithFields(log.Fields{
		"powID":         types.Kawpow,
		"height":        kawJob.height,
		"achievedDiff":  achievedStratumDiff,
		"workshareDiff": workshareStratumDiff,
	}).Info("submitting kawpow workshare to node")

	err = s.backend.ReceiveMinedHeader(pending)
	if err != nil {
		s.logger.WithFields(log.Fields{"powID": types.Kawpow, "height": kawJob.height, "worker": sess.user + "." + sess.workerName, "error": err}).Error("kawpow workshare rejected by node")
	} else {
		s.logger.WithFields(log.Fields{"powID": types.Kawpow, "height": kawJob.height, "worker": sess.user + "." + sess.workerName, "hash": pending.Hash().Hex()}).Info("kawpow workshare accepted by node")
	}
	return err
}

// swapWords32x32 swaps byte order within each 4-byte word of a 32-byte array.
func swapWords32x32(in [32]byte) [32]byte {
	var out [32]byte
	for off := 0; off < 32; off += 4 {
		out[off+0] = in[off+3]
		out[off+1] = in[off+2]
		out[off+2] = in[off+1]
		out[off+3] = in[off+0]
	}
	return out
}

// fullReverse reverses the entire byte slice.
func fullReverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

func powIDFromChain(chain string) types.PowID {
	switch strings.ToLower(chain) {
	case "sha256":
		return types.SHA_BCH
	case "scrypt":
		return types.Scrypt
	case "kawpow":
		return types.Kawpow
	default:
		return types.SHA_BTC
	}
}

// parseUsername parses the stratum username format: address.workername.frequency=X (or freq=X)
// Returns the address, worker name, and job frequency.
// Examples:
//   - "0x1234..." -> ("0x1234...", "default", 5s)
//   - "0x1234....rig1" -> ("0x1234...", "rig1", 5s)
//   - "0x1234....rig1.frequency=2.5" or "0x1234....rig1.freq=2.5" -> ("0x1234...", "rig1", 2.5s)
//   - "0x1234....freq=3" -> ("0x1234...", "default", 3s)
func parseUsername(u string) (address string, workerName string, jobFreq time.Duration) {
	jobFreq = defaultJobFrequency
	workerName = "default"

	// Split by dots, but be careful with addresses that may contain dots
	// Format: address.workername.frequency=X or address.frequency=X
	parts := strings.Split(u, ".")

	if len(parts) == 0 {
		return u, workerName, jobFreq
	}

	// First part is always the address
	address = parts[0]

	// Process remaining parts
	for i := 1; i < len(parts); i++ {
		part := parts[i]

		// Check if this part is a frequency setting (accept both "frequency=" and "freq=")
		freqStr, isFreq := strings.CutPrefix(part, "frequency=")
		if !isFreq {
			freqStr, isFreq = strings.CutPrefix(part, "freq=")
		}
		if isFreq {
			if freq, err := strconv.ParseFloat(freqStr, 64); err == nil {
				// Convert seconds to duration and clamp to bounds
				freqDuration := time.Duration(freq * float64(time.Second))
				if freqDuration < minJobFrequency {
					freqDuration = defaultJobFrequency
				} else if freqDuration > maxJobFrequency {
					freqDuration = defaultJobFrequency
				}
				jobFreq = freqDuration
			}
		} else if workerName == "default" {
			// First non-frequency part after address is the worker name
			workerName = part
		}
	}

	return address, workerName, jobFreq
}

// parsePassword parses the stratum password field for optional parameters.
// Supports: d=<difficulty>, frequency=<seconds> (or freq=<seconds>), skip=<true|false>
// Parts are separated by ',' character (to allow decimal values).
// Examples:
//   - "x" or "" -> nil, 0, false (use defaults)
//   - "d=0.1" -> difficulty=0.1, frequency=0, skip=false
//   - "frequency=2.5" or "freq=2.5" -> difficulty=nil, frequency=2.5s, skip=false
//   - "d=0.1,freq=2,skip=true" -> difficulty=0.1, frequency=2s, skip=true
func parsePassword(password string) (*float64, time.Duration, bool) {
	if password == "" || password == "x" {
		return nil, 0, false
	}

	var difficulty *float64
	var frequency time.Duration
	var skipBlocks bool

	// Split by ',' to allow decimal values in parameters
	parts := strings.Split(password, ",")
	for _, part := range parts {
		// Check for d=<difficulty> format
		if diffStr, ok := strings.CutPrefix(part, "d="); ok {
			if diff, err := strconv.ParseFloat(diffStr, 64); err == nil && diff > 0 {
				difficulty = &diff
			}
			continue
		}
		// Check for frequency=<seconds> or freq=<seconds> format
		freqStr, isFreq := strings.CutPrefix(part, "frequency=")
		if !isFreq {
			freqStr, isFreq = strings.CutPrefix(part, "freq=")
		}
		if isFreq {
			if freq, err := strconv.ParseFloat(freqStr, 64); err == nil && freq >= 1 && freq <= 10 {
				frequency = time.Duration(freq * float64(time.Second))
			}
			continue
		}
		// Check for skip=<true|false> format
		if skipStr, ok := strings.CutPrefix(part, "skip="); ok {
			skipBlocks = skipStr == "true" || skipStr == "1"
			continue
		}
	}

	return difficulty, frequency, skipBlocks
}
