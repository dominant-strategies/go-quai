package core

import (
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

func TestValidateQiTxInputsRejectsDuplicateBeforeUTXORead(t *testing.T) {
	outpoint := types.OutPoint{TxHash: common.Hash{1}, Index: 2}
	tx := types.NewTx(&types.QiTx{TxIn: types.TxIns{
		{PreviousOutPoint: outpoint},
		{PreviousOutPoint: outpoint},
	}})
	_, err := ValidateQiTxInputs(tx, nil, nil, nil, nil, common.Location{0, 0}, *tx.ChainId())
	if err == nil || !strings.Contains(err.Error(), "duplicate Qi input") {
		t.Fatalf("got %v, want duplicate Qi input", err)
	}
}
