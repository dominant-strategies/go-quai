package types

import (
	"fmt"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

// The EtxSet is a list of ETX hashes, ETXs and the block heights in which they became available.
// If no entry exists for a given ETX hash, then that ETX is not available.
type EtxSet struct {
	ETXHashes []byte
}

func NewEtxSet() *EtxSet {
	etxSet := EtxSet{
		ETXHashes: make([]byte, 0, 0),
	}
	return &etxSet
}

// updateInboundEtxs updates the set of inbound ETXs available to be mined into
// a block in this location. This method adds any new ETXs to the set and
// removes expired ETXs.
func (set *EtxSet) Update(newInboundEtxs Transactions, nodeLocation common.Location, WriteETXFunc func(hash common.Hash, etx *Transaction)) error {
	// Add new ETX entries to the inbound set
	for _, etx := range newInboundEtxs {
		if etx.To().Location().Equal(nodeLocation) {
			hash := etx.Hash()
			WriteETXFunc(hash, etx)
			set.ETXHashes = append(set.ETXHashes, hash.Bytes()...)
		} else {
			return fmt.Errorf("cannot add ETX %s destined to other chain to our ETX set", etx.Hash())
		}
	}
	return nil
}

// ProtoEncode encodes the EtxSet to protobuf format.
func (set *EtxSet) ProtoEncode() *ProtoEtxSet {
	protoSet := &ProtoEtxSet{
		EtxHashes: set.ETXHashes,
	}
	return protoSet
}

// ProtoDecode decodes the EtxSet from protobuf format.
func (set *EtxSet) ProtoDecode(protoSet *ProtoEtxSet) error {
	set.ETXHashes = protoSet.GetEtxHashes()
	return nil
}

func (set *EtxSet) Pop() common.Hash {
	if set.ETXHashes == nil || len(set.ETXHashes) == 0 {
		return common.Hash{}
	}
	hash := set.ETXHashes[:common.HashLength]
	set.ETXHashes = set.ETXHashes[common.HashLength:]
	return common.BytesToHash(hash)
}

func (set *EtxSet) GetHashAtIndex(index int) common.Hash {
	if len(set.ETXHashes) < (index+1)*common.HashLength {
		return common.Hash{}
	}
	hash := set.ETXHashes[index*common.HashLength : (index+1)*common.HashLength]
	return common.BytesToHash(hash)
}

func (set *EtxSet) Len() int {
	return len(set.ETXHashes) / common.HashLength
}

// Commit returns a hashed commitment of all ETX hashes in the ETX set
func (set *EtxSet) Hash() common.Hash {
	return crypto.Keccak256Hash(set.ETXHashes)
}
