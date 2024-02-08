package types

import (
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/math"
)

// IsCoinBaseTx determines whether or not a transaction is a coinbase.  A coinbase
// is a special transaction created by miners that has no inputs.  This is
// represented in the block chain by a transaction with a single input that has
// a previous output transaction index set to the maximum value along with a
// zero hash.
//
// This function only differs from IsCoinBase in that it works with a raw wire
// transaction as opposed to a higher level util transaction.
func IsCoinBaseTx(msgTx *Transaction) bool {
	// A coin base must only have one transaction input.
	if len(msgTx.inner.txIn()) != 1 {
		return false
	}

	// The previous output of a coin base must have a max value index and
	// a non-zero hash.
	prevOut := &msgTx.inner.txIn()[0].PreviousOutPoint
	if (prevOut.Index != math.MaxUint32 || prevOut.TxHash == common.Hash{}) {
		return false
	}

	return true
}

// CheckTransactionInputs performs a series of checks on the inputs to a
// transaction to ensure they are valid.  An example of some of the checks
// include verifying all inputs exist, detecting double spends, validating all values and fees
// are in the legal range and the total output amount doesn't exceed the input
// amount, and verifying the signatures to prove the spender was the owner of
// the Qi and therefore allowed to spend them.  As it checks the inputs,
// it also calculates the total fees for the transaction and returns that value.
//
// NOTE: The transaction MUST have already been sanity checked with the
// CheckTransactionSanity function prior to calling this function.
func CheckTransactionInputs(tx *Transaction, txHeight uint64, utxoView *UtxoViewpoint) (uint64, error) {
	// Coinbase transactions have no inputs.
	if IsCoinBaseTx(tx) {
		return 0, nil
	}

	var totalQitIn uint64

	// Calculate the total output amount for this transaction.  It is safe
	// to ignore overflow and out of range errors here because those error
	// conditions would have already been caught by checkTransactionSanity.
	var totalQitOut uint64
	for _, txOut := range tx.inner.txOut() {
		totalQitOut += txOut.Value
	}

	// Ensure the transaction does not spend more than its inputs.
	if totalQitIn < totalQitOut {
		return 0, fmt.Errorf("transaction spends more than its inputs")
	}

	// NOTE: bitcoind checks if the transaction fees are < 0 here, but that
	// is an impossible condition because of the check above that ensures
	// the inputs are >= the outputs.
	txFeeInQit := totalQitIn - totalQitOut
	return txFeeInQit, nil
}
