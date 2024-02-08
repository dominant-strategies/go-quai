package types

// TxoFlags is a bitmask defining additional information and state for a
// transaction output in a utxo view.
type TxoFlags uint8

const (
	// TfCoinBase indicates that a txout was contained in a coinbase tx.
	TfCoinBase TxoFlags = 1 << iota

	// TfSpent indicates that a txout is spent.
	TfSpent

	// TfModified indicates that a txout has been modified since it was
	// loaded.
	TfModified
)

// UtxoEntry houses details about an individual transaction output in a utxo
// view such as whether or not it was contained in a coinbase tx, the height of
// the block that contains the tx, whether or not it is spent, its public key
// script, and how much it pays.
type UtxoEntry struct {
	// NOTE: Additions, deletions, or modifications to the order of the
	// definitions in this struct should not be changed without considering
	// how it affects alignment on 64-bit platforms.  The current order is
	// specifically crafted to result in minimal padding.  There will be a
	// lot of these in memory, so a few extra bytes of padding adds up.

	Amount      uint64
	Address     []byte // The address of the output holder.
	BlockHeight uint64 // Height of block containing tx.

	// packedFlags contains additional info about output such as whether it
	// is a coinbase, whether it is spent, and whether it has been modified
	// since it was loaded.  This approach is used in order to reduce memory
	// usage since there will be a lot of these in memory.
	PackedFlags TxoFlags
}

// isModified returns whether or not the output has been modified since it was
// loaded.
func (entry *UtxoEntry) IsModified() bool {
	return entry.PackedFlags&TfModified == TfModified
}

// IsCoinBase returns whether or not the output was contained in a coinbase
// transaction.
func (entry *UtxoEntry) IsCoinBase() bool {
	return entry.PackedFlags&TfCoinBase == TfCoinBase
}

// IsSpent returns whether or not the output has been spent based upon the
// current state of the unspent transaction output view it was obtained from.
func (entry *UtxoEntry) IsSpent() bool {
	return entry.PackedFlags&TfSpent == TfSpent
}

// Spend marks the output as spent.  Spending an output that is already spent
// has no effect.
func (entry *UtxoEntry) Spend() {
	// Nothing to do if the output is already spent.
	if entry.IsSpent() {
		return
	}

	// Mark the output as spent and modified.
	entry.PackedFlags |= TfSpent | TfModified
}

// Clone returns a shallow copy of the utxo entry.
func (entry *UtxoEntry) Clone() *UtxoEntry {
	if entry == nil {
		return nil
	}

	return &UtxoEntry{
		Amount:      entry.Amount,
		Address:     entry.Address,
		BlockHeight: entry.BlockHeight,
		PackedFlags: entry.PackedFlags,
	}
}

// NewUtxoEntry returns a new UtxoEntry built from the arguments.
func NewUtxoEntry(
	txOut *TxOut, blockHeight uint64, isCoinbase bool) *UtxoEntry {
	var cbFlag TxoFlags
	if isCoinbase {
		cbFlag |= TfCoinBase
	}

	return &UtxoEntry{
		Amount:      txOut.Value,
		Address:     txOut.Address,
		BlockHeight: blockHeight,
		PackedFlags: cbFlag,
	}
}

// UtxoViewpoint represents a view into the set of unspent transaction outputs
// from a specific point of view in the chain.  For example, it could be for
// the end of the main chain, some point in the history of the main chain, or
// down a side chain.
//
// The unspent outputs are needed by other transactions for things such as
// script validation and double spend prevention.
type UtxoViewpoint struct {
	Entries map[OutPoint]*UtxoEntry
}

// LookupEntry returns information about a given transaction output according to
// the current state of the view.  It will return nil if the passed output does
// not exist in the view or is otherwise not available such as when it has been
// disconnected during a reorg.
func (view *UtxoViewpoint) LookupEntry(outpoint OutPoint) *UtxoEntry {
	return view.Entries[outpoint]
}

func (view *UtxoViewpoint) AddEntry(outpoints []OutPoint, i int, entry *UtxoEntry) {
	view.Entries[outpoints[i]] = entry
}

// addTxOut adds the specified output to the view if it is not provably
// unspendable.  When the view already has an entry for the output, it will be
// marked unspent.  All fields will be updated for existing entries since it's
// possible it has changed during a reorg.
func (view *UtxoViewpoint) addTxOut(outpoint OutPoint, txOut *TxOut, isCoinBase bool, blockHeight uint64) {
	// Update existing entries.  All fields are updated because it's
	// possible (although extremely unlikely) that the existing entry is
	// being replaced by a different transaction with the same hash.  This
	// is allowed so long as the previous transaction is fully spent.
	entry := view.LookupEntry(outpoint)
	if entry == nil {
		entry = new(UtxoEntry)
		view.Entries[outpoint] = entry
	}

	entry.Amount = txOut.Value
	entry.Address = txOut.Address
	entry.BlockHeight = blockHeight
	entry.PackedFlags = TfModified
	if isCoinBase {
		entry.PackedFlags |= TfCoinBase
	}
}

// AddTxOuts adds all outputs in the passed transaction which are not provably
// unspendable to the view.  When the view already has entries for any of the
// outputs, they are simply marked unspent.  All fields will be updated for
// existing entries since it's possible it has changed during a reorg.
func (view *UtxoViewpoint) AddTxOuts(tx *Transaction, blockHeight uint64) {
	// Loop all of the transaction outputs and add those which are not
	// provably unspendable.
	isCoinBase := IsCoinBaseTx(tx)
	prevOut := OutPoint{TxHash: tx.Hash()}
	for txOutIdx, txOut := range tx.inner.txOut() {
		// Update existing entries.  All fields are updated because it's
		// possible (although extremely unlikely) that the existing
		// entry is being replaced by a different transaction with the
		// same hash.  This is allowed so long as the previous
		// transaction is fully spent.
		prevOut.Index = uint32(txOutIdx)
		view.addTxOut(prevOut, txOut, isCoinBase, blockHeight)
	}
}

// NewUtxoViewpoint returns a new empty unspent transaction output view.
func NewUtxoViewpoint() *UtxoViewpoint {
	return &UtxoViewpoint{
		Entries: make(map[OutPoint]*UtxoEntry),
	}
}

// connectTransaction updates the view by adding all new utxos created by the
// passed transaction and marking all utxos that the transactions spend as
// spent.  In addition, when the 'stxos' argument is not nil, it will be updated
// to append an entry for each spent txout.  An error will be returned if the
// view does not contain the required utxos.
func (view *UtxoViewpoint) ConnectTransaction(tx *Transaction, blockHeight uint64, stxos *[]SpentTxOut) error {
	// Coinbase transactions don't have any inputs to spend.
	if IsCoinBaseTx(tx) {
		// Add the transaction's outputs as available utxos.
		view.AddTxOuts(tx, blockHeight)
		return nil
	}

	// Spend the referenced utxos by marking them spent in the view and,
	// if a slice was provided for the spent txout details, append an entry
	// to it.
	for _, txIn := range tx.inner.txIn() {
		// Ensure the referenced utxo exists in the view.  This should
		// never happen unless there is a bug is introduced in the code.
		entry := view.Entries[txIn.PreviousOutPoint]
		if entry == nil {
			return nil
		}

		// Only create the stxo details if requested.
		if stxos != nil {
			// Populate the stxo details using the utxo entry.
			var stxo = SpentTxOut{
				Amount:     entry.Amount,
				Address:    entry.Address,
				Height:     entry.BlockHeight,
				IsCoinBase: entry.IsCoinBase(),
			}
			*stxos = append(*stxos, stxo)
		}

		// Mark the entry as spent.  This is not done until after the
		// relevant details have been accessed since spending it might
		// clear the fields from memory in the future.
		entry.Spend()
	}

	// Add the transaction's outputs as available utxos.
	view.AddTxOuts(tx, blockHeight)
	return nil
}
