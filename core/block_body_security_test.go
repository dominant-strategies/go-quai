package core

import (
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

func TestWriteBlockRejectsUnserializableQiBodyBeforeStorage(t *testing.T) {
	key := make([]byte, 65)
	key[0] = 4
	key[64] = 1
	tx := types.NewTx(&types.QiTx{
		ChainID: big.NewInt(9),
		TxIn: types.TxIns{{PreviousOutPoint: types.OutPoint{
			TxHash: common.Hash{4}, Index: 1,
		}, PubKey: key}},
		Signature: new(schnorr.Signature),
	})
	header := &types.WorkObjectHeader{}
	header.SetLocation(common.Location{0, 0})
	body := types.EmptyWorkObjectBody()
	body.SetTransactions(types.Transactions{tx})
	block := types.NewWorkObject(header, body, nil)
	c := &Core{sl: &Slice{hc: &HeaderChain{bc: &BodyDb{chainConfig: params.TestChainConfig}}}, logger: log.Global}
	// WriteBlock must return before the raw database's Fatal-on-encode path.
	c.WriteBlock(block)
}
