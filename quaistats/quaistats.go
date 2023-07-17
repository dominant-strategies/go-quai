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
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"io/ioutil"
	"math/big"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"

	"os/exec"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/eth/downloader"
	ethproto "github.com/dominant-strategies/go-quai/eth/protocols/eth"
	"github.com/dominant-strategies/go-quai/event"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/node"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/gorilla/websocket"
)

const (
	// historyUpdateRange is the number of blocks a node should report upon login or
	// history request.
	historyUpdateRange = 50

	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10
	chainSideChanSize = 10

	// reportInterval is the time interval between two reports.
	reportInterval = 15

	c_alpha               = 8
	c_tpsLookupCacheLimit = 100
	c_gasLookupCacheLimit = 100
	c_statsErrorValue     = int64(-1)
)

// backend encompasses the bare-minimum functionality needed for quaistats reporting
type backend interface {
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription
	SubscribeNewTxsEvent(ch chan<- core.NewTxsEvent) event.Subscription
	CurrentHeader() *types.Header
	TotalLogS(header *types.Header) *big.Int
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	Stats() (pending int, queued int)
	Downloader() *downloader.Downloader
	ChainConfig() *params.ChainConfig
}

// fullNodeBackend encompasses the functionality necessary for a full node
// reporting to quaistats
type fullNodeBackend interface {
	backend
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error)
	CurrentBlock() *types.Block
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
}

// Service implements an Quai netstats reporting daemon that pushes local
// chain statistics up to a monitoring server.
type Service struct {
	server  *p2p.Server // Peer-to-peer server to retrieve networking infos
	backend backend
	engine  consensus.Engine // Consensus engine to retrieve variadic block fields

	node string // Name of the node to display on the monitoring page
	pass string // Password to authorize access to the monitoring page
	host string // Remote address of the monitoring service

	pongCh  chan struct{} // Pong notifications are fed into this channel
	histCh  chan []uint64 // History request block numbers are fed into this channel
	headSub event.Subscription
	sideSub event.Subscription

	tpsLookupCache *lru.Cache
	gasLookupCache *lru.Cache

	chainID *big.Int

	instanceDir string // Path to the node's instance directory
}

// connWrapper is a wrapper to prevent concurrent-write or concurrent-read on the
// websocket.
//
// From Gorilla websocket docs:
//
//	Connections support one concurrent reader and one concurrent writer.
//	Applications are responsible for ensuring that no more than one goroutine calls the write methods
//	  - NextWriter, SetWriteDeadline, WriteMessage, WriteJSON, EnableWriteCompression, SetCompressionLevel
//	concurrently and that no more than one goroutine calls the read methods
//	  - NextReader, SetReadDeadline, ReadMessage, ReadJSON, SetPongHandler, SetPingHandler
//	concurrently.
//	The Close and WriteControl methods can be called concurrently with all other methods.
type connWrapper struct {
	conn *websocket.Conn

	rlock sync.Mutex
	wlock sync.Mutex
}

func newConnectionWrapper(conn *websocket.Conn, jwt *string) *connWrapper {
	return &connWrapper{conn: conn}
}

// WriteJSON wraps corresponding method on the websocket but is safe for concurrent calling
func (w *connWrapper) WriteJSON(v interface{}) error {
	w.wlock.Lock()
	defer w.wlock.Unlock()

	return w.conn.WriteJSON(v)
}

// ReadJSON wraps corresponding method on the websocket but is safe for concurrent calling
func (w *connWrapper) ReadJSON(v interface{}) error {
	w.rlock.Lock()
	defer w.rlock.Unlock()

	return w.conn.ReadJSON(v)
}

// Close wraps corresponding method on the websocket but is safe for concurrent calling
func (w *connWrapper) Close() error {
	// The Close and WriteControl methods can be called concurrently with all other methods,
	// so the mutex is not used here
	return w.conn.Close()
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
func New(node *node.Node, backend backend, engine consensus.Engine, url string) error {
	parts, err := parseEthstatsURL(url)
	if err != nil {
		return err
	}

	tpsLookupCache, _ := lru.New(c_tpsLookupCacheLimit)
	gasLookupCache, _ := lru.New(c_gasLookupCacheLimit)

	quaistats := &Service{
		backend:        backend,
		engine:         engine,
		server:         node.Server(),
		node:           parts[0],
		pass:           parts[1],
		host:           parts[2],
		pongCh:         make(chan struct{}),
		histCh:         make(chan []uint64, 1),
		chainID:        backend.ChainConfig().ChainID,
		tpsLookupCache: tpsLookupCache,
		gasLookupCache: gasLookupCache,
		instanceDir:    node.InstanceDir(),
	}

	node.RegisterLifecycle(quaistats)
	return nil
}

// Start implements node.Lifecycle, starting up the monitoring and reporting daemon.
func (s *Service) Start() error {
	// Subscribe to chain events to execute updates on
	chainHeadCh := make(chan core.ChainHeadEvent, chainHeadChanSize)
	chainSideCh := make(chan core.ChainSideEvent, chainSideChanSize)

	s.headSub = s.backend.SubscribeChainHeadEvent(chainHeadCh)
	s.sideSub = s.backend.SubscribeChainSideEvent(chainSideCh)

	go s.loop(chainHeadCh, chainSideCh)

	log.Info("Stats daemon started")
	return nil
}

// Stop implements node.Lifecycle, terminating the monitoring and reporting daemon.
func (s *Service) Stop() error {
	s.headSub.Unsubscribe()
	s.sideSub.Unsubscribe()
	log.Info("Stats daemon stopped")
	return nil
}

// loop keeps trying to connect to the netstats server, reporting chain events
// until termination.
func (s *Service) loop(chainHeadCh chan core.ChainHeadEvent, chainSideCh chan core.ChainSideEvent) {
	nodeCtx := common.NodeLocation.Context()
	// Start a goroutine that exhausts the subscriptions to avoid events piling up
	var (
		quitCh = make(chan struct{})
		headCh = make(chan *types.Block, 1)
		sideCh = make(chan *types.Block, 1)
	)
	go func() {
	HandleLoop:
		for {
			select {
			// Notify of chain head events, but drop if too frequent
			case head := <-chainHeadCh:
				select {
				case headCh <- head.Block:
				default:
				}
			// Notify of chain side events, but drop if too frequent
			case sideEvent := <-chainSideCh:
				select {
				case sideCh <- sideEvent.Block:
				default:
				}
			case <-s.headSub.Err():
				break HandleLoop
			}
		}
		close(quitCh)
	}()

	// Resolve the URL, defaulting to TLS, but falling back to none too
	path := fmt.Sprintf("%s/api", s.host)
	authPath := fmt.Sprintf("")
	urls := []string{path}

	// url.Parse and url.IsAbs is unsuitable (https://github.com/golang/go/issues/19779)
	if !strings.Contains(path, "://") {
		urls = []string{"wss://" + path, "ws://" + path}
	}

	errTimer := time.NewTimer(0)
	defer errTimer.Stop()
	var authJwt *string
	// Loop reporting until termination
	for {
		select {
		case <-quitCh:
			return
		case <-errTimer.C:
			// If we don't have a JWT or it's expired, get a new one
			if authJwt == nil || s.isJwtExpired(authJwt) {
				var err error
				authJwt, err = s.login2()
				if err != nil {
					log.Warn("Unable to retrieve JWT", "err", err)
					errTimer.Reset(10 * time.Second)
					continue
				}
			}
			// Establish a websocket connection to the server on any supported URL
			var (
				conn *connWrapper
				err  error
			)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			header := make(http.Header)
			header.Set("origin", "http://localhost")
			for _, url := range urls {
				c, _, e := dialer.Dial(url, header)
				err = e
				if err == nil {
					conn = newConnectionWrapper(c, authJwt)
					break
				}
			}
			if err != nil {
				log.Warn("Stats server unreachable", "err", err)
				errTimer.Reset(10 * time.Second)
				continue
			}
			// Authenticate the client with the server
			if err = s.login(conn); err != nil {
				log.Warn("Stats login failed", "err", err)
				conn.Close()
				errTimer.Reset(10 * time.Second)
				continue
			}
			go s.readLoop(conn)

			// Send the initial stats so our node looks decent from the get go
			if err = s.report(conn); err != nil {
				log.Warn("Initial stats report failed", "err", err)
				conn.Close()
				errTimer.Reset(0)
				continue
			}
			// Keep sending status updates until the connection breaks
			fullReport := time.NewTicker(reportInterval * time.Second)

			for err == nil {
				select {
				case <-quitCh:
					fullReport.Stop()
					// Make sure the connection is closed
					conn.Close()
					return

				case <-fullReport.C:
					if err = s.report(conn); err != nil {
						log.Warn("Full stats report failed", "err", err)
					}
				case list := <-s.histCh:
					if err = s.reportHistory(conn, list); err != nil {
						log.Warn("Requested history report failed", "err", err)
					}
				case head := <-headCh:
					if err = s.reportBlock(conn, head); err != nil {
						log.Warn("Block stats report failed", "err", err)
					}
					if nodeCtx == common.ZONE_CTX {
						if err = s.reportPending(conn); err != nil {
							log.Warn("Post-block transaction stats report failed", "err", err)
						}
					}
				case sideEvent := <-sideCh:
					if err = s.reportSideBlock(conn, sideEvent); err != nil {
						log.Warn("Block stats report failed", "err", err)
					}
				}
			}
			fullReport.Stop()
			// Close the current connection and establish a new one
			conn.Close()
			errTimer.Reset(0)
		}
	}
}

// readLoop loops as long as the connection is alive and retrieves data packets
// from the network socket. If any of them match an active request, it forwards
// it, if they themselves are requests it initiates a reply, and lastly it drops
// unknown packets.
func (s *Service) readLoop(conn *connWrapper) {
	// If the read loop exits, close the connection
	defer conn.Close()

	for {
		// Retrieve the next generic network packet and bail out on error
		var blob json.RawMessage
		if err := conn.ReadJSON(&blob); err != nil {
			log.Warn("Failed to retrieve stats server message", "err", err)
			return
		}
		// If the network packet is a system ping, respond to it directly
		var ping string
		if err := json.Unmarshal(blob, &ping); err == nil && strings.HasPrefix(ping, "primus::ping::") {
			if err := conn.WriteJSON(strings.Replace(ping, "ping", "pong", -1)); err != nil {
				log.Warn("Failed to respond to system ping message", "err", err)
				return
			}
			continue
		}
		// Not a system ping, try to decode an actual state message
		var msg map[string][]interface{}
		if err := json.Unmarshal(blob, &msg); err != nil {
			log.Warn("Failed to decode stats server message", "err", err)
			return
		}
		log.Trace("Received message from stats server", "msg", msg)
		if len(msg["emit"]) == 0 {
			log.Warn("Stats server sent non-broadcast", "msg", msg)
			return
		}
		command, ok := msg["emit"][0].(string)
		if !ok {
			log.Warn("Invalid stats server message type", "type", msg["emit"][0])
			return
		}
		// If the message is a ping reply, deliver (someone must be listening!)
		if len(msg["emit"]) == 2 && command == "node-pong" {
			select {
			case s.pongCh <- struct{}{}:
				// Pong delivered, continue listening
				continue
			default:
				// Ping routine dead, abort
				log.Warn("Stats server pinger seems to have died")
				return
			}
		}
		// If the message is a history request, forward to the event processor
		if len(msg["emit"]) == 2 && command == "history" {
			// Make sure the request is valid and doesn't crash us
			request, ok := msg["emit"][1].(map[string]interface{})
			if !ok {
				log.Warn("Invalid stats history request", "msg", msg["emit"][1])
				select {
				case s.histCh <- nil: // Treat it as an no indexes request
				default:
				}
				continue
			}
			list, ok := request["list"].([]interface{})
			if !ok {
				log.Warn("Invalid stats history block list", "list", request["list"])
				return
			}
			// Convert the block number list to an integer list
			numbers := make([]uint64, len(list))
			for i, num := range list {
				n, ok := num.(float64)
				if !ok {
					log.Warn("Invalid stats history block number", "number", num)
					return
				}
				numbers[i] = uint64(n)
			}
			select {
			case s.histCh <- numbers:
				continue
			default:
			}
		}
		// Report anything else and continue
		log.Info("Unknown stats message", "msg", msg)
	}
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
	name     string `json:"name"`
	password string `json:"password"`
}

// login tries to authorize the client at the remote server.
func (s *Service) login(conn *connWrapper) error {
	// Construct and send the login authentication
	infos := s.server.NodeInfo()

	var protocols []string
	for _, proto := range s.server.Protocols {
		protocols = append(protocols, fmt.Sprintf("%s/%d", proto.Name, proto.Version))
	}
	var network string
	if info := infos.Protocols["eth"]; info != nil {
		network = fmt.Sprintf("%d", info.(*ethproto.NodeInfo).Network)
	}
	auth := &authMsg{
		ID: s.node,
		Info: nodeInfo{
			Name:     s.node,
			Node:     infos.Name,
			Port:     infos.Ports.Listener,
			Network:  network,
			Protocol: strings.Join(protocols, ", "),
			API:      "No",
			Os:       runtime.GOOS,
			OsVer:    runtime.GOARCH,
			Client:   "0.1.1",
			History:  true,
			Chain:    common.NodeLocation.Name(),
			ChainID:  s.chainID.Uint64(),
		},
		Secret: loginSecret{
			name:     "admin",
			password: s.pass,
		},
	}
	login := map[string][]interface{}{
		"emit": {"hello", auth},
	}
	if err := conn.WriteJSON(login); err != nil {
		return err
	}
	// Retrieve the remote ack or connection termination
	var ack map[string][]string
	if err := conn.ReadJSON(&ack); err != nil || len(ack["emit"]) != 1 || ack["emit"][0] != "ready" {
		return errors.New("unauthorized")
	}
	return nil
}

type Credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
}

func (s *Service) login2() (*string, error) {
	// Substitute with your actual service address and port
	url := "http://localhost:3000/login"

	creds := &Credentials{
		Name:     "admin",
		Password: s.pass,
	}

	credsJson, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(credsJson))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	var authResponse AuthResponse
	err = json.Unmarshal(body, &authResponse)
	if err != nil {
		return nil, err
	}

	if authResponse.Success {
		return &authResponse.Token, nil
	}

	return nil, fmt.Errorf("login failed")
}

// isJwtExpired checks if the JWT token is expired
func (s *Service) isJwtExpired(authJwt *string) (bool, error) {
	if authJwt == nil {
		return false, errors.New("token is nil")
	}

	parts := strings.Split(*authJwt, ".")
	if len(parts) != 3 {
		return false, errors.New("invalid token")
	}

	claims := jwt.MapClaims{}
	_, _, err := new(jwt.Parser).ParseUnverified(*authJwt, claims)
	if err != nil {
		return false, err
	}

	if exp, ok := claims["exp"].(float64); ok {
		return time.Now().Unix() >= int64(exp), nil
	}

	return false, errors.New("exp claim not found in token")
}

// report collects all possible data to report and send it to the stats server.
// This should only be used on reconnects or rarely to avoid overloading the
// server. Use the individual methods for reporting subscribed events.
func (s *Service) report(conn *connWrapper) error {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx == common.ZONE_CTX {
		if err := s.reportPending(conn); err != nil {
			return err
		}
	}
	if err := s.reportStats(conn); err != nil {
		return err
	}
	if err := s.reportPeers(conn); err != nil {
		return err
	}
	return nil
}

type latencyReport struct {
	Latency int `json:"latency"`
}

// reportLatency sends a ping request to the server, measures the RTT time and
// finally sends a latency update.
func (s *Service) reportLatency(conn *connWrapper) error {
	// Send the current time to the quaistats server
	start := time.Now()

	ping := map[string][]interface{}{
		"emit": {"node-ping", map[string]string{
			"id":         s.node,
			"clientTime": start.String(),
		}},
	}
	if err := conn.WriteJSON(ping); err != nil {
		return err
	}
	// Wait for the pong request to arrive back
	select {
	case <-s.pongCh:
		// Pong delivered, report the latency
	case <-time.After(5 * time.Second):
		// Ping timeout, abort
		return errors.New("ping timed out")
	}

	latency := int((time.Since(start) / time.Duration(2)).Nanoseconds() / 1000000)

	// Send back the measured latency
	log.Trace("Sending measured latency to ethstats", "latency", strconv.Itoa(latency))

	latencyReport := map[string]interface{}{
		"id": s.node,
		"latency": &latencyReport{
			Latency: latency,
		},
	}

	report := map[string][]interface{}{
		"emit": {"latency", latencyReport},
	}

	return conn.WriteJSON(report)
}

// blockStats is the information to report about individual blocks.
type blockStats struct {
	Number        *big.Int       `json:"number"`
	Hash          common.Hash    `json:"hash"`
	ParentHash    common.Hash    `json:"parentHash"`
	Timestamp     *big.Int       `json:"timestamp"`
	Miner         common.Address `json:"miner"`
	GasUsed       uint64         `json:"gasUsed"`
	GasLimit      uint64         `json:"gasLimit"`
	Diff          string         `json:"difficulty"`
	Entropy       string         `json:"entropy"`
	Txs           []txStats      `json:"transactions"`
	TxHash        common.Hash    `json:"transactionsRoot"`
	EtxHash       common.Hash    `json:"extTransactionsRoot"`
	EtxRollupHash common.Hash    `json:"extRollupRoot"`
	ManifestHash  common.Hash    `json:"manifestHash"`
	Root          common.Hash    `json:"stateRoot"`
	Uncles        uncleStats     `json:"uncles"`
	Chain         string         `json:"chain"`
	ChainID       uint64         `json:"chainId"`
	Tps           int64          `json:"tps"`
	AppendTime    time.Duration  `json:"appendTime"`
	AvgGasPerSec  int64          `json:"avgGasPerSec"`
}

type blockTpsCacheDto struct {
	Tps  int64 `json:"tps"`
	Time int64 `json:"time"`
}

type blockAvgGasPerSecCacheDto struct {
	AvgGasPerSec int64 `json:"avgGasPerSec"`
	Time         int64 `json:"time"`
}

// txStats is the information to report about individual transactions.
type txStats struct {
	Hash common.Hash `json:"hash"`
}

// uncleStats is a custom wrapper around an uncle array to force serializing
// empty arrays instead of returning null for them.
type uncleStats []*types.Header

func (s uncleStats) MarshalJSON() ([]byte, error) {
	if uncles := ([]*types.Header)(s); len(uncles) > 0 {
		return json.Marshal(uncles)
	}
	return []byte("[]"), nil
}

func (s *Service) computeAvgGasPerSec(block *types.Block) int64 {
	var parentTime, parentGasPerSec int64
	if parentCached, ok := s.gasLookupCache.Get(block.ParentHash()); ok {
		parentTime = parentCached.(blockAvgGasPerSecCacheDto).Time
		parentGasPerSec = parentCached.(blockAvgGasPerSecCacheDto).AvgGasPerSec
	} else {
		log.Warn(fmt.Sprintf("computeGas: block %d's parent not found in cache: %s", block.Number(), block.ParentHash().String()))
		fullBackend, ok := s.backend.(fullNodeBackend)
		if ok {
			parent, err := fullBackend.BlockByNumber(context.Background(), rpc.BlockNumber(block.NumberU64()-1))
			if err != nil {
				log.Error("error getting parent block %s: %s", block.ParentHash().String(), err.Error())
				return c_statsErrorValue
			}
			parentTime = int64(parent.Time())
			parentGasPerSec = c_statsErrorValue
		} else {
			log.Error("computeGas: not running fullnode, cannot get parent block")
			return c_statsErrorValue
		}
	}
	instantGas := int64(block.GasUsed())
	if dt := int64(block.Time()) - parentTime; dt != 0 { // this is a hack to avoid dividing by 0
		instantGas /= dt
	}

	var blockGasPerSec int64
	if parentGasPerSec == c_statsErrorValue {
		blockGasPerSec = instantGas
	} else {
		blockGasPerSec = ((c_alpha-1)*parentGasPerSec + instantGas) / c_alpha
	}

	s.gasLookupCache.Add(block.Hash(), blockAvgGasPerSecCacheDto{
		AvgGasPerSec: blockGasPerSec,
		Time:         int64(block.Time()),
	})

	return blockGasPerSec
}

func (s *Service) computeTps(block *types.Block) int64 {
	var parentTime, parentTps int64

	if parentCached, ok := s.tpsLookupCache.Get(block.ParentHash()); ok {
		parentTime = parentCached.(blockTpsCacheDto).Time
		parentTps = parentCached.(blockTpsCacheDto).Tps
	} else {
		log.Warn(fmt.Sprintf("block %d's parent not found in cache: %s", block.Number(), block.ParentHash().String()))
		fullBackend, ok := s.backend.(fullNodeBackend)
		if ok {
			parent, err := fullBackend.BlockByNumber(context.Background(), rpc.BlockNumber(block.NumberU64()-1))
			if err != nil {
				log.Error("error getting parent block %s: %s", block.ParentHash().String(), err.Error())
				return c_statsErrorValue
			}
			parentTime = int64(parent.Time())
			parentTps = c_statsErrorValue
		} else {
			log.Error("not running fullnode, cannot get parent block")
			return c_statsErrorValue
		}
	}

	instantTps := int64(len(block.Transactions()))
	if dt := int64(block.Time()) - parentTime; dt != 0 { // this is a hack to avoid dividing by 0
		instantTps /= dt
	}

	var blockTps int64
	if parentTps == c_statsErrorValue {
		blockTps = instantTps
	} else {
		blockTps = ((c_alpha-1)*parentTps + instantTps) / c_alpha
	}

	s.tpsLookupCache.Add(block.Hash(), blockTpsCacheDto{
		Tps:  blockTps,
		Time: int64(block.Time()),
	})

	return blockTps
}

// reportSideBlock retrieves the current chain side event and reports it to the stats server.
func (s *Service) reportSideBlock(conn *connWrapper, block *types.Block) error {
	log.Trace("Sending new side block to quaistats", "number", block.Number(), "hash", block.Hash())

	stats := map[string]interface{}{
		"id":        s.node,
		"sideBlock": block,
	}
	report := map[string][]interface{}{
		"emit": {"sideBlock", stats},
	}
	return conn.WriteJSON(report)
}

// reportBlock retrieves the current chain head and reports it to the stats server.
func (s *Service) reportBlock(conn *connWrapper, block *types.Block) error {
	// Gather the block details from the header or block chain
	details := s.assembleBlockStats(block)

	// Assemble the block report and send it to the server
	log.Trace("Sending new block to quaistats", "number", details.Number, "hash", details.Hash)

	stats := map[string]interface{}{
		"id":    s.node,
		"block": details,
	}
	report := map[string][]interface{}{
		"emit": {"block", stats},
	}
	return conn.WriteJSON(report)
}

// assembleBlockStats retrieves any required metadata to report a single block
// and assembles the block stats. If block is nil, the current head is processed.
func (s *Service) assembleBlockStats(block *types.Block) *blockStats {
	// Gather the block infos from the local blockchain
	var (
		header  *types.Header
		entropy *big.Int
		txs     []txStats
		uncles  []*types.Header
	)
	header = block.Header()
	entropy = s.backend.TotalLogS(block.Header())

	txs = make([]txStats, len(block.Transactions()))
	for i, tx := range block.Transactions() {
		txs[i].Hash = tx.Hash()
	}
	uncles = block.Uncles()

	// Assemble and return the block stats
	author, _ := s.engine.Author(header)

	tps := s.computeTps(block)
	avgGasPerSec := s.computeAvgGasPerSec(block)

	appendTime := block.GetAppendTime()

	return &blockStats{
		Number:        header.Number(),
		Hash:          header.Hash(),
		ParentHash:    header.ParentHash(),
		Timestamp:     new(big.Int).SetUint64(header.Time()),
		Miner:         author,
		GasUsed:       header.GasUsed(),
		GasLimit:      header.GasLimit(),
		Diff:          header.Difficulty().String(),
		Entropy:       entropy.String(),
		Txs:           txs,
		TxHash:        header.TxHash(),
		EtxHash:       header.EtxHash(),
		EtxRollupHash: header.EtxRollupHash(),
		ManifestHash:  header.ManifestHash(),
		Root:          header.Root(),
		Uncles:        uncles,
		Chain:         common.NodeLocation.Name(),
		ChainID:       s.chainID.Uint64(),
		Tps:           tps,
		AppendTime:    appendTime,
		AvgGasPerSec:  avgGasPerSec,
	}
}

// reportHistory retrieves the most recent batch of blocks and reports it to the
// stats server.
func (s *Service) reportHistory(conn *connWrapper, list []uint64) error {
	// Figure out the indexes that need reporting
	indexes := make([]uint64, 0, historyUpdateRange)
	if len(list) > 0 {
		// Specific indexes requested, send them back in particular
		indexes = append(indexes, list...)
	} else {
		// No indexes requested, send back the top ones
		head := s.backend.CurrentHeader().Number().Int64()
		start := head - historyUpdateRange + 1
		if start < 0 {
			start = 0
		}
		for i := uint64(start); i <= uint64(head); i++ {
			indexes = append(indexes, i)
		}
	}
	// Gather the batch of blocks to report
	history := make([]*blockStats, len(indexes))
	for i, number := range indexes {
		fullBackend, ok := s.backend.(fullNodeBackend)
		// Retrieve the next block if it's known to us
		var block *types.Block
		if ok {
			block, _ = fullBackend.BlockByNumber(context.Background(), rpc.BlockNumber(number)) // TODO ignore error here ?
		} else {
			if header, _ := s.backend.HeaderByNumber(context.Background(), rpc.BlockNumber(number)); header != nil {
				block = types.NewBlockWithHeader(header)
			}
		}
		// If we do have the block, add to the history and continue
		if block != nil {
			history[len(history)-1-i] = s.assembleBlockStats(block)
			continue
		}
		// Ran out of blocks, cut the report short and send
		history = history[len(history)-i:]
		break
	}
	// Assemble the history report and send it to the server
	if len(history) > 0 {
		log.Trace("Sending historical blocks to quaistats", "first", history[0].Number, "last", history[len(history)-1].Number)
	} else {
		log.Trace("No history to send to stats server")
	}
	stats := map[string]interface{}{
		"id":      s.node,
		"history": history,
	}
	report := map[string][]interface{}{
		"emit": {"history", stats},
	}
	return conn.WriteJSON(report)
}

// pendStats is the information to report about pending transactions.
type pendStats struct {
	Pending int `json:"pending"`
}

// reportPending retrieves the current number of pending transactions and reports
// it to the stats server.
func (s *Service) reportPending(conn *connWrapper) error {
	// Retrieve the pending count from the local blockchain
	pending, _ := s.backend.Stats()
	// Assemble the transaction stats and send it to the server
	log.Trace("Sending pending transactions to quaistats", "count", strconv.Itoa(pending))

	stats := map[string]interface{}{
		"id": s.node,
		"pending": &pendStats{
			Pending: pending,
		},
	}
	report := map[string][]interface{}{
		"emit": {"pending", stats},
	}
	return conn.WriteJSON(report)
}

// nodeStats is the information to report about the local node.
type nodeStats struct {
	Name             string  `json:"name"`
	Active           bool    `json:"active"`
	Syncing          bool    `json:"syncing"`
	Mining           bool    `json:"mining"`
	Hashrate         int     `json:"hashrate"`
	Peers            int     `json:"peers"`
	GasPrice         int     `json:"gasPrice"`
	Uptime           int     `json:"uptime"`
	Chain            string  `json:"chain"`
	ChainID          uint64  `json:"chainId"`
	LatestHeight     uint64  `json:"height"`
	LatestHash       string  `json:"hash"`
	CPUPercentUsage  float32 `json:"cpuPercentUsage"`
	CPUUsage         float32 `json:"cpuUsage"`
	RAMPercentUsage  float32 `json:"ramPercentUsage"`
	RAMUsage         int64   `json:"ramUsage"` // in bytes
	SwapPercentUsage float32 `json:"swapPercentUsage"`
	SwapUsage        int64   `json:"swapUsage"`
	DiskUsage        int64   `json:"diskUsage"` // in bytes
}

// reportStats retrieves various stats about the node at the networking and
// mining layer and reports it to the stats server.
func (s *Service) reportStats(conn *connWrapper) error {
	nodeCtx := common.NodeLocation.Context()
	// Gather the syncing and mining infos from the local miner instance
	var (
		mining   bool
		hashrate int
		syncing  bool
		gasprice int
	)
	// check if backend is a full node
	fullBackend, ok := s.backend.(fullNodeBackend)
	header := s.backend.CurrentHeader()
	if ok {
		sync := fullBackend.Downloader().Progress()
		syncing = fullBackend.CurrentHeader().Number().Uint64() >= sync.HighestBlock

		if nodeCtx == common.ZONE_CTX {
			price, _ := fullBackend.SuggestGasTipCap(context.Background())
			gasprice = int(price.Uint64())
			if basefee := fullBackend.CurrentHeader().BaseFee(); basefee != nil {
				gasprice += int(basefee.Uint64())
			}
		}
	} else {
		sync := s.backend.Downloader().Progress()
		syncing = header.Number().Uint64() >= sync.HighestBlock
	}

	var cpuPercentUsed float32
	if cpuStat, err := cpu.Percent(0, false); err == nil {
		cpuPercentUsed = float32(cpuStat[0])
	} else {
		log.Warn("Error getting CPU percent usage:", err)
		cpuPercentUsed = float32(c_statsErrorValue)
	}

	var cpuUsed float32
	if cpuStat, err := cpu.Times(false); err == nil {
		cpuUsed = float32(cpuStat[0].Total())

	} else {
		log.Warn("Error getting CPU times:", err)
		cpuUsed = float32(c_statsErrorValue)
	}

	var ramUsed int64
	var ramPercentUsed float32
	// Get RAM usage
	if vmStat, err := mem.VirtualMemory(); err == nil {
		ramUsed = int64(vmStat.Used)
		ramPercentUsed = float32(vmStat.UsedPercent)
	} else {
		log.Warn("Error getting RAM usage:", err)
		ramUsed = c_statsErrorValue
		ramPercentUsed = float32(c_statsErrorValue)
	}

	var swapUsed int64
	var swapPercentUsed float32
	// Get swap usage
	if swapStat, err := mem.SwapMemory(); err == nil {
		swapUsed = int64(swapStat.Used)
		swapPercentUsed = float32(swapStat.UsedPercent)
	} else {
		log.Warn("Error getting swap usage:", err)
		swapUsed = c_statsErrorValue
		swapPercentUsed = float32(c_statsErrorValue)
	}

	// Get disk usage
	diskUsage, err := dirSize(s.instanceDir)
	if err != nil {
		log.Warn("Error calculating directory sizes:", err)
		diskUsage = c_statsErrorValue
	}

	// Assemble the node stats and send it to the server
	log.Trace("Sending node details to quaistats")

	stats := map[string]interface{}{
		"id": s.node,
		"stats": &nodeStats{
			Name:             s.node,
			Active:           true,
			Mining:           mining,
			Hashrate:         hashrate,
			Peers:            s.server.PeerCount(),
			GasPrice:         gasprice,
			Syncing:          syncing,
			Uptime:           100,
			Chain:            common.NodeLocation.Name(),
			ChainID:          s.chainID.Uint64(),
			LatestHeight:     header.Number().Uint64(),
			LatestHash:       header.Hash().String(),
			CPUPercentUsage:  cpuPercentUsed,
			CPUUsage:         cpuUsed,
			RAMPercentUsage:  ramPercentUsed,
			RAMUsage:         ramUsed, // in bytes
			SwapPercentUsage: swapPercentUsed,
			SwapUsage:        swapUsed,
			DiskUsage:        diskUsage, // in bytes
		},
	}

	report := map[string][]interface{}{
		"emit": {"stats", stats},
	}
	return conn.WriteJSON(report)
}

// peerStats is the information to report about peers.
type peerStats struct {
	Chain    string      `json:"chain"`
	ChainID  uint64      `json:"chainId"`
	Count    int         `json:"count"`
	PeerData []*peerData `json:"peerData"`
}

// peerStat is the information to report about peers.
type peerData struct {
	Chain             string        `json:"chain"`
	Enode             string        `json:"enode"`        // Unique node identifier (enode URL)
	SoftwareName      string        `json:"softwareName"` // Node software version
	LocalAddress      string        `json:"localAddress"`
	RemoteAddress     string        `json:"remoteAddress"`
	RTT               string        `json:"rtt"`
	LatestHeight      uint64        `json:"latestHeight"`
	LatestEntropy     string        `json:"latestEntropy"`
	LatestHash        string        `json:"latestHash"`
	RecvLastBlockTime time.Time     `json:"recvLastBlockTime"`
	ConnectedTime     time.Duration `json:"uptime"`
	PeerUptime        string        `json:"peerUptime"` // TODO: add peer uptime
}

// reportPeers retrieves various stats about the peers for a node.
func (s *Service) reportPeers(conn *connWrapper) error {
	// Assemble the node stats and send it to the server
	log.Trace("Sending peer details to quaistats")

	peerInfo := s.backend.Downloader().PeerSet()

	allPeerData := make([]*peerData, 0)

	srvPeers := s.server.Peers()

	for _, peer := range srvPeers {
		peerStat := &peerData{
			Enode:         peer.ID().String(),
			SoftwareName:  peer.Fullname(),
			LocalAddress:  peer.LocalAddr().String(),
			RemoteAddress: peer.RemoteAddr().String(),
			Chain:         common.NodeLocation.Name(),
			ConnectedTime: peer.ConnectedTime(),
		}

		downloaderPeer := peerInfo.Peer(peer.ID().String())
		if downloaderPeer != nil {
			hash, number, entropy, receivedAt := downloaderPeer.Peer().Head()
			if number == nil {
				number = big.NewInt(0)
			}
			peerStat.RTT = downloaderPeer.Tracker().Roundtrip().String()
			peerStat.LatestHeight = number.Uint64()
			peerStat.LatestEntropy = entropy.String()
			peerStat.LatestHash = hash.String()
			if receivedAt != *new(time.Time) {
				peerStat.RecvLastBlockTime = receivedAt.UTC()
			}
		}
		allPeerData = append(allPeerData, peerStat)
	}

	peers := map[string]interface{}{
		"id": s.node,
		"peers": &peerStats{
			Chain:    common.NodeLocation.Name(),
			ChainID:  s.chainID.Uint64(),
			Count:    len(srvPeers),
			PeerData: allPeerData,
		},
	}
	report := map[string][]interface{}{
		"emit": {"peers", peers},
	}
	return conn.WriteJSON(report)
}

// dirSize returns the size of a directory in bytes.
func dirSize(path string) (int64, error) {
	cmd := exec.Command("du", "-bs", path)
	if output, err := cmd.Output(); err != nil {
		return -1, err
	} else {
		size, _ := strconv.ParseInt(strings.Split(string(output), "\t")[0], 10, 64)
		return size, nil
	}
}
