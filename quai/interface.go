package quai

import (
	"context"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	chain "github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"

	"github.com/dominant-strategies/go-quai/trie"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/peer"
)

// The consensus backend will implement the following interface to provide information to the networking backend.
type ConsensusAPI interface {
	// Returns the current block height for the given location
	GetHeight(common.Location) uint64

	// Handle new data propagated from the gossip network. Should return quickly.
	// Specify the peer which propagated the data to us, as well as the data itself.
	// Return true if this data should be relayed to peers. False if it should be ignored.
	OnNewBroadcast(core.PeerID, string, string, interface{}, common.Location) bool

	// Creates the function that will be used to determine if a message should be propagated.
	ValidatorFunc() func(ctx context.Context, id peer.ID, msg *pubsub.Message, location common.Location) pubsub.ValidationResult

	// Asks the consensus backend to lookup a block by hash and location.
	// If the block is found, it should be returned. Otherwise, nil should be returned.
	LookupBlock(common.Hash, common.Location) *types.WorkObject

	LookupBlockHashByNumber(*big.Int, common.Location) *common.Hash

	LookupBlockByNumber(*big.Int, common.Location) *types.WorkObject

	// Asks the consensus backend to lookup a trie node by hash and location,
	// and return the data in the trie node.
	GetTrieNode(hash common.Hash, location common.Location) *trie.TrieNodeResponse

	// GetBackend gets the backend for the given location
	GetBackend(nodeLocation common.Location) *quaiapi.Backend

	// SetApiBackend sets the backend for the given location
	SetApiBackend(*quaiapi.Backend, common.Location)

	// SetCurrentExpansionNumber sets the current expansion number for the given location
	SetCurrentExpansionNumber(uint8)

	// SetSubInteface sets the sub interface for the given location
	SetSubInterface(chain.CoreBackend, common.Location, common.Location)

	// SetDomInterface sets the dom interface for the given location
	SetDomInterface(chain.CoreBackend, common.Location)

	// AddGenesisPendingEtxs adds the genesis pending etxs for the given location
	AddGenesisPendingEtxs(*types.WorkObject, common.Location)

	// WriteGenesisBlock adds the genesis block to the database and also writes the block to the disk
	WriteGenesisBlock(*types.WorkObject, common.Location)

	// Returns if the location is processing state
	ProcessingState(common.Location) bool
}

// The networking backend will implement the following interface to enable consensus to communicate with other nodes.
type NetworkingAPI interface {
	// Start the p2p node
	Start() error

	// Stop the p2p node
	Stop() error

	// Subscribe/UnSubscribe to a type of data from a given location
	Subscribe(common.Location, interface{}) error
	Unsubscribe(common.Location, interface{}) error

	// Method to broadcast data to the network
	// Specify location and the data to send
	Broadcast(common.Location, interface{}) error

	// SetConsensusBackend sets the consensus API into the p2p interface
	SetConsensusBackend(ConsensusAPI)

	// Method to request data from the network
	// Specify location, data hash, and data type to request
	Request(location common.Location, requestData interface{}, responseDataType interface{}) chan interface{}

	// Adjust a peer's quality score
	AdjustPeerQuality(core.PeerID, func(int) int)

	// Protects the peer's connection from being pruned
	ProtectPeer(core.PeerID)
	// Remove protection from the peer's connection
	UnprotectPeer(core.PeerID)
	// Ban will close the connection and prevent future connections with this peer
	BanPeer(core.PeerID)
}
