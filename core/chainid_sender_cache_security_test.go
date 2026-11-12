package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/crypto/multiset"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

func TestForeignChainSenderCacheCannotAffectPoolOrMessage(t *testing.T) {
	location := common.Location{0, 0}
	config := *params.TestChainConfig
	config.Location = location
	localSigner := types.NewSigner(config.ChainID, location)
	foreignID := new(big.Int).Add(config.ChainID, big.NewInt(1))
	foreignSigner := types.NewSigner(foreignID, location)

	var keyAddr common.Address
	var key *ecdsa.PrivateKey
	for i := 0; i < 100000; i++ {
		secret, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		keyAddr = crypto.PubkeyToAddress(secret.PublicKey, location)
		if keyAddr.IsInQuaiLedgerScope() && keyAddr.Location().Equal(location) {
			key = secret
			break
		}
	}
	if key == nil {
		t.Fatal("could not find Quai-scope key")
	}
	makeSigned := func(chainID *big.Int, signer types.Signer) *types.Transaction {
		tx := types.NewTx(&types.QuaiTx{ChainID: new(big.Int).Set(chainID), Nonce: 0, GasPrice: big.NewInt(1), Gas: 21000, To: &keyAddr, Value: big.NewInt(1), AccessList: types.AccessList{}, V: new(big.Int), R: new(big.Int), S: new(big.Int), ParentHash: &common.Hash{}, MixHash: &common.Hash{}, WorkNonce: new(types.BlockNonce)})
		signed, err := types.SignTx(tx, signer, key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	foreign := makeSigned(foreignID, foreignSigner)
	internal, err := keyAddr.InternalAndQuaiAddress()
	if err != nil {
		t.Fatal(err)
	}
	foreign.Hash() // HashWithError populates tx.from under the foreign signer.
	if cached := foreign.From(location); cached == nil || !cached.Equal(keyAddr) {
		t.Fatal("foreign transaction did not populate its untrusted sender cache")
	}

	db := rawdb.NewMemoryDatabase(log.Global)
	statedb, err := state.New(types.EmptyRootHash, types.EmptyRootHash, new(big.Int), state.NewDatabase(db), state.NewDatabase(db), nil, location, log.Global)
	if err != nil {
		t.Fatal(err)
	}
	statedb.CreateAccount(internal)
	statedb.AddBalance(internal, big.NewInt(1_000_000))
	current := types.EmptyWorkObject(common.ZONE_CTX)
	current.Header().SetBaseFee(big.NewInt(1))
	current.Header().SetGasLimit(10_000_000)
	senders, err := lru.New[common.Hash, common.InternalAddress](16)
	if err != nil {
		t.Fatal(err)
	}
	pool := &TxPool{chainconfig: &config, chain: securityQiChain{current: current}, gasPrice: big.NewInt(1), signer: localSigner, currentState: statedb, currentMaxGas: current.GasLimit(), senders: senders, sendersCh: make(chan newSender, 2), logger: log.Global}
	if err := pool.validateTx(foreign); !errors.Is(err, ErrInvalidSender) {
		t.Fatalf("foreign transaction pool error = %v, want %v", err, ErrInvalidSender)
	}
	select {
	case sender := <-pool.sendersCh:
		t.Fatalf("foreign sender queued: %v", sender)
	default:
	}
	if _, found := pool.PeekSender(foreign.Hash()); found {
		t.Fatal("foreign sender reached the pool cache")
	}
	if _, err := foreign.AsMessageWithSender(localSigner, big.NewInt(1), &internal); !errors.Is(err, types.ErrInvalidChainId) {
		t.Fatalf("warm block message error = %v, want %v", err, types.ErrInvalidChainId)
	}

	// A cached sender must not override the signer even for a valid local tx.
	localTx := makeSigned(config.ChainID, localSigner)
	if err := pool.validateTx(localTx); err != nil {
		t.Fatalf("local transaction rejected: %v", err)
	}
	select {
	case queued := <-pool.sendersCh:
		if queued.hash != localTx.Hash() || queued.sender != internal {
			t.Fatalf("wrong local sender queued: %v", queued)
		}
	default:
		t.Fatal("local sender was not queued")
	}
	wrong := common.InternalAddress{}
	msg, err := localTx.AsMessageWithSender(localSigner, big.NewInt(1), &wrong)
	if err != nil || !msg.From().Equal(keyAddr) {
		t.Fatalf("local block message sender = %v, error = %v", msg.From(), err)
	}

	// The same foreign transaction must fail block processing with either
	// an empty pool cache or a cache entry from a rejected transaction.
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		t.Run(name+" block", func(t *testing.T) {
			blockDB := rawdb.NewMemoryDatabase(log.Global)
			parentStateDB := state.NewDatabase(blockDB)
			parentState, err := state.New(types.EmptyRootHash, types.EmptyRootHash, new(big.Int), parentStateDB, state.NewDatabase(blockDB), nil, location, log.Global)
			if err != nil {
				t.Fatal(err)
			}
			parentState.CreateAccount(internal)
			parentState.AddBalance(internal, big.NewInt(1_000_000))
			root, err := parentState.Commit(false)
			if err != nil {
				t.Fatal(err)
			}
			if err := parentStateDB.TrieDB().Commit(root, false, nil); err != nil {
				t.Fatal(err)
			}
			parent := types.EmptyWorkObject(common.ZONE_CTX)
			parent.WorkObjectHeader().SetLocation(location)
			parent.SetNumber(big.NewInt(0), common.ZONE_CTX)
			parent.WorkObjectHeader().SetDifficulty(big.NewInt(1_000_000_000_000))
			parent.Header().SetBaseFee(big.NewInt(1))
			parent.Header().SetGasLimit(10_000_000)
			parent.Header().SetExchangeRate(big.NewInt(1))
			parent.Header().SetEVMRoot(root)
			parent.Header().SetEtxSetRoot(types.EmptyRootHash)
			parent.Header().SetQuaiStateSize(big.NewInt(1_000_000))
			parent.WorkObjectHeader().SetHeaderHash(parent.Header().Hash())
			rawdb.WriteTermini(blockDB, parent.Hash(), types.EmptyTermini())
			rawdb.WriteWorkObject(blockDB, parent.Hash(), parent, types.BlockObject, common.ZONE_CTX)
			rawdb.WriteCanonicalHash(blockDB, parent.Hash(), 0)
			rawdb.WriteMultiSet(blockDB, parent.Hash(), multiset.New())

			body := NewTestBodyDb(blockDB)
			body.chainConfig = &config
			body.logger = log.Global
			poolSenders, _ := lru.New[common.Hash, common.InternalAddress](16)
			if warm {
				poolSenders.Add(foreign.Hash(), internal)
			}
			blockPool := &TxPool{chainconfig: &config, signer: localSigner, senders: poolSenders, logger: log.Global}
			headers, _ := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
			numbers, _ := lru.New[common.Hash, uint64](numberCacheLimit)
			powHashes, _ := lru.New[common.Hash, common.Hash](c_powCacheLimit)
			orders, _ := lru.New[common.Hash, calcOrderResponse](c_calcOrderCacheLimit)
			hc := &HeaderChain{config: &config, bc: body, headerDb: blockDB, headerCache: headers, numberCache: numbers, powHashCache: powHashes, calcOrderCache: orders, pool: blockPool, logger: log.Global}
			processor := &StateProcessor{config: &config, hc: hc, cacheConfig: defaultCacheConfig, stateCache: state.NewDatabase(blockDB), etxCache: state.NewDatabase(blockDB), logger: log.Global}
			block := types.EmptyWorkObject(common.ZONE_CTX)
			block.WorkObjectHeader().SetLocation(location)
			block.SetNumber(big.NewInt(1), common.ZONE_CTX)
			block.SetParentHash(parent.Hash(), common.ZONE_CTX)
			block.WorkObjectHeader().SetDifficulty(big.NewInt(1_000_000_000_000))
			block.WorkObjectHeader().SetPrimeTerminusNumber(big.NewInt(0))
			block.Header().SetPrimeTerminusHash(parent.Hash())
			block.WorkObjectHeader().SetPrimaryCoinbase(keyAddr)
			block.Header().SetBaseFee(big.NewInt(1))
			block.Header().SetGasLimit(10_000_000)
			block.Header().SetEVMRoot(types.EmptyRootHash)
			block.Header().SetEtxSetRoot(types.EmptyRootHash)
			block.Header().SetQuaiStateSize(big.NewInt(1_000_000))
			block.Body().SetTransactions(types.Transactions{foreign})
			_, _, _, _, _, _, _, _, _, err = processor.Process(block, blockDB.NewBatch())
			if !errors.Is(err, types.ErrInvalidChainId) {
				t.Fatalf("%s block error = %v, want %v", name, err, types.ErrInvalidChainId)
			}
		})
	}
}
