package types

// IsCoinBaseTx determines whether or not a transaction is a coinbase.  A coinbase
// is a special transaction created by miners that has no inputs.  This is
// represented in the block chain by a transaction with a single input that has
// a previous output transaction index set to the maximum value along with a
// zero hash.
//
// This function only differs from IsCoinBase in that it works with a raw wire
// transaction as opposed to a higher level util transaction.
func IsCoinBaseTx(tx *Transaction) bool {
	if tx == nil || tx.inner == nil {
		return false
	}
	if tx.Type() == ExternalTxType {
		return tx.EtxType() == CoinbaseType
	}
	return false
}
