package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

type securityQiChain struct {
	blockChain
	current *types.WorkObject
}

func (c securityQiChain) CurrentBlock() *types.WorkObject { return c.current }

func TestRemoteQiBatchReturnsOneErrorPerTransaction(t *testing.T) {
	current := types.NewWorkObject(&types.WorkObjectHeader{}, types.EmptyWorkObjectBody(), nil)
	current.WorkObjectHeader().SetLocation(common.Location{0, 0})
	pool := &TxPool{
		chain:       securityQiChain{current: current},
		db:          rawdb.NewMemoryDatabase(log.Global),
		chainconfig: params.TestChainConfig,
		signer:      types.NewSigner(params.TestChainConfig.ChainID, common.Location{0, 0}),
		all:         newTxLookup(),
		logger:      log.Global,
	}
	pool.qiPool, _ = lru.New[common.Hash, *types.TxWithMinerFee](10)
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	badDestination := make([]byte, 20)
	badDestination[0] = 0xff
	qi := types.NewTx(&types.QiTx{
		ChainID: new(big.Int).Set(params.TestChainConfig.ChainID),
		TxIn: types.TxIns{{PreviousOutPoint: types.OutPoint{
			TxHash: common.Hash{1}, Index: 1,
		}, PubKey: crypto.FromECDSAPub(&private.PublicKey)}},
		TxOut:     types.TxOuts{*types.NewTxOut(0, badDestination, big.NewInt(0))},
		Signature: new(schnorr.Signature),
	})
	if _, err := qi.ProtoEncode(); err != nil {
		t.Fatalf("test transaction is not serializable as a workshare: %v", err)
	}
	errs := pool.addTxs([]*types.Transaction{qi}, false, false)
	if len(errs) != 1 || errs[0] == nil || !strings.Contains(errs[0].Error(), "inactive chain") {
		t.Fatalf("got %v, want one inactive-chain error", errs)
	}
}
