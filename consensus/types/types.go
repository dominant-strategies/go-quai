package types

import (
	"strconv"
)

// Hash represents a 256 bit hash
type Hash [32]byte

type Context struct {
	location string
	level    int
}

var (
	PRIME_CTX  = Context{"prime", 0}
	REGION_CTX = Context{"region", 1}
	ZONE_CTX   = Context{"zone", 2}
)

type SliceID struct {
	Context Context
	Region  int
	Zone    int
}

func (sliceID SliceID) String() string {
	return strconv.Itoa(sliceID.Region) + "." + strconv.Itoa(sliceID.Zone)
}
