package core

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// NewTxsEvent is posted when a batch of transactions enter the transaction pool.
type NewTxsEvent struct{ Txs []*types.Transaction }

// NewMinedBlockEvent is posted when a block has been imported.
type NewMinedBlockEvent struct{ Block *types.WorkObject }

// RemovedLogsEvent is posted when a reorg happens
type RemovedLogsEvent struct{ Logs []*types.Log }

type ChainEvent struct {
	Block   *types.WorkObject
	Hash    common.Hash
	Logs    []*types.Log
	Order   int
	Entropy *big.Int
}

type UnlocksEvent struct {
	Hash    common.Hash
	Unlocks []common.Unlock
}

type ChainSideEvent struct {
	Blocks []*types.WorkObject
}

type ChainHeadEvent struct {
	Block *types.WorkObject
}

type ExpansionEvent struct {
	Block *types.WorkObject
}

type NewWorkshareEvent struct {
	Workshare *types.WorkObject
}
