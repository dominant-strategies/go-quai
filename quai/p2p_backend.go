package quai

import (
	"context"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/rpc"
	"github.com/dominant-strategies/go-quai/trie"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
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
	zoneBackends := make([][]*quaiapi.Backend, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		zoneBackends[i] = make([]*quaiapi.Backend, common.HierarchyDepth)
	}
	return &QuaiBackend{regionApiBackends: make([]*quaiapi.Backend, common.HierarchyDepth), zoneApiBackends: zoneBackends}, nil
}

// Adds the p2pBackend into the given QuaiBackend
func (qbe *QuaiBackend) SetP2PApiBackend(p2pBackend NetworkingAPI) {
	qbe.p2pBackend = p2pBackend
}

func (qbe *QuaiBackend) SetApiBackend(apiBackend quaiapi.Backend, location common.Location) {
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
func (qbe *QuaiBackend) SetPrimeApiBackend(primeBackend quaiapi.Backend) {
	qbe.primeApiBackend = &primeBackend
}

// Set the RegionBackend into the QuaiBackend
func (qbe *QuaiBackend) SetRegionApiBackend(regionBackend quaiapi.Backend, location common.Location) {
	qbe.regionApiBackends[location.Region()] = &regionBackend
}

// Set the ZoneBackend into the QuaiBackend
func (qbe *QuaiBackend) SetZoneApiBackend(zoneBackend quaiapi.Backend, location common.Location) {
	qbe.zoneApiBackends[location.Region()][location.Zone()] = &zoneBackend
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
func (qbe *QuaiBackend) OnNewBroadcast(sourcePeer p2p.PeerID, data interface{}, nodeLocation common.Location) bool {
	switch data.(type) {
	case types.Block:
		block := data.(types.Block)
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		// TODO: Verify the Block before writing it
		// TODO: Determine if the block information was lively or stale and rate
		// the peer accordingly
		backend.WriteBlock(&block)
		return true
	case types.Header:
	case types.Transaction:
		tx := data.(types.Transaction)
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		backend.SendRemoteTx(&tx)
		// TODO: Handle the error here and mark the peers accordingly
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
		switch data.(type) {
		case types.Block:
			block := data.(types.Block)
			backend := *qbe.GetBackend(block.Location())
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer":     id,
					"hash":     block.Hash(),
					"location": block.Location(),
				}).Error("no backend found for this location")
				return pubsub.ValidationReject
			}
		case types.Transaction:
			return pubsub.ValidationAccept
		}
		return pubsub.ValidationAccept
	}
}

func (qbe *QuaiBackend) LookupBlock(hash common.Hash, location common.Location) *types.Block {
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
		log.Global.Tracef("Error looking up the BlockByNumber", location)
	}
	if block != nil {
		blockHash := block.Hash()
		return &blockHash
	} else {
		return nil
	}
}
