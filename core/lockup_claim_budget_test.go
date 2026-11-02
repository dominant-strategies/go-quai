package core

import (
	"crypto/ecdsa"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/core/vm"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
	lru "github.com/hashicorp/golang-lru/v2"
)

func lockupBudgetBlock(prime, zone uint64) *types.WorkObject {
	location := common.Location{0, 0}
	pow := types.NewPowShareDiffAndCount(big.NewInt(1), big.NewInt(0), big.NewInt(0))
	wh := types.NewWorkObjectHeader(common.Hash{1}, common.Hash{2}, new(big.Int).SetUint64(zone), big.NewInt(1000000), new(big.Int).SetUint64(prime), common.Hash{3}, types.BlockNonce{}, 0, 1, location, common.ZeroAddress(location), nil, nil, pow, pow, big.NewInt(1), big.NewInt(1), big.NewInt(1000000))
	block := types.NewWorkObject(wh, types.EmptyWorkObjectBody(), nil)
	block.Header().SetGasLimit(1000000)
	block.Header().SetBaseFee(big.NewInt(0))
	block.Header().SetExchangeRate(new(big.Int).Set(params.ExchangeRate))
	block.Header().SetQuaiStateSize(big.NewInt(0))
	return block
}

type lockupBudgetEngine struct{ consensus.Engine }

func (lockupBudgetEngine) ComputePowHash(*types.WorkObjectHeader) (common.Hash, error) {
	return common.Hash{9}, nil
}

func budgetClaimKey(t *testing.T, loc common.Location) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	for i := 0; i < 10000; i++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		addr := crypto.PubkeyToAddress(key.PublicKey, loc)
		if _, err := addr.InternalAndQuaiAddress(); err == nil {
			return key, addr
		}
	}
	t.Fatal("could not generate local Quai sender")
	return nil, common.Zero
}

func TestLockupClaimBudgetFailureRestoresWorkerBatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		destination string
		context     int
		overGas     uint64
		wantError   string
	}{
		{"cross-prime", "0x1000000000000000000000000000000000000000", common.PRIME_CTX, params.ETXPLimitMin + 1, "emits too many cross-prime ETXs"},
		{"cross-region", "0x0100000000000000000000000000000000000000", common.REGION_CTX, params.ETXRLimitMin + 1, "emits too many cross-region ETXs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testLockupClaimBudgetFailureRestoresWorkerBatch(t, tc.destination, tc.context, tc.overGas, tc.wantError)
		})
	}
}

func testLockupClaimBudgetFailureRestoresWorkerBatch(t *testing.T, destinationHex string, context int, overGas uint64, wantError string) {
	loc := common.Location{0, 0}
	cfg := *params.TestChainConfig
	cfg.Location = loc
	lockup := common.HexToAddress("0x000000000000000000000000000000000000000a", loc)
	old, existed := vm.LockupContractAddresses[[2]byte{0, 0}]
	vm.LockupContractAddresses[[2]byte{0, 0}] = lockup
	t.Cleanup(func() {
		if existed {
			vm.LockupContractAddresses[[2]byte{0, 0}] = old
		} else {
			delete(vm.LockupContractAddresses, [2]byte{0, 0})
		}
	})

	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(common.Hash{}, common.Hash{}, big.NewInt(0), state.NewDatabase(db), state.NewDatabase(db), nil, loc, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	owner := common.HexToAddress("0x0011000000000000000000000000000000000000", loc)
	beneficiary := common.HexToAddress("0x0022000000000000000000000000000000000000", loc)
	destination := common.HexToAddress(destinationHex, loc)
	if destination.Location().CommonDom(loc).Context() != context {
		t.Fatalf("destination has context %d, want %d", destination.Location().CommonDom(loc).Context(), context)
	}
	ownerInternal, err := owner.InternalAndQuaiAddress()
	if err != nil {
		t.Fatal(err)
	}
	statedb.CreateAccount(ownerInternal)

	// Copy the established lockup-claim fixture. The contract reverts when
	// the inner claim call fails, so a missing lockup gives a failed receipt.
	code := []byte{0x60, 53, 0x60, 0, 0x60, 0, byte(vm.CALLDATACOPY), 0x60, 0, 0x60, 0, 0x60, 53, 0x60, 0, 0x60, 0, byte(vm.PUSH20)}
	code = append(code, lockup.Bytes()...)
	code = append(code, 0x63, 0, 0x20, 0, 0, byte(vm.CALL)) // 2,097,152 gas for claim
	branch := len(code)
	code = append(code, byte(vm.ISZERO), byte(vm.PUSH1), byte(branch+18), byte(vm.JUMPI), 0x60, 53, byte(vm.CALLDATALOAD), byte(vm.ISZERO), byte(vm.PUSH1), byte(branch+16), byte(vm.JUMPI), 0x60, 0, 0x60, 0, byte(vm.REVERT), byte(vm.JUMPDEST), byte(vm.STOP), byte(vm.JUMPDEST), 0xfe)
	statedb.SetCode(ownerInternal, code)

	amount := big.NewInt(123)
	if _, err := rawdb.WriteCoinbaseLockup(db, owner, beneficiary, 0, 1, amount, 1, 1, beneficiary); err != nil {
		t.Fatal(err)
	}
	keyA, senderA := budgetClaimKey(t, loc)
	keyB, senderB := budgetClaimKey(t, loc)
	if senderA.Equal(senderB) {
		t.Fatal("senders must differ")
	}
	for _, sender := range []common.Address{senderA, senderB} {
		internal, err := sender.InternalAndQuaiAddress()
		if err != nil {
			t.Fatal(err)
		}
		statedb.CreateAccount(internal)
		statedb.AddBalance(internal, big.NewInt(100_000_000))
	}

	prime := params.SecurityHardeningForkBlock + 1
	parent := lockupBudgetBlock(prime, params.CoinbaseEpochBlocks)
	parent.Header().SetGasLimit(8_000_000)
	parent.WorkObjectHeader().SetHeaderHash(parent.Header().Hash())
	coinbase := types.NewAuxPowCoinbaseTx(types.Kawpow, 100, []byte{0x51}, parent.WorkObjectHeader().SealHash(), 123)
	root := types.CalculateMerkleRoot(types.Kawpow, coinbase, nil)
	donor := &types.RavencoinBlockHeader{Version: 4, HashMerkleRoot: root, Bits: 0x1d00ffff}
	parent.WorkObjectHeader().SetAuxPow(types.NewAuxPow(types.Kawpow, types.NewAuxPowHeader(donor), nil, nil, nil, coinbase))
	parent.WorkObjectHeader().SetHeaderHash(parent.Header().Hash())
	header := lockupBudgetBlock(prime, params.CoinbaseEpochBlocks+1)
	header.Header().SetGasLimit(8_000_000)
	header.SetParentHash(parent.Hash(), common.ZONE_CTX)
	header.Header().SetPrimeTerminusHash(parent.Hash())
	rawdb.WriteGenesisHashes(db, []common.Hash{parent.Hash()})
	rawdb.WriteTermini(db, parent.Hash(), types.EmptyTermini())
	body := NewTestBodyDb(db)
	body.chainConfig = &cfg
	body.logger = log.Global
	body.blockCache.Add(parent.Hash(), *parent)
	headers, _ := lru.New[common.Hash, types.WorkObject](8)
	headers.Add(parent.Hash(), *parent)
	orders, _ := lru.New[common.Hash, calcOrderResponse](8)
	hc := &HeaderChain{config: &cfg, headerDb: db, bc: body, headerCache: headers, calcOrderCache: orders,
		engine: []consensus.Engine{lockupBudgetEngine{}}, logger: log.Global}
	hc.AddToCalcOrderCache(parent.Hash(), common.PRIME_CTX, big.NewInt(1))
	body.processor = &StateProcessor{config: &cfg, hc: hc, logger: log.Global, vmConfig: vm.Config{}}
	w := &worker{hc: hc, chainConfig: &cfg, logger: log.Global}
	makeClaim := func(key *ecdsa.PrivateKey, gasPrice int64, etxGas uint64) *types.Transaction {
		input := make([]byte, 54)
		copy(input[:20], beneficiary.Bytes())
		copy(input[20:40], destination.Bytes())
		binary.BigEndian.PutUint32(input[41:45], 1)
		binary.BigEndian.PutUint64(input[45:53], etxGas)
		tx, err := types.SignNewTx(key, types.MakeSigner(&cfg, header.Number(common.ZONE_CTX)), &types.QuaiTx{
			ChainID: cfg.ChainID, Nonce: 0, GasPrice: big.NewInt(gasPrice), Gas: 2_500_000,
			To: &owner, Value: big.NewInt(0), Data: input,
			AccessList: types.AccessList{{Address: lockup}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	txA := makeClaim(keyA, 2, overGas)
	txB := makeClaim(keyB, 1, params.TxGas)
	signer := types.MakeSigner(&cfg, header.Number(common.ZONE_CTX))
	pending := types.NewTransactionsByPriceAndNonce(signer, nil, map[common.AddressBytes]types.Transactions{
		senderA.Bytes20(): {txA}, senderB.Bytes20(): {txB},
	})
	if pending.Peek() == nil || pending.Peek().Hash() != txA.Hash() {
		t.Fatal("tx_A was not selected first")
	}
	pending.PopNoSort()
	if pending.Peek() == nil || pending.Peek().Hash() != txB.Hash() {
		t.Fatal("tx_B was not selected second")
	}

	batch := db.NewBatch()
	batch.SetPending(true)
	parentOrder := common.PRIME_CTX
	env := &environment{state: statedb, batch: batch, wo: header,
		gasPool: new(types.GasPool).AddGas(8_000_000), parentOrder: &parentOrder,
		primaryCoinbase: owner,
		etxRLimit:       params.ETXRLimitMin, etxPLimit: params.ETXPLimitMin,
		quaiFees: big.NewInt(0)}
	statedb.Prepare(txA.Hash(), 0)
	statedb.AddAddressToAccessList(lockup.Bytes20())
	_, _, err = w.commitTransaction(env, parent, txA)
	if err == nil || !strings.Contains(err.Error(), wantError) || len(env.txs) != 0 {
		t.Fatalf("tx_A did not reach worker budget error: included=%d err=%v", len(env.txs), err)
	}
	remaining, _, _, _ := rawdb.ReadCoinbaseLockup(db, batch, owner, beneficiary, 0, 1)
	if remaining.Cmp(amount) != 0 {
		t.Fatalf("worker kept a rejected claim's batch delete: %v", remaining)
	}

	cleanState := statedb.Copy()
	statedb.Prepare(txB.Hash(), 0)
	statedb.AddAddressToAccessList(lockup.Bytes20())
	_, _, err = w.commitTransaction(env, parent, txB)
	if err != nil || len(env.txs) != 1 || env.txs[0].Hash() != txB.Hash() || len(env.receipts) != 1 || env.receipts[0].Status != types.ReceiptStatusSuccessful || len(env.receipts[0].OutboundEtxs) != 1 {
		t.Fatalf("tx_B in worker batch: included=%v receipts=%v err=%v", env.txs, env.receipts, err)
	}

	// Replay only the included tx against a clean batch, as a validator does.
	cleanBatch := db.NewBatch()
	cleanBatch.SetPending(true)
	cleanGas := new(types.GasPool).AddGas(8_000_000)
	var cleanUsedGas, cleanUsedState uint64
	cleanRegion, cleanPrime := params.ETXRLimitMin, params.ETXPLimitMin
	cleanState.Prepare(txB.Hash(), 0)
	cleanState.AddAddressToAccessList(lockup.Bytes20())
	cleanReceipt, _, err := ApplyTransaction(&cfg, parent, common.PRIME_CTX, hc, nil, cleanGas, cleanState, header, txB, &cleanUsedGas, &cleanUsedState, vm.Config{}, &cleanRegion, &cleanPrime, cleanBatch, log.Global)
	if err != nil || cleanReceipt == nil || cleanReceipt.Status != types.ReceiptStatusSuccessful || len(cleanReceipt.OutboundEtxs) != 1 {
		t.Fatalf("validator replay of tx_B: receipt=%v err=%v", cleanReceipt, err)
	}
	workerReceiptRoot := types.DeriveSha(types.Receipts(env.receipts), trie.NewStackTrie(nil))
	replayReceiptRoot := types.DeriveSha(types.Receipts{cleanReceipt}, trie.NewStackTrie(nil))
	if workerReceiptRoot != replayReceiptRoot {
		t.Fatal("worker and clean replay have different receipt roots")
	}
	if len(env.etxs) != len(cleanReceipt.OutboundEtxs) {
		t.Fatalf("worker and clean replay emitted different ETX counts: %d != %d", len(env.etxs), len(cleanReceipt.OutboundEtxs))
	}
	t.Log("rejected claim restored the lockup; worker and clean replay agree on tx_B")
}
