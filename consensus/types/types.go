package types

import (
    "strconv"
)

// Hash represents a 256 bit hash
type Hash [32]byte

// Location describes the ontological location of a chain within the Quai hierarchy of blockchains
type Location [2]byte

type SliceID struct {
    Region  int
    Zone    int
}

func (sliceID SliceID) String() string {
    return strconv.Itoa(sliceID.Region) + "." + strconv.Itoa(sliceID.Zone)
}