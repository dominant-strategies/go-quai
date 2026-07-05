// Copyright 2025 The go-quai Authors
// This file is part of the go-quai library.

package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
)

// HealthServer provides a simple HTTP health check endpoint
type HealthServer struct {
	config      *Config
	server      *http.Server
	listener    net.Listener
	logger      *log.Logger
	stratumUrl  string
	zoneBackend quaiapi.Backend
}

// jsonRPCRequest represents a JSON-RPC request
type jsonRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

// jsonRPCResponse represents a JSON-RPC response
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
	ID      int             `json:"id"`
}

// jsonRPCError represents a JSON-RPC error
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewHealthServer creates a new health server
func NewHealthServer(config *Config, logger *log.Logger, zoneBackend quaiapi.Backend) *HealthServer {
	return &HealthServer{
		config:      config,
		logger:      logger,
		zoneBackend: zoneBackend,
	}
}

// Start starts the health server
func (h *HealthServer) Start() error {
	if !h.config.HealthEnabled {
		return nil
	}

	addr := fmt.Sprintf(":%d", h.config.HealthPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start health server: %w", err)
	}
	h.listener = listener

	h.server = &http.Server{
		Handler:      http.HandlerFunc(h.handleHealth),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		h.logger.WithField("addr", addr).Info("Health check server started")
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			h.logger.WithField("err", err).Error("Health server error")
		}
	}()

	return nil
}

// Stop stops the health server
func (h *HealthServer) Stop() error {
	if h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.server.Shutdown(ctx)
}

// checkStratumHealth checks if the stratum server is accepting connections
func (h *HealthServer) checkStratumHealth() (bool, error) {
	conn, err := net.DialTimeout("tcp", h.config.StratumUrl, 2*time.Second)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

// handleHealth handles health check requests
func (h *HealthServer) handleHealth(w http.ResponseWriter, r *http.Request) {

	stratumHealthy := false
	if h.config.StratumEnabled {
		// Check stratum server health first
		var stratumErr error
		stratumHealthy, stratumErr = h.checkStratumHealth()
		if !stratumHealthy {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			errMsg := "stratum server not responding"
			if stratumErr != nil {
				errMsg = fmt.Sprintf("stratum server error: %v", stratumErr)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"healthy":        false,
				"stratumHealthy": false,
				"error":          errMsg,
			})
			return
		}
	}

	localBlockNum := h.zoneBackend.CurrentBlock().NumberU64(common.ZONE_CTX)

	// Get reference block numbers from configured URLs
	referenceURLs := h.config.HealthReferenceURLs
	if len(referenceURLs) == 0 {
		referenceURLs = []string{"https://rpc.quai.network/cyprus1"}
	}

	maxReferenceBlockNum := uint64(0)
	var referenceErrors []string

	// Query all reference URLs concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]uint64)

	for _, refURL := range referenceURLs {
		wg.Add(1)
		go func(urlStr string) {
			defer wg.Done()

			blockNum, err := h.getBlockNumber(urlStr)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				referenceErrors = append(referenceErrors, fmt.Sprintf("%s: %v", urlStr, err))
				return
			}
			results[urlStr] = blockNum
			if blockNum > maxReferenceBlockNum {
				maxReferenceBlockNum = blockNum
			}
		}(refURL)
	}
	wg.Wait()

	// If we couldn't reach any reference nodes, return 503
	if len(results) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"healthy":         false,
			"localBlockNum":   localBlockNum,
			"error":           "could not reach any reference nodes",
			"referenceErrors": referenceErrors,
		})
		return
	}

	// Calculate how far behind we are
	blocksBehind := int64(0)
	if maxReferenceBlockNum > localBlockNum {
		blocksBehind = int64(maxReferenceBlockNum - localBlockNum)
	}

	// Check if we're too far behind
	maxBehind := h.config.HealthMaxBlocksBehind
	if maxBehind == 0 {
		maxBehind = 5
	}

	healthy := blocksBehind <= int64(maxBehind)

	response := map[string]interface{}{
		"healthy":              healthy,
		"stratumEnabled":       h.config.StratumEnabled,
		"stratumHealthy":       stratumHealthy,
		"localBlockNum":        localBlockNum,
		"referenceBlockNum":    maxReferenceBlockNum,
		"blocksBehind":         blocksBehind,
		"maxBlocksBehindLimit": maxBehind,
		"referenceResults":     results,
	}

	if len(referenceErrors) > 0 {
		response["referenceErrors"] = referenceErrors
	}

	w.Header().Set("Content-Type", "application/json")
	if healthy {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(response)
}

// getBlockNumber fetches the block number from a remote node
func (h *HealthServer) getBlockNumber(urlStr string) (uint64, error) {
	// Normalize the URL
	normalizedURL, err := h.normalizeURL(urlStr)
	if err != nil {
		return 0, fmt.Errorf("invalid URL: %w", err)
	}

	// Create JSON-RPC request
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "quai_blockNumber",
		Params:  []interface{}{},
		ID:      1,
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", normalizedURL, bytes.NewReader(reqBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON-RPC response
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if rpcResp.Error != nil {
		return 0, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	// Parse the hex block number
	var blockNumHex string
	if err := json.Unmarshal(rpcResp.Result, &blockNumHex); err != nil {
		return 0, fmt.Errorf("failed to parse block number: %w", err)
	}

	// Remove 0x prefix and parse
	blockNumHex = strings.TrimPrefix(blockNumHex, "0x")
	var blockNum uint64
	if _, err := fmt.Sscanf(blockNumHex, "%x", &blockNum); err != nil {
		return 0, fmt.Errorf("failed to parse hex block number: %w", err)
	}

	return blockNum, nil
}

// normalizeURL normalizes the URL, adding scheme and handling IP addresses
func (h *HealthServer) normalizeURL(urlStr string) (string, error) {
	// If it looks like just an IP:port, add http://
	if !strings.Contains(urlStr, "://") {
		// Check if it's an IP address (with optional port)
		if strings.Contains(urlStr, ":") {
			// Has port, assume http
			urlStr = "http://" + urlStr
		} else {
			// No port, assume https on port 443
			urlStr = "https://" + urlStr
		}
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}

	// If no port specified, use default based on scheme
	if parsed.Port() == "" {
		if parsed.Scheme == "https" {
			parsed.Host = parsed.Host + ":443"
		} else {
			parsed.Host = parsed.Host + ":80"
		}
	}

	return parsed.String(), nil
}
