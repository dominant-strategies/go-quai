package types

// SpentTxOut contains a spent transaction output and potentially additional
// contextual information such as whether or not it was contained in a coinbase
// transaction, the version of the transaction it was contained in, and which
// block height the containing transaction was included in.  As described in
// the comments above, the additional contextual information will only be valid
// when this spent txout is spending the last unspent output of the containing
// transaction.
type SpentTxOut struct {
	// Amount is the amount of the output.
	Denomination uint8

	// Address is the output holder's address.
	Address []byte

	// Height is the height of the block containing the creating tx.
	Height uint64

	// Denotes if the creating tx is a coinbase.
	IsCoinBase bool
}

// countSpentOutputs returns the number of utxos the passed block spends.
func CountSpentOutputs(block *Block) int {
	transactions := block.QiTransactions()
	if len(transactions) == 0 {
		return 0
	}
	if IsCoinBaseTx(transactions[0]) {
		transactions = transactions[1:]
	}
	// Exclude the coinbase transaction since it can't spend anything.
	var numSpent int
	for _, tx := range transactions {
		numSpent += len(tx.TxIn())
	}

	return numSpent
}
