package types

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
)

type ExternalTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nilString"` // nil means contract creation
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

// PendingEtxs are ETXs which have been emitted from the zone which produced
// the given block. Specifically, it contains the collection of ETXs emitted
// since our prior coincident with our sub in that slice. In Prime context, our
// subordinate will be a region node, so the Etxs list will contain the rollup
// of ETXs emitted from each zone block since the zone's prior coincidence with
// the region. In Region context, our subordinate chain will be the zone
// itself, so the Etxs list will just contain the ETXs emitted directly in that
// zone block (a.k.a. a singleton).
type PendingEtxs struct {
	Header *Header      `json:"header" gencodec:"required"`
	Etxs   Transactions `json:"etxs"   gencodec:"required"`
}

func (p *PendingEtxs) IsValid(hasher TrieHasher) bool {
	nodeCtx := common.NodeLocation.Context()
	if p == nil || p.Header == nil || p.Etxs == nil {
		return false
	}
	switch nodeCtx {
	case common.PRIME_CTX:
		// In prime context, the pending etx object contains the rollup of ETXs
		// emitted in each zone block since the zone's prior coincidence with the
		// region.
		return DeriveSha(p.Etxs, hasher) == p.Header.EtxRollupHash()
	case common.REGION_CTX:
		// In region context, the pending etx object contains the singleton of ETXs
		// emitted by the zone.
		return DeriveSha(p.Etxs, hasher) == p.Header.EtxHash()
	default:
		// PendingEtxs cannot exist in zone context
		return false
	}
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
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
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
func (tx *ExternalTx) txType() byte              { return ExternalTxType }
func (tx *ExternalTx) chainID() *big.Int         { return tx.ChainID }
func (tx *ExternalTx) protected() bool           { return true }
func (tx *ExternalTx) accessList() AccessList    { return tx.AccessList }
func (tx *ExternalTx) data() []byte              { return tx.Data }
func (tx *ExternalTx) gas() uint64               { return tx.Gas }
func (tx *ExternalTx) gasFeeCap() *big.Int       { return tx.GasFeeCap }
func (tx *ExternalTx) gasTipCap() *big.Int       { return tx.GasTipCap }
func (tx *ExternalTx) gasPrice() *big.Int        { return tx.GasFeeCap }
func (tx *ExternalTx) value() *big.Int           { return tx.Value }
func (tx *ExternalTx) nonce() uint64             { return tx.Nonce }
func (tx *ExternalTx) to() *common.Address       { return tx.To }
func (tx *ExternalTx) etxGasLimit() uint64       { panic("external TX does not have etxGasLimit") }
func (tx *ExternalTx) etxGasPrice() *big.Int     { panic("external TX does not have etxGasPrice") }
func (tx *ExternalTx) etxGasTip() *big.Int       { panic("external TX does not have etxGasTip") }
func (tx *ExternalTx) etxData() []byte           { panic("external TX does not have etxData") }
func (tx *ExternalTx) etxAccessList() AccessList { panic("external TX does not have etxAccessList") }

func (tx *ExternalTx) rawSignatureValues() (v, r, s *big.Int) {
	// Signature values are ignored for external transactions
	return nil, nil, nil
}

func (tx *ExternalTx) setSignatureValues(chainID, v, r, s *big.Int) {
	// Signature values are ignored for external transactions
}
