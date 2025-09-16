// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package common

import (
	"bytes"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"strconv"
	"strings"

	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rlp"
)

// Lengths of hashes and addresses in bytes.
const (
	// HashLength is the expected length of the hash
	HashLength = 32
	// AddressLength is the expected length of the address
	AddressLength = 20

	// Constants to mnemonically index into context arrays
	PRIME_CTX  = 0
	REGION_CTX = 1
	ZONE_CTX   = 2

	//  Depth  of the tree, i.e. prime, region, zone
	HierarchyDepth     = 3
	MaxRegions         = 16
	MaxZones           = 16
	MaxWidth           = 16
	MaxExpansionNumber = 32
	InterlinkDepth     = 4
)

var (
	hashT = reflect.TypeOf(Hash{})
	// The zero address (0x0)
	ZeroExternal = ExternalAddress{}
	Zero         = Address{&ZeroExternal} // For utility purposes only. It is out-of-scope for state purposes.
)

var (
	ErrInvalidLocation = errors.New("invalid location")
)

// Hash represents the 32 byte Keccak256 hash of arbitrary data.
type Hash [HashLength]byte

// BytesToHash sets b to hash.
// If b is larger than len(h), b will be cropped from the left.
func BytesToHash(b []byte) Hash {
	var h Hash
	h.SetBytes(b)
	return h
}

// BigToHash sets byte representation of b to hash.
// If b is larger than len(h), b will be cropped from the left.
func BigToHash(b *big.Int) Hash { return BytesToHash(b.Bytes()) }

// HexToHash sets byte representation of s to hash.
// If b is larger than len(h), b will be cropped from the left.
func HexToHash(s string) Hash { return BytesToHash(FromHex(s)) }

// Cmp compares two hashes.
func (h Hash) Cmp(other Hash) int {
	return bytes.Compare(h[:], other[:])
}

// Bytes gets the byte representation of the underlying hash.
func (h Hash) Bytes() []byte { return h[:] }

// Big converts a hash to a big integer.
func (h Hash) Big() *big.Int { return new(big.Int).SetBytes(h[:]) }

// Hex converts a hash to a hex string.
func (h Hash) Hex() string { return hexutil.Encode(h[:]) }

// Reverse returns a new Hash with the bytes in reverse order.
func (h Hash) Reverse() Hash {
	var reversed Hash
	for i := 0; i < HashLength; i++ {
		reversed[i] = h[HashLength-1-i]
	}
	return reversed
}

// TerminalString implements log.TerminalStringer, formatting a string for console
// output during logging.
func (h Hash) TerminalString() string {
	return fmt.Sprintf("%x..%x", h[:3], h[29:])
}

// String implements the stringer interface and is used also by the logger when
// doing full logging into a file.
func (h Hash) String() string {
	return h.Hex()
}

// ProtoEncode converts the hash into the ProtoHash type
func (h Hash) ProtoEncode() *ProtoHash {
	return &ProtoHash{Value: h.Bytes()}
}

// ProtoDecode converts the ProtoHash into the Hash type
func (h *Hash) ProtoDecode(hash *ProtoHash) {
	h.SetBytes(hash.GetValue())
}

// Format implements fmt.Formatter.
// Hash supports the %v, %s, %v, %x, %X and %d format verbs.
func (h Hash) Format(s fmt.State, c rune) {
	hexb := make([]byte, 2+len(h)*2)
	copy(hexb, "0x")
	hex.Encode(hexb[2:], h[:])

	switch c {
	case 'x', 'X':
		if !s.Flag('#') {
			hexb = hexb[2:]
		}
		if c == 'X' {
			hexb = bytes.ToUpper(hexb)
		}
		fallthrough
	case 'v', 's':
		s.Write(hexb)
	case 'q':
		q := []byte{'"'}
		s.Write(q)
		s.Write(hexb)
		s.Write(q)
	case 'd':
		fmt.Fprint(s, ([len(h)]byte)(h))
	default:
		fmt.Fprintf(s, "%%!%c(hash=%x)", c, h)
	}
}

// UnmarshalText parses a hash in hex syntax.
func (h *Hash) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("Hash", input, h[:])
}

// UnmarshalJSON parses a hash in hex syntax.
func (h *Hash) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(hashT, input, h[:])
}

// MarshalText returns the hex representation of h.
func (h Hash) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// SetBytes sets the hash to the value of b.
// If b is larger than len(h), b will be cropped from the left.
func (h *Hash) SetBytes(b []byte) {
	if len(b) > len(h) {
		b = b[len(b)-HashLength:]
	}

	copy(h[HashLength-len(b):], b)
}

// Generate implements testing/quick.Generator.
func (h Hash) Generate(rand *rand.Rand, size int) reflect.Value {
	m := rand.Intn(len(h))
	for i := len(h) - 1; i > m; i-- {
		h[i] = byte(rand.Uint32())
	}
	return reflect.ValueOf(h)
}

// Scan implements Scanner for database/sql.
func (h *Hash) Scan(src interface{}) error {
	srcB, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("can't scan %T into Hash", src)
	}
	if len(srcB) != HashLength {
		return fmt.Errorf("can't scan []byte of len %d into Hash, want %d", len(srcB), HashLength)
	}
	copy(h[:], srcB)
	return nil
}

// Value implements valuer for database/sql.
func (h Hash) Value() (driver.Value, error) {
	return h[:], nil
}

// UnprefixedHash allows marshaling a Hash without 0x prefix.
type UnprefixedHash Hash

// UnmarshalText decodes the hash from hex. The 0x prefix is optional.
func (h *UnprefixedHash) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedUnprefixedText("UnprefixedHash", input, h[:])
}

// MarshalText encodes the hash as hex.
func (h UnprefixedHash) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(h[:])), nil
}

// Hashes is a slice of Hash
type Hashes []Hash

func (h Hashes) ProtoEncode() *ProtoHashes {
	res := make([]*ProtoHash, len(h))
	for i, hash := range h {
		res[i] = hash.ProtoEncode()
	}
	return &ProtoHashes{Hashes: res}
}

func (h *Hashes) ProtoDecode(hashes *ProtoHashes) {
	res := make([]Hash, len(hashes.GetHashes()))
	for i, hash := range hashes.GetHashes() {
		res[i].ProtoDecode(hash)
	}
	*h = res
}

// Len returns the length of h.
func (h Hashes) Len() int { return len(h) }

func (h Hashes) EncodeIndex(i int, w *bytes.Buffer) {
	rlp.Encode(w, h[i])
}

/////////// Address

type addrPrefixRange struct {
	lo uint8
	hi uint8
}

func NewRange(l, h uint8) addrPrefixRange {
	return addrPrefixRange{
		lo: l,
		hi: h,
	}
}

// UnprefixedAddress allows marshaling an Address without 0x prefix.
type UnprefixedAddress InternalAddress

// UnmarshalText decodes the address from hex. The 0x prefix is optional.
func (a *UnprefixedAddress) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedUnprefixedText("UnprefixedAddress", input, a[:])
}

// MarshalText encodes the address as hex.
func (a UnprefixedAddress) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(a[:])), nil
}

// MixedcaseAddress retains the original string, which may or may not be
// correctly checksummed
type MixedcaseAddress struct {
	addr     Address
	original string
}

// NewMixedcaseAddress constructor (mainly for testing)
func NewMixedcaseAddress(addr Address) MixedcaseAddress {
	return MixedcaseAddress{addr: addr, original: addr.inner.Hex()}
}

// NewMixedcaseAddressFromString is mainly meant for unit-testing
func NewMixedcaseAddressFromString(hexaddr string, nodeLocation Location) (*MixedcaseAddress, error) {
	if !IsHexAddress(hexaddr) {
		return nil, errors.New("invalid address")
	}
	a := FromHex(hexaddr)
	return &MixedcaseAddress{addr: BytesToAddress(a, nodeLocation), original: hexaddr}, nil
}

// UnmarshalJSON parses MixedcaseAddress
func (ma *MixedcaseAddress) UnmarshalJSON(input []byte) error {
	var temp [AddressLength]byte

	if err := hexutil.UnmarshalFixedJSON(reflect.TypeOf(InternalAddress{}), input, temp[:]); err != nil {
		return err
	}

	ma.addr.inner = Bytes20ToAddress(temp, Location{}).inner

	return json.Unmarshal(input, &ma.original)
}

// MarshalJSON marshals the original value
func (ma *MixedcaseAddress) MarshalJSON() ([]byte, error) {
	if strings.HasPrefix(ma.original, "0x") || strings.HasPrefix(ma.original, "0X") {
		return json.Marshal(fmt.Sprintf("0x%s", ma.original[2:]))
	}
	return json.Marshal(fmt.Sprintf("0x%s", ma.original))
}

// Address returns the address
func (ma *MixedcaseAddress) Address() Address {
	return ma.addr
}

// String implements fmt.Stringer
func (ma *MixedcaseAddress) String() string {
	if ma.ValidChecksum() {
		return fmt.Sprintf("%s [chksum ok]", ma.original)
	}
	return fmt.Sprintf("%s [chksum INVALID]", ma.original)
}

// ValidChecksum returns true if the address has valid checksum
func (ma *MixedcaseAddress) ValidChecksum() bool {
	return ma.original == ma.addr.inner.Hex()
}

// Original returns the mixed-case input string
func (ma *MixedcaseAddress) Original() string {
	return ma.original
}

// Location of a chain within the Quai hierarchy
// Location is encoded as a path from the root of the tree to the specified
// chain. Not all indices need to be populated, e.g:
// prime     = []
// region[0] = [0]
// zone[1,2] = [1, 2]
type Location []byte

// Implements the shard topology defined in QIP2
func LocationFromAddressBytes(addr []byte) Location {
	region := (addr[0] & 0xF0) >> 4 // bits[0..3]
	zone := addr[0] & 0x0F          // bits[4..7]
	return []byte{region, zone}
}

// ProtoEncode converts the Location type into ProtoLocation
func (loc Location) ProtoEncode() *ProtoLocation {
	return &ProtoLocation{Value: loc}
}

// ProtoDecode converts the ProtoLocation type back into Location
func (loc *Location) ProtoDecode(location *ProtoLocation) {
	*loc = location.GetValue()
}

// Constructs the byte prefix from the location type
func (loc Location) BytePrefix() byte {
	return loc[0]<<4 + loc[1]
}

func (loc Location) Region() int {
	if len(loc) >= 1 {
		return int(loc[REGION_CTX-1])
	} else {
		return -1
	}
}

func (loc Location) HasRegion() bool {
	return loc.Region() >= 0
}

func (loc Location) Zone() int {
	if len(loc) >= 2 {
		return int(loc[ZONE_CTX-1])
	} else {
		return -1
	}
}

func (loc Location) HasZone() bool {
	return loc.Zone() >= 0
}

func (loc Location) Context() int {
	if loc.Zone() >= 0 {
		return ZONE_CTX
	} else if loc.Region() >= 0 {
		return REGION_CTX
	} else {
		return PRIME_CTX
	}
}

// DomLocation returns the location of your dominant chain
func (loc Location) DomIndex(nodeLocation Location) int {
	switch nodeLocation.Context() {
	case PRIME_CTX:
		return 0
	case REGION_CTX:
		return loc.Region()
	default:
		return loc.Zone()
	}
}

// SubIndex returns the index of the subordinate chain for a given location
func (loc Location) SubIndex(nodeCtx int) int {
	switch nodeCtx {
	case PRIME_CTX:
		return loc.Region()
	case REGION_CTX:
		return loc.Zone()
	default:
		return -1
	}
}

// SubInSlice returns the location of the subordinate chain within the specified
// slice. For example:
//   - if prime calls SubInSlice(Location{0,0}) the result will be Location{0},
//     i.e. region-0's location, because Prime's subordinate in that slice is
//     region-0
//   - if region-0 calls SubInSlice(Location{0,0}) the result will be
//     Location{0,0}, i.e. zone-0-0's location, because region-0's subordinate in
//     that slice is zone-0-0
func (loc Location) SubInSlice(slice Location) Location {
	if len(slice) <= len(loc) {
		log.Global.Info("cannot determine sub location, because slice location is not deeper than self")
		return nil
	}
	subLoc := append(loc, slice[len(loc)])
	return subLoc
}

// GetDoms returns the dom locations that must be running for a given location
// For example:
//   - if a region-0 calls GetDoms() the result will be
//     [prime, region-0]
//   - if a zone-0-0 calls GetDoms() the result will be
//     [prime, region-0, zone-0-0]
func (loc Location) GetDoms() []Location {
	var dominantLocations []Location

	// Always start with the prime location
	dominantLocations = append(dominantLocations, Location{})

	for i := range loc {
		dominantLocations = append(dominantLocations, loc[:i+1])
	}

	return dominantLocations
}

func (loc Location) InSameSliceAs(cmp Location) bool {
	// Figure out which location is shorter
	shorter := loc
	longer := cmp
	if len(loc) > len(cmp) {
		longer = loc
		shorter = cmp
	}
	// Compare bytes up to the shorter depth
	return shorter.Equal(longer[:len(shorter)])
}
func (loc Location) NameAtOrder(order int) string {
	regionName := ""
	switch loc.Region() {
	case 0:
		regionName = "cyprus"
	case 1:
		regionName = "paxos"
	case 2:
		regionName = "hydra"
	default:
		regionName = "unknownregion"
	}

	zoneNum := strconv.Itoa(loc.Zone() + 1)
	switch order {
	case PRIME_CTX:
		return "prime"
	case REGION_CTX:
		return regionName
	case ZONE_CTX:
		return regionName + zoneNum
	default:
		log.Global.Info("cannot name invalid location")
		return "invalid-location"
	}

}

func (loc Location) Name() string {
	regionName := ""
	switch loc.Region() {
	case 0:
		regionName = "cyprus"
	case 1:
		regionName = "paxos"
	case 2:
		regionName = "hydra"
	default:
		regionName = "unknownregion"
	}

	zoneNum := strconv.Itoa(loc.Zone() + 1)
	switch loc.Context() {
	case PRIME_CTX:
		return "prime"
	case REGION_CTX:
		return regionName
	case ZONE_CTX:
		return regionName + zoneNum
	default:
		log.Global.Info("cannot name invalid location")
		return "invalid-location"
	}
}

func (loc Location) Equal(cmp Location) bool {
	return bytes.Equal(loc, cmp)
}

// CommonDom identifies the highest context chain which exists in both locations
// * zone-0-0 & zone-0-1 would share region-0 as their highest context common dom
// * zone-0-0 & zone-1-0 would share Prime as their highest context common dom
func (loc Location) CommonDom(cmp Location) Location {
	common := Location{}
	shorterLen := len(loc)
	if len(loc) > len(cmp) {
		shorterLen = len(cmp)
	}
	for i := 0; i < shorterLen; i++ {
		if loc[i] == cmp[i] {
			common = append(common, loc[i])
		} else {
			break
		}
	}
	return common
}

// Determines if the given address belongs to the location
func (l Location) ContainsAddress(a Address) bool {
	if l.Context() != ZONE_CTX {
		return false
	} else {
		return l.BytePrefix() == a.Bytes()[0]
	}
}

func (l Location) RPCMarshal() []hexutil.Uint64 {
	res := make([]hexutil.Uint64, 0)
	for _, i := range l {
		res = append(res, hexutil.Uint64(i))
	}

	return res
}

// MarshalJSON marshals the location into a JSON array of integers
func (l Location) MarshalJSON() ([]byte, error) {
	intSlice := make([]int, len(l))
	for i, v := range l {
		intSlice[i] = int(v)
	}
	return json.Marshal(intSlice)
}

// NewLocation verifies the inputs for region and zone and returns a valid location
func NewLocation(region, zone int) (Location, error) {
	loc := Location{}
	err := loc.SetRegion(region)
	if err != nil {
		return nil, err
	}
	err = loc.SetZone(zone)
	if err != nil {
		return nil, err
	}

	return loc, nil
}

func (l *Location) SetRegion(region int) error {
	if region < 0 || region > 15 {
		return ErrInvalidLocation
	}
	if len(*l) < 1 {
		// Extend location to include region if its too short
		newLoc := make([]byte, 1)
		*l = newLoc
	}
	(*l)[0] = byte(region)
	return nil
}

func (l *Location) SetZone(zone int) error {
	if zone < 0 || zone > 15 {
		return ErrInvalidLocation
	}
	if len(*l) < 2 {
		// Extend the slice while preserving the first byte, if it exists
		newSlice := make([]byte, 2)
		if len(*l) > 0 {
			newSlice[0] = (*l)[0] // Preserve existing first byte
		}
		*l = newSlice
	}
	(*l)[1] = byte(zone)
	return nil
}

// regionMappings maps region names to their corresponding byte values.
var regionMappings = map[string]byte{
	"cyprus": 0,
	"paxos":  1,
	"hydra":  2,
}

// LocationFromName parses a location name and returns a Location.
func LocationFromName(name string) (Location, error) {
	if name == "" || name == "prime" {
		return Location{}, nil
	}

	parts := strings.Fields(name)
	if len(parts) == 1 {
		// Check if name was provided as a string
		regionIndex, err := parseRegion(parts[0])
		if err != nil {
			log.Global.WithField("error", err).Error("Error parsing region index")
			return Location{}, err
		}
		return Location{byte(regionIndex)}, nil
	} else if len(parts) == 2 {
		// Check if name was provided as a string
		regionIndex, err := parseRegion(parts[0])
		if err != nil {
			log.Global.WithField("error", err).Error("Error parsing region index")
			return Location{}, err
		}
		zoneIndex, err := strconv.Atoi(parts[1])
		if err != nil {
			log.Global.WithField("error", err).Error("Error parsing zone index")
			return nil, err
		}
		return Location{byte(regionIndex), byte(zoneIndex - 1)}, nil
	}

	return nil, fmt.Errorf("invalid location format")
}

// parseRegion attempts to parse a region from a string.
func parseRegion(part string) (byte, error) {
	// Check if the part is a region name
	if regionIndex, ok := regionMappings[strings.ToLower(part)]; ok {
		return regionIndex, nil
	}
	// Otherwise, treat it as a numerical region index
	regionIndex, err := strconv.Atoi(part)
	if err != nil {
		return 0, err
	}
	return byte(regionIndex), nil
}

func IsInChainScope(b []byte, nodeLocation Location) bool {
	nodeCtx := nodeLocation.Context()
	// IsInChainScope only be called for a zone chain
	if nodeCtx != ZONE_CTX {
		return false
	}
	if BytesToHash(b) == ZeroAddress(nodeLocation).Hash() {
		return true
	}
	if len(b) == 0 {
		return false
	}
	return b[0] == nodeLocation.BytePrefix()
}

func OrderToString(order int) string {
	switch order {
	case PRIME_CTX:
		return "Prime"
	case REGION_CTX:
		return "Region"
	case ZONE_CTX:
		return "Zone"
	default:
		return "Invalid"
	}
}

func ZeroInternal(nodeLocation Location) InternalAddress {
	return InternalAddress{nodeLocation.BytePrefix()}
}

// OneInternal returns an address starting with the byte prefix and ending in 1
func OneInternal(nodeLocation Location) InternalAddress {
	one := ZeroInternal(nodeLocation)
	one[AddressLength-1] = 1
	return one
}

func ZeroAddress(nodeLocation Location) Address {
	internal := InternalAddress{nodeLocation.BytePrefix()}
	return Address{&internal}
}

// GenerateLocations generates a logical sequence of locations
func GenerateLocations(maxRegions, zonesPerRegion int) []Location {
	var locations []Location

	// Prime location
	locations = append(locations, Location{})

	// Iterate over each region
	for regionIndex := 0; regionIndex < maxRegions; regionIndex++ {
		// Add region
		locations = append(locations, Location{byte(regionIndex)})

		// Add zones for the current region
		for zoneIndex := 0; zoneIndex < zonesPerRegion; zoneIndex++ {
			locations = append(locations, Location{byte(regionIndex), byte(zoneIndex)})
		}
	}

	return locations
}

// GetHierarchySizeForExpansionNumber calculates the number of regions and zones for a given expansion number.
func GetHierarchySizeForExpansionNumber(expansion uint8) (uint64, uint64) {
	// Handle special cases for genesis and the first expansion directly
	switch expansion {
	case 0: // Genesis
		return 1, 1
	case 1:
		return 1, 2
	default:
		regions, zones := GetHierarchySizeForExpansionNumber(expansion - 1)
		if expansion%2 == 0 {
			return regions + 1, zones
		} else {
			return regions, zones + 1
		}
	}
}

// NewChainsAdded returns the new chains added on the given expansion number
func NewChainsAdded(expansionNumber uint8) []Location {
	newChains := []Location{}
	oldRegions, _ := GetHierarchySizeForExpansionNumber(expansionNumber - 1)
	newRegions, newZones := GetHierarchySizeForExpansionNumber(expansionNumber)

	newRegionsAdded := newRegions > oldRegions

	// If new region was not added, the new chains are the extra zones added to all the current regions
	if !newRegionsAdded {
		for i := 0; i < int(oldRegions); i++ {
			newChains = append(newChains, Location{byte(i), byte(newZones - 1)})
		}
	} else {
		// Region chain is added
		newChains = append(newChains, Location{byte(newRegions - 1)})
		// If new region was added, the new chains are the extra zones added to all the new regions
		for i := 0; i < int(newZones); i++ {
			newChains = append(newChains, Location{byte(newRegions - 1), byte(i)})
		}
	}
	return newChains
}

// SetBlockHashForQuai sets the correct first 4 bytes in the block hash for QIP10 and Quai origin
func SetBlockHashForQuai(blockHash Hash, nodeLocation Location) Hash {
	// Set the first byte of the block hash to the zone prefix
	origin := (uint8(nodeLocation[0]) * 16) + uint8(nodeLocation[1])
	blockHash[0] = origin
	blockHash[2] = origin
	blockHash[1] &= 0x7F // 01111111 in binary (set first bit to 0)
	blockHash[3] &= 0x7F // 01111111 in binary (set first bit to 0)
	return blockHash
}

// SetBlockHashForQuai sets the correct first 4 bytes in the block hash for QIP10 and Qi origin
func SetBlockHashForQi(blockHash Hash, nodeLocation Location) Hash {
	// Set the first byte of the block hash to the zone prefix
	origin := (uint8(nodeLocation[0]) * 16) + uint8(nodeLocation[1])
	blockHash[0] = origin
	blockHash[2] = origin
	blockHash[1] |= 0x80 // 10000000 in binary (set first bit to 1)
	blockHash[3] |= 0x80 // 10000000 in binary (set first bit to 1)
	return blockHash
}

type Unlock struct {
	Addr InternalAddress
	Amt  *big.Int
}
