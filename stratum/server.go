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
	"time"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus/kawpow"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// randReader uses time-based fallback if crypto/rand isn't imported; here we just read from net.Conn deadlines which is not desirable.
// Implement a simple wrapper over math/rand seeded by time if needed. For our use (4 bytes), this is sufficient.
type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	now := time.Now().UnixNano()
	for i := range p {
		now = (now*1103515245 + 12345) & 0x7fffffff
		p[i] = byte(now & 0xff)
	}
	return len(p), nil
}

// StratumConfig holds configuration for the stratum server
type StratumConfig struct {
	SHAAddr    string // Address for SHA miners (e.g., ":3333")
	ScryptAddr string // Address for Scrypt miners (e.g., ":4444")
	KawpowAddr string // Address for Kawpow miners (e.g., ":5555")
}

// templateState tracks the last template sent for change detection
type templateState struct {
	sealHash   common.Hash // For SHA/Scrypt: header seal hash
	parentHash common.Hash // For change detection (new block)
	height     uint64      // Block height
	quaiHeight int64       // Quai chain height (for kawpow stale detection)
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
	// simple counters for debugging submission quality
	submits      uint64
	passPowCount uint64
	passRelCount uint64
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
	return &Server{
		config:         config,
		backend:        backend,
		logger:         logger,
		stats:          NewPoolStats(),
		kawpowEngine:   kawpowEngine,
		sessionsSHA:    make(map[*session]struct{}),
		sessionsScrypt: make(map[*session]struct{}),
		sessionsKawpow: make(map[*session]struct{}),
	}
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
	case "sha":
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
	case "sha":
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

	// Start SHA listener if configured
	if s.config.SHAAddr != "" {
		ln, err := net.Listen("tcp", s.config.SHAAddr)
		if err != nil {
			return fmt.Errorf("failed to start SHA listener on %s: %v", s.config.SHAAddr, err)
		}
		s.lnSHA = ln
		s.logger.WithField("addr", s.config.SHAAddr).Info("SHA stratum listener started")
		go s.acceptLoop(ln, "sha")
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
		go s.templatePollingLoop("sha")
	}
	if s.config.ScryptAddr != "" {
		go s.templatePollingLoop("scrypt")
	}
	if s.config.KawpowAddr != "" {
		go s.templatePollingLoop("kawpow")
	}

	// Start force broadcast loops to ensure miners always have work.
	// Runs every 1 second (minimum frequency granularity) and checks each session's
	// individual jobFrequency setting. Miners can configure their frequency via username:
	// address.workername.frequency=X (1-10 seconds, supports decimals, default 5s)
	if s.config.SHAAddr != "" {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.forceBroadcastStale("sha")
			}
		}()
	}
	if s.config.ScryptAddr != "" {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.forceBroadcastStale("scrypt")
			}
		}()
	}
	if s.config.KawpowAddr != "" {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.forceBroadcastStale("kawpow")
			}
		}()
	}

	return nil
}

// templatePollingLoop polls for new templates and broadcasts to connected miners
func (s *Server) templatePollingLoop(algorithm string) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		changed, clean := s.checkTemplateChanged(algorithm)
		if changed {
			s.broadcastJob(algorithm, clean)
		}
	}
}

// checkTemplateChanged checks if the template has changed for the given algorithm.
// Returns (changed bool, clean bool) - clean is true if miners should abandon current work.
func (s *Server) checkTemplateChanged(algorithm string) (bool, bool) {
	powID := powIDFromChain(algorithm)

	// Get pending header (using a dummy address since we just need to check for changes)
	pending, err := s.backend.GetPendingHeader(types.PowID(powID), common.Address{})
	if err != nil || pending == nil || pending.WorkObjectHeader() == nil {
		return false, false
	}

	header := pending.WorkObjectHeader()
	auxPow := header.AuxPow()
	if auxPow == nil || auxPow.Header() == nil {
		return false, false
	}

	// Use AuxPow header's PrevBlock (donor chain parent) for change detection,
	// not Quai's ParentHash. Miners need new work when the donor chain advances.
	auxParentHash := auxPow.Header().PrevBlock()
	newState := &templateState{
		sealHash:   header.SealHash(),
		parentHash: common.BytesToHash(auxParentHash[:]),
		height:     uint64(auxPow.Header().Height()), // AuxPow height (donor chain block number)
		quaiHeight: int64(pending.NumberU64(common.ZONE_CTX)),
	}

	s.templateMu.Lock()
	defer s.templateMu.Unlock()

	var lastState *templateState
	switch algorithm {
	case "sha":
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
		return false, false
	}

	// Check for changes based on algorithm
	switch algorithm {
	case "sha", "scrypt":
		// For SHA/Scrypt: check if parent hash or height changed
		// Always use clean=true for SHA/Scrypt (simpler, matches pool behavior)
		if lastState.parentHash != newState.parentHash {
			s.logger.WithFields(log.Fields{
				"algo":      algorithm,
				"oldHeight": lastState.height,
				"newHeight": newState.height,
			}).Info("New block detected")
			return true, true
		}

		if lastState.quaiHeight != newState.quaiHeight {
			s.logger.WithFields(log.Fields{
				"algo":          algorithm,
				"oldQuaiHeight": lastState.quaiHeight,
				"newQuaiHeight": newState.quaiHeight,
			}).Info("QuaiHeight changed")
			return true, true
		}

	case "kawpow":
		// For Kawpow: check parent hash and quai height
		if lastState.parentHash != newState.parentHash {
			s.logger.WithFields(log.Fields{
				"algo":      algorithm,
				"oldHeight": lastState.height,
				"newHeight": newState.height,
			}).Info("New block detected")
			return true, true
		}
		if lastState.quaiHeight != newState.quaiHeight {
			s.logger.WithFields(log.Fields{
				"algo":          algorithm,
				"oldQuaiHeight": lastState.quaiHeight,
				"newQuaiHeight": newState.quaiHeight,
			}).Info("QuaiHeight changed")
			return true, true
		}
		// Seal hash change without parent/quaiHeight change - template update, clean=false
		if lastState.sealHash != newState.sealHash {
			s.logger.WithFields(log.Fields{
				"algo":   algorithm,
				"height": newState.height,
			}).Trace("Template updated (sealhash changed)")
			return true, true // Kawpow: clean=false for same-block updates
		}
	}

	return false, false
}

// broadcastJob sends a new job to all connected miners for the given algorithm
func (s *Server) broadcastJob(algorithm string, clean bool) {
	s.sessionsMu.RLock()
	var sessions map[*session]struct{}
	switch algorithm {
	case "sha":
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
		"algo":   algorithm,
		"miners": len(sessionList),
		"clean":  clean,
	}).Info("Broadcasting new job")

	// Broadcast to all sessions
	for _, sess := range sessionList {
		go func(sess *session) {
			if err := s.sendJobAndNotify(sess, clean); err != nil {
				s.logger.WithFields(log.Fields{
					"user":  sess.user,
					"error": err,
				}).Debug("Failed to broadcast job")
			}
		}(sess)
	}
}

// forceBroadcastStale checks all sessions for an algorithm and force-broadcasts
// the latest template to any session that hasn't received a job within its configured
// jobFrequency. Each miner can configure their own frequency via username format:
// address.workername.frequency=X (1-10 seconds, supports decimals, default 5s)
func (s *Server) forceBroadcastStale(algorithm string) {
	s.sessionsMu.RLock()
	var sessions map[*session]struct{}
	switch algorithm {
	case "sha":
		sessions = s.sessionsSHA
	case "scrypt":
		sessions = s.sessionsScrypt
	case "kawpow":
		sessions = s.sessionsKawpow
	}
	// Copy stale sessions to avoid holding lock during broadcast
	staleSessions := make([]*session, 0)
	now := time.Now()
	for sess := range sessions {
		if !sess.authorized {
			continue
		}
		sess.mu.Lock()
		timeSinceLastJob := now.Sub(sess.lastJobSent)
		threshold := sess.jobFrequency
		sess.mu.Unlock()
		// Use per-session threshold (configured via username)
		if timeSinceLastJob >= threshold {
			staleSessions = append(staleSessions, sess)
		}
	}
	s.sessionsMu.RUnlock()

	if len(staleSessions) == 0 {
		return
	}

	s.logger.WithFields(log.Fields{
		"algo":   algorithm,
		"miners": len(staleSessions),
	}).Debug("Force broadcasting to stale sessions")

	// Broadcast with clean=true to stale sessions
	for _, sess := range staleSessions {
		go func(sess *session) {
			if err := s.sendJobAndNotify(sess, true); err != nil {
				s.logger.WithFields(log.Fields{
					"user":  sess.user,
					"error": err,
				}).Debug("Failed to force broadcast job")
			}
		}(sess)
	}
}

func (s *Server) Stop() error {
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
		// Enable TCP keepalive to detect dead connections faster than OS default (2hrs)
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(30 * time.Minute) // Probe every 30 min after idle
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

type session struct {
	conn       net.Conn
	enc        *json.Encoder
	dec        *json.Decoder
	encMu      sync.Mutex // protects enc writes (broadcasts + responses can race)
	authorized bool
	user       string // payout address
	workerName string // worker name (from user.workerName format)
	chain      string // sha|scrypt
	job        *job
	kawJob     *kawpowJob // kawpow-specific job data
	xnonce1    []byte
	// vardiff state (per-connection)
	difficulty    float64   // last sent miner difficulty
	vdWindowStart time.Time // window start for submit rate
	vdSubmits     int       // submits counted in window
	// version rolling state
	versionRolling bool
	versionMask    uint32
	// job tracking
	mu         sync.Mutex // protects job, jobs, jobSeq, jobHistory, difficulty
	jobs       map[string]*job
	kawJobs    map[string]*kawpowJob // kawpow job tracking
	jobSeq     uint64
	jobHistory []string // FIFO of recent job IDs for simple expiry
	// share de-duplication (per-connection LRU)
	seenShares map[string]struct{}
	seenOrder  []string
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
	dec := json.NewDecoder(bufio.NewReader(c))
	enc := json.NewEncoder(c)
	sess := &session{
		conn:         c,
		enc:          enc,
		dec:          dec,
		chain:        algorithm, // Set algorithm from the port they connected to
		jobs:         make(map[string]*job),
		kawJobs:      make(map[string]*kawpowJob),
		seenShares:   make(map[string]struct{}),
		seenOrder:    make([]string, 0, 1024),
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

	// No read deadline - miners may go hours/days between shares with high workshare difficulty.
	// Dead connections are detected via TCP keepalive or when writes fail.
	// If explicit timeout is needed later, consider 24+ hours or implement ping/pong.

	for {

		var req stratumReq
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		switch req.Method {
		case "mining.subscribe":
			// Response differs by algorithm:
			// - Kawpow: [nil, "00"] - extranonce not used (64-bit header nonce is sufficient)
			// - SHA/Scrypt: [[subscriptions], extranonce1, extranonce2_size]
			if sess.chain == "kawpow" {
				// Kawpow doesn't use extranonce - the 64-bit header nonce provides enough search space
				result := []interface{}{
					nil,  // Session ID (not used for resuming)
					"00", // extranonce1 - placeholder, not used
				}
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: result, Error: nil})
			} else {
				// SHA/Scrypt use extranonce for coinbase modification
				x1 := []byte{0x01, 0x01, 0x01, 0x01} // All 1s instead of random
				sess.xnonce1 = append([]byte{}, x1...)
				result := []interface{}{
					[]interface{}{
						[]interface{}{"mining.set_difficulty", "1"},
						[]interface{}{"mining.notify", "1"},
					},
					hex.EncodeToString(x1), // Will send "01010101"
					8,                      // extranonce2_size (set to 8 per nerdqaxe++ expectations)
				}
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: result, Error: nil})
			}
		case "mining.extranonce.subscribe":
			_ = sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil})
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
				sess.versionRolling = true
				sess.versionMask = 0x1fffe000 // Standard mask (bits 13-28)
				resp["version-rolling"] = true
				resp["version-rolling.mask"] = fmt.Sprintf("%08x", sess.versionMask)
				resp["version-rolling.min-bit-count"] = 0
				s.logger.WithField("mask", fmt.Sprintf("0x%08x", sess.versionMask)).Info("VERSION-ROLLING enabled")
			}
			_ = sess.sendJSON(stratumResp{ID: req.ID, Result: resp, Error: nil})

			// Send mining.set_version_mask for miners that expect it after configure
			if sess.versionRolling {
				note := map[string]interface{}{"id": nil, "method": "mining.set_version_mask", "params": []interface{}{fmt.Sprintf("%08x", sess.versionMask)}}
				_ = sess.sendJSON(note)
			}
		case "mining.authorize":
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
			// Parse password for optional difficulty and frequency settings
			// Format: d=<difficulty>,frequency=<seconds> or just one of them
			if len(req.Params) >= 2 {
				if p, ok := req.Params[1].(string); ok {
					var pwFreq time.Duration
					sess.minerDifficulty, pwFreq = parsePassword(p)
					if pwFreq > 0 {
						sess.jobFrequency = pwFreq // Password frequency overrides username frequency
					}
				}
			}
			// Validate that the address is internal (belongs to this zone)
			address := common.HexToAddress(sess.user, s.backend.NodeLocation())
			if _, err := address.InternalAddress(); err != nil {
				s.logger.WithFields(log.Fields{
					"user":  sess.user,
					"error": err,
				}).Warn("miner address is not internal to this zone")
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "address is not internal to this zone"})
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
			}
			if sess.minerDifficulty != nil {
				logFields["minerDifficulty"] = *sess.minerDifficulty
			}
			s.logger.WithFields(logFields).Info("miner authorized")
			_ = sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil})
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
							if sess.job != nil || sess.kawJob != nil {
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
			// SHA/Scrypt uses: [worker, job_id, ex2, ntime, nonce, version_bits?]
			if powIDFromChain(sess.chain) == types.Kawpow {
				if len(req.Params) < 5 {
					_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "bad kawpow params"})
					continue
				}
				// Kawpow params: [worker, job_id, nonce, header_hash, mix_hash]
				jobID, _ := req.Params[1].(string)
				nonceHex, _ := req.Params[2].(string)
				headerHashHex, _ := req.Params[3].(string)
				mixHashHex, _ := req.Params[4].(string)

				sess.mu.Lock()
				kawJob, ok := sess.kawJobs[jobID]
				sess.mu.Unlock()
				if !ok {
					s.logger.WithFields(log.Fields{"jobID": jobID, "known": len(sess.kawJobs)}).Error("unknown kawpow jobID")
					s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, false, true) // stale share
					_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "nojob"})
					continue
				}

				// Submit kawpow share
				if err := s.submitKawpowShare(sess, kawJob, nonceHex, headerHashHex, mixHashHex); err != nil {
					s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, false, false) // invalid share
					_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: err.Error()})
				} else {
					s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, true, false) // valid share
					s.logger.WithFields(log.Fields{"addr": sess.user, "nonce": nonceHex, "mixHash": mixHashHex}).Info("kawpow submit accepted")
					sess.mu.Lock()
					delete(sess.kawJobs, jobID)
					sess.mu.Unlock()
					_ = sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil})
					// Send fresh job
					go func() {
						select {
						case <-sess.done:
							return
						default:
							if err := s.sendJobAndNotify(sess, true); err != nil {
								s.logger.WithField("error", err).Error("failed to send new kawpow job")
							}
						}
					}()
				}
				continue
			}

			// SHA/Scrypt submit handling
			if len(req.Params) < 5 {
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "bad params"})
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

			sess.mu.Lock()
			j, ok := sess.jobs[jobID]
			sess.mu.Unlock()
			if !ok {
				sess.sendJSON(stratumResp{
					ID:     req.ID,
					Result: false,
					Error:  fmt.Errorf("no such jobID %s", jobID).Error(),
				})
				continue
			}
			sess.job = j

			// Look up the submitted job by ID to avoid stale/current mismatches
			sess.mu.Lock()
			if j2, ok2 := sess.jobs[jobID]; ok2 {
				sess.job = j2
				sess.mu.Unlock()
			} else {
				known := len(sess.jobs)
				sess.mu.Unlock()
				s.logger.WithFields(log.Fields{"jobID": jobID, "known": known}).Error("unknown or stale jobID")
				s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, false, true) // stale share
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: "nojob"})
				continue
			}
			// apply nonce into AuxPow header and submit as workshare
			refreshJob, err := s.submitAsWorkShare(sess, ex2hex, ntimeHex, nonceHex, versionBits)
			if err != nil {
				s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, false, false) // invalid share
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: false, Error: err.Error()})
			} else {
				s.stats.ShareSubmitted(sess.user, sess.workerName, sess.difficulty, true, false) // valid share
				s.logger.WithFields(log.Fields{"addr": sess.user, "chain": sess.chain, "nonce": nonceHex}).Info("submit accepted")
				// Mark this job ID as consumed to prevent duplicate submissions
				sess.mu.Lock()
				delete(sess.jobs, jobID)
				sess.mu.Unlock()
				_ = sess.sendJSON(stratumResp{ID: req.ID, Result: true, Error: nil})
				// Send a fresh job after successful workshare to keep miner on latest work
				// Skip for sub-shares (met miner difficulty but not workshare difficulty)
				if refreshJob {
					go func() {
						select {
						case <-sess.done:
							return
						default:
							if err := s.sendJobAndNotify(sess, true); err != nil {
								s.logger.WithField("error", err).Error("failed to send new job after workshare")
							}
						}
					}()
				}
			}
		default:
			_ = sess.sendJSON(stratumResp{ID: req.ID, Result: nil, Error: nil})
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
	s.logger.WithField("chain", sess.chain).Debug("routing to SHA/Scrypt stratum")

	j, err := s.makeJob(sess)
	if err != nil {
		return err
	}
	// Assign a unique job ID and track it (protected by session mutex)
	sess.mu.Lock()
	j.id = s.newJobID(sess)
	sess.job = j
	if sess.jobs == nil {
		sess.jobs = make(map[string]*job)
	}
	sess.jobs[j.id] = j
	// Maintain job history to allow stale shares; keep 32 jobs for solo mining
	sess.jobHistory = append(sess.jobHistory, j.id)
	if len(sess.jobHistory) > 32 {
		old := sess.jobHistory[0]
		sess.jobHistory = sess.jobHistory[1:]
		delete(sess.jobs, old)
	}
	sess.mu.Unlock()
	s.logger.WithFields(log.Fields{"jobID": j.id, "chain": sess.chain}).Info("notify job")

	// Stratum difficulty mapping:
	// - SHA256: stratumDiff = workshareDiff / 2^32 (diff1 = 4294967296 hashes)
	// - Scrypt: stratumDiff = workshareDiff / 2^16 (diff1 = 65536 hashes)
	const (
		sha256Diff1 = 4294967296.0 // 2^32
		scryptDiff1 = 65536.0      // 2^16
	)
	d := 1e-10 // fallback
	switch powIDFromChain(sess.chain) {
	case types.SHA_BTC, types.SHA_BCH:
		if j.pending != nil && j.pending.WorkObjectHeader() != nil && j.pending.WorkObjectHeader().ShaDiffAndCount() != nil && j.pending.WorkObjectHeader().ShaDiffAndCount().Difficulty() != nil {
			sd := j.pending.WorkObjectHeader().ShaDiffAndCount().Difficulty()
			// stratumDiff = workshareDiff / 2^32
			diffF, _ := new(big.Float).Quo(new(big.Float).SetInt(sd), big.NewFloat(sha256Diff1)).Float64()
			if diffF <= 0 {
				diffF = d
			}
			sess.mu.Lock()
			sess.difficulty = diffF
			sess.mu.Unlock()

			s.logger.WithFields(log.Fields{"ShaDiff": sd.String(), "minerDiff": diffF}).Debug("sendJobAndNotify SHA diff")
		} else {
			s.logger.WithField("fallback", d).Debug("sendJobAndNotify: No SHA diff available, using fallback")
		}
	case types.Scrypt:
		if j.pending != nil && j.pending.WorkObjectHeader() != nil && j.pending.WorkObjectHeader().ScryptDiffAndCount() != nil && j.pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty() != nil {
			sd := j.pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
			// stratumDiff = workshareDiff / 2^16
			diffF, _ := new(big.Float).Quo(new(big.Float).SetInt(sd), big.NewFloat(scryptDiff1)).Float64()
			if diffF <= 0 {
				diffF = d
			}
			sess.mu.Lock()
			sess.difficulty = diffF
			sess.mu.Unlock()
			s.logger.WithFields(log.Fields{"ScryptDiff": sd.String(), "minerDiff": diffF}).Debug("sendJobAndNotify Scrypt diff")
		} else {
			s.logger.WithField("fallback", d).Debug("sendJobAndNotify: No Scrypt diff available, using fallback")
		}
	default:
		// Kawpow uses sendKawpowJob() which doesn't use mining.set_difficulty
		s.logger.WithFields(log.Fields{"chain": sess.chain, "fallback": d}).Debug("sendJobAndNotify: Non-SHA/Scrypt chain, using fallback")
	}

	// Send set_difficulty (and set_target for miners that honor it) then the job notify
	// If miner specified custom difficulty via password (d=<difficulty>), use that instead
	sess.mu.Lock()
	minerDiff := sess.difficulty
	sess.mu.Unlock()
	if sess.minerDifficulty != nil && *sess.minerDifficulty > 0 {
		minerDiff = *sess.minerDifficulty
		s.logger.WithFields(log.Fields{"minerDifficulty": minerDiff, "workshareDiff": sess.difficulty}).Debug("Using miner-specified difficulty for set_difficulty")
	} else {
		s.logger.WithField("difficulty", minerDiff).Debug("Sending mining.set_difficulty to miner")
	}
	diffNote := map[string]interface{}{"id": nil, "method": "mining.set_difficulty", "params": []interface{}{minerDiff}}
	_ = sess.sendJSON(diffNote)

	params := []interface{}{j.id, j.prevHashLE, j.coinb1, j.coinb2, j.merkleBranch, fmt.Sprintf("%08x", j.version), fmt.Sprintf("%08x", j.nBits), fmt.Sprintf("%08x", j.nTime), clean}

	note := map[string]interface{}{"id": nil, "method": "mining.notify", "params": params}

	err = sess.sendJSON(note)
	if err == nil {
		sess.mu.Lock()
		sess.lastJobSent = time.Now()
		sess.mu.Unlock()
	}
	return err
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

	// Get kawpow difficulty and convert to target
	var targetHex string
	if sess.minerDifficulty != nil && *sess.minerDifficulty > 0 {
		// Use miner-specified difficulty from password (d=<difficulty>)
		// target = KawpowDiff1 / minerDifficulty
		minerDiffBig := new(big.Float).SetFloat64(*sess.minerDifficulty)
		diff1Float := new(big.Float).SetInt(KawpowDiff1)
		targetFloat := new(big.Float).Quo(diff1Float, minerDiffBig)
		targetInt, _ := targetFloat.Int(nil)
		// Convert to 32-byte hex string, zero-padded
		targetBytes := targetInt.Bytes()
		result := make([]byte, 32)
		copy(result[32-len(targetBytes):], targetBytes)
		targetHex = hex.EncodeToString(result)
		s.logger.WithFields(log.Fields{"minerDifficulty": *sess.minerDifficulty, "target": targetHex, "height": height, "epoch": epoch}).Debug("kawpow job with miner difficulty")
	} else if pending.WorkObjectHeader().KawpowDifficulty() != nil {
		kawpowDiff := core.CalculateKawpowShareDiff(pending.WorkObjectHeader())
		targetHex = common.GetTargetInHex(kawpowDiff)
		s.logger.WithFields(log.Fields{"KawpowDiff": kawpowDiff.String(), "target": targetHex, "height": height, "epoch": epoch}).Debug("kawpow job params")
	} else {
		// Fallback to max target
		targetHex = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		s.logger.Warn("No kawpow difficulty available, using max target")
	}

	// Get nBits and header hash from AuxPow header
	// For kawpow, the header hash is the double SHA256 of the Ravencoin-style header
	// (version, prevHash, merkleRoot, time, bits, height) - excludes nonce and mixhash
	var nBits uint32
	var headerHash string
	auxPowHeader := pending.WorkObjectHeader().AuxPow().Header()
	if auxPowHeader != nil {
		nBits = auxPowHeader.Bits()
		// SealHash() returns GetKAWPOWHeaderHash() for RavencoinBlockHeader
		// which is the double SHA256 of the 80-byte header input fields
		sealHash := auxPowHeader.SealHash()
		// Reverse bytes for stratum (miners expect little-endian)7
		reversed := make([]byte, 32)
		for i := 0; i < 32; i++ {
			reversed[i] = sealHash[31-i]
		}
		headerHash = hex.EncodeToString(reversed)
	} else {
		s.logger.Error("AuxPow header is nil for kawpow job")
		return fmt.Errorf("AuxPow header is nil")
	}

	// Create kawpow job
	sess.mu.Lock()
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
	sess.kawJobs[jobID] = kawJob

	// Maintain job history to allow stale shares; keep 32 jobs for solo mining
	sess.jobHistory = append(sess.jobHistory, jobID)
	if len(sess.jobHistory) > 32 {
		old := sess.jobHistory[0]
		sess.jobHistory = sess.jobHistory[1:]
		delete(sess.kawJobs, old)
	}
	sess.mu.Unlock()

	s.logger.WithFields(log.Fields{"jobID": jobID, "height": height, "epoch": epoch}).Info("notify kawpow job")

	// Send set_target for kawpow miners (some expect this)
	targetNote := map[string]interface{}{"id": nil, "method": "mining.set_target", "params": []interface{}{targetHex}}
	_ = sess.sendJSON(targetNote)

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
	if err == nil {
		sess.mu.Lock()
		sess.lastJobSent = time.Now()
		sess.mu.Unlock()
	}
	return err
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

// submitAsWorkShare validates and submits a SHA/Scrypt share.
// Returns (refreshJob, error) where refreshJob indicates whether to send a new job after acceptance.
// refreshJob is false for sub-shares (met miner difficulty but not workshare difficulty).
func (s *Server) submitAsWorkShare(sess *session, ex2hex, ntimeHex, nonceHex, versionBits string) (bool, error) {
	powID := powIDFromChain(sess.chain)
	// fmt.Printf("[stratum] DEBUG submitAsWorkShare: chain=%s powID=%d ex2=%s ntime=%s nonce=%s versionBits=%s\n",
	// 	sess.chain, powID, ex2hex, ntimeHex, nonceHex, versionBits)

	// Snapshot current job under lock to avoid races with async job refresh
	sess.mu.Lock()
	curJob := sess.job
	sess.mu.Unlock()
	if curJob == nil {
		return false, fmt.Errorf("no current job")
	}
	pending := curJob.pending
	if pending == nil || pending.WorkObjectHeader() == nil || pending.WorkObjectHeader().AuxPow() == nil {
		return false, fmt.Errorf("no pending header for job")
	}

	// Rebuild donor header for SHA chains with updated merkle root and nTime
	templateHeader := pending.AuxPow().Header()

	// Reconstruct coinbase from coinb1 + ex2 + coinb2
	ex2, _ := hex.DecodeString(ex2hex)
	if len(ex2) != 8 {
		return false, fmt.Errorf("bad extranonce2 length")
	}

	// Reconstruct full coinbase: coinb1 + [0x04][ex1] + [0x04][ex2] + coinb2
	coinb1Bytes, _ := hex.DecodeString(curJob.coinb1)
	coinb2Bytes, _ := hex.DecodeString(curJob.coinb2)

	fullCoinb := make([]byte, 0, len(coinb1Bytes)+12+len(coinb2Bytes))
	fullCoinb = append(fullCoinb, coinb1Bytes...)
	fullCoinb = append(fullCoinb, sess.xnonce1...) // use the exact extranonce1 you sent in subscribe
	fullCoinb = append(fullCoinb, ex2...)          // miner’s extranonce2
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
	if sess.versionRolling && versionBits != "" {
		vb, _ := strconv.ParseUint(versionBits, 16, 32)

		// ALWAYS apply mask - even for new ex2!
		finalVersion = (uint32(curJob.version) & ^sess.versionMask) |
			(uint32(vb) & sess.versionMask)
	} else {
		finalVersion = uint32(curJob.version)
	}

	templateHeader = types.NewBlockHeader(
		types.PowID(powID),
		int32(finalVersion),
		templateHeader.PrevBlock(), // Use same format as sent to miner
		merkleRoot,
		uint32(ntime),         // Correctly parsed uint32
		templateHeader.Bits(), // Use original nBits from job
		uint32(nonce),         // Correctly parsed uint32
		0,
	)

	hashBytes := templateHeader.PowHash().Bytes()

	powHashBigInt := new(big.Int).SetBytes(hashBytes)
	var workShareTarget *big.Int
	// Note: Kawpow shares use submitKawpowShare(), not this function
	switch pending.AuxPow().PowID() {
	case types.Scrypt:
		workShareTarget = new(big.Int).Div(common.Big2e256, pending.WorkObjectHeader().ScryptDiffAndCount().Difficulty())
	default:
		// SHA_BTC, SHA_BCH
		workShareTarget = new(big.Int).Div(common.Big2e256, pending.WorkObjectHeader().ShaDiffAndCount().Difficulty())
	}

	if workShareTarget == nil {
		return false, fmt.Errorf("missing workshare difficulty")
	}

	// Log how close the hash is to the target (target/hash as a percentage)
	var achievedDiff *big.Int
	if workShareTarget != nil && powHashBigInt.Sign() > 0 {
		// targetF := new(big.Float).SetInt(workShareTarget)
		hashF := new(big.Float).SetInt(powHashBigInt)
		if hashF.Sign() != 0 {
			// closeness := new(big.Float).Quo(targetF, hashF) // target/hash
			// closenessPct, _ := closeness.Float64()
			// fmt.Printf("[stratum] Work closeness: %.4f%% (target/hash)\n", closenessPct*100)

			if workShareTarget != nil {
				achievedDiff = new(big.Int).Div(new(big.Int).Set(common.Big2e256), powHashBigInt)
				// ratio := new(big.Float).Quo(new(big.Float).SetInt(achievedDiff), new(big.Float).SetInt(workShareTarget))
				// ratioF, _ := ratio.Float64()
				// fmt.Printf("[stratum] Difficulty achieved: %s, target: %s (%.4fx)\n", achievedDiff.String(), workShareTarget.String(), ratioF)
			}
		}
	}

	if pending.WorkObjectHeader().AuxPow() == nil {
		return false, fmt.Errorf("work object missing auxpow")
	}

	pending.WorkObjectHeader().AuxPow().SetHeader(templateHeader)
	pending.WorkObjectHeader().AuxPow().SetTransaction(fullCoinb)

	// Check if share meets workshare target (hash must be <= target)
	meetsWorkshareTarget := powHashBigInt.Cmp(workShareTarget) <= 0

	// Handle miner-specified difficulty (from password field d=<difficulty>)
	// For SHA/Scrypt, difficulty is in stratum units (not raw difficulty)
	// SHA256: stratumDiff 1 = 2^32 hashes, Scrypt: stratumDiff 1 = 2^16 hashes
	if sess.minerDifficulty != nil && achievedDiff != nil {
		// Convert achieved difficulty to stratum units for comparison
		var diff1 float64
		switch powID {
		case types.Scrypt:
			diff1 = 65536.0 // 2^16
		default:
			diff1 = 4294967296.0 // 2^32 for SHA
		}
		achievedStratumDiff := float64(achievedDiff.Uint64()) / diff1
		if achievedDiff.BitLen() > 63 {
			achievedStratumDiff, _ = new(big.Float).Quo(
				new(big.Float).SetInt(achievedDiff),
				new(big.Float).SetFloat64(diff1),
			).Float64()
		}

		// Check if share meets miner's custom difficulty
		if achievedStratumDiff < *sess.minerDifficulty {
			s.logger.WithFields(log.Fields{
				"achievedDiff":    achievedStratumDiff,
				"minerDifficulty": *sess.minerDifficulty,
			}).Debug("share did not meet miner difficulty")
			return false, fmt.Errorf("share difficulty %.4f below miner difficulty %.4f", achievedStratumDiff, *sess.minerDifficulty)
		}

		// Share meets miner difficulty - if it doesn't meet workshare target,
		// accept for liveness tracking but don't submit to node
		if !meetsWorkshareTarget {
			s.logger.WithFields(log.Fields{
				"powID":           powID,
				"achievedDiff":    achievedStratumDiff,
				"minerDifficulty": *sess.minerDifficulty,
			}).Debug("share accepted for liveness (below workshare difficulty)")
			return false, nil // Accept share but don't submit to node, don't refresh job
		}
	} else {
		// No custom difficulty - use workshare target directly
		if !meetsWorkshareTarget {
			return false, fmt.Errorf("did not meet threshold")
		}
	}

	// LRU de-dup: reject identical shares (same pow hash) for this session
	shareKey := hex.EncodeToString(hashBytes)
	sess.mu.Lock()
	if _, seen := sess.seenShares[shareKey]; seen {
		sess.mu.Unlock()
		return false, fmt.Errorf("duplicate share")
	}
	// Insert and evict oldest if capacity exceeded
	const lruCap = 1024
	sess.seenShares[shareKey] = struct{}{}
	sess.seenOrder = append(sess.seenOrder, shareKey)
	if len(sess.seenOrder) > lruCap {
		oldest := sess.seenOrder[0]
		sess.seenOrder = sess.seenOrder[1:]
		delete(sess.seenShares, oldest)
	}
	sess.mu.Unlock()

	s.logger.WithFields(log.Fields{"powID": pending.AuxPow().PowID(), "achievedDiff": achievedDiff.String(), "hashBytes": hex.EncodeToString(hashBytes)}).Info("submitting workshare to node")

	err = s.backend.ReceiveMinedHeader(pending)
	if err != nil {
		s.logger.WithFields(log.Fields{"powID": pending.AuxPow().PowID(), "user": sess.user, "worker": sess.workerName, "error": err}).Error("workshare rejected by node")
	} else {
		s.logger.WithFields(log.Fields{"powID": pending.AuxPow().PowID(), "user": sess.user, "worker": sess.workerName, "hash": pending.Hash().Hex()}).Info("workshare accepted by node")
	}
	return true, err // Workshare submitted - refresh job
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
	// This properly uses the DAG to compute the real mixHash and powHash
	calculatedMixHash, powHash, err := s.kawpowEngine.VerifyKawpowShare(kawpowHeaderHash, nonce, blockNumber)
	if err != nil {
		return fmt.Errorf("failed to compute kawpow hash: %v", err)
	}

	// Miner submits mixhash in big-endian, but VerifyKawpowShare returns little-endian
	// Reverse the miner's mixhash to little-endian for comparison
	mixHashLE := make([]byte, 32)
	for i := 0; i < 32; i++ {
		mixHashLE[i] = mixHashBytes[31-i]
	}
	submittedMixHash := common.BytesToHash(mixHashLE)

	// Verify the mix hash matches what the miner submitted
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

	// Convert pow hash to big.Int for comparison
	powHashInt := new(big.Int).SetBytes(powHash.Bytes())

	// Check if share meets workshare target (hash must be <= target)
	meetsWorkshareTarget := powHashInt.Cmp(workShareTarget) <= 0

	// Handle miner-specified difficulty (from password field d=<difficulty>)
	if sess.minerDifficulty != nil {
		// Calculate achieved stratum difficulty: stratumDiff = diff1 / hash
		// Kawpow diff1 = 0x00000000ff000000... (standard Ravencoin stratum, ~2^32 hashes per diff 1)
		// This is different from go-quai's internal difficulty which uses 2^256 as max target.
		achievedStratumDiff, _ := new(big.Float).Quo(
			new(big.Float).SetInt(KawpowDiff1),
			new(big.Float).SetInt(powHashInt),
		).Float64()

		// Check if share meets miner's custom difficulty
		if achievedStratumDiff < *sess.minerDifficulty {
			s.logger.WithFields(log.Fields{
				"achievedDiff":    achievedStratumDiff,
				"minerDifficulty": *sess.minerDifficulty,
			}).Debug("share did not meet miner difficulty")
			return fmt.Errorf("share difficulty %.4f below miner difficulty %.4f", achievedStratumDiff, *sess.minerDifficulty)
		}

		// Share meets miner difficulty - if it doesn't meet workshare target,
		// accept for liveness tracking but don't submit to node
		if !meetsWorkshareTarget {
			s.logger.WithFields(log.Fields{
				"powID":           types.Kawpow,
				"height":          kawJob.height,
				"achievedDiff":    achievedStratumDiff,
				"minerDifficulty": *sess.minerDifficulty,
				"workshareDiff":   kawpowShareDiff.String(),
			}).Debug("share accepted for liveness (below workshare difficulty)")
			return nil // Accept share but don't submit to node
		}
	} else {
		// No custom difficulty - use workshare target directly
		if !meetsWorkshareTarget {
			s.logger.WithFields(log.Fields{
				"powHash": hex.EncodeToString(powHash.Bytes()),
				"target":  workShareTarget.String(),
			}).Debug("share did not meet workshare target")
			return fmt.Errorf("share did not meet target")
		}
	}

	// LRU de-dup using pow hash
	shareKey := hex.EncodeToString(powHash.Bytes())
	sess.mu.Lock()
	if _, seen := sess.seenShares[shareKey]; seen {
		sess.mu.Unlock()
		return fmt.Errorf("duplicate share")
	}
	const lruCap = 1024
	sess.seenShares[shareKey] = struct{}{}
	sess.seenOrder = append(sess.seenOrder, shareKey)
	if len(sess.seenOrder) > lruCap {
		oldest := sess.seenOrder[0]
		sess.seenOrder = sess.seenOrder[1:]
		delete(sess.seenShares, oldest)
	}
	sess.mu.Unlock()

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
	s.logger.WithFields(log.Fields{
		"powID":        types.Kawpow,
		"height":       kawJob.height,
		"nonce":        nonceHex,
		"powHash":      hex.EncodeToString(powHash.Bytes()[:8]),
		"achievedDiff": achievedDiff.String(),
		"targetDiff":   kawpowShareDiff.String(),
	}).Info("submitting kawpow workshare to node")

	err = s.backend.ReceiveMinedHeader(pending)
	if err != nil {
		s.logger.WithFields(log.Fields{"powID": types.Kawpow, "height": kawJob.height, "user": sess.user, "worker": sess.workerName, "error": err}).Error("kawpow workshare rejected by node")
	} else {
		s.logger.WithFields(log.Fields{"powID": types.Kawpow, "height": kawJob.height, "user": sess.user, "worker": sess.workerName, "hash": pending.Hash().Hex()}).Info("kawpow workshare accepted by node")
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
	case "sha":
		return types.SHA_BCH
	case "scrypt":
		return types.Scrypt
	case "kawpow":
		return types.Kawpow
	default:
		return types.SHA_BTC
	}
}

// parseUsername parses the stratum username format: address.workername.frequency=X
// Returns the address, worker name, and job frequency.
// Examples:
//   - "0x1234..." -> ("0x1234...", "default", 5s)
//   - "0x1234....rig1" -> ("0x1234...", "rig1", 5s)
//   - "0x1234....rig1.frequency=2.5" -> ("0x1234...", "rig1", 2.5s)
//   - "0x1234....frequency=3" -> ("0x1234...", "default", 3s)
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

		// Check if this part is a frequency setting
		if strings.HasPrefix(part, "frequency=") {
			freqStr := strings.TrimPrefix(part, "frequency=")
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
// Supports: d=<difficulty> and/or frequency=<seconds>
// Parts are separated by ',' character (to allow decimal values).
// Examples:
//   - "x" or "" -> nil, 0 (use defaults)
//   - "d=0.1" -> difficulty=0.1, frequency=0
//   - "frequency=2.5" -> difficulty=nil, frequency=2.5s
//   - "d=0.1,frequency=2" -> difficulty=0.1, frequency=2s
func parsePassword(password string) (*float64, time.Duration) {
	if password == "" || password == "x" {
		return nil, 0
	}

	var difficulty *float64
	var frequency time.Duration

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
		// Check for frequency=<seconds> format
		if freqStr, ok := strings.CutPrefix(part, "frequency="); ok {
			if freq, err := strconv.ParseFloat(freqStr, 64); err == nil && freq >= 1 && freq <= 10 {
				frequency = time.Duration(freq * float64(time.Second))
			}
			continue
		}
	}

	return difficulty, frequency
}
