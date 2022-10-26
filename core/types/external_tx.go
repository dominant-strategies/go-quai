package types

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
)

type ExternalTx struct {
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nil"` // nil means contract creation
	Value      *big.Int
	Data       []byte
	AccessList AccessList
	Sender     common.Address

	// External transactions do not have signatures. The origin chain will
	// emit an ETX, and consequently 'authorization' of this transaction comes
	// from chain consensus and not from an account signature.
	//
	// Before an ETX can be processed at the destination chain, the ETX must
	// become referencable through block manifests, thereby guaranteeing that
	// the origin chain indeed confirmed emission of that ETX.
}

// PendingEtxs are ETXs which have been emitted from a block, but are not yet
// referencable through the block manifest. Once a coincident block is found,
// the dominant nodes will collect all pending ETXs from each block listed in
// the manifest, and mark them referencable so they may be included at the
// destination chain.
type PendingEtxs struct {
	Header *Header        `json:"header" gencodec:"required"`
	Etxs   []Transactions `json:"etxs"   gencodec:"required"`
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *ExternalTx) copy() TxData {
	cpy := &ExternalTx{
		Nonce:  tx.Nonce,
		To:     tx.To, // TODO: copy pointed-to address
		Data:   common.CopyBytes(tx.Data),
		Gas:    tx.Gas,
		Sender: tx.Sender,

		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	return cpy
}

// accessors for innerTx.
func (tx *ExternalTx) txType() byte                { return ExternalTxType }
func (tx *ExternalTx) chainID() *big.Int           { return nil }
func (tx *ExternalTx) protected() bool             { return true }
func (tx *ExternalTx) accessList() AccessList      { return tx.AccessList }
func (tx *ExternalTx) data() []byte                { return tx.Data }
func (tx *ExternalTx) gas() uint64                 { return tx.Gas }
func (tx *ExternalTx) gasFeeCap() *big.Int         { return tx.GasFeeCap }
func (tx *ExternalTx) gasTipCap() *big.Int         { return tx.GasTipCap }
func (tx *ExternalTx) gasPrice() *big.Int          { return tx.GasFeeCap }
func (tx *ExternalTx) value() *big.Int             { return tx.Value }
func (tx *ExternalTx) nonce() uint64               { return tx.Nonce }
func (tx *ExternalTx) to() *common.Address         { return tx.To }
func (tx *ExternalTx) toChain() *common.Location   { return tx.To.Location() }
func (tx *ExternalTx) fromChain() *common.Location { return tx.Sender.Location() }

func (tx *ExternalTx) rawSignatureValues() (v, r, s *big.Int) {
	// Signature values are ignored for external transactions
	return nil, nil, nil
}

func (tx *ExternalTx) setSignatureValues(chainID, v, r, s *big.Int) {
	// Signature values are ignored for external transactions
}
