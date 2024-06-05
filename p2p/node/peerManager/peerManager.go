package peerManager

import (
	"context"
	"maps"
	"math/rand"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/multiformats/go-multiaddr"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"google.golang.org/protobuf/proto"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/node/peerManager/peerdb"
	"github.com/dominant-strategies/go-quai/p2p/node/pubsubManager"
	"github.com/dominant-strategies/go-quai/p2p/node/streamManager"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
	"github.com/dominant-strategies/go-quai/params"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"

	"github.com/libp2p/go-libp2p-kad-dht/dual"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	basicConnGater "github.com/libp2p/go-libp2p/p2p/net/conngater"
	basicConnMgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"
)

const (
	// Represents the minimum ratio of positive to negative reports
	// 	e.g. live_reports / latent_reports = 0.8
	c_qualityThreshold = 0.8

	// c_minBestPeersFromDb is the number of peers we want to randomly read from the db
	c_minBestPeersFromDb = 6
	// c_minResponsiveBestPeersFromDb is the number of peers we want to randomly read from the db
	c_minResponsivePeersFromDb = 3
	// c_minLastResortPeersFromDb is the number of peers we want to randomly read from the db
	c_minLastResortPeersFromDb = 1

	// c_maxBootNodes is the maximum number of bootnodes to connect to when bootstrapping
	c_maxBootNodes = 10
)

type PeerQuality int

const (
	Best PeerQuality = iota
	Responsive
	LastResort
	All
)

var (
	dbNames = [3]string{"bestPeersDB", "responsivePeersDB", "lastResortPeersDB"}
)

// PeerManager is an interface that extends libp2p Connection Manager and Gater
type PeerManager interface {
	connmgr.ConnManager
	connmgr.ConnectionGater
	streamManager.StreamManager

	BlockAddr(ip net.IP) error
	BlockPeer(p peer.ID) error
	BlockSubnet(ipnet *net.IPNet) error
	ListBlockedAddrs() []net.IP
	ListBlockedPeers() []peer.ID
	ListBlockedSubnets() []*net.IPNet
	UnblockAddr(ip net.IP) error
	UnblockPeer(p peer.ID) error
	UnblockSubnet(ipnet *net.IPNet) error

	// Sets the ID for the node running the peer manager
	SetSelfID(p2p.PeerID)
	GetSelfID() p2p.PeerID

	// Sets the DHT provided from the Host interface
	SetDHT(*dual.DHT)

	// Sets the streamManager interface
	SetStreamManager(streamManager.StreamManager)

	// Announces to the DHT that we are providing this data
	Provide(context.Context, common.Location, interface{}) error

	// Removes a peer from all the quality buckets
	RemovePeer(p2p.PeerID) error

	// GetPeers gets randomized set of peers from the database based on the
	// request degree of the topic
	GetPeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{}

	// RefreshBootpeers returns all the current bootpeers for bootstrapping
	RefreshBootpeers() []peer.AddrInfo

	// Increases the peer's liveliness score
	MarkLivelyPeer(peerID p2p.PeerID, topic *pubsubManager.Topic)
	// Decreases the peer's liveliness score
	MarkLatentPeer(peerID p2p.PeerID, topic *pubsubManager.Topic)

	// Increases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkResponsivePeer(peerID p2p.PeerID, topic *pubsubManager.Topic)
	// Decreases the peer's liveliness score. Not exposed outside of NetworkingAPI
	MarkUnresponsivePeer(peerID p2p.PeerID, topic *pubsubManager.Topic)

	// Protects the peer's connection from being disconnected
	ProtectPeer(p2p.PeerID)
	// Remove protection from the peer's connection
	UnprotectPeer(p2p.PeerID)
	// Bans the peer's connection from being re-established
	BanPeer(p2p.PeerID)

	// Stops the peer manager
	Stop() error
}

type BasicPeerManager struct {
	*basicConnGater.BasicConnectionGater
	*basicConnMgr.BasicConnMgr

	// Stream management interface instance
	streamManager streamManager.StreamManager

	// Tracks peers in different quality buckets
	peerDBs map[string][]*peerdb.PeerDB

	// Initial bootpeers passed via config
	bootpeers []peer.AddrInfo

	// DHT instance
	dht *dual.DHT

	// This peer's ID to distinguish self-broadcasts
	selfID p2p.PeerID

	// Genesis hash to append to topics
	genesis common.Hash

	ctx    context.Context
	cancel context.CancelFunc
	logger *log.Logger
}

func NewManager(ctx context.Context, low int, high int, datastore datastore.Datastore) (PeerManager, error) {
	mgr, err := basicConnMgr.NewConnManager(low, high)
	if err != nil {
		return nil, err
	}

	gater, err := basicConnGater.NewBasicConnectionGater(datastore)
	if err != nil {
		return nil, err
	}

	bootpeers, err := loadBootPeers()
	if err != nil {
		return nil, err
	}

	peerDBs, err := loadPeerDBs()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	logger := log.NewLogger("peers.log", viper.GetString(utils.PeersLogLevelFlag.Name))

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		q := query.Query{}
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Info("Reporting peer stats")
				for locName, location := range peerDBs {
					locPeer := []log.Fields{}
					for dbName, db := range location {
						results, err := db.Query(ctx, q)
						if err != nil {
							logger.Errorf("Error querying peerDB: %s", err)
							continue
						}
						for result := range results.Next() {
							peerID, err := peer.Decode(strings.TrimPrefix(result.Key, "/"))
							if err != nil {
								logger.Errorf("Error decoding peer ID: %s", err)
								continue
							}
							peerInfo := &peerdb.ProtoPeerInfo{}
							err = proto.Unmarshal(result.Value, peerInfo)
							if err != nil {
								logger.Errorf("Error unmarshaling peer info: %s", err)
								continue
							}
							locPeer = append(locPeer, log.Fields{
								"peerID": peerID,
								"info":   peerInfo.String(),
							})
						}
						logger.WithFields(log.Fields{
							"location":  locName,
							"peerCount": len(locPeer),
							"bucket":    dbNames[dbName],
							"peers":     locPeer}).Info("Peer stats")
					}
				}
			}
		}
	}()

	return &BasicPeerManager{
		ctx:                  ctx,
		cancel:               cancel,
		BasicConnMgr:         mgr,
		BasicConnectionGater: gater,
		genesis:              utils.MakeGenesis().ToBlock(0).Hash(),
		bootpeers:            bootpeers,
		peerDBs:              peerDBs,
		logger:               logger,
	}, nil
}

func loadPeerDBs() (map[string][]*peerdb.PeerDB, error) {
	var dataTypes = []interface{}{
		&types.WorkObjectHeaderView{},
		&types.WorkObjectBlockView{},
		&types.WorkObjectShareView{},
		common.Hash{},
	}

	generateLocations := func() []common.Location {
		locations := make([]common.Location, 9)
		for region := byte(0); region <= 2; region++ {
			for zone := byte(0); zone <= 2; zone++ {
				locations = append(locations, common.Location{region, zone})
			}
		}
		return locations
	}

	// Initialize the expected peerDBs
	peerDBs := make(map[string][]*peerdb.PeerDB)

	for _, loc := range generateLocations() {
		domLocations := loc.GetDoms()
		for _, domLoc := range domLocations {
			for _, dataType := range dataTypes {
				topic, err := pubsubManager.NewTopic(utils.MakeGenesis().ToBlock(0).Hash(), domLoc, dataType)
				if err != nil {
					return nil, err
				}
				if peerDBs[topic.String()] != nil {
					// This peerDB has already been initialized
					continue
				}
				peerDBs[topic.String()] = make([]*peerdb.PeerDB, 3)
				peerDBs[topic.String()][Best], err = peerdb.NewPeerDB(dbNames[Best], topic.String())
				if err != nil {
					return nil, err
				}
				peerDBs[topic.String()][Responsive], err = peerdb.NewPeerDB(dbNames[Responsive], topic.String())
				if err != nil {
					return nil, err
				}
				peerDBs[topic.String()][LastResort], err = peerdb.NewPeerDB(dbNames[LastResort], topic.String())
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return peerDBs, nil
}

// Loads bootpeers addresses from the config and returns a list of peer.AddrInfo
func loadBootPeers() ([]peer.AddrInfo, error) {
	if viper.GetBool(utils.SoloFlag.Name) || viper.GetString(utils.EnvironmentFlag.Name) == params.LocalName {
		return nil, nil
	}

	var bootpeers []peer.AddrInfo
	for _, p := range viper.GetStringSlice(utils.BootPeersFlag.Name) {
		addr, err := multiaddr.NewMultiaddr(p)
		if err != nil {
			return nil, err
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, err
		}
		bootpeers = append(bootpeers, *info)
	}

	return bootpeers, nil
}

func queryAllPeers(peerDBs map[string][]*peerdb.PeerDB, quality PeerQuality, peerCount int) ([]peer.AddrInfo, error) {
	q := query.Query{}

	bootpeers := make(map[peer.ID]struct{})
	for _, dbList := range peerDBs {
		if quality != All {
			dbList = []*peerdb.PeerDB{dbList[quality]}
		}
		for _, db := range dbList {
			results, err := db.Query(context.Background(), q)
			if err != nil {
				return nil, err
			}
			for result := range results.Next() {
				peerID, err := peer.Decode(strings.TrimPrefix(result.Key, "/"))
				if err != nil {
					// If there is an error, move to the next peer
					continue
				}
				bootpeers[peerID] = struct{}{}
				if len(bootpeers) == peerCount {
					break
				}
			}
		}
	}
	peerList := make([]peer.AddrInfo, 0, len(bootpeers))
	for peerID := range bootpeers {
		peerList = append(peerList, peer.AddrInfo{ID: peerID})
	}
	return peerList, nil
}

func (pm *BasicPeerManager) RefreshBootpeers() []peer.AddrInfo {
	bootpeers, _ := queryAllPeers(pm.peerDBs, Best, c_maxBootNodes)
	bootpeers = append(bootpeers, pm.bootpeers...)
	return bootpeers
}

func (pm *BasicPeerManager) SetDHT(dht *dual.DHT) {
	pm.dht = dht
}

func (pm *BasicPeerManager) Provide(ctx context.Context, location common.Location, data interface{}) error {
	t, err := pubsubManager.NewTopic(pm.genesis, location, data)
	if err != nil {
		return err
	}
	return pm.dht.Provide(ctx, pubsubManager.TopicToCid(t), true)
}

func (pm *BasicPeerManager) RemovePeer(peerID p2p.PeerID) error {
	err := pm.removePeerFromAllDBs(peerID)
	if err != nil {
		return err
	}
	return pm.streamManager.CloseStream(peerID)
}

// Removes peer from the bucket it is in. Does not return an error if the peer is not found
func (pm *BasicPeerManager) removePeerFromAllDBs(peerID p2p.PeerID) error {
	for topic := range pm.peerDBs {
		err := pm.removePeerFromTopic(peerID, topic)
		if err != nil {
			return err
		}
	}
	return nil
}

// Removes peer from the bucket it is in. Does not return an error if the peer is not found
func (pm *BasicPeerManager) removePeerFromTopic(peerID p2p.PeerID, topicStr string) error {
	key := datastore.NewKey(peerID.String())
	for _, db := range pm.peerDBs[topicStr] {
		if exists, _ := db.Has(pm.ctx, key); exists {
			return db.Delete(pm.ctx, key)
		}
	}
	return nil
}

func (pm *BasicPeerManager) SetSelfID(selfID p2p.PeerID) {
	pm.selfID = selfID
}

func (pm *BasicPeerManager) GetSelfID() p2p.PeerID {
	return pm.selfID
}

func (pm *BasicPeerManager) getPeersHelper(peerDB *peerdb.PeerDB, numPeers int) map[p2p.PeerID]struct{} {
	peerSubset := make(map[p2p.PeerID]struct{})
	q := query.Query{
		Limit: numPeers,
	}
	results, err := peerDB.Query(pm.ctx, q)
	if err != nil {
		return nil
	}

	for result := range results.Next() {
		peerID, err := peer.Decode(strings.TrimPrefix(result.Key, "/"))
		if err != nil {
			// If there is an error, move to the next peer
			continue
		}
		peerSubset[peerID] = struct{}{}
	}

	return peerSubset
}

func (pm *BasicPeerManager) GetPeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	if pm.peerDBs[topic.String()] == nil {
		// There have not been any peers added to this topic
		return make(map[peer.ID]struct{})
	}
	peerList := make(map[p2p.PeerID]struct{})
	// Add best peers into the peerList
	bestPeers := pm.getBestPeers(topic)
	maps.Copy(peerList, bestPeers)
	// Add responsive peers into the peerList
	responsivePeers := pm.getResponsivePeers(topic)
	maps.Copy(peerList, responsivePeers)
	// Add last resort peers into the peerList
	lastResortPeers := pm.getLastResortPeers(topic)
	maps.Copy(peerList, lastResortPeers)

	// Randomly select request degree number of peers from the peerList
	lenPeer := len(peerList)
	// If we have more peers than the request degree, randomly select peers and
	// return, otherwise ask the dht for the extra required peers
	if lenPeer >= topic.GetRequestDegree() {
		peers := make([]p2p.PeerID, 0)
		for peer, _ := range peerList {
			peers = append(peers, peer)
		}
		rand.Shuffle(len(peers), func(i, j int) {
			peers[i], peers[j] = peers[j], peers[i]
		})

		randomPeers := make(map[p2p.PeerID]struct{})
		for _, peer := range peers[:topic.GetRequestDegree()] {
			randomPeers[peer] = struct{}{}
		}

		return randomPeers
	}

	// Query the DHT for more peers
	return pm.queryDHT(topic, peerList, topic.GetRequestDegree()-lenPeer)
}

func (pm *BasicPeerManager) queryDHT(topic *pubsubManager.Topic, peerList map[p2p.PeerID]struct{}, peerCount int) map[p2p.PeerID]struct{} {
	// create a Cid from the slice location
	shardCid := pubsubManager.TopicToCid(topic)

	// Internal list of peers from the dht
	dhtPeers := make(map[p2p.PeerID]struct{})
	log.Global.Infof("Querying DHT for slice Cid %s", shardCid)
	// query the DHT for peers in the slice
	for peer := range pm.dht.FindProvidersAsync(pm.ctx, shardCid, peerCount) {
		if peer.ID != pm.selfID {
			dhtPeers[peer.ID] = struct{}{}
		}
	}
	log.Global.Info("Found the following peers from the DHT: ", dhtPeers)
	maps.Copy(peerList, dhtPeers)
	return peerList
}

func (pm *BasicPeerManager) getBestPeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	return pm.getPeersHelper(pm.peerDBs[topic.String()][Best], c_minBestPeersFromDb)
}

func (pm *BasicPeerManager) getResponsivePeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	return pm.getPeersHelper(pm.peerDBs[topic.String()][Responsive], c_minResponsivePeersFromDb)
}

func (pm *BasicPeerManager) getLastResortPeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	return pm.getPeersHelper(pm.peerDBs[topic.String()][LastResort], c_minLastResortPeersFromDb)
}

func (pm *BasicPeerManager) MarkLivelyPeer(peer p2p.PeerID, topic *pubsubManager.Topic) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "liveness_reports", 1)
	pm.recategorizePeer(peer, topic)
}

func (pm *BasicPeerManager) MarkLatentPeer(peer p2p.PeerID, topic *pubsubManager.Topic) {
	if peer == pm.selfID {
		return
	}
	pm.TagPeer(peer, "latency_reports", 1)
	pm.recategorizePeer(peer, topic)
}

func (pm *BasicPeerManager) calculatePeerLiveness(peer p2p.PeerID) float64 {
	peerTag := pm.GetTagInfo(peer)
	if peerTag == nil {
		return 0
	}

	liveness := peerTag.Tags["liveness_reports"]
	latents := peerTag.Tags["latency_reports"]
	return float64(liveness) / float64(latents)
}

func (pm *BasicPeerManager) MarkResponsivePeer(peer p2p.PeerID, topic *pubsubManager.Topic) {
	pm.TagPeer(peer, "responses_served", 1)
	pm.recategorizePeer(peer, topic)
}

func (pm *BasicPeerManager) MarkUnresponsivePeer(peer p2p.PeerID, topic *pubsubManager.Topic) {
	pm.TagPeer(peer, "responses_missed", 1)
	pm.recategorizePeer(peer, topic)
}

func (pm *BasicPeerManager) calculatePeerResponsiveness(peer p2p.PeerID) float64 {
	peerTag := pm.GetTagInfo(peer)
	if peerTag == nil {
		return 0
	}
	responses := peerTag.Tags["responses_served"]
	misses := peerTag.Tags["responses_missed"]
	return float64(responses) / float64(misses)
}

// Each peer can only be in one of the following buckets:
// 1. bestPeers
//   - peers with high liveness and responsiveness
//
// 2. responsivePeers
//   - peers with high responsiveness, but low liveness
//
// 3. peers
//   - all other peers
func (pm *BasicPeerManager) recategorizePeer(peerID p2p.PeerID, topic *pubsubManager.Topic) error {
	liveness := pm.calculatePeerLiveness(peerID)
	responsiveness := pm.calculatePeerResponsiveness(peerID)

	// remove peer from DB first
	err := pm.removePeerFromTopic(peerID, topic.String())
	if err != nil {
		return err
	}

	key := datastore.NewKey(peerID.String())
	// TODO: construct peerDB.PeerInfo and marshal it to bytes
	peerInfo, err := proto.Marshal((&peerdb.PeerInfo{
		AddrInfo: peerdb.AddrInfo{
			AddrInfo: peer.AddrInfo{
				ID: peerID,
			},
		},
	}).ProtoEncode())
	if err != nil {
		return errors.Wrap(err, "error marshaling peer info")
	}

	if liveness >= c_qualityThreshold && responsiveness >= c_qualityThreshold {
		// Best peers: high liveness and responsiveness
		err := pm.peerDBs[topic.String()][Best].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in bestPeersDB")
		}

	} else if responsiveness >= c_qualityThreshold {
		// Responsive peers: high responsiveness, but low liveness
		err := pm.peerDBs[topic.String()][Responsive].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in responsivePeersDB")
		}

	} else {
		// All other peers
		err := pm.peerDBs[topic.String()][LastResort].Put(pm.ctx, key, peerInfo)
		if err != nil {
			return errors.Wrap(err, "error putting peer in allPeersDB")
		}
	}

	return nil
}

func (pm *BasicPeerManager) ProtectPeer(peer p2p.PeerID) {
	pm.Protect(peer, "gen_protection")
}

func (pm *BasicPeerManager) UnprotectPeer(peer p2p.PeerID) {
	pm.Unprotect(peer, "gen_protection")
}

func (pm *BasicPeerManager) BanPeer(peer p2p.PeerID) {
	pm.BlockPeer(peer)
}

func (pm *BasicPeerManager) Stop() error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var closeErrors []string

	closeFuncs := []func() error{
		pm.BasicConnMgr.Close,
		pm.dht.Close,
	}

	wg.Add(len(closeFuncs))

	for _, closeFunc := range closeFuncs {
		go func(cf func() error) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Global.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Fatal("Go-Quai Panicked")
				}
			}()
			if err := cf(); err != nil {
				mu.Lock()
				closeErrors = append(closeErrors, err.Error())
				mu.Unlock()
			}
		}(closeFunc)
	}

	for _, db := range pm.peerDBs {
		for _, peerDB := range db {
			wg.Add(1)
			go func(db *peerdb.PeerDB) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						log.Global.WithFields(log.Fields{
							"error":      r,
							"stacktrace": string(debug.Stack()),
						}).Fatal("Go-Quai Panicked")
					}
				}()
				if err := db.Close(); err != nil {
					mu.Lock()
					closeErrors = append(closeErrors, err.Error())
					mu.Unlock()
				}
			}(peerDB)
		}
	}

	wg.Wait()

	if len(closeErrors) > 0 {
		return errors.New("failed to close some resources: " + strings.Join(closeErrors, "; "))
	}

	return nil
}

// Implementation of underlying StreamManager interface
func (pm *BasicPeerManager) SetStreamManager(streamManager streamManager.StreamManager) {
	pm.streamManager = streamManager
}

// Set the host for the stream manager
func (pm *BasicPeerManager) SetP2PBackend(p2pnode protocol.QuaiP2PNode) {
	pm.streamManager.SetP2PBackend(p2pnode)
}

func (pm *BasicPeerManager) GetHost() host.Host {
	return pm.streamManager.GetHost()
}

func (pm *BasicPeerManager) SetHost(host host.Host) {
	pm.streamManager.SetHost(host)
}

func (pm *BasicPeerManager) GetStream(peerID p2p.PeerID) (network.Stream, error) {
	return pm.streamManager.GetStream(peerID)
}

func (pm *BasicPeerManager) CloseStream(peerID p2p.PeerID) error {
	return pm.streamManager.CloseStream(peerID)
}

func (pm *BasicPeerManager) WriteMessageToStream(peerId p2p.PeerID, stream network.Stream, msg []byte) error {
	return pm.streamManager.WriteMessageToStream(peerId, stream, msg)
}
