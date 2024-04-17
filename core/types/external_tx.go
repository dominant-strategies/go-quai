package types

import (
	"errors"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
)

type ExternalTx struct {
	OriginatingTxHash common.Hash
	ETXIndex          uint16
	Gas               uint64
	To                *common.Address `rlp:"nilString"` // nil means contract creation
	Value             *big.Int
	Data              []byte
	AccessList        AccessList
	Sender            common.Address

	// External transactions do not have signatures. The origin chain will
	// emit an ETX, and consequently 'authorization' of this transaction comes
	// from chain consensus and not from an account signature.
	//
	// Before an ETX can be processed at the destination chain, the ETX must
	// become referencable through block manifests, thereby guaranteeing that
	// the origin chain indeed confirmed emission of that ETX.
}

// PendingEtxsRollup is Header and EtxRollups of that header that should
// be forward propagated
type PendingEtxsRollup struct {
	Header     *WorkObject  `json:"header" gencodec:"required"`
	EtxsRollup Transactions `json:"etxsrollup" gencodec:"required"`
}

func (p *PendingEtxsRollup) IsValid(hasher TrieHasher) bool {
	if p == nil || p.Header == nil || p.EtxsRollup == nil {
		return false
	}
	return DeriveSha(p.EtxsRollup, hasher) == p.Header.EtxRollupHash()
}

// ProtoEncode encodes the PendingEtxsRollup to protobuf format.
func (p *PendingEtxsRollup) ProtoEncode() (*ProtoPendingEtxsRollup, error) {
	header, err := p.Header.ProtoEncode(PEtxObject)
	if err != nil {
		return nil, err
	}
	etxRollup, err := p.EtxsRollup.ProtoEncode()
	if err != nil {
		return nil, err
	}
	return &ProtoPendingEtxsRollup{
		Header:     header,
		EtxsRollup: etxRollup,
	}, nil
}

// ProtoDecode decodes the protobuf to a PendingEtxsRollup representation.
func (p *PendingEtxsRollup) ProtoDecode(protoPendingEtxsRollup *ProtoPendingEtxsRollup, location common.Location) error {
	if protoPendingEtxsRollup.Header == nil {
		return errors.New("header is nil in ProtoDecode")
	}
	p.Header = new(WorkObject)
	err := p.Header.ProtoDecode(protoPendingEtxsRollup.GetHeader(), location, PEtxObject)
	if err != nil {
		return err
	}
	p.EtxsRollup = Transactions{}
	err = p.EtxsRollup.ProtoDecode(protoPendingEtxsRollup.GetEtxsRollup(), location)
	if err != nil {
		return err
	}
	return nil
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
	Header *WorkObject  `json:"header" gencodec:"required"`
	Etxs   Transactions `json:"etxs"   gencodec:"required"`
}

func (p *PendingEtxs) IsValid(hasher TrieHasher) bool {
	if p == nil || p.Header == nil || p.Etxs == nil {
		return false
	}
	return DeriveSha(p.Etxs, hasher) == p.Header.EtxHash()
}

// ProtoEncode encodes the PendingEtxs to protobuf format.
func (p *PendingEtxs) ProtoEncode() (*ProtoPendingEtxs, error) {
	header, err := p.Header.ProtoEncode(PEtxObject)
	if err != nil {
		return nil, err
	}
	etxs, err := p.Etxs.ProtoEncode()
	if err != nil {
		return nil, err
	}
	return &ProtoPendingEtxs{
		Header: header,
		Etxs:   etxs,
	}, nil
}

// ProtoDecode decodes the protobuf to a PendingEtxs representation.
func (p *PendingEtxs) ProtoDecode(protoPendingEtxs *ProtoPendingEtxs, location common.Location) error {
	if protoPendingEtxs.Header == nil {
		return errors.New("header is nil in ProtoDecode")
	}
	p.Header = new(WorkObject)
	err := p.Header.ProtoDecode(protoPendingEtxs.GetHeader(), location, PEtxObject)
	if err != nil {
		return err
	}
	p.Etxs = Transactions{}
	err = p.Etxs.ProtoDecode(protoPendingEtxs.GetEtxs(), location)
	if err != nil {
		return err
	}
	return nil
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *ExternalTx) copy() TxData {
	cpy := &ExternalTx{
		To:                tx.To, // TODO: copy pointed-to address
		Data:              common.CopyBytes(tx.Data),
		Gas:               tx.Gas,
		OriginatingTxHash: tx.OriginatingTxHash,
		ETXIndex:          tx.ETXIndex,
		Sender:            tx.Sender,

		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	return cpy
}

// accessors for innerTx.
func (tx *ExternalTx) txType() byte                   { return ExternalTxType }
func (tx *ExternalTx) chainID() *big.Int              { panic("external TX does not have chainid") }
func (tx *ExternalTx) protected() bool                { return true }
func (tx *ExternalTx) accessList() AccessList         { return tx.AccessList }
func (tx *ExternalTx) data() []byte                   { return tx.Data }
func (tx *ExternalTx) gas() uint64                    { return tx.Gas }
func (tx *ExternalTx) gasFeeCap() *big.Int            { return new(big.Int) } // placeholder
func (tx *ExternalTx) gasTipCap() *big.Int            { return new(big.Int) } // placeholder
func (tx *ExternalTx) gasPrice() *big.Int             { return new(big.Int) } // placeholder
func (tx *ExternalTx) value() *big.Int                { return tx.Value }
func (tx *ExternalTx) to() *common.Address            { return tx.To }
func (tx *ExternalTx) etxSender() common.Address      { return tx.Sender }
func (tx *ExternalTx) originatingTxHash() common.Hash { return tx.OriginatingTxHash }
func (tx *ExternalTx) etxIndex() uint16               { return tx.ETXIndex }
func (tx *ExternalTx) nonce() uint64                  { panic("external TX does not have nonce") }
func (tx *ExternalTx) etxGasLimit() uint64            { panic("external TX does not have etxGasLimit") }
func (tx *ExternalTx) etxGasPrice() *big.Int          { panic("external TX does not have etxGasPrice") }
func (tx *ExternalTx) etxGasTip() *big.Int            { panic("external TX does not have etxGasTip") }
func (tx *ExternalTx) etxData() []byte                { panic("external TX does not have etxData") }
func (tx *ExternalTx) etxAccessList() AccessList      { panic("external TX does not have etxAccessList") }
func (tx *ExternalTx) txIn() TxIns                    { panic("external TX does not have txIn") }
func (tx *ExternalTx) txOut() TxOuts                  { panic("external TX does not have txOut") }
func (tx *ExternalTx) getSchnorrSignature() *schnorr.Signature {
	panic("external TX does not have getSchnorrSignature")
}

func (tx *ExternalTx) getEcdsaSignatureValues() (v, r, s *big.Int) {
	// Signature values are ignored for external transactions
	return nil, nil, nil
}

func (tx *ExternalTx) setEcdsaSignatureValues(chainID, v, r, s *big.Int) {
	// Signature values are ignored for external transactions
}

func (tx *ExternalTx) setTo(to common.Address) {
	panic("You should not set To on an external transaction")
}
