package quai

import (
	"context"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/metrics_config"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/trie"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

const c_maxTxInWorkShare = 200

var (
	//
	// TxPool propagation metrics
	//
	txPropagationMetrics = metrics_config.NewCounterVec("TxPropagation", "Transaction propagation counter")
	txIngressCounter     = txPropagationMetrics.WithLabelValues("ingress")
	txEgressCounter      = txPropagationMetrics.WithLabelValues("egress")
)

// QuaiBackend implements the quai consensus protocol
type QuaiBackend struct {
	p2pBackend        NetworkingAPI // Interface for all the P2P methods the libp2p exposes to consensus
	primeApiBackend   *quaiapi.Backend
	regionApiBackends []*quaiapi.Backend
	zoneApiBackends   [][]*quaiapi.Backend
}

// Create a new instance of the QuaiBackend consensus service
func NewQuaiBackend() (*QuaiBackend, error) {
	zoneBackends := make([][]*quaiapi.Backend, common.MaxRegions)
	for i := 0; i < common.MaxRegions; i++ {
		zoneBackends[i] = make([]*quaiapi.Backend, common.MaxZones)
	}
	return &QuaiBackend{regionApiBackends: make([]*quaiapi.Backend, common.MaxZones), zoneApiBackends: zoneBackends}, nil
}

// Adds the p2pBackend into the given QuaiBackend
func (qbe *QuaiBackend) SetP2PApiBackend(p2pBackend NetworkingAPI) {
	qbe.p2pBackend = p2pBackend
}

func (qbe *QuaiBackend) SetApiBackend(apiBackend *quaiapi.Backend, location common.Location) {
	switch location.Context() {
	case common.PRIME_CTX:
		qbe.SetPrimeApiBackend(apiBackend)
	case common.REGION_CTX:
		qbe.SetRegionApiBackend(apiBackend, location)
	case common.ZONE_CTX:
		qbe.SetZoneApiBackend(apiBackend, location)
	}
}

// Set the PrimeBackend into the QuaiBackend
func (qbe *QuaiBackend) SetPrimeApiBackend(primeBackend *quaiapi.Backend) {
	qbe.primeApiBackend = primeBackend
}

// Set the RegionBackend into the QuaiBackend
func (qbe *QuaiBackend) SetRegionApiBackend(regionBackend *quaiapi.Backend, location common.Location) {
	qbe.regionApiBackends[location.Region()] = regionBackend
}

// Set the ZoneBackend into the QuaiBackend
func (qbe *QuaiBackend) SetZoneApiBackend(zoneBackend *quaiapi.Backend, location common.Location) {
	qbe.zoneApiBackends[location.Region()][location.Zone()] = zoneBackend
}

func (qbe *QuaiBackend) GetBackend(location common.Location) *quaiapi.Backend {
	switch location.Context() {
	case common.PRIME_CTX:
		return qbe.primeApiBackend
	case common.REGION_CTX:
		return qbe.regionApiBackends[location.Region()]
	case common.ZONE_CTX:
		return qbe.zoneApiBackends[location.Region()][location.Zone()]
	}
	return nil
}

// Handle consensus data propagated to us from our peers
func (qbe *QuaiBackend) OnNewBroadcast(sourcePeer p2p.PeerID, Id string, topic string, data interface{}, nodeLocation common.Location) bool {
	defer types.ObjectPool.Put(data)
	switch data := data.(type) {
	case types.WorkObjectBlockView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		// TODO: Verify the Block before writing it
		// TODO: Determine if the block information was lively or stale and rate
		// the peer accordingly
		backend.WriteBlock(data.WorkObject)
		// If it was a good broadcast, mark the peer as lively
		qbe.p2pBackend.MarkLivelyPeer(sourcePeer, topic)
	case types.WorkObjectHeaderView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		// Only append this in the case of the slice
		if !backend.ProcessingState() && backend.NodeCtx() == common.ZONE_CTX {
			backend.WriteBlock(data.WorkObject)
		}
		// If it was a good broadcast, mark the peer as lively
		qbe.p2pBackend.MarkLivelyPeer(sourcePeer, topic)
	case types.WorkObjectShareView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		txIngressCounter.Add(float64(len(data.WorkObject.Transactions())))
		if backend.ProcessingState() {
			// check if the work share is valid before accepting the transactions
			// from the peer
			if data.WorkObject == nil {
				backend.Logger().Error("work share received from peer has a nil work object")
				return false
			}
			if data.WorkObject.WorkObjectHeader() == nil {
				backend.Logger().Error("work share received from peer has a nil work object header")
				return false
			}
			if ok := backend.CheckIfValidWorkShare(data.WorkObject.WorkObjectHeader()); !ok {
				backend.Logger().Error("work share received from peer is not valid")
				return false
			}
			// check if the txs in the workObject hash to the tx hash in the body header
			if hash := types.DeriveSha(data.WorkObject.Transactions(), trie.NewStackTrie(nil)); hash != data.WorkObject.TxHash() {
				backend.Logger().Error("TxHash doesnt match the hash of the transactions in the work object received from peer")
				return false
			}

			if len(data.WorkObject.Transactions()) > c_maxTxInWorkShare {
				backend.Logger().Error("TxHash doesnt match the hash of the transactions in the work object received from peer")
				return false
			}

			powHash, err := backend.Engine().ComputePowHash(data.WorkObject.WorkObjectHeader())
			if err != nil {
				backend.Logger().Error("Error computing the powHash of the work object header received from peer")
				return false
			}

			backend.Logger().WithFields(log.Fields{"powHash": powHash, "tx count": len(data.WorkObject.Transactions()), "message id": Id}).Info("Received a work share broadcast")
			// Unpack the workobjectheader and the transactions
			backend.SendWorkShare(data.WorkObject.WorkObjectHeader())
			backend.SendRemoteTxs(data.WorkObject.Transactions())
		}
		// If it was a good broadcast, mark the peer as lively
		qbe.p2pBackend.MarkLivelyPeer(sourcePeer, topic)
	default:
		log.Global.WithFields(log.Fields{
			"peer":     sourcePeer,
			"topic":    topic,
			"location": nodeLocation,
		}).Error("received unknown broadcast")
		qbe.p2pBackend.BanPeer(sourcePeer)
		return false
	}
	return true
}

// GetTrieNode returns the TrieNodeResponse for a given hash
func (qbe *QuaiBackend) GetTrieNode(hash common.Hash, location common.Location) *trie.TrieNodeResponse {
	// Example/mock implementation
	panic("todo")
}

// Returns the current block height for the given location
func (qbe *QuaiBackend) GetHeight(location common.Location) uint64 {
	// Example/mock implementation
	panic("todo")
}

func (qbe *QuaiBackend) ValidatorFunc() func(ctx context.Context, id p2p.PeerID, msg *pubsub.Message) pubsub.ValidationResult {
	return func(ctx context.Context, id peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
		var data interface{}
		data = msg.Message.GetData()
		switch data := data.(type) {
		case types.WorkObject:
			backend := *qbe.GetBackend(data.Location())
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer":     id,
					"hash":     data.Hash(),
					"location": data.Location(),
				}).Error("no backend found for this location")
				return pubsub.ValidationReject
			}
		case types.Transaction:
			return pubsub.ValidationAccept
		}
		return pubsub.ValidationAccept
	}
}

// SetCurrentExpansionNumber sets the expansion number into the slice object on all the backends
func (qbe *QuaiBackend) SetCurrentExpansionNumber(expansionNumber uint8) {
	primeBackend := qbe.GetBackend(common.Location{})
	if primeBackend == nil {
		log.Global.Error("no backend found")
		return
	}
	backend := *primeBackend
	backend.SetCurrentExpansionNumber(expansionNumber)

	for i := 0; i < common.MaxRegions; i++ {
		regionBackend := qbe.GetBackend(common.Location{byte(i)})
		if regionBackend != nil {
			backend := *regionBackend
			backend.SetCurrentExpansionNumber(expansionNumber)
		}
	}

	for i := 0; i < common.MaxRegions; i++ {
		for j := 0; j < common.MaxZones; j++ {
			zoneBackend := qbe.GetBackend(common.Location{byte(i), byte(j)})
			if zoneBackend != nil {
				backend := *zoneBackend
				backend.SetCurrentExpansionNumber(expansionNumber)
			}
		}
	}
}

// WriteGenesisBlock adds the genesis block to the database and also writes the block to the disk
func (qbe *QuaiBackend) WriteGenesisBlock(block *types.WorkObject, location common.Location) {
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return
	}
	backend.WriteGenesisBlock(block, location)
}

// SetSubInterface sets the sub interface for the given subLocation
func (qbe *QuaiBackend) SetSubInterface(subInterface core.CoreBackend, nodeLocation common.Location, subLocation common.Location) {
	backend := *qbe.GetBackend(nodeLocation)
	if backend == nil {
		log.Global.Error("no backend found")
		return
	}
	backend.SetSubInterface(subInterface, subLocation)
}

// SetDomInterface sets the dom interface for the given location
func (qbe *QuaiBackend) SetDomInterface(domInterface core.CoreBackend, nodeLocation common.Location) {
	backend := *qbe.GetBackend(nodeLocation)
	if backend == nil {
		log.Global.Error("no backend found")
		return
	}
	backend.SetDomInterface(domInterface)
}

// AddGenesisPendingEtxs adds the genesis pending etxs for the given location
func (qbe *QuaiBackend) AddGenesisPendingEtxs(block *types.WorkObject, location common.Location) {
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return
	}
	backend.AddGenesisPendingEtxs(block)
}

func (qbe *QuaiBackend) LookupBlock(hash common.Hash, location common.Location) *types.WorkObject {
	if qbe == nil {
		return nil
	}
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return nil
	}
	return backend.BlockOrCandidateByHash(hash)
}

func (qbe *QuaiBackend) LookupBlockHashByNumber(number *big.Int, location common.Location) *common.Hash {
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return nil
	}
	block, err := backend.BlockByNumber(context.Background(), rpc.BlockNumber(number.Int64()))
	if err != nil {
		log.Global.Trace("Error looking up the BlockByNumber", location)
	}
	if block != nil {
		blockHash := block.Hash()
		return &blockHash
	} else {
		return nil
	}
}

func (qbe *QuaiBackend) ProcessingState(location common.Location) bool {
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return false
	}
	return backend.ProcessingState()
}
