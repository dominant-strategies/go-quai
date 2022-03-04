// Copyright 2017 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// faucet is an Ether faucet backed by a light client.
package main

//go:generate go-bindata -nometadata -o website.go faucet.html
//go:generate gofmt -w -s website.go

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sasha-s/go-deadlock"
	"github.com/spruce-solutions/go-quai/accounts/keystore"
	"github.com/spruce-solutions/go-quai/cmd/utils"
	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/eth/downloader"
	"github.com/spruce-solutions/go-quai/eth/ethconfig"
	"github.com/spruce-solutions/go-quai/ethclient"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/node"
	"github.com/spruce-solutions/go-quai/p2p"
	"github.com/spruce-solutions/go-quai/p2p/enode"
	"github.com/spruce-solutions/go-quai/p2p/nat"
	"github.com/spruce-solutions/go-quai/params"
)

var (
	genesisFlag   = flag.String("genesis", "", "Genesis json file to seed the chain with, currently not used")
	apiPortFlag   = flag.Int("apiport", 8080, "Listener port for the HTTP API connection")
	ethPortFlag   = flag.Int("ethport", 30307, "Listener port for the devp2p connection, currently not used")
	bootFlag      = flag.String("bootnodes", "", "Comma separated bootnode enode URLs to seed with, currently not used")
	netFlag       = flag.Uint64("network", 0, "Network ID to use for the Ethereum protocol, currently not used")
	statsFlag     = flag.String("ethstats", "", "Ethstats network monitoring auth string")
	nodeFlag      = flag.String("node", "ws://45.76.19.78", "Full Node RPC IP address, ideally websockets, without port")
	numChainsFlag = flag.Int("numchains", 13, "Number of blockchains to run the faucet for, default 13. Ensure you have the equivalent number of keys with proper naming!")
	netnameFlag   = flag.String("faucet.name", "Quai", "Network name to assign to the faucet")
	payoutFlag    = flag.Int("faucet.amount", 1, "Number of Ethers to pay out per user request") // No longer used because it is an integer but we pay out less than 1
	minutesFlag   = flag.Int("faucet.minutes", 1, "Number of minutes to wait between funding rounds")
	tiersFlag     = flag.Int("faucet.tiers", 1, "Number of funding tiers to enable (x3 time, x2.5 funds)")

	accJSONFlag = flag.String("account.json", "zone1.json", "Key json file to fund user requests with")
	accPassFlag = flag.String("account.pass", "password", "Decryption password file to access faucet funds")

	captchaToken  = flag.String("captcha.token", "", "Recaptcha site key to authenticate client side")
	captchaSecret = flag.String("captcha.secret", "", "Recaptcha secret key to authenticate server side")

	noauthFlag = flag.Bool("noauth", true, "Enables funding requests without authentication")
	logFlag    = flag.Int("loglevel", 3, "Log level to use for Ethereum and the faucet")

	twitterTokenFlag   = flag.String("twitter.token", "", "Bearer token to authenticate with the v2 Twitter API")
	twitterTokenV1Flag = flag.String("twitter.token.v1", "", "Bearer token to authenticate with the v1.1 Twitter API")

	goerliFlag  = flag.Bool("goerli", false, "Initializes the faucet with Görli network config")
	rinkebyFlag = flag.Bool("rinkeby", false, "Initializes the faucet with Rinkeby network config")
)

var (
	divisor         = new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil) // 18 = 1, 17 = 0.1, 16 = 0.01, etc
	ether           = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	displayedAmount = 0.01                                                                                                                                       // amount to be displayed
	chainList       = []string{"prime", "cyprus", "cyprus1", "cyprus2", "cyprus3", "paxos", "paxos1", "paxos2", "paxos3", "hydra", "hydra1", "hydra2", "hydra3"} // all files must have .json at the end and be in the same directory!
	portList        = []string{"8547", "8579", "8611", "8643", "8675", "8581", "8613", "8645", "8677", "8583", "8615", "8647", "8679"}                           // for websockets
)

var (
	gitCommit = "" // Git SHA1 commit hash of the release (set via linker flags)
	gitDate   = "" // Git commit date YYYYMMDD of the release (set via linker flags)
)

func main() {
	// Parse the flags and set up the logger to print everything requested
	flag.Parse()
	log.Root().SetHandler(log.LvlFilterHandler(log.Lvl(*logFlag), log.StreamHandler(os.Stderr, log.TerminalFormat(true))))

	// Construct the payout tiers
	amounts := make([]string, *tiersFlag)
	periods := make([]string, *tiersFlag)
	for i := 0; i < *tiersFlag; i++ {
		// Calculate the amount for the next tier and format it
		amount := float64(displayedAmount) /*float64(*payoutFlag)*/ * math.Pow(2.5, float64(i))
		amounts[i] = fmt.Sprintf("%s Quai", strconv.FormatFloat(amount, 'f', -1, 64))
		if amount == 1 {
			amounts[i] = strings.TrimSuffix(amounts[i], "s")
		}
		// Calculate the period for the next tier and format it
		period := *minutesFlag * int(math.Pow(3, float64(i)))
		periods[i] = fmt.Sprintf("%d mins", period)
		if period%60 == 0 {
			period /= 60
			periods[i] = fmt.Sprintf("%d hours", period)

			if period%24 == 0 {
				period /= 24
				periods[i] = fmt.Sprintf("%d days", period)
			}
		}
		if period == 1 {
			periods[i] = strings.TrimSuffix(periods[i], "s")
		}
	}
	// Load up and render the faucet website
	tmpl, err := Asset("faucet.html")
	if err != nil {
		log.Crit("Failed to load the faucet template", "err", err)
	}
	website := new(bytes.Buffer)
	err = template.Must(template.New("").Parse(string(tmpl))).Execute(website, map[string]interface{}{
		"Network":   *netnameFlag,
		"Amounts":   amounts,
		"Periods":   periods,
		"Recaptcha": *captchaToken,
		"NoAuth":    *noauthFlag,
	})
	if err != nil {
		log.Crit("Failed to render the faucet template", "err", err)
	}
	// Load and parse the genesis block requested by the user
	/*
		genesis, err := getGenesis(genesisFlag, *goerliFlag, *rinkebyFlag)
		if err != nil {
			log.Crit("Failed to parse genesis config", "err", err)
		}*/
	zone := params.MainnetZoneChainConfigs[0][0]
	genesis := core.MainnetZoneGenesisBlock(&zone)
	types.QuaiNetworkContext = zone.Context
	//prime := params.MainnetPrimeChainConfig
	//genesis := core.MainnetPrimeGenesisBlock()
	// Convert the bootnodes to internal enode representations
	var enodes []*enode.Node
	/*for _, boot := range strings.Split(*bootFlag, ",") {
		if url, err := enode.Parse(enode.ValidSchemes, boot); err == nil {
			enodes = append(enodes, url)
		} else {
			log.Error("Failed to parse bootnode URL", "url", boot, "err", err)
		}
	}*/
	for _, boot := range params.MainnetBootnodes {
		if url, err := enode.Parse(enode.ValidSchemes, boot); err == nil {
			enodes = append(enodes, url)
		}
	}
	ks := keystore.NewKeyStore(filepath.Join(os.Getenv("HOME"), ".faucet", "keys"), keystore.StandardScryptN, keystore.StandardScryptP)
	// Load up the account keys and decrypt its password
	blob, err := ioutil.ReadFile(*accPassFlag)
	if err != nil {
		log.Crit("Failed to read account password contents", "file", *accPassFlag, "err", err)
	}
	pass := strings.TrimSuffix(string(blob), "\n")

	for i := 0; i < *numChainsFlag; i++ {
		if blob, err = ioutil.ReadFile(chainList[i] + ".json"); err != nil {
			log.Crit("Failed to read account key contents", "file", chainList[i]+".json", "err", err)
		}
		acc, err := ks.Import(blob, pass, pass)
		if err != nil && err != keystore.ErrAccountAlreadyExists {
			log.Crit("Failed to import faucet signer account", "err", err)
		}
		if err := ks.Unlock(acc, pass); err != nil {
			log.Crit("Failed to unlock faucet signer account", "err", err)
		}
	}

	// Assemble and start the faucet light service
	faucet, err := newFaucet(genesis, *ethPortFlag, enodes /* *netFlag */, zone.ChainID.Uint64(), *statsFlag, *nodeFlag, ks, website.Bytes())
	if err != nil {
		log.Crit("Failed to start faucet", "err", err)
	}
	defer faucet.close()

	if err := faucet.listenAndServe(*apiPortFlag); err != nil {
		log.Crit("Failed to launch faucet API", "err", err)
	}

}

// request represents an accepted funding request.
type request struct {
	Avatar  string             `json:"avatar"`  // Avatar URL to make the UI nicer
	Account common.Address     `json:"account"` // Ethereum address being funded
	Time    time.Time          `json:"time"`    // Timestamp when the request was accepted
	Tx      *types.Transaction `json:"tx"`      // Transaction funding the account
}

// faucet represents a crypto faucet backed by an Ethereum light client.
type faucet struct {
	//config  *params.ChainConfig // Chain configurations for signing
	stack   *node.Node          // Ethereum protocol stack
	clients []*ethclient.Client // Client connection to the Ethereum chain
	index   []byte              // Index page to serve up on the web

	keystore *keystore.KeyStore // Keystore containing the single signer
	//account  accounts.Account   // Account funding user faucet requests
	head    []*types.Header // Current head header of the faucet
	balance []*big.Int      // Current balance of the faucet
	nonce   []uint64        // Current pending nonce of the faucet
	price   *big.Int        // Current gas price to issue funds with

	conns    []*wsConn            // Currently live websocket connections
	timeouts map[string]time.Time // History of users and their funding timeouts
	reqs     [][]*request         // Currently pending funding requests
	update   chan struct{}        // Channel to signal request updates

	lock deadlock.RWMutex // Lock protecting the faucet's internals
}

// wsConn wraps a websocket connection with a write mutex as the underlying
// websocket library does not synchronize access to the stream.
type wsConn struct {
	conn  *websocket.Conn
	wlock deadlock.Mutex
}

func newFaucet(genesis *core.Genesis, port int, enodes []*enode.Node, network uint64, stats string, nodeAddr string, ks *keystore.KeyStore, index []byte) (*faucet, error) {
	// Assemble the raw devp2p protocol stack
	stack, err := node.New(&node.Config{
		Name:    "geth",
		Version: params.VersionWithCommit(gitCommit, gitDate),
		DataDir: filepath.Join(os.Getenv("HOME"), ".faucet"),
		P2P: p2p.Config{
			NAT:              nat.Any(),
			NoDiscovery:      true,
			DiscoveryV5:      true,
			ListenAddr:       fmt.Sprintf(":%d", port),
			MaxPeers:         25,
			BootstrapNodesV5: enodes,
		},
	})
	if err != nil {
		return nil, err
	}

	// Assemble the Ethereum light client protocol
	cfg := ethconfig.Defaults
	cfg.SyncMode = downloader.FastSync
	cfg.NetworkId = network
	cfg.Genesis = genesis
	utils.SetDNSDiscoveryDefaults(&cfg, genesis.ToBlock(nil).Hash())

	/*lesBackend, err := les.New(stack, &cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to register the Ethereum service: %w", err)
	}

	// Assemble the ethstats monitoring and reporting service'
	if stats != "" {
		if err := ethstats.New(stack, lesBackend.ApiBackend, lesBackend.Engine(), stats); err != nil {
			return nil, err
		}
	}
	// Boot up the client and ensure it connects to bootnodes
	if err := stack.Start(); err != nil {
		return nil, err
	}
	for _, boot := range enodes {
		old, err := enode.Parse(enode.ValidSchemes, boot.String())
		if err == nil {
			stack.Server().AddPeer(old)
		}
	}
	// Attach to the client and retrieve and interesting metadatas
	api, err := stack.Attach()
	if err != nil {
		stack.Close()
		return nil, err
	}
	client := ethclient.NewClient(api)*/

	var clients []*ethclient.Client

	for i := 0; i < *numChainsFlag; i++ {
		client, err := ethclient.Dial(nodeAddr + ":" + portList[i])
		if err != nil {
			stack.Close()
			return nil, err
		}
		clients = append(clients, client)
	}

	return &faucet{
		//config:   genesis.Config,
		stack:    stack,
		clients:  clients,
		index:    index,
		keystore: ks,
		//account:  ks.Accounts()[0], // this is probably bad
		balance:  make([]*big.Int, *numChainsFlag),
		nonce:    make([]uint64, *numChainsFlag),
		head:     make([]*types.Header, *numChainsFlag),
		reqs:     make([][]*request, *numChainsFlag),
		timeouts: make(map[string]time.Time),
		update:   make(chan struct{}, 1),
	}, nil
}

// close terminates the Ethereum connection and tears down the faucet.
func (f *faucet) close() error {
	return f.stack.Close()
}

// listenAndServe registers the HTTP handlers for the faucet and boots it up
// for service user funding requests.
func (f *faucet) listenAndServe(port int) error {
	for i := 0; i < *numChainsFlag; i++ {
		go f.loop(i)
	}
	http.HandleFunc("/", f.webHandler)
	http.HandleFunc("/api", f.apiHandler)
	log.Info("Faucet starting on port " + fmt.Sprintf("%d", port))
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}

// webHandler handles all non-api requests, simply flattening and returning the
// faucet website.
func (f *faucet) webHandler(w http.ResponseWriter, r *http.Request) {
	w.Write(f.index)
}

// apiHandler handles requests for Ether grants and transaction statuses.
func (f *faucet) apiHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn("Error with connection", "err", err)
		return
	}

	// Start tracking the connection and drop at the end
	defer conn.Close()

	f.lock.Lock()
	wsconn := &wsConn{conn: conn}
	f.conns = append(f.conns, wsconn)
	f.lock.Unlock()

	defer func() {
		f.lock.Lock()
		for i, c := range f.conns {
			if c.conn == conn {
				f.conns = append(f.conns[:i], f.conns[i+1:]...)
				break
			}
		}
		f.lock.Unlock()
	}()
	// Gather the initial stats from the network to report
	var (
		head    *types.Header
		balance *big.Int
		nonce   uint64
	)

	for head == nil || balance == nil {
		// Retrieve the current stats cached by the faucet
		f.lock.RLock()
		if f.head != nil {
			head = types.CopyHeader(f.head[2]) // just show cyprus 1 for now. This will break with less than 3 in numchains
		}
		if f.balance != nil {
			balance = new(big.Int).Set(f.balance[2])
		}
		nonce = f.nonce[2]
		f.lock.RUnlock()

		if head == nil || balance == nil {
			// Report the faucet offline until initial stats are ready
			//lint:ignore ST1005 This error is to be displayed in the browser
			if err = sendError(wsconn, errors.New("Faucet offline - cyprus 1")); err != nil {
				log.Warn("Failed to send faucet error to client", "err", err)
				return
			}
			time.Sleep(3 * time.Second)
		}
	}
	// Send over the initial stats and the latest header
	f.lock.RLock()
	reqs := f.reqs
	f.lock.RUnlock()
	if err = send(wsconn, map[string]interface{}{
		"funds":    new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetInt(ether)).Text('f', 3),
		"funded":   nonce,
		"peers":    "cyprus1", //hardcoded, initial state is cyprus1
		"requests": flatten(reqs),
	}, 3*time.Second); err != nil {
		log.Warn("Failed to send initial stats to client", "err", err)
		return
	}
	if err = send(wsconn, head, 3*time.Second); err != nil {
		log.Warn("Failed to send initial header to client", "err", err)
		return
	}
	// Keep reading requests from the websocket until the connection breaks
	for {
		// Fetch the next funding request and validate against github
		var msg struct {
			URL     string `json:"url"`
			Tier    uint   `json:"tier"`
			Captcha string `json:"captcha"`
		}
		if err = conn.ReadJSON(&msg); err != nil {
			log.Warn("Error reading JSON", "err", err)
			return
		}
		if !*noauthFlag && !strings.HasPrefix(msg.URL, "https://twitter.com/") && !strings.HasPrefix(msg.URL, "https://www.facebook.com/") {
			if err = sendError(wsconn, errors.New("URL doesn't link to supported services")); err != nil {
				log.Warn("Failed to send URL error to client", "err", err)
				return
			}
			continue
		}
		if msg.Tier >= uint(*tiersFlag) {
			//lint:ignore ST1005 This error is to be displayed in the browser
			if err = sendError(wsconn, errors.New("Invalid funding tier requested")); err != nil {
				log.Warn("Failed to send tier error to client", "err", err)
				return
			}
			continue
		}
		log.Info("Faucet funds requested", "url", msg.URL, "tier", msg.Tier)

		// If captcha verifications are enabled, make sure we're not dealing with a robot
		if *captchaToken != "" {
			form := url.Values{}
			form.Add("secret", *captchaSecret)
			form.Add("response", msg.Captcha)

			res, err := http.PostForm("https://www.google.com/recaptcha/api/siteverify", form)
			if err != nil {
				if err = sendError(wsconn, err); err != nil {
					log.Warn("Failed to send captcha post error to client", "err", err)
					return
				}
				continue
			}
			var result struct {
				Success bool            `json:"success"`
				Errors  json.RawMessage `json:"error-codes"`
			}
			err = json.NewDecoder(res.Body).Decode(&result)
			res.Body.Close()
			if err != nil {
				if err = sendError(wsconn, err); err != nil {
					log.Warn("Failed to send captcha decode error to client", "err", err)
					return
				}
				continue
			}
			if !result.Success {
				log.Warn("Captcha verification failed", "err", string(result.Errors))
				//lint:ignore ST1005 it's funny and the robot won't mind
				if err = sendError(wsconn, errors.New("Beep-bop, you're a robot!")); err != nil {
					log.Warn("Failed to send captcha failure to client", "err", err)
					return
				}
				continue
			}
		}
		// Retrieve the Ethereum address to fund, the requesting user and a profile picture
		var (
			id       string
			username string
			avatar   string
			address  common.Address
		)
		switch {
		case strings.HasPrefix(msg.URL, "https://twitter.com/"):
			id, username, avatar, address, err = authTwitter(msg.URL, *twitterTokenV1Flag, *twitterTokenFlag)
		case strings.HasPrefix(msg.URL, "https://www.facebook.com/"):
			username, avatar, address, err = authFacebook(msg.URL)
			id = username
		case *noauthFlag:
			username, avatar, address, err = authNoAuth(msg.URL)
			id = username
		default:
			//lint:ignore ST1005 This error is to be displayed in the browser
			err = errors.New("Something funky happened, please open an issue at https://github.com/ethereum/go-ethereum/issues")
		}
		if err != nil {
			if err = sendError(wsconn, err); err != nil {
				log.Warn("Failed to send prefix error to client", "err", err)
				return
			}
			continue
		}
		addrPrefix, err := strconv.ParseUint(address.String()[2:4], 16, 64)
		if err != nil {
			log.Error("Failed to parse address prefix, assuming prime", "err", err)
			addrPrefix = 0
		}
		var client int
		var chainID *big.Int
		switch {
		case 0 <= addrPrefix && addrPrefix <= 9:
			client = 0
			chainID = params.MainnetPrimeChainConfig.ChainID // Prime
		case 10 <= addrPrefix && addrPrefix <= 19:
			client = 1
			chainID = params.MainnetRegionChainConfigs[0].ChainID // Cyprus
		case 20 <= addrPrefix && addrPrefix <= 29:
			client = 2
			chainID = params.MainnetZoneChainConfigs[0][0].ChainID
		case 30 <= addrPrefix && addrPrefix <= 39:
			client = 3
			chainID = params.MainnetZoneChainConfigs[0][1].ChainID
		case 40 <= addrPrefix && addrPrefix <= 49:
			client = 4
			chainID = params.MainnetZoneChainConfigs[0][2].ChainID
		case 50 <= addrPrefix && addrPrefix <= 59:
			client = 5
			chainID = params.MainnetRegionChainConfigs[1].ChainID // Paxos
		case 60 <= addrPrefix && addrPrefix <= 69:
			client = 6
			chainID = params.MainnetZoneChainConfigs[1][0].ChainID
		case 70 <= addrPrefix && addrPrefix <= 79:
			client = 7
			chainID = params.MainnetZoneChainConfigs[1][1].ChainID
		case 80 <= addrPrefix && addrPrefix <= 89:
			client = 8
			chainID = params.MainnetZoneChainConfigs[1][2].ChainID
		case 90 <= addrPrefix && addrPrefix <= 99:
			client = 9
			chainID = params.MainnetRegionChainConfigs[2].ChainID // Hydra
		case 100 <= addrPrefix && addrPrefix <= 109:
			client = 10
			chainID = params.MainnetZoneChainConfigs[2][0].ChainID
		case 110 <= addrPrefix && addrPrefix <= 119:
			client = 11
			chainID = params.MainnetZoneChainConfigs[2][1].ChainID
		case 120 <= addrPrefix && addrPrefix <= 129:
			client = 12
			chainID = params.MainnetZoneChainConfigs[2][2].ChainID
		default:
			client = 0
			chainID = params.MainnetPrimeChainConfig.ChainID // Default is Prime
		}

		log.Info("Faucet request valid", "url", msg.URL, "tier", msg.Tier, "user", username, "address", address, "chain", chainList[client])

		// Ensure the user didn't request funds too recently
		f.lock.Lock()
		var (
			fund    bool
			timeout time.Time
			txhash  common.Hash
		)
		if timeout = f.timeouts[id]; time.Now().After(timeout) {
			// User wasn't funded recently, create the funding transaction
			amount := new(big.Int).Mul(big.NewInt(int64(1 /**payoutFlag*/)), divisor) // We no longer use the payout flag because it is a whole integer
			amount = new(big.Int).Mul(amount, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(msg.Tier)), nil))
			amount = new(big.Int).Div(amount, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(msg.Tier)), nil))

			tx := types.NewTransaction(f.nonce[client]+uint64(len(f.reqs[client])), address, amount, 21000, f.price, nil)
			signed, err := f.keystore.SignTx(f.keystore.Accounts()[client], tx, chainID)
			if err != nil {
				f.lock.Unlock()
				if err = sendError(wsconn, err); err != nil {
					log.Warn("Failed to send transaction creation error to client", "err", err)
					return
				}
				continue
			}
			// Submit the transaction and mark as funded if successful
			if err := f.clients[client].SendTransaction(context.Background(), signed); err != nil {
				if err.Error() == "replacement transaction underpriced" {
					f.nonce[client]++
				}
				f.lock.Unlock()
				if err = sendError(wsconn, err); err != nil {
					log.Warn("Failed to send transaction transmission error to client", "err", err)
					return
				}
				continue
			}
			f.reqs[client] = append(f.reqs[client], &request{
				Avatar:  avatar,
				Account: address,
				Time:    time.Now(),
				Tx:      signed,
			})
			timeout := time.Duration(*minutesFlag*int(math.Pow(3, float64(msg.Tier)))) * time.Minute
			grace := timeout / 288 // 24h timeout => 5m grace

			f.timeouts[id] = time.Now().Add(timeout - grace)
			fund = true
			txhash = signed.Hash()
			log.Info("Transaction sent", "from", f.keystore.Accounts()[client].Address.String(), "to", address.String(), "chain", chainList[client], "hash", txhash.String())
		}
		f.lock.Unlock()

		// Send an error if too frequent funding, othewise a success
		if !fund {
			if err = sendError(wsconn, fmt.Errorf("%s left until next allowance", common.PrettyDuration(time.Until(timeout)))); err != nil { // nolint: gosimple
				log.Warn("Failed to send funding error to client", "err", err)
				return
			}
			continue
		}

		if err = sendSuccess(wsconn, fmt.Sprintf("Funding request accepted from chain %s to address %s, txhash: %s", chainList[client], address.Hex(), txhash.String())); err != nil {
			log.Warn("Failed to send funding success to client", "err", err)
			return
		}
		select {
		case f.update <- struct{}{}:
		default:
		}
	}
}

// refresh attempts to retrieve the latest header from the chain and extract the
// associated faucet balance and nonce for connectivity caching.
func (f *faucet) refresh(head *types.Header, client int) error {
	// Ensure a state update does not run for too long
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// If no header was specified, use the current chain head
	var err error
	if head == nil {
		if head, err = f.clients[client].HeaderByNumber(ctx, nil); err != nil {
			return err
		}
	}
	// Retrieve the balance, nonce and gas price from the current head
	var (
		balance *big.Int
		nonce   uint64
		price   *big.Int
	)
	if balance, err = f.clients[client].BalanceAt(ctx, f.keystore.Accounts()[client].Address, head.Number[types.QuaiNetworkContext]); err != nil {
		return err
	}
	if nonce, err = f.clients[client].NonceAt(ctx, f.keystore.Accounts()[client].Address, head.Number[types.QuaiNetworkContext]); err != nil {
		return err
	}
	/*if price, err = f.clients[client].SuggestGasPrice(ctx); err != nil {
		return err
	}*/
	price = big.NewInt(1) // hardcoded gas price for now
	// Everything succeeded, update the cached stats and eject old requests
	f.lock.Lock()
	f.head[client], f.balance[client] = head, balance
	f.price, f.nonce[client] = price, nonce
	for len(f.reqs[client]) > 0 && f.reqs[client][0].Tx.Nonce() < nonce {
		f.reqs[client] = f.reqs[client][1:]
	}
	f.lock.Unlock()

	return nil
}

// loop keeps waiting for interesting events and pushes them out to connected
// websockets.
func (f *faucet) loop(client int) {
	// Wait for chain events and push them to clients
	heads := make(chan *types.Header, 16)
	sub, err := f.clients[client].SubscribeNewHead(context.Background(), heads)
	if err != nil {
		log.Crit("Failed to subscribe to head events", "err", err)
	}
	defer sub.Unsubscribe()

	// Start a goroutine to update the state from head notifications in the background
	update := make(chan *types.Header)

	go func() {
		for head := range update {
			// New chain head arrived, query the current stats and stream to clients
			timestamp := time.Unix(int64(head.Time), 0)
			if time.Since(timestamp) > time.Hour*24 {
				log.Warn("Skipping faucet refresh, head too old", "number", head.Number, "hash", head.Hash(), "age", common.PrettyAge(timestamp))
				continue
			}
			if err := f.refresh(head, client); err != nil {
				log.Warn("Failed to update faucet state", "block", head.Number, "hash", head.Hash(), "err", err)
				continue
			}
			// Faucet state retrieved, update locally and send to clients
			f.lock.RLock()

			balance := new(big.Float).Quo(new(big.Float).SetInt(f.balance[client]), new(big.Float).SetInt(ether)).Text('f', 3) // big int to floating point (decimal) to string

			log.Info("Updated faucet state", "number", head.Number, "hash", head.Hash(), "age", common.PrettyAge(timestamp), "balance", balance, "nonce", f.nonce, "price", f.price)

			for _, conn := range f.conns {
				if err := send(conn, map[string]interface{}{
					"funds":    balance,
					"funded":   f.nonce,
					"peers":    chainList[client],
					"requests": flatten(f.reqs),
				}, time.Second); err != nil {
					log.Warn("Failed to send stats to client", "err", err)
					conn.conn.Close()
					continue
				}
				if err := send(conn, head, time.Second); err != nil {
					log.Warn("Failed to send header to client", "err", err)
					conn.conn.Close()
				}
			}
			f.lock.RUnlock()
		}
	}()
	head, err := f.clients[client].HeaderByNumber(context.Background(), nil)
	if err != nil {
		log.Warn("Failed to get latest header", "err", err)
	} else {
		update <- head
	}
	// Wait for various events and assing to the appropriate background threads
	for {
		select {
		case head := <-heads:
			// New head arrived, send if for state update if there's none running
			select {
			case update <- head:
			default:
			}

		case <-f.update:
			// Pending requests updated, stream to clients
			f.lock.RLock()
			for _, conn := range f.conns {
				if err := send(conn, map[string]interface{}{"requests": flatten(f.reqs)}, time.Second); err != nil {
					log.Warn("Failed to send requests to client", "err", err)
					conn.conn.Close()
				}
			}
			f.lock.RUnlock()
		}
	}
}

// sends transmits a data packet to the remote end of the websocket, but also
// setting a write deadline to prevent waiting forever on the node.
func send(conn *wsConn, value interface{}, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	conn.wlock.Lock()
	defer conn.wlock.Unlock()
	conn.conn.SetWriteDeadline(time.Now().Add(timeout))
	return conn.conn.WriteJSON(value)
}

// sendError transmits an error to the remote end of the websocket, also setting
// the write deadline to 1 second to prevent waiting forever.
func sendError(conn *wsConn, err error) error {
	return send(conn, map[string]string{"error": err.Error()}, time.Second)
}

// sendSuccess transmits a success message to the remote end of the websocket, also
// setting the write deadline to 1 second to prevent waiting forever.
func sendSuccess(conn *wsConn, msg string) error {
	return send(conn, map[string]string{"success": msg}, time.Second)
}

// authTwitter tries to authenticate a faucet request using Twitter posts, returning
// the uniqueness identifier (user id/username), username, avatar URL and Ethereum address to fund on success.
func authTwitter(url string, tokenV1, tokenV2 string) (string, string, string, common.Address, error) {
	// Ensure the user specified a meaningful URL, no fancy nonsense
	parts := strings.Split(url, "/")
	if len(parts) < 4 || parts[len(parts)-2] != "status" {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", "", common.Address{}, errors.New("Invalid Twitter status URL")
	}
	// Strip any query parameters from the tweet id and ensure it's numeric
	tweetID := strings.Split(parts[len(parts)-1], "?")[0]
	if !regexp.MustCompile("^[0-9]+$").MatchString(tweetID) {
		return "", "", "", common.Address{}, errors.New("Invalid Tweet URL")
	}
	// Twitter's API isn't really friendly with direct links.
	// It is restricted to 300 queries / 15 minute with an app api key.
	// Anything more will require read only authorization from the users and that we want to avoid.

	// If Twitter bearer token is provided, use the API, selecting the version
	// the user would prefer (currently there's a limit of 1 v2 app / developer
	// but unlimited v1.1 apps).
	switch {
	case tokenV1 != "":
		return authTwitterWithTokenV1(tweetID, tokenV1)
	case tokenV2 != "":
		return authTwitterWithTokenV2(tweetID, tokenV2)
	}
	// Twiter API token isn't provided so we just load the public posts
	// and scrape it for the Ethereum address and profile URL. We need to load
	// the mobile page though since the main page loads tweet contents via JS.
	url = strings.Replace(url, "https://twitter.com/", "https://mobile.twitter.com/", 1)

	res, err := http.Get(url)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	defer res.Body.Close()

	// Resolve the username from the final redirect, no intermediate junk
	parts = strings.Split(res.Request.URL.String(), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "status" {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", "", common.Address{}, errors.New("Invalid Twitter status URL")
	}
	username := parts[len(parts)-3]

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	address := common.HexToAddress(string(regexp.MustCompile("0x[0-9a-fA-F]{40}").Find(body)))
	if address == (common.Address{}) {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", "", common.Address{}, errors.New("No Ethereum address found to fund")
	}
	var avatar string
	if parts = regexp.MustCompile("src=\"([^\"]+twimg.com/profile_images[^\"]+)\"").FindStringSubmatch(string(body)); len(parts) == 2 {
		avatar = parts[1]
	}
	return username + "@twitter", username, avatar, address, nil
}

// authTwitterWithTokenV1 tries to authenticate a faucet request using Twitter's v1
// API, returning the user id, username, avatar URL and Ethereum address to fund on
// success.
func authTwitterWithTokenV1(tweetID string, token string) (string, string, string, common.Address, error) {
	// Query the tweet details from Twitter
	url := fmt.Sprintf("https://api.twitter.com/1.1/statuses/show.json?id=%s", tweetID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	defer res.Body.Close()

	var result struct {
		Text string `json:"text"`
		User struct {
			ID       string `json:"id_str"`
			Username string `json:"screen_name"`
			Avatar   string `json:"profile_image_url"`
		} `json:"user"`
	}
	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	address := common.HexToAddress(regexp.MustCompile("0x[0-9a-fA-F]{40}").FindString(result.Text))
	if address == (common.Address{}) {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", "", common.Address{}, errors.New("No Ethereum address found to fund")
	}
	return result.User.ID + "@twitter", result.User.Username, result.User.Avatar, address, nil
}

// authTwitterWithTokenV2 tries to authenticate a faucet request using Twitter's v2
// API, returning the user id, username, avatar URL and Ethereum address to fund on
// success.
func authTwitterWithTokenV2(tweetID string, token string) (string, string, string, common.Address, error) {
	// Query the tweet details from Twitter
	url := fmt.Sprintf("https://api.twitter.com/2/tweets/%s?expansions=author_id&user.fields=profile_image_url", tweetID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", common.Address{}, err
	}
	defer res.Body.Close()

	var result struct {
		Data struct {
			AuthorID string `json:"author_id"`
			Text     string `json:"text"`
		} `json:"data"`
		Includes struct {
			Users []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
				Avatar   string `json:"profile_image_url"`
			} `json:"users"`
		} `json:"includes"`
	}

	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		return "", "", "", common.Address{}, err
	}

	address := common.HexToAddress(regexp.MustCompile("0x[0-9a-fA-F]{40}").FindString(result.Data.Text))
	if address == (common.Address{}) {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", "", common.Address{}, errors.New("No Ethereum address found to fund")
	}
	return result.Data.AuthorID + "@twitter", result.Includes.Users[0].Username, result.Includes.Users[0].Avatar, address, nil
}

// authFacebook tries to authenticate a faucet request using Facebook posts,
// returning the username, avatar URL and Ethereum address to fund on success.
func authFacebook(url string) (string, string, common.Address, error) {
	// Ensure the user specified a meaningful URL, no fancy nonsense
	parts := strings.Split(strings.Split(url, "?")[0], "/")
	if parts[len(parts)-1] == "" {
		parts = parts[0 : len(parts)-1]
	}
	if len(parts) < 4 || parts[len(parts)-2] != "posts" {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", common.Address{}, errors.New("Invalid Facebook post URL")
	}
	username := parts[len(parts)-3]

	// Facebook's Graph API isn't really friendly with direct links. Still, we don't
	// want to do ask read permissions from users, so just load the public posts and
	// scrape it for the Ethereum address and profile URL.
	//
	// Facebook recently changed their desktop webpage to use AJAX for loading post
	// content, so switch over to the mobile site for now. Will probably end up having
	// to use the API eventually.
	crawl := strings.Replace(url, "www.facebook.com", "m.facebook.com", 1)

	res, err := http.Get(crawl)
	if err != nil {
		return "", "", common.Address{}, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", "", common.Address{}, err
	}
	address := common.HexToAddress(string(regexp.MustCompile("0x[0-9a-fA-F]{40}").Find(body)))
	if address == (common.Address{}) {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", common.Address{}, errors.New("No Ethereum address found to fund")
	}
	var avatar string
	if parts = regexp.MustCompile("src=\"([^\"]+fbcdn.net[^\"]+)\"").FindStringSubmatch(string(body)); len(parts) == 2 {
		avatar = parts[1]
	}
	return username + "@facebook", avatar, address, nil
}

// authNoAuth tries to interpret a faucet request as a plain Ethereum address,
// without actually performing any remote authentication. This mode is prone to
// Byzantine attack, so only ever use for truly private networks.
func authNoAuth(url string) (string, string, common.Address, error) {
	address := common.HexToAddress(regexp.MustCompile("0x[0-9a-fA-F]{40}").FindString(url))
	if address == (common.Address{}) {
		//lint:ignore ST1005 This error is to be displayed in the browser
		return "", "", common.Address{}, errors.New("No Ethereum address found to fund")
	}
	return address.Hex() + "@noauth", "", address, nil
}

// getGenesis returns a genesis based on input args
func getGenesis(genesisFlag *string, goerliFlag bool, rinkebyFlag bool) (*core.Genesis, error) {
	switch {
	case genesisFlag != nil:
		var genesis core.Genesis
		err := common.LoadJSON(*genesisFlag, &genesis)
		return &genesis, err
	default:
		return nil, fmt.Errorf("no genesis flag provided")
	}
}

func flatten(reqs [][]*request) []*request {
	var flattenedReqs []*request
	for _, array := range reqs {
		for _, request := range array {
			flattenedReqs = append(flattenedReqs, request)
		}
	}
	return flattenedReqs
}
