package peerManager

import (
	"context"
	"maps"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

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

	// The number of peers to return when querying for peers
	C_peerCount = 3
)

type PeerQuality int

const (
	Best PeerQuality = iota
	Responsive
	LastResort
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

	// Returns c_peerCount peers starting at the requested quality level of peers
	// If there are not enough peers at the requested quality, it will return lower quality peers
	// If there still aren't enough peers, it will query the DHT for more
	GetPeers(topic *pubsubManager.Topic, quality PeerQuality) map[p2p.PeerID]struct{}

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

	// Initialize the expected peerDBs
	peerDBs := make(map[string][]*peerdb.PeerDB)

	generateLocations := func() []common.Location {
		locations := make([]common.Location, 9)
		for region := byte(0); region <= 2; region++ {
			for zone := byte(0); zone <= 2; zone++ {
				locations = append(locations, common.Location{region, zone})
			}
		}
		return locations
	}

	var dataTypes = []interface{}{
		&types.WorkObjectHeaderView{},
		&types.WorkObjectBlockView{},
		common.Hash{},
		&types.Transactions{},
	}

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

	ctx, cancel := context.WithCancel(ctx)

	logger := log.NewLogger("peers.log", viper.GetString(utils.PeersLogLevelFlag.Name))

	go func() {
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
		peerDBs:              peerDBs,
		logger:               logger,
	}, nil
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
	peerSubset := make(map[p2p.PeerID]struct{}, C_peerCount)
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
			return nil
		}
		peerSubset[peerID] = struct{}{}
	}

	return peerSubset
}

func (pm *BasicPeerManager) GetPeers(topic *pubsubManager.Topic, quality PeerQuality) map[p2p.PeerID]struct{} {
	var peerList map[p2p.PeerID]struct{}
	switch quality {
	case Best:
		peerList = pm.getBestPeersWithFallback(topic)
	case Responsive:
		peerList = pm.getResponsivePeersWithFallback(topic)
	case LastResort:
		peerList = pm.getLastResortPeers(topic)
	default:
		panic("Invalid peer quality")
	}

	lenPeer := len(peerList)
	if lenPeer >= C_peerCount {
		// Found sufficient number of peers
		return peerList
	}

	// Query the DHT for more peers
	return pm.queryDHT(topic, peerList, C_peerCount-lenPeer)
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

func (pm *BasicPeerManager) getBestPeersWithFallback(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	if pm.peerDBs[topic.String()] == nil {
		// There have not been any peers added to this topic
		return make(map[peer.ID]struct{}, C_peerCount)
	}

	bestPeersCount := pm.peerDBs[topic.String()][Best].GetPeerCount()
	if bestPeersCount < C_peerCount {
		bestPeerList := pm.getPeersHelper(pm.peerDBs[topic.String()][Best], bestPeersCount)
		maps.Copy(bestPeerList, pm.getResponsivePeersWithFallback(topic))
		return bestPeerList
	}
	return pm.getPeersHelper(pm.peerDBs[topic.String()][Best], C_peerCount)
}

func (pm *BasicPeerManager) getResponsivePeersWithFallback(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	responsivePeersCount := pm.peerDBs[topic.String()][Responsive].GetPeerCount()
	if responsivePeersCount < C_peerCount {
		responsivePeerList := pm.getPeersHelper(pm.peerDBs[topic.String()][Responsive], responsivePeersCount)
		maps.Copy(responsivePeerList, pm.getLastResortPeers(topic))

		return responsivePeerList
	}
	return pm.getPeersHelper(pm.peerDBs[topic.String()][Responsive], C_peerCount)

}

func (pm *BasicPeerManager) getLastResortPeers(topic *pubsubManager.Topic) map[p2p.PeerID]struct{} {
	return pm.getPeersHelper(pm.peerDBs[topic.String()][LastResort], C_peerCount)
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
