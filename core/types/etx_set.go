package types

import (
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

// The EtxSet is a list of ETX hashes, ETXs and the block heights in which they became available.
// If no entry exists for a given ETX hash, then that ETX is not available.
type EtxSet struct {
	ETXs      []*Transaction
	ETXHashes []byte
}

func NewEtxSet() *EtxSet {
	etxSet := EtxSet{
		ETXs:      make([]*Transaction, 0, 0),
		ETXHashes: make([]byte, 0, 0),
	}
	return &etxSet
}

// updateInboundEtxs updates the set of inbound ETXs available to be mined into
// a block in this location. This method adds any new ETXs to the set and
// removes expired ETXs.
func (set *EtxSet) Update(newInboundEtxs Transactions, currentHeight uint64, nodeLocation common.Location) error {
	// Add new ETX entries to the inbound set
	for _, etx := range newInboundEtxs {
		if etx.To().Location().Equal(nodeLocation) {
			set.ETXs = append(set.ETXs, etx)
			set.ETXHashes = append(set.ETXHashes, etx.Hash().Bytes()...)
		} else {
			return fmt.Errorf("cannot add ETX %s destined to other chain to our ETX set", etx.Hash())
		}
	}
	return nil
}

// ProtoEncode encodes the EtxSet to protobuf format.
func (set *EtxSet) ProtoEncode() *ProtoEtxSet {
	protoSet := &ProtoEtxSet{}
	for i, entry := range set.ETXs {
		etx, err := entry.ProtoEncode()
		if err != nil {
			panic(err)
		}
		protoSet.Etxs = append(protoSet.Etxs, etx)
		protoSet.EtxHashes = append(protoSet.EtxHashes, set.ETXHashes[i*common.HashLength:(i+1)*common.HashLength]...)
	}
	return protoSet
}

// ProtoDecode decodes the EtxSet from protobuf format.
func (set *EtxSet) ProtoDecode(protoSet *ProtoEtxSet, location common.Location) error {
	for _, entry := range protoSet.GetEtxs() {
		etx := new(Transaction)
		err := etx.ProtoDecode(entry, location)
		if err != nil {
			return err
		}
		set.ETXs = append(set.ETXs, etx)
	}
	set.ETXHashes = protoSet.GetEtxHashes() // TODO: Check if this is correct
	return nil
}

func (set *EtxSet) Pop() *Transaction {
	if len(set.ETXs) == 0 {
		return nil
	}
	entry := set.ETXs[0]
	set.ETXs = set.ETXs[1:]
	hash := set.ETXHashes[:common.HashLength]
	if entry.Hash() != common.BytesToHash(hash) {
		panic("ETX hash mismatch: expected " + entry.Hash().String() + " but got " + common.BytesToHash(hash).String())
	}
	set.ETXHashes = set.ETXHashes[common.HashLength:]
	return entry
}

func (set *EtxSet) Len() int {
	return len(set.ETXs)
}

// Commit returns a hashed commitment of all ETX hashes in the ETX set
func (set *EtxSet) Hash() common.Hash {
	return crypto.Keccak256Hash(set.ETXHashes)
}
