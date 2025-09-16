// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package quaistats implements the network stats reporting service.
package quaistats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/ethdb"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"

	"os/exec"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
)

const (
	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10

	// reportInterval is the time interval between two reports.
	reportInterval = 15

	c_alpha           = 8
	c_statsErrorValue = int64(-1)

	// Max number of stats objects to send in one batch
	c_queueBatchSize uint64 = 10

	// Number of blocks to include in one batch of transactions
	c_txBatchSize uint64 = 10

	// Seconds that we want to iterate over (3600s = 1 hr)
	c_windowSize uint64 = 3600

	// Max number of objects to keep in queue
	c_maxQueueSize int = 100
)

var (
	chainID9000  = big.NewInt(9000)
	chainID12000 = big.NewInt(12000)
	chainID15000 = big.NewInt(15000)
	chainID17000 = big.NewInt(17000)
	chainID1337  = big.NewInt(1337)
)

// backend encompasses the bare-minimum functionality needed for quaistats reporting
type backend interface {
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	CurrentHeader() *types.WorkObject
	TotalLogEntropy(header *types.WorkObject) *big.Int
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	Stats() (pending int, queued int, qi int)
	ChainConfig() *params.ChainConfig
	ProcessingState() bool
	NodeCtx() int
	NodeLocation() common.Location
	Logger() *log.Logger
	Database() ethdb.Database
	UncleWorkShareClassification(workshare *types.WorkObjectHeader) types.WorkShareValidity
}

// fullNodeBackend encompasses the functionality necessary for a full node
// reporting to quaistats
type fullNodeBackend interface {
	backend
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.WorkObject, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*types.WorkObject, error)
	CurrentBlock() *types.WorkObject
	CalcOrder(wo *types.WorkObject) (*big.Int, int, error)
}

// Service implements an Quai netstats reporting daemon that pushes local
// chain statistics up to a monitoring server.
type Service struct {
	backend backend

	node          string // Name of the node to display on the monitoring page
	pass          string // Password to authorize access to the monitoring page
	host          string // Remote address of the monitoring service
	sendfullstats bool   // Whether the node is sending full stats or not

	pongCh  chan struct{} // Pong notifications are fed into this channel
	headSub event.Subscription

	transactionStatsQueue *StatsQueue
	detailStatsQueue      *StatsQueue
	appendTimeStatsQueue  *StatsQueue

	// After handling a block and potentially adding to the queues, it will notify the sendStats goroutine
	// that stats are ready to be sent
	statsReadyCh chan struct{}

	blockLookupCache *lru.Cache[common.Hash, cachedBlock]

	chainID *big.Int

	instanceDir string // Path to the node's instance directory
}

// StatsQueue is a thread-safe queue designed for managing and processing stats data.
//
// The primary objective of the StatsQueue is to provide a safe mechanism for enqueuing,
// dequeuing, and requeuing stats objects concurrently across multiple goroutines.
//
// Key Features:
//   - Enqueue: Allows adding an item to the end of the queue.
//   - Dequeue: Removes and returns the item from the front of the queue.
//   - RequeueFront: Adds an item back to the front of the queue, useful for failed processing attempts.
//
// Concurrent Access:
//   - The internal state of the queue is protected by a mutex to prevent data races and ensure
//     that the operations are atomic. As a result, it's safe to use across multiple goroutines
//     without external synchronization.
type StatsQueue struct {
	data  []interface{}
	mutex sync.Mutex
}

func NewStatsQueue() *StatsQueue {
	return &StatsQueue{
		data: make([]interface{}, 0),
	}
}

func (q *StatsQueue) Enqueue(item interface{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if len(q.data) >= c_maxQueueSize {
		q.dequeue()
	}

	q.data = append(q.data, item)
}

func (q *StatsQueue) Dequeue() interface{} {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if len(q.data) == 0 {
		return nil
	}

	return q.dequeue()
}

func (q *StatsQueue) dequeue() interface{} {
	item := q.data[0]
	q.data = q.data[1:]
	return item
}

func (q *StatsQueue) EnqueueFront(item interface{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if len(q.data) >= c_maxQueueSize {
		q.dequeue()
	}

	q.data = append([]interface{}{item}, q.data...)
}

func (q *StatsQueue) EnqueueFrontBatch(items []interface{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	// Iterate backwards through the items and add them to the front of the queue
	// Going backwards means oldest items are not added in case of overflow
	for i := len(items) - 1; i >= 0; i-- {
		if len(q.data) >= c_maxQueueSize {
			break
		}
		q.data = append([]interface{}{items[i]}, q.data...)
	}
}

func (q *StatsQueue) Size() int {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	return len(q.data)
}

// parseEthstatsURL parses the netstats connection url.
// URL argument should be of the form <nodename:secret@host:port>
// If non-erroring, the returned slice contains 3 elements: [nodename, pass, host]
func parseEthstatsURL(url string) (parts []string, err error) {
	err = fmt.Errorf("invalid netstats url: \"%s\", should be nodename:secret@host:port", url)

	hostIndex := strings.LastIndex(url, "@")
	if hostIndex == -1 || hostIndex == len(url)-1 {
		return nil, err
	}
	preHost, host := url[:hostIndex], url[hostIndex+1:]

	passIndex := strings.LastIndex(preHost, ":")
	if passIndex == -1 {
		return []string{preHost, "", host}, nil
	}
	nodename, pass := preHost[:passIndex], ""
	if passIndex != len(preHost)-1 {
		pass = preHost[passIndex+1:]
	}

	return []string{nodename, pass, host}, nil
}

// New returns a monitoring service ready for stats reporting.
func New(node *node.Node, backend backend, url string, sendfullstats bool) error {
	parts, err := parseEthstatsURL(url)
	if err != nil {
		return err
	}

	chainID := backend.ChainConfig().ChainID
	var durationLimit *big.Int

	switch {
	case chainID.Cmp(chainID9000) == 0:
		durationLimit = params.DurationLimit
	case chainID.Cmp(chainID12000) == 0:
		durationLimit = params.GardenDurationLimit
	case chainID.Cmp(chainID15000) == 0:
		durationLimit = params.OrchardDurationLimit
	case chainID.Cmp(chainID17000) == 0:
		durationLimit = params.LighthouseDurationLimit
	case chainID.Cmp(chainID1337) == 0:
		durationLimit = params.LocalDurationLimit
	default:
		durationLimit = params.DurationLimit
	}

	durationLimitInt := durationLimit.Uint64()

	c_blocksPerWindow := c_windowSize / durationLimitInt

	blockLookupCache, _ := lru.New[common.Hash, cachedBlock](int(c_blocksPerWindow * 2))

	quaistats := &Service{
		backend:               backend,
		node:                  parts[0],
		pass:                  parts[1],
		host:                  parts[2],
		pongCh:                make(chan struct{}),
		chainID:               backend.ChainConfig().ChainID,
		transactionStatsQueue: NewStatsQueue(),
		detailStatsQueue:      NewStatsQueue(),
		appendTimeStatsQueue:  NewStatsQueue(),
		statsReadyCh:          make(chan struct{}, 1),
		sendfullstats:         sendfullstats,
		blockLookupCache:      blockLookupCache,
		instanceDir:           node.InstanceDir(),
	}

	node.RegisterLifecycle(quaistats)
	return nil
}

// Start implements node.Lifecycle, starting up the monitoring and reporting daemon.
func (s *Service) Start() error {
	// Subscribe to chain events to execute updates on
	chainHeadCh := make(chan core.ChainHeadEvent, chainHeadChanSize)

	s.headSub = s.backend.SubscribeChainHeadEvent(chainHeadCh)

	go s.loopBlocks(chainHeadCh)
	go s.loopSender(s.initializeURLMap())

	s.backend.Logger().Info("Stats daemon started")
	return nil
}

// Stop implements node.Lifecycle, terminating the monitoring and reporting daemon.
func (s *Service) Stop() error {
	s.headSub.Unsubscribe()
	s.backend.Logger().Info("Stats daemon stopped")
	return nil
}

func (s *Service) loopBlocks(chainHeadCh chan core.ChainHeadEvent) {
	defer func() {
		if r := recover(); r != nil {
			s.backend.Logger().WithFields(log.Fields{"err": r, "stacktrace": string(debug.Stack())}).Error("Stats process crashed")
			go s.loopBlocks(chainHeadCh)
		}
	}()

	quitCh := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.backend.Logger().WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		for {
			select {
			case head := <-chainHeadCh:
				// Directly handle the block
				go s.handleBlock(head.Block)
			case <-s.headSub.Err():
				close(quitCh)
				return
			}
		}
	}()

	// Wait for the goroutine to signal completion or error
	<-quitCh
}

// loop keeps trying to connect to the netstats server, reporting chain events
// until termination.
func (s *Service) loopSender(urlMap map[string]string) {
	defer func() {
		if r := recover(); r != nil {
			s.backend.Logger().WithFields(log.Fields{"err": r, "stacktrace": string(debug.Stack())}).Error("Stats process crashed")
			go s.loopSender(urlMap)
		}
	}()

	// Start a goroutine that exhausts the subscriptions to avoid events piling up
	var (
		quitCh = make(chan struct{})
	)

	nodeStatsMod := 0

	errTimer := time.NewTimer(0)
	defer errTimer.Stop()
	var authJwt = ""
	// Loop reporting until termination
	for {
		select {
		case <-quitCh:
			return
		case <-errTimer.C:
			// If we don't have a JWT or it's expired, get a new one
			isJwtExpiredResult, jwtIsExpiredErr := s.isJwtExpired(authJwt)
			if authJwt == "" || isJwtExpiredResult || jwtIsExpiredErr != nil {
				s.backend.Logger().Info("Trying to login to quaistats")
				var err error
				authJwt, err = s.login2(urlMap["login"])
				if err != nil {
					s.backend.Logger().WithField("err", err).Warn("Stats login failed")
					errTimer.Reset(10 * time.Second)
					continue
				}
			}

			errs := make(map[string]error)

			// Authenticate the client with the server
			for key, url := range urlMap {
				switch key {
				case "login":
					continue
				case "nodeStats":
					if errs[key] = s.reportNodeStats(url, 0, authJwt); errs[key] != nil {
						s.backend.Logger().WithField("err", errs[key]).Warn("Initial stats report failed for " + key)
						errTimer.Reset(0)
						continue
					}
				case "blockTransactionStats":
					if errs[key] = s.sendTransactionStats(url, authJwt); errs[key] != nil {
						s.backend.Logger().WithField("err", errs[key]).Warn("Initial stats report failed for " + key)
						errTimer.Reset(0)
						continue
					}
				case "blockDetailStats":
					if errs[key] = s.sendDetailStats(url, authJwt); errs[key] != nil {
						s.backend.Logger().WithField("err", errs[key]).Warn("Initial stats report failed for " + key)
						errTimer.Reset(0)
						continue
					}
				case "blockAppendTime":
					if errs[key] = s.sendAppendTimeStats(url, authJwt); errs[key] != nil {
						s.backend.Logger().WithField("err", errs[key]).Warn("Initial stats report failed for " + key)
						errTimer.Reset(0)
						continue
					}
				}
			}

			// Keep sending status updates until the connection breaks
			fullReport := time.NewTicker(reportInterval * time.Second)

			var noErrs = true
			for noErrs {
				var err error
				select {
				case <-quitCh:
					fullReport.Stop()
					return

				case <-fullReport.C:
					nodeStatsMod ^= 1
					if err = s.reportNodeStats(urlMap["nodeStats"], nodeStatsMod, authJwt); err != nil {
						noErrs = false
						s.backend.Logger().WithField("err", err).Warn("nodeStats full stats report failed")
					}
				case <-s.statsReadyCh:
					if url, ok := urlMap["blockTransactionStats"]; ok {
						if err := s.sendTransactionStats(url, authJwt); err != nil {
							noErrs = false
							errTimer.Reset(0)
							s.backend.Logger().Warn("blockTransactionStats stats report failed", "err", err)
						}
					}
					if url, ok := urlMap["blockDetailStats"]; ok {
						if err := s.sendDetailStats(url, authJwt); err != nil {
							noErrs = false
							errTimer.Reset(0)
							s.backend.Logger().Warn("blockDetailStats stats report failed", "err", err)
						}
					}
					if url, ok := urlMap["blockAppendTime"]; ok {
						if err := s.sendAppendTimeStats(url, authJwt); err != nil {
							noErrs = false
							errTimer.Reset(0)
							s.backend.Logger().Warn("blockAppendTime stats report failed", "err", err)
						}
					}
				}
				errTimer.Reset(0)
			}
			fullReport.Stop()
		}
	}
}

func (s *Service) initializeURLMap() map[string]string {
	return map[string]string{
		"blockTransactionStats": fmt.Sprintf("http://%s/stats/blockTransactionStats", s.host),
		"blockAppendTime":       fmt.Sprintf("http://%s/stats/blockAppendTime", s.host),
		"blockDetailStats":      fmt.Sprintf("http://%s/stats/blockDetailStats", s.host),
		"nodeStats":             fmt.Sprintf("http://%s/stats/nodeStats", s.host),
		"login":                 fmt.Sprintf("http://%s/auth/login", s.host),
	}
}

func (s *Service) handleBlock(block *types.WorkObject) {
	defer func() {
		if r := recover(); r != nil {
			s.backend.Logger().WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Fatal("Go-Quai Panicked")
		}
	}()
	// Cache Block
	s.backend.Logger().WithFields(log.Fields{
		"detailsQueueSize":     s.detailStatsQueue.Size(),
		"appendTimeQueueSize":  s.appendTimeStatsQueue.Size(),
		"transactionQueueSize": s.transactionStatsQueue.Size(),
		"blockNumber":          block.NumberU64(s.backend.NodeCtx()),
	}).Trace("Handling block")

	s.cacheBlock(block)

	if s.sendfullstats {
		dtlStats := s.assembleBlockDetailStats(block)
		if dtlStats != nil {
			s.detailStatsQueue.Enqueue(dtlStats)
		}
	}

	appStats := s.assembleBlockAppendTimeStats(block)
	if appStats != nil {
		s.appendTimeStatsQueue.Enqueue(appStats)
	}

	if block.NumberU64(s.backend.NodeCtx())%c_txBatchSize == 0 && s.sendfullstats && block.Location().Context() == common.ZONE_CTX {
		txStats := s.assembleBlockTransactionStats(block)
		if txStats != nil {
			s.transactionStatsQueue.Enqueue(txStats)
		}
	}
	// After handling a block and potentially adding to the queues, notify the sendStats goroutine
	// that stats are ready to be sent
	select {
	case s.statsReadyCh <- struct{}{}:
	default:
		s.backend.Logger().Debug("Stats ready channel is full")
	}
}

func (s *Service) reportNodeStats(url string, mod int, authJwt string) error {
	if url == "" {
		s.backend.Logger().Warn("node stats url is empty")
		return errors.New("node stats connection is empty")
	}

	isRegion := strings.Contains(s.instanceDir, "region")
	isPrime := strings.Contains(s.instanceDir, "prime")

	if isRegion || isPrime {
		s.backend.Logger().Debug("Skipping node stats for region or prime. Filtered out on backend")
		return nil
	}

	// Don't send if dirSize < 1
	// Get disk usage (as a percentage)
	diskUsage, err := dirSize(s.instanceDir + "/../..")
	if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Error calculating directory sizes")
		diskUsage = c_statsErrorValue
	}

	diskSize, err := diskTotalSize()
	if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Error calculating disk size")
		diskUsage = c_statsErrorValue
	}

	diskUsagePercent := float64(c_statsErrorValue)
	if diskSize > 0 {
		diskUsagePercent = float64(diskUsage) / float64(diskSize)
	} else {
		s.backend.Logger().Warn("Error calculating disk usage percent: disk size is 0")
	}

	// Usage in your main function
	ramUsage, err := getQuaiRAMUsage()
	if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Error getting Quai RAM usage")
		return err
	}
	var ramUsagePercent, ramFreePercent, ramAvailablePercent float64
	if vmStat, err := mem.VirtualMemory(); err == nil {
		ramUsagePercent = float64(ramUsage) / float64(vmStat.Total)
		ramFreePercent = float64(vmStat.Free) / float64(vmStat.Total)
		ramAvailablePercent = float64(vmStat.Available) / float64(vmStat.Total)
	} else {
		s.backend.Logger().WithField("err", err).Warn("Error getting RAM stats")
		return err
	}

	// Get CPU usage
	cpuUsageQuai, err := getQuaiCPUUsage()
	if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Error getting Quai CPU usage")
		return err
	} else {
		cpuUsageQuai /= float64(100)
	}

	var cpuFree float32
	if cpuUsageTotal, err := cpu.Percent(0, false); err == nil {
		cpuFree = 1 - float32(cpuUsageTotal[0]/float64(100))
	} else {
		s.backend.Logger().WithField("err", err).Warn("Error getting CPU free")
		return err
	}

	currentHeader := s.backend.CurrentHeader()

	if currentHeader == nil {
		s.backend.Logger().Warn("Current header is nil")
		return errors.New("current header is nil")
	}
	// Get current block number
	currentBlockHeight := currentHeader.NumberArray()

	// Get location
	location := currentHeader.Location()

	// Get the first non-loopback MAC address
	var macAddress string
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, interf := range interfaces {
			if interf.HardwareAddr != nil && len(interf.HardwareAddr.String()) > 0 && (interf.Flags&net.FlagLoopback) == 0 {
				macAddress = interf.HardwareAddr.String()
				break
			}
		}
	} else {
		s.backend.Logger().WithField("err", err).Warn("Error getting MAC address")
		return err
	}

	// Hash the MAC address
	var hashedMAC string
	if macAddress != "" {
		hash := sha256.Sum256([]byte(macAddress))
		hashedMAC = hex.EncodeToString(hash[:])
	}

	document := map[string]interface{}{
		"id": s.node,
		"nodeStats": &nodeStats{
			Name:                s.node,
			Timestamp:           big.NewInt(time.Now().Unix()), // Current timestamp
			RAMUsage:            int64(ramUsage),
			RAMUsagePercent:     float32(ramUsagePercent),
			RAMFreePercent:      float32(ramFreePercent),
			RAMAvailablePercent: float32(ramAvailablePercent),
			CPUUsagePercent:     float32(cpuUsageQuai),
			CPUFree:             float32(cpuFree),
			DiskUsageValue:      int64(diskUsage),
			DiskUsagePercent:    float32(diskUsagePercent),
			CurrentBlockNumber:  currentBlockHeight,
			RegionLocation:      location.Region(),
			ZoneLocation:        location.Zone(),
			NodeStatsMod:        mod,
			HashedMAC:           hashedMAC,
		},
	}

	jsonData, err := json.Marshal(document)
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to marshal node stats")
		return err
	}

	// Create a new HTTP request
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to create new HTTP request")
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authJwt)

	// Send the request using the default HTTP client
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to send node stats")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			s.backend.Logger().WithField("err", err).Error("Failed to read response body")
			return err
		}
		s.backend.Logger().WithFields(log.Fields{
			"status": resp.Status,
			"body":   string(body),
		}).Error("Received non-OK response")
		return errors.New("Received non-OK response: " + resp.Status)
	}
	return nil
}

func (s *Service) sendTransactionStats(url string, authJwt string) error {
	if len(s.transactionStatsQueue.data) == 0 {
		return nil
	}
	statsBatch := make([]*blockTransactionStats, 0, c_queueBatchSize)

	for i := 0; i < int(c_queueBatchSize) && len(s.transactionStatsQueue.data) > 0; i++ {
		stat := s.transactionStatsQueue.Dequeue()
		if stat == nil {
			break
		}
		statsBatch = append(statsBatch, stat.(*blockTransactionStats))
	}

	if len(statsBatch) == 0 {
		return nil
	}

	err := s.report(url, "blockTransactionStats", statsBatch, authJwt)
	if err != nil && strings.Contains(err.Error(), "Received non-OK response") {
		s.backend.Logger().WithField("err", err).Warn("Failed to send transaction stats, requeuing stats")
		// Re-enqueue the failed stats from end to beginning
		tempSlice := make([]interface{}, len(statsBatch))
		for i, item := range statsBatch {
			tempSlice[len(statsBatch)-1-i] = item
		}
		s.transactionStatsQueue.EnqueueFrontBatch(tempSlice)
		return err
	} else if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Failed to send transaction stats")
		return err
	}
	return nil
}

func (s *Service) sendDetailStats(url string, authJwt string) error {
	if len(s.detailStatsQueue.data) == 0 {
		return nil
	}
	statsBatch := make([]*blockDetailStats, 0, c_queueBatchSize)

	for i := 0; i < int(c_queueBatchSize) && s.detailStatsQueue.Size() > 0; i++ {
		stat := s.detailStatsQueue.Dequeue()
		if stat == nil {
			break
		}
		statsBatch = append(statsBatch, stat.(*blockDetailStats))
	}

	if len(statsBatch) == 0 {
		return nil
	}

	err := s.report(url, "blockDetailStats", statsBatch, authJwt)
	if err != nil && strings.Contains(err.Error(), "Received non-OK response") {
		s.backend.Logger().WithField("err", err).Warn("Failed to send detail stats, requeuing stats")
		// Re-enqueue the failed stats from end to beginning
		tempSlice := make([]interface{}, len(statsBatch))
		for i, item := range statsBatch {
			tempSlice[len(statsBatch)-1-i] = item
		}
		s.detailStatsQueue.EnqueueFrontBatch(tempSlice)
		return err
	} else if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Failed to send detail stats")
		return err
	}
	return nil
}

func (s *Service) sendAppendTimeStats(url string, authJwt string) error {
	if len(s.appendTimeStatsQueue.data) == 0 {
		return nil
	}

	statsBatch := make([]*blockAppendTime, 0, c_queueBatchSize)

	for i := 0; i < int(c_queueBatchSize) && s.appendTimeStatsQueue.Size() > 0; i++ {
		stat := s.appendTimeStatsQueue.Dequeue()
		if stat == nil {
			break
		}
		statsBatch = append(statsBatch, stat.(*blockAppendTime))
	}

	if len(statsBatch) == 0 {
		return nil
	}

	err := s.report(url, "blockAppendTime", statsBatch, authJwt)
	if err != nil && strings.Contains(err.Error(), "Received non-OK response") {
		s.backend.Logger().WithField("err", err).Warn("Failed to send append time stats, requeuing stats")
		// Re-enqueue the failed stats from end to beginning
		tempSlice := make([]interface{}, len(statsBatch))
		for i, item := range statsBatch {
			tempSlice[len(statsBatch)-1-i] = item
		}
		s.appendTimeStatsQueue.EnqueueFrontBatch(tempSlice)
		return err
	} else if err != nil {
		s.backend.Logger().WithField("err", err).Warn("Failed to send append time stats")
		return err
	}
	return nil
}

func (s *Service) report(url string, dataType string, stats interface{}, authJwt string) error {
	if url == "" {
		s.backend.Logger().Warn(dataType + " url is empty")
		return errors.New(dataType + " url is empty")
	}

	if stats == nil {
		s.backend.Logger().Warn(dataType + " stats are nil")
		return errors.New(dataType + " stats are nil")
	}

	s.backend.Logger().WithField("datatype", dataType).Trace("Sending stats to quaistats")

	document := map[string]interface{}{
		"id":     s.node,
		dataType: stats,
	}

	jsonData, err := json.Marshal(document)
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to marshal " + dataType + " stats")
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to create new request for " + dataType + " stats")
		return err
	}

	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authJwt) // Add this line for the Authorization header

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to send " + dataType + " stats")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			s.backend.Logger().WithField("err", err).Error("Failed to read response body")
			return err
		}
		s.backend.Logger().WithFields(log.Fields{
			"status": resp.Status,
			"body":   string(body),
		}).Error("Received non-OK response")
		return errors.New("Received non-OK response: " + resp.Status)
	}
	s.backend.Logger().WithField("datatype", dataType).Trace("Successfully sent stats to quaistats")
	return nil
}

type cachedBlock struct {
	number      uint64
	parentHash  common.Hash
	txCount     float64
	quaiTxCount float64
	qiTxCount   float64
	etxCount    float64
	etxInCount  float64
	etxOutCount float64
	time        uint64
}

// nodeInfo is the collection of meta information about a node that is displayed
// on the monitoring page.
type nodeInfo struct {
	Name     string `json:"name"`
	Node     string `json:"node"`
	Port     int    `json:"port"`
	Network  string `json:"net"`
	Protocol string `json:"protocol"`
	API      string `json:"api"`
	Os       string `json:"os"`
	OsVer    string `json:"os_v"`
	Client   string `json:"client"`
	History  bool   `json:"canUpdateHistory"`
	Chain    string `json:"chain"`
	ChainID  uint64 `json:"chainId"`
}

// authMsg is the authentication infos needed to login to a monitoring server.
type authMsg struct {
	ID     string      `json:"id"`
	Info   nodeInfo    `json:"info"`
	Secret loginSecret `json:"secret"`
}

type loginSecret struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type Credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
}

func (s *Service) login2(url string) (string, error) {
	// Substitute with your actual service address and port

	var secretUser string
	if s.sendfullstats {
		secretUser = "admin"
	} else {
		secretUser = s.node
	}

	auth := &authMsg{
		ID: s.node,
		Info: nodeInfo{
			Name:    s.node,
			API:     "No",
			Os:      runtime.GOOS,
			OsVer:   runtime.GOARCH,
			Client:  "0.1.1",
			History: true,
			Chain:   s.backend.NodeLocation().Name(),
			ChainID: s.chainID.Uint64(),
		},
		Secret: loginSecret{
			Name:     secretUser,
			Password: s.pass,
		},
	}

	authJson, err := json.Marshal(auth)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(authJson))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to read response body")
		return "", err
	}

	var authResponse AuthResponse
	err = json.Unmarshal(body, &authResponse)
	if err != nil {
		return "", err
	}

	if authResponse.Success {
		return authResponse.Token, nil
	}

	return "", fmt.Errorf("login failed")
}

// isJwtExpired checks if the JWT token is expired
func (s *Service) isJwtExpired(authJwt string) (bool, error) {
	if authJwt == "" {
		return false, errors.New("token is nil")
	}

	parts := strings.Split(authJwt, ".")
	if len(parts) != 3 {
		return false, errors.New("invalid token")
	}

	claims := jwt.MapClaims{}
	_, _, err := new(jwt.Parser).ParseUnverified(authJwt, claims)
	if err != nil {
		return false, err
	}

	if exp, ok := claims["exp"].(float64); ok {
		return time.Now().Unix() >= int64(exp), nil
	}

	return false, errors.New("exp claim not found in token")
}

// Trusted Only
type blockTransactionStats struct {
	Timestamp *big.Int `json:"timestamp"`
	Tx        subTps   `json:"tx"`
	QuaiTx    subTps   `json:"quaiTx"`
	QiTx      subTps   `json:"qiTx"`
	EtxIn     subTps   `json:"etxIn"`
	EtxOut    subTps   `json:"etxOut"`
	Chain     string   `json:"chain"`
}

// Trusted Only
type blockDetailStats struct {
	Timestamp            *big.Int `json:"timestamp"`
	ZoneHeight           uint64   `json:"zoneHeight"`
	RegionHeight         uint64   `json:"regionHeight"`
	PrimeHeight          uint64   `json:"primeHeight"`
	Chain                string   `json:"chain"`
	Entropy              string   `json:"entropy"`
	Difficulty           string   `json:"difficulty"`
	QuaiPerQi            string   `json:"quaiPerQi"`
	QuaiReward           string   `json:"quaiReward"`
	QiReward             string   `json:"qiReward"`
	QiType               bool     `json:"qiType"`
	UncleCount           uint64   `json:"uncleCount"`
	WoCount              uint64   `json:"woCount"`
	ExchangeRate         *big.Int `json:"exchangeRate"`
	BaseFee              *big.Int `json:"baseFee"`
	GasLimit             uint64   `json:"gasLimit"`
	GasUsed              uint64   `json:"gasUsed"`
	StateLimit           uint64   `json:"stateLimit"`
	StateUsed            uint64   `json:"stateUsed"`
	QuaiSupplyAdded      *big.Int `json:"quaiSupplyAdded"`
	QuaiSupplyRemoved    *big.Int `json:"quaiSupplyRemoved"`
	QuaiSupplyTotal      *big.Int `json:"quaiSupplyTotal"`
	QiSupplyAdded        *big.Int `json:"qiSupplyAdded"`
	QiSupplyRemoved      *big.Int `json:"qiSupplyRemoved"`
	QiSupplyTotal        *big.Int `json:"qiSupplyTotal"`
	KQuai                *big.Int `json:"kQuai"`
	KQi                  *big.Int `json:"kQi"`
	KQuaiSlip            *big.Int `json:"kQuaiSlip"`
	ConversionFlowAmount *big.Int `json:"conversionFlowAmount"`
	MinerDifficulty      *big.Int `json:"minerDifficulty"`
	Slip                 *big.Int `json:"slip"`
}

// Everyone sends every block
type blockAppendTime struct {
	AppendTime                time.Duration `json:"appendTime"`
	StateProcessTime          time.Duration `json:"stateProcessTime"`
	PendingHeaderCreationTime time.Duration `json:"pendingHeaderCreationTime"`
	BlockNumber               *big.Int      `json:"number"`
	Chain                     string        `json:"chain"`
}

type nodeStats struct {
	Name                string     `json:"name"`
	Timestamp           *big.Int   `json:"timestamp"`
	RAMUsage            int64      `json:"ramUsage"`
	RAMUsagePercent     float32    `json:"ramUsagePercent"`
	RAMFreePercent      float32    `json:"ramFreePercent"`
	RAMAvailablePercent float32    `json:"ramAvailablePercent"`
	CPUUsagePercent     float32    `json:"cpuPercent"`
	CPUFree             float32    `json:"cpuFree"`
	DiskUsagePercent    float32    `json:"diskUsagePercent"`
	DiskUsageValue      int64      `json:"diskUsageValue"`
	CurrentBlockNumber  []*big.Int `json:"currentBlockNumber"`
	RegionLocation      int        `json:"regionLocation"`
	ZoneLocation        int        `json:"zoneLocation"`
	NodeStatsMod        int        `json:"nodeStatsMod"`
	HashedMAC           string     `json:"hashedMAC"`
}

type subTps struct {
	TotalNoTransactions1h float64 `json:"totalNoTransactions1h"`
	TPS1m                 float64 `json:"tps1m"`
	TPS1hr                float64 `json:"tps1hr"`
}

type tps struct {
	Tx     subTps
	QuaiTx subTps
	QiTx   subTps
	EtxIn  subTps
	EtxOut subTps
}

type BatchObject struct {
	TotalNoTransactions uint64
	OldestBlockTime     uint64
}

func (s *Service) cacheBlock(block *types.WorkObject) cachedBlock {
	txCount := float64(len(block.Transactions()))
	txInfo := block.TransactionsInfo()
	quaiTxCount := float64(txInfo["quai"].(int))
	qiTxCount := float64(txInfo["qi"].(int))
	etxInCount := float64(txInfo["etxInbound"].(int))

	currentBlock := cachedBlock{
		number:      block.NumberU64(s.backend.NodeCtx()),
		parentHash:  block.ParentHash(s.backend.NodeCtx()),
		txCount:     txCount,
		quaiTxCount: quaiTxCount,
		qiTxCount:   qiTxCount,
		etxInCount:  etxInCount,
		etxOutCount: float64(len(block.OutboundEtxs())),
		time:        block.Time(),
	}
	s.blockLookupCache.Add(block.Hash(), currentBlock)
	return currentBlock
}

func (s *Service) calculateTPS(block *types.WorkObject) *tps {
	var totalTransactions1h float64
	var totalTransactions1m float64
	var quaiTotalTransactions1h float64
	var quaiTotalTransactions1m float64
	var qiTotalTransactions1h float64
	var qiTotalTransactions1m float64
	var extInTotalTransactions1h float64
	var extInTotalTransactions1m float64
	var extOutTotalTransactions1h float64
	var extOutTotalTransactions1m float64
	var currentBlock interface{}
	var ok bool

	fullNodeBackend := s.backend.(fullNodeBackend)
	withinMinute := true

	currentBlock, ok = s.blockLookupCache.Get(block.Hash())
	if !ok {
		currentBlock = s.cacheBlock(block)
	}

	for {
		// If the current block is nil or the block is older than the window size, break
		if currentBlock == nil || currentBlock.(cachedBlock).time+c_windowSize < block.Time() {
			break
		}

		// Add the number of transactions in the block to the total
		totalTransactions1h += currentBlock.(cachedBlock).txCount
		quaiTotalTransactions1h += currentBlock.(cachedBlock).quaiTxCount
		qiTotalTransactions1h += currentBlock.(cachedBlock).qiTxCount
		extInTotalTransactions1h += currentBlock.(cachedBlock).etxInCount
		extOutTotalTransactions1h += currentBlock.(cachedBlock).etxOutCount
		if withinMinute && currentBlock.(cachedBlock).time+60 > block.Time() {
			totalTransactions1m += currentBlock.(cachedBlock).txCount
			quaiTotalTransactions1m += currentBlock.(cachedBlock).quaiTxCount
			qiTotalTransactions1m += currentBlock.(cachedBlock).qiTxCount
			extInTotalTransactions1m += currentBlock.(cachedBlock).etxInCount
			extOutTotalTransactions1m += currentBlock.(cachedBlock).etxOutCount
		} else {
			withinMinute = false
		}

		// If the current block is the genesis block, break
		if currentBlock.(cachedBlock).number == 1 {
			break
		}

		// Get the parent block
		parentHash := currentBlock.(cachedBlock).parentHash
		currentBlock, ok = s.blockLookupCache.Get(parentHash)
		if !ok {

			// If the parent block is not cached, get it from the full node backend and cache it
			fullBlock, fullBlockOk := fullNodeBackend.BlockByHash(context.Background(), parentHash)
			if fullBlockOk != nil {
				s.backend.Logger().WithField("hash", parentHash.String()).Error("Error getting block from full node backend")
				return &tps{}
			}
			currentBlock = s.cacheBlock(fullBlock)
		}
	}

	// Catches if we get to genesis block and are still within the window
	if currentBlock.(cachedBlock).number == 1 && withinMinute {
		delta := float64(block.Time() - currentBlock.(cachedBlock).time)
		return &tps{
			Tx: subTps{
				TPS1m:                 totalTransactions1m / delta,
				TPS1hr:                totalTransactions1h / delta,
				TotalNoTransactions1h: totalTransactions1h,
			},
			QuaiTx: subTps{
				TPS1m:                 quaiTotalTransactions1m / delta,
				TPS1hr:                quaiTotalTransactions1h / delta,
				TotalNoTransactions1h: quaiTotalTransactions1h,
			},
			QiTx: subTps{
				TPS1m:                 qiTotalTransactions1m / delta,
				TPS1hr:                qiTotalTransactions1h / delta,
				TotalNoTransactions1h: qiTotalTransactions1h,
			},
			EtxIn: subTps{
				TPS1m:                 extInTotalTransactions1m / delta,
				TPS1hr:                extInTotalTransactions1h / delta,
				TotalNoTransactions1h: extInTotalTransactions1h,
			},
			EtxOut: subTps{
				TPS1m:                 extOutTotalTransactions1m / delta,
				TPS1hr:                extOutTotalTransactions1h / delta,
				TotalNoTransactions1h: extOutTotalTransactions1h,
			},
		}
	} else if currentBlock.(cachedBlock).number == 1 {
		delta := float64(block.Time() - currentBlock.(cachedBlock).time)
		return &tps{
			Tx: subTps{
				TPS1m:                 totalTransactions1m / 60,
				TPS1hr:                totalTransactions1h / delta,
				TotalNoTransactions1h: totalTransactions1h,
			},
			QuaiTx: subTps{
				TPS1m:                 quaiTotalTransactions1m / 60,
				TPS1hr:                quaiTotalTransactions1h / delta,
				TotalNoTransactions1h: quaiTotalTransactions1h,
			},
			QiTx: subTps{
				TPS1m:                 qiTotalTransactions1m / 60,
				TPS1hr:                qiTotalTransactions1h / delta,
				TotalNoTransactions1h: qiTotalTransactions1h,
			},
			EtxIn: subTps{
				TPS1m:                 extInTotalTransactions1m / 60,
				TPS1hr:                extInTotalTransactions1h / delta,
				TotalNoTransactions1h: extInTotalTransactions1h,
			},
			EtxOut: subTps{
				TPS1m:                 extOutTotalTransactions1m / 60,
				TPS1hr:                extOutTotalTransactions1h / delta,
				TotalNoTransactions1h: extOutTotalTransactions1h,
			},
		}
	}
	// make window size float
	cWindowSizeFloat := float64(c_windowSize)

	return &tps{
		Tx: subTps{
			TPS1m:                 totalTransactions1m / 60,
			TPS1hr:                totalTransactions1h / cWindowSizeFloat,
			TotalNoTransactions1h: totalTransactions1h,
		},
		QuaiTx: subTps{
			TPS1m:                 quaiTotalTransactions1m / 60,
			TPS1hr:                quaiTotalTransactions1h / cWindowSizeFloat,
			TotalNoTransactions1h: quaiTotalTransactions1h,
		},
		QiTx: subTps{
			TPS1m:                 qiTotalTransactions1m / 60,
			TPS1hr:                qiTotalTransactions1h / cWindowSizeFloat,
			TotalNoTransactions1h: qiTotalTransactions1h,
		},
		EtxIn: subTps{
			TPS1m:                 extInTotalTransactions1m / 60,
			TPS1hr:                extInTotalTransactions1h / cWindowSizeFloat,
			TotalNoTransactions1h: extInTotalTransactions1h,
		},
		EtxOut: subTps{
			TPS1m:                 extOutTotalTransactions1m / 60,
			TPS1hr:                extOutTotalTransactions1h / cWindowSizeFloat,
			TotalNoTransactions1h: extOutTotalTransactions1h,
		},
	}
}

func (s *Service) assembleBlockDetailStats(block *types.WorkObject) *blockDetailStats {
	if block == nil {
		return nil
	}
	uncleCount := uint64(0)
	woCount := uint64(0)
	for _, uncle := range block.Uncles() {
		validity := s.backend.UncleWorkShareClassification(uncle)
		switch validity {
		case types.Block:
			uncleCount += 1
		case types.Valid:
			woCount += 1
		}
	}

	// exhange rate
	primeTerminus, err := s.backend.(fullNodeBackend).BlockByHash(context.Background(), block.PrimeTerminusHash())
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to get prime terminus block")
		return nil
	}

	exchangeRate := primeTerminus.ExchangeRate()

	qiType := block.PrimaryCoinbase().IsInQiLedgerScope()
	difficulty := block.Difficulty().String()
	quaiPerQi := misc.QiToQuai(block, exchangeRate, block.Difficulty(), big.NewInt(1)).String()
	var quaiReward *big.Int
	var qiReward *big.Int
	if qiType {
		qiReward = misc.CalculateReward(block.WorkObjectHeader(), block.Difficulty(), exchangeRate)
		quaiReward = misc.QiToQuai(block, exchangeRate, block.Difficulty(), qiReward)
	} else {
		quaiReward = misc.CalculateReward(block.WorkObjectHeader(), block.Difficulty(), exchangeRate)
		qiReward = misc.QuaiToQi(block, exchangeRate, block.Difficulty(), quaiReward)
	}
	supplyAddedQuai, supplyRemovedQuai, totalSupplyQuai, supplyAddedQi, supplyRemovedQi, totalSupplyQi, err := rawdb.ReadSupplyAnalyticsForBlock(s.backend.Database(), block.Hash())
	if err != nil {
		s.backend.Logger().WithField("err", err).Error("Failed to get supply analytics")
	}

	kQuaiSlip := primeTerminus.KQuaiDiscount()
	conversionFlowAmount := primeTerminus.ConversionFlowAmount()
	kQuai := primeTerminus.ExchangeRate()
	kQi := params.OneOverKqi(primeTerminus.NumberU64(common.ZONE_CTX))
	minerDifficulty := primeTerminus.MinerDifficulty()
	slip := big.NewInt(0)

	// Assemble and return the block stats
	return &blockDetailStats{
		Timestamp:            new(big.Int).SetUint64(block.Time()),
		ZoneHeight:           block.NumberU64(2),
		RegionHeight:         block.NumberU64(1),
		PrimeHeight:          block.NumberU64(0),
		Chain:                s.backend.NodeLocation().Name(),
		Entropy:              common.BigBitsToBits(s.backend.TotalLogEntropy(block)).String(),
		Difficulty:           difficulty,
		QuaiPerQi:            quaiPerQi,
		QuaiReward:           quaiReward.String(),
		QiReward:             qiReward.String(),
		QiType:               qiType,
		UncleCount:           uncleCount,
		WoCount:              woCount,
		ExchangeRate:         exchangeRate,
		BaseFee:              block.BaseFee(),
		GasLimit:             block.GasLimit(),
		GasUsed:              block.GasUsed(),
		StateLimit:           block.StateLimit(),
		StateUsed:            block.StateUsed(),
		QuaiSupplyAdded:      supplyAddedQuai,
		QuaiSupplyRemoved:    supplyRemovedQuai,
		QuaiSupplyTotal:      totalSupplyQuai,
		QiSupplyAdded:        supplyAddedQi,
		QiSupplyRemoved:      supplyRemovedQi,
		QiSupplyTotal:        totalSupplyQi,
		KQuaiSlip:            kQuaiSlip,
		ConversionFlowAmount: conversionFlowAmount,
		KQuai:                kQuai,
		KQi:                  kQi,
		MinerDifficulty:      minerDifficulty,
		Slip:                 slip,
	}
}

func (s *Service) assembleBlockAppendTimeStats(block *types.WorkObject) *blockAppendTime {
	if block == nil {
		return nil
	}
	appendTime := block.GetAppendTime()
	stateProcessTime := block.GetStateProcessTime()
	pendingHeaderCreationTime := block.GetPendingHeaderCreationTime()

	// Assemble and return the block stats
	return &blockAppendTime{
		AppendTime:                appendTime,
		StateProcessTime:          stateProcessTime,
		PendingHeaderCreationTime: pendingHeaderCreationTime,
		BlockNumber:               block.Number(s.backend.NodeCtx()),
		Chain:                     s.backend.NodeLocation().Name(),
	}
}

func (s *Service) assembleBlockTransactionStats(block *types.WorkObject) *blockTransactionStats {
	if block == nil {
		return nil
	}
	tps := s.calculateTPS(block)
	if tps == nil {
		return nil
	}

	// Assemble and return the block stats
	return &blockTransactionStats{
		Timestamp: new(big.Int).SetUint64(block.Time()),
		Tx:        tps.Tx,
		QuaiTx:    tps.QuaiTx,
		QiTx:      tps.QiTx,
		EtxIn:     tps.EtxIn,
		EtxOut:    tps.EtxOut,
		Chain:     s.backend.NodeLocation().Name(),
	}
}

func getQuaiCPUUsage() (float64, error) {
	// 'ps' command options might vary depending on your OS
	cmd := exec.Command("ps", "aux")
	numCores := runtime.NumCPU()

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	var totalCpuUsage float64
	var cpuUsage float64
	for _, line := range lines {
		if strings.Contains(line, "go-quai") {
			fields := strings.Fields(line)
			if len(fields) > 2 {
				// Assuming %CPU is the third column, command is the eleventh
				cpuUsage, err = strconv.ParseFloat(fields[2], 64)
				if err != nil {
					return 0, err
				}
				totalCpuUsage += cpuUsage
			}
		}
	}

	if totalCpuUsage == 0 {
		return 0, errors.New("quai process not found")
	}

	return totalCpuUsage / float64(numCores), nil
}

func getQuaiRAMUsage() (uint64, error) {
	// Get a list of all running processes
	processes, err := process.Processes()
	if err != nil {
		return 0, err
	}

	var totalRam uint64

	for _, p := range processes {
		cmdline, err := p.Cmdline()
		if err != nil {
			continue
		}

		if strings.Contains(cmdline, "go-quai") {
			memInfo, err := p.MemoryInfo()
			if err != nil {
				return 0, err
			}
			totalRam += memInfo.RSS
		}
	}

	if totalRam == 0 {
		return 0, errors.New("go-quai process not found")
	}

	return totalRam, nil
}

// dirSize returns the size of a directory in bytes.
func dirSize(path string) (int64, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("du", "-sk", path)
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("du", "-bs", path)
	} else {
		return -1, errors.New("unsupported OS")
	}
	// Execute command
	output, err := cmd.Output()
	if err != nil {
		return -1, err
	}

	// Split the output and parse the size.
	sizeStr := strings.Split(string(output), "\t")[0]
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return -1, err
	}

	// If on macOS, convert size from kilobytes to bytes.
	if runtime.GOOS == "darwin" {
		size *= 1024
	}

	return size, nil
}

// diskTotalSize returns the total size of the disk in bytes.
func diskTotalSize() (int64, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("df", "-k", "/")
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("df", "--block-size=1K", "/")
	} else {
		return 0, errors.New("unsupported OS")
	}

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errors.New("unexpected output from df command")
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return 0, errors.New("unexpected output from df command")
	}

	totalSize, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}

	return totalSize * 1024, nil // convert from kilobytes to bytes
}
