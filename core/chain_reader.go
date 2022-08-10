package core

import (
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/params"
)

// ChainReader defines a small collection of methods needed to access the local
// blockchain during header verification. It's implemented by both blockchain
// and lightchain.
type ChainReader interface {
	// Config retrieves the header chain's chain configuration.
	Config() *params.ChainConfig

	// GetTd returns the total difficulty of a local block.
	GetTd(common.Hash, uint64) []*big.Int

	// GetBlockByHash retrieves a block from the database by hash, caching it if found.
	GetBlockByHash(hash common.Hash) *types.Block

	// GetHeaderByHash retrieves a block header from the database by hash, caching it if
	// found.
	GetHeaderByHash(hash common.Hash) *types.Header

	// Gets the difficulty order of a header
	GetDifficultyOrder(header *types.Header) (int, error)
}
