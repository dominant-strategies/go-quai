// Copyright 2015 The go-ethereum Authors
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

package core

import (
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/params"
)

// BlockGen creates blocks for testing.
// See GenerateChain for a detailed explanation.
type BlockGen struct {
	i       int
	parent  *types.Block
	chain   []*types.Block
	header  *types.Header
	statedb *state.StateDB

	gasPool  *GasPool
	txs      []*types.Transaction
	receipts []*types.Receipt
	uncles   []*types.Header

	config *params.ChainConfig
	engine consensus.Engine
}

// SetCoinbase sets the coinbase of the generated block.
// It can be called at most once.
func (b *BlockGen) SetCoinbase(addr common.Address) {
	if b.gasPool != nil {
		if len(b.txs) > 0 {
			panic("coinbase must be set before adding transactions")
		}
		panic("coinbase can only be set once")
	}
	b.header.SetCoinbase(addr)
	b.gasPool = new(GasPool).AddGas(b.header.GasLimit())
}

// SetExtra sets the extra data field of the generated block.
func (b *BlockGen) SetExtra(data []byte) {
	b.header.SetExtra(data)
}

// SetNonce sets the nonce field of the generated block.
func (b *BlockGen) SetNonce(nonce types.BlockNonce) {
	b.header.SetNonce(nonce)
}

// SetDifficulty sets the difficulty field of the generated block. This method is
// useful for Clique tests where the difficulty does not depend on time. For the
// blake3pow tests, please use OffsetTime, which implicitly recalculates the diff.
func (b *BlockGen) SetDifficulty(diff *big.Int) {
	b.header.SetDifficulty(diff)
}

// AddTx adds a transaction to the generated block. If no coinbase has
// been set, the block's coinbase is set to the zero address.
//
// AddTx panics if the transaction cannot be executed. In addition to
// the protocol-imposed limitations (gas limit, etc.), there are some
// further limitations on the content of transactions that can be
// added. Notably, contract code relying on the BLOCKHASH instruction
// will panic during execution.
func (b *BlockGen) AddTx(tx *types.Transaction) {
	b.AddTxWithChain(nil, tx)
}

// AddTxWithChain adds a transaction to the generated block. If no coinbase has
// been set, the block's coinbase is set to the zero address.
//
// AddTxWithChain panics if the transaction cannot be executed. In addition to
// the protocol-imposed limitations (gas limit, etc.), there are some
// further limitations on the content of transactions that can be
// added. If contract code relies on the BLOCKHASH instruction,
// the block in chain will be returned.
func (b *BlockGen) AddTxWithChain(hc *HeaderChain, tx *types.Transaction) {
	if b.gasPool == nil {
		b.SetCoinbase(common.Address{})
	}
	b.statedb.Prepare(tx.Hash(), len(b.txs))
	coinbase := b.header.Coinbase()
	gasUsed := b.header.GasUsed()
	receipt, err := ApplyTransaction(b.config, hc, &coinbase, b.gasPool, b.statedb, b.header, tx, &gasUsed, vm.Config{})
	if err != nil {
		panic(err)
	}
	b.txs = append(b.txs, tx)
	b.receipts = append(b.receipts, receipt)
}

// GetBalance returns the balance of the given address at the generated block.
func (b *BlockGen) GetBalance(addr common.Address) *big.Int {
	return b.statedb.GetBalance(addr)
}

// AddUncheckedTx forcefully adds a transaction to the block without any
// validation.
//
// AddUncheckedTx will cause consensus failures when used during real
// chain processing. This is best used in conjunction with raw block insertion.
func (b *BlockGen) AddUncheckedTx(tx *types.Transaction) {
	b.txs = append(b.txs, tx)
}

// Number returns the block number of the block being generated.
func (b *BlockGen) Number() *big.Int {
	return new(big.Int).Set(b.header.Number())
}

// BaseFee returns the EIP-1559 base fee of the block being generated.
func (b *BlockGen) BaseFee() *big.Int {
	return new(big.Int).Set(b.header.BaseFee())
}

// AddUncheckedReceipt forcefully adds a receipts to the block without a
// backing transaction.
//
// AddUncheckedReceipt will cause consensus failures when used during real
// chain processing. This is best used in conjunction with raw block insertion.
func (b *BlockGen) AddUncheckedReceipt(receipt *types.Receipt) {
	b.receipts = append(b.receipts, receipt)
}

// TxNonce returns the next valid transaction nonce for the
// account at addr. It panics if the account does not exist.
func (b *BlockGen) TxNonce(addr common.Address) uint64 {
	if !b.statedb.Exist(addr) {
		panic("account does not exist")
	}
	return b.statedb.GetNonce(addr)
}

// AddUncle adds an uncle header to the generated block.
func (b *BlockGen) AddUncle(h *types.Header) {
	b.uncles = append(b.uncles, h)
}

// PrevBlock returns a previously generated block by number. It panics if
// num is greater or equal to the number of the block being generated.
// For index -1, PrevBlock returns the parent block given to GenerateChain.
func (b *BlockGen) PrevBlock(index int) *types.Block {
	if index >= b.i {
		panic(fmt.Errorf("block index %d out of range (%d,%d)", index, -1, b.i))
	}
	if index == -1 {
		return b.parent
	}
	return b.chain[index]
}

// OffsetTime modifies the time instance of a block, implicitly changing its
// associated difficulty. It's useful to test scenarios where forking is not
// tied to chain length directly.
func (b *BlockGen) OffsetTime(seconds int64) {
	b.header.SetTime(b.header.Time() + uint64(seconds))
	if b.header.Time() <= b.parent.Header().Time() {
		panic("block time out of range")
	}
	chainreader := &fakeChainReader{config: b.config}
	b.header.SetDifficulty(b.engine.CalcDifficulty(chainreader, b.parent.Header()))
}

// GenerateChain creates a chain of n blocks. The first block's
// parent will be the provided parent. db is used to store
// intermediate states and should contain the parent's state trie.
//
// The generator function is called with a new block generator for
// every block. Any transactions and uncles added to the generator
// become part of the block. If gen is nil, the blocks will be empty
// and their coinbase will be the zero address.
//
// Blocks created by GenerateChain do not contain valid proof of work
// values. Inserting them into BlockChain requires use of FakePow or
// a similar non-validating proof of work implementation.
func GenerateChain(config *params.ChainConfig, parent *types.Block, engine consensus.Engine, db ethdb.Database, n int, gen func(int, *BlockGen)) ([]*types.Block, []types.Receipts) {
	if config == nil {
		config = params.TestChainConfig
	}
	blocks, receipts := make(types.Blocks, n), make([]types.Receipts, n)
	chainreader := &fakeChainReader{config: config}
	genblock := func(i int, parent *types.Block, statedb *state.StateDB) (*types.Block, types.Receipts) {
		b := &BlockGen{i: i, chain: blocks, parent: parent, statedb: statedb, config: config, engine: engine}
		b.header = makeHeader(chainreader, parent, statedb, b.engine)

		// Mutate the state and block according to any hard-fork specs
		if daoBlock := config.DAOForkBlock; daoBlock != nil {
			limit := new(big.Int).Add(daoBlock, params.DAOForkExtraRange)
			if b.header.Number().Cmp(daoBlock) >= 0 && b.header.Number().Cmp(limit) < 0 {
				if config.DAOForkSupport {
					b.header.SetExtra(common.CopyBytes(params.DAOForkBlockExtra))
				}
			}
		}
		if config.DAOForkSupport && config.DAOForkBlock != nil && config.DAOForkBlock.Cmp(b.header.Number()) == 0 {
			misc.ApplyDAOHardFork(statedb)
		}
		// Execute any user modifications to the block
		if gen != nil {
			gen(i, b)
		}
		if b.engine != nil {
			// Finalize and seal the block
			block, _ := b.engine.FinalizeAndAssemble(chainreader, b.header, statedb, b.txs, b.uncles, b.receipts)

			// Write state changes to db
			root, err := statedb.Commit(config.IsEIP158(b.header.Number()))
			if err != nil {
				panic(fmt.Sprintf("state write error: %v", err))
			}
			if err := statedb.Database().TrieDB().Commit(root, false, nil); err != nil {
				panic(fmt.Sprintf("trie write error: %v", err))
			}
			return block, b.receipts
		}
		return nil, nil
	}
	for i := 0; i < n; i++ {
		statedb, err := state.New(parent.Root(), state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}
		block, receipt := genblock(i, parent, statedb)
		blocks[i] = block
		receipts[i] = receipt
		parent = block
	}
	return blocks, receipts
}

func makeHeader(chain consensus.ChainReader, parent *types.Block, state *state.StateDB, engine consensus.Engine) *types.Header {
	var time uint64
	if parent.Time() == 0 {
		time = 10
	} else {
		time = parent.Time() + 10 // block time is fixed at 10 seconds
	}

	// Temporary header values just to calc difficulty
	diffheader := types.EmptyHeader()
	diffheader.SetDifficulty(parent.Difficulty())
	diffheader.SetNumber(parent.Number())
	diffheader.SetTime(time - 10)
	diffheader.SetUncleHash(parent.UncleHash())

	// Make new header
	header := types.EmptyHeader()
	header.SetRoot(state.IntermediateRoot(chain.Config().IsEIP158(parent.Number())))
	header.SetParentHash(parent.Hash())
	header.SetCoinbase(parent.Coinbase())
	header.SetDifficulty(engine.CalcDifficulty(chain, diffheader))
	header.SetGasLimit(parent.GasLimit())
	header.SetNumber(new(big.Int).Add(parent.Number(), common.Big1))
	header.SetTime(time)
	if chain.Config().IsLondon(header.Number()) {
		header.SetBaseFee(misc.CalcBaseFee(chain.Config(), parent.Header()))
		if !chain.Config().IsLondon(parent.Number()) {
			parentGasLimit := parent.GasLimit() * params.ElasticityMultiplier
			header.SetGasLimit(CalcGasLimit(parentGasLimit, parentGasLimit))
		}
	}
	return header
}

// makeHeaderChain creates a deterministic chain of headers rooted at parent.
func makeHeaderChain(parent *types.Header, n int, engine consensus.Engine, db ethdb.Database, seed int) []*types.Header {
	blocks := makeBlockChain(types.NewBlockWithHeader(parent), n, engine, db, seed)
	headers := make([]*types.Header, len(blocks))
	for i, block := range blocks {
		headers[i] = block.Header()
	}
	return headers
}

// makeBlockChain creates a deterministic chain of blocks rooted at parent.
func makeBlockChain(parent *types.Block, n int, engine consensus.Engine, db ethdb.Database, seed int) []*types.Block {
	blocks, _ := GenerateChain(params.TestChainConfig, parent, engine, db, n, func(i int, b *BlockGen) {
		b.SetCoinbase(common.Address{0: byte(seed), 19: byte(i)})
	})
	return blocks
}

type fakeChainReader struct {
	config *params.ChainConfig
}

// Config returns the chain configuration.
func (cr *fakeChainReader) Config() *params.ChainConfig {
	return cr.config
}

func (cr *fakeChainReader) CurrentHeader() *types.Header                            { return nil }
func (cr *fakeChainReader) GetHeaderByNumber(number uint64) *types.Header           { return nil }
func (cr *fakeChainReader) GetHeaderByHash(hash common.Hash) *types.Header          { return nil }
func (cr *fakeChainReader) GetHeader(hash common.Hash, number uint64) *types.Header { return nil }
func (cr *fakeChainReader) GetBlock(hash common.Hash, number uint64) *types.Block   { return nil }

type knot struct {
	block *types.Block
}

func GenerateKnot(config *params.ChainConfig, parent *types.Block, engine consensus.Engine, db ethdb.Database, n int, gen func(int, *BlockGen)) ([]*types.Block, []types.Receipts) {
	if config == nil {
		config = params.TestChainConfig
	}
	blocks, receipts := make(types.Blocks, n), make([]types.Receipts, n)
	chainreader := &fakeChainReader{config: config}

	resultLoop := func(i int, k *knot, results chan *types.Header, stop chan struct{}, wg *sync.WaitGroup) error {
		for {
			select {
			case header := <-results:
				k.block = types.NewBlockWithHeader(header)
				fmt.Println("Mined block: ", k.block.Hash(), k.block.Header().NumberArray(), k.block.Header().DifficultyArray())
				defer wg.Done()
				return nil
			}
		}
	}

	genblock := func(i int, parent *types.Block, parentOfParent *types.Header, genesis *types.Header, primedb *state.StateDB, regiondb *state.StateDB, zonedb *state.StateDB, location common.Location, resultCh chan *types.Header, stopCh chan struct{}) {
		b := &BlockGen{i: i, chain: blocks, parent: parent, statedb: primedb, config: config, engine: engine}
		b.header = makeKnotHeader(chainreader, parent, parentOfParent, genesis, primedb, b.engine, location, i)

		// Execute any user modifications to the block
		if gen != nil {
			gen(i, b)
		}
		if b.engine != nil {
			// Finalize and seal the block
			b.engine.FinalizeAtIndex(chainreader, b.header, zonedb, b.txs, b.uncles, common.ZONE_CTX)
			// Write state changes to db
			root, err := zonedb.Commit(config.IsEIP158(b.header.Number(common.ZONE_CTX)))
			if err != nil {
				panic(fmt.Sprintf("state write error: %v", err))
			}
			if err := zonedb.Database().TrieDB().Commit(root, false, nil); err != nil {
				panic(fmt.Sprintf("trie write error: %v", err))
			}

			b.engine.FinalizeAtIndex(chainreader, b.header, regiondb, b.txs, b.uncles, common.REGION_CTX)
			// Write state changes to db
			root, err = regiondb.Commit(config.IsEIP158(b.header.Number(common.REGION_CTX)))
			if err != nil {
				panic(fmt.Sprintf("state write error: %v", err))
			}
			if err := regiondb.Database().TrieDB().Commit(root, false, nil); err != nil {
				panic(fmt.Sprintf("trie write error: %v", err))
			}

			block, _ := b.engine.FinalizeAndAssemble(chainreader, b.header, primedb, b.txs, b.uncles, b.receipts)

			// Write state changes to db
			root, err = primedb.Commit(config.IsEIP158(b.header.Number(common.PRIME_CTX)))
			if err != nil {
				panic(fmt.Sprintf("state write error: %v", err))
			}
			if err := primedb.Database().TrieDB().Commit(root, false, nil); err != nil {
				panic(fmt.Sprintf("trie write error: %v", err))
			}

			// Mine the block
			if err := engine.Seal(block.Header(), resultCh, stopCh); err != nil {
				log.Println("Block sealing failed", "err", err)
			}
		}
	}

	knot := &knot{
		block: parent,
	}

	genesis := parent.Header()
	fmt.Println("genesis hash", genesis.Hash())

	locations := []common.Location{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {2, 0}, {2, 1}, {2, 2}}
	for i := 0; i < len(locations); i++ {
		primedb, err := state.New(parent.Root(), state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}

		regionParentRoot := common.Hash{}
		// mod 3 is amount of regions
		if i%3 == 0 {
			regionParentRoot = genesis.Root(common.REGION_CTX)
		} else {
			regionParentRoot = parent.Header().Root(common.REGION_CTX)
		}
		regiondb, err := state.New(regionParentRoot, state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}

		zonedb, err := state.New(genesis.Root(common.ZONE_CTX), state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}

		resultCh := make(chan *types.Header)
		exitCh := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go resultLoop(i, knot, resultCh, exitCh, &wg)
		if i != 0 {
			genblock(i, parent, blocks[i-1].Header(), genesis, primedb, regiondb, zonedb, locations[i], resultCh, exitCh)
		} else {
			genblock(i, parent, types.EmptyHeader(), genesis, primedb, regiondb, zonedb, locations[i], resultCh, exitCh)
		}
		wg.Wait()
		blocks[i] = knot.block
		parent = knot.block
	}
	return blocks, receipts
}

func makeKnotHeader(chain consensus.ChainReader, parent *types.Block, parentOfParent *types.Header, genesis *types.Header, state *state.StateDB, engine consensus.Engine, location common.Location, i int) *types.Header {
	var timestamp uint64
	if parent.Time() == 0 {
		timestamp = uint64(time.Now().Unix())
	} else {
		timestamp = parent.Time() + 10 // block time is fixed at 10 seconds
	}

	baseFee := misc.CalcBaseFee(chain.Config(), parent.Header())

	header := types.EmptyHeader()
	for i := 0; i < common.HierarchyDepth; i++ {
		header.SetNumber(big.NewInt(1), i)
		header.SetParentHash(genesis.Hash(), i)
		header.SetDifficulty(genesis.Difficulty(i), i)
		header.SetUncleHash(types.EmptyUncleHash, i)
		header.SetTxHash(types.EmptyRootHash, i)
		header.SetReceiptHash(types.EmptyRootHash, i)
		header.SetBaseFee(baseFee, i)
		header.SetGasLimit(params.MinGasLimit, i)
	}

	header.SetLocation(location)
	header.SetTime(timestamp)

	parentHeader := parent.Header()

	header.SetParentHash(parent.Hash())
	header.SetCoinbase(parent.Coinbase())

	// PRIME difficulty
	header.SetDifficulty(engine.CalcDifficultyAtIndex(chain, genesis.Hash(), parentHeader, parentOfParent, common.NodeLocation.Context()))
	// REGION difficulty
	if i%common.HierarchyDepth == 0 {
		header.SetDifficulty(engine.CalcDifficultyAtIndex(chain, genesis.Hash(), genesis, parentHeader, common.NodeLocation.Context()+1), common.NodeLocation.Context()+1)
	} else if i%common.HierarchyDepth == 1 {
		header.SetDifficulty(engine.CalcDifficultyAtIndex(chain, genesis.Hash(), parentHeader, genesis, common.NodeLocation.Context()+1), common.NodeLocation.Context()+1)
	} else {
		header.SetDifficulty(engine.CalcDifficultyAtIndex(chain, genesis.Hash(), parentHeader, parentOfParent, common.NodeLocation.Context()+1), common.NodeLocation.Context()+1)
	}
	// ZONE difficulty
	header.SetDifficulty(engine.CalcDifficultyAtIndex(chain, genesis.Hash(), genesis, parentOfParent, common.NodeLocation.Context()+2), common.NodeLocation.Context()+2)

	header.SetNumber(new(big.Int).Add(parent.Number(), common.Big1))

	headerLocation := header.Location()
	parentLocation := parent.Header().Location()

	headerLocation.AssertValid()

	if len(parentLocation) > 0 && headerLocation.Region() == parentLocation.Region() {
		header.SetNumber(new(big.Int).Add(parent.Header().Number(common.NodeLocation.Context()+1), common.Big1), common.NodeLocation.Context()+1)
		header.SetParentHash(parent.Hash(), common.NodeLocation.Context()+1)
	}

	return header
}
