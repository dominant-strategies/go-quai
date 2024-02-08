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
