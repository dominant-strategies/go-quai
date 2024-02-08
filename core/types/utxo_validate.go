package types

import (
	"errors"
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
func CheckTransactionInputs(tx *Transaction, utxoView *UtxoViewpoint) (uint64, error) {
	// Coinbase transactions have no inputs.
	if IsCoinBaseTx(tx) {
		return 0, nil
	}

	var totalQitIn uint64
	for index, txIn := range tx.inner.txIn() {
		// Ensure the referenced input transaction is available.
		utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
		if utxo == nil || utxo.IsSpent() { // why comment this out?
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d either does not exist or "+
				"has already been spent", txIn.PreviousOutPoint,
				tx.Hash(), index)
			return 0, errors.New(str)
		}

		// Ensure the transaction amounts are in range.  Each of the
		// output values of the input transactions must not be negative
		// or more than the max allowed per transaction.  All amounts in
		// a transaction are in a unit value known as a Qit.  One
		// bitcoin is a quantity of Qit as defined by the
		// QitPerBitcoin constant.
		originTxQit := utxo.Amount

		// The total of all outputs must not be more than the max
		// allowed per transaction.  Also, we could potentially overflow
		// the accumulator so check for overflow.
		lastQitIn := totalQitIn
		totalQitIn += originTxQit
		if totalQitIn < lastQitIn ||
			totalQitIn > math.MaxUint64 {
			str := fmt.Sprintf("total value of all transaction "+
				"inputs is %v which is higher than max "+
				"allowed value", totalQitIn)
			return 0, errors.New(str)
		}
	}

	// Calculate the total output amount for this transaction.  It is safe
	// to ignore overflow and out of range errors here because those error
	// conditions would have already been caught by checkTransactionSanity.
	var totalQitOut uint64
	for _, txOut := range tx.inner.txOut() {
		totalQitOut += txOut.Value
		if _, err := common.BytesToAddress(txOut.Address, utxoView.Location).InternalAddress(); err != nil {
			return 0, errors.New("invalid output address: " + err.Error())
		}
	}

	// Ensure the transaction does not spend more than its inputs.
	if totalQitIn < totalQitOut {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", tx.Hash(), totalQitIn, totalQitOut)
		return 0, errors.New(str)
	}

	// NOTE: bitcoind checks if the transaction fees are < 0 here, but that
	// is an impossible condition because of the check above that ensures
	// the inputs are >= the outputs.
	txFeeInQit := totalQitIn - totalQitOut
	return txFeeInQit, nil
}
