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
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// TxPool propagation metrics
	txPropagationMetrics = metrics_config.NewCounterVec("TxCount", "Transaction counter")
	txTotalCounter       = txPropagationMetrics.WithLabelValues("total txs")
	txCountersBySlice    = make(map[string]prometheus.Counter)

	workObjectMetrics = metrics_config.NewCounterVec("WorkObjectCounters", "Tracks block statistics")
	// Block propagation metrics
	blockIngressCounter   = workObjectMetrics.WithLabelValues("blocks/ingress")
	blockKnownCounter     = workObjectMetrics.WithLabelValues("blocks/known")
	blockMaliciousCounter = workObjectMetrics.WithLabelValues("blocks/malicious")

	// Header propagation metrics
	headerIngressCounter   = workObjectMetrics.WithLabelValues("headers/ingress")
	headerKnownCounter     = workObjectMetrics.WithLabelValues("headers/known")
	headerMaliciousCounter = workObjectMetrics.WithLabelValues("headers/malicious")

	// WorkShare propagation metrics
	workShareIngressCounter   = workObjectMetrics.WithLabelValues("workShares/ingress")
	workShareKnownCounter     = workObjectMetrics.WithLabelValues("workShares/known")
	workShareMaliciousCounter = workObjectMetrics.WithLabelValues("workShares/malicious")
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
	return &QuaiBackend{regionApiBackends: make([]*quaiapi.Backend, common.MaxRegions), zoneApiBackends: zoneBackends}, nil
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
	qbe.p2pBackend.AdjustPeerQuality(sourcePeer, topic, p2p.QualityAdjOnBroadcast)
	switch data := data.(type) {
	case types.WorkObjectBlockView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}

		backend.Logger().WithFields(log.Fields{"message id": Id, "Number": data.WorkObject.NumberArray(), "Hash": data.WorkObject.Hash()}).Info("Received a work object block view broadcast")

		backend.WriteBlock(data.WorkObject)
		blockIngressCounter.Inc()
	case types.WorkObjectHeaderView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}

		backend.Logger().WithFields(log.Fields{"message id": Id, "Number": data.WorkObject.NumberArray(), "Hash": data.WorkObject.Hash()}).Info("Received a work object header view broadcast")

		// Only append this in the case of the slice
		if !backend.ProcessingState() && backend.NodeCtx() == common.ZONE_CTX {
			backend.WriteBlock(data.WorkObject)
		}

		headerIngressCounter.Inc()
	case types.WorkObjectShareView:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		if backend.ProcessingState() {

			backend.Logger().WithFields(log.Fields{"tx count": len(data.WorkObject.Transactions()), "message id": Id}).Info("Received a work share broadcast")
			// Unpack the workobjectheader and the transactions
			err := backend.SendWorkShare(data.WorkObject.WorkObjectHeader())
			if err != nil {
				backend.Logger().WithFields(log.Fields{
					"error": err.Error(),
					"hash":  data.WorkObject.Hash().Hex(),
				}).Warn("Failed to process received workshare")
			}
			backend.SendRemoteTxs(data.WorkObject.Transactions())

			workShareIngressCounter.Inc()
			sliceName := data.Location().Name()
			txCount := float64(len(data.WorkObject.Transactions()))
			txTotalCounter.Add(txCount)
			if counter, exists := txCountersBySlice[sliceName]; exists {
				counter.Add(txCount)
			} else {
				newCounter := txPropagationMetrics.WithLabelValues(sliceName + " txs")
				newCounter.Add(txCount)
				txCountersBySlice[sliceName] = newCounter
			}
		}
	case *types.AuxTemplate:
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}

		backend.Logger().WithFields(log.Fields{
			"message id": Id,
			"chainID":    data.PowID(),
			"nbits":      data.Bits(),
		}).Info("Received an aux template broadcast")

		if backend.NodeCtx() == common.ZONE_CTX && backend.ProcessingState() {
			backend.SendAuxPowTemplate(data)
		}

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
	be := qbe.GetBackend(location)
	return (*be).CurrentHeader().NumberU64(location.Context())
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

func (qbe *QuaiBackend) LookupBlockByNumber(number *big.Int, location common.Location) *types.WorkObject {
	if qbe == nil {
		return nil
	}
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return nil
	}
	block, err := backend.BlockByNumber(context.Background(), rpc.BlockNumber(number.Int64()))
	if err != nil {
		backend.Logger().WithField("err", err).Error("Error getting BlockByNumber")
		return nil
	}
	return block
}

func (qbe *QuaiBackend) LookupBlockHashByNumber(number *big.Int, location common.Location) *common.Hash {
	backend := *qbe.GetBackend(location)
	if backend == nil {
		log.Global.Error("no backend found")
		return nil
	}
	block, err := backend.BlockByNumber(context.Background(), rpc.BlockNumber(number.Int64()))
	if err != nil {
		log.Global.WithField("err", err).Trace("Error looking up the BlockByNumber", location)
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
