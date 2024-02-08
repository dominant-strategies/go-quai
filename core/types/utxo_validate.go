package types

import (
	"github.com/dominant-strategies/go-quai/common"
)

// IsCoinBaseTx determines whether or not a transaction is a coinbase.  A coinbase
// is a special transaction created by miners that has no inputs.  This is
// represented in the block chain by a transaction with a single input that has
// a previous output transaction index set to the maximum value along with a
// zero hash.
//
// This function only differs from IsCoinBase in that it works with a raw wire
// transaction as opposed to a higher level util transaction.
func IsCoinBaseTx(tx *Transaction, parentHash common.Hash) bool {
	if tx == nil || tx.inner == nil || tx.Type() != QiTxType {
		return false
	}
	// A coin base must only have one transaction input.
	if len(tx.inner.txIn()) != 1 {
		return false
	}

	// The previous output of a coin base must have a max value index and a zero hash.
	prevOut := &tx.inner.txIn()[0].PreviousOutPoint
	if prevOut.Index != MaxOutputIndex || prevOut.TxHash != parentHash {
		return false
	}

	return true
}
