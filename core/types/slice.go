package types

import (
	"encoding/hex"
	"strconv"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/pkg/errors"
)

// Hash represents a 256 bit hash
type Hash [32]byte

func NewHashFromString(s string) (common.Hash, error) {
	if len(s) != 64 { // 2 hex chars per byte
		return common.Hash{}, errors.New("invalid string length for hash")
	}
	hashSlice, err := hex.DecodeString(s)
	if err != nil {
		return common.Hash{}, err
	}
	var hash common.Hash
	copy(hash[:], hashSlice)
	return hash, nil
}

type Context struct {
	Location string
	Level    uint32
}

var (
	PRIME_CTX  = Context{"prime", 0}
	REGION_CTX = Context{"region", 1}
	ZONE_CTX   = Context{"zone", 2}
)

type Slice struct {
	SliceID SliceID
}

type SliceID struct {
	Context Context
	Region  uint32
	Zone    uint32
}

func (sliceID SliceID) String() string {
	return strconv.Itoa(int(sliceID.Region)) + "." + strconv.Itoa(int(sliceID.Zone))
}
