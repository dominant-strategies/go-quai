package common

import (
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"io"
	"math/big"

	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/rlp"
	"golang.org/x/crypto/sha3"
)

type Address struct {
	inner AddressData
}

type AddressBytes [20]byte

type AddressData interface {
	Bytes() []byte
	Hash() Hash
	Hex() string
	String() string
	checksumHex() []byte
	Format(s fmt.State, c rune)
	MarshalText() ([]byte, error)
	UnmarshalText(input []byte) error
	UnmarshalJSON(input []byte) error
	Scan(src interface{}) error
	Value() (driver.Value, error)
	Location() *Location
	setBytes(b []byte)
}

func (a Address) InternalAddress() (InternalAddress, error) {
	if a.inner == nil {
		return InternalAddress{}, nil
	}
	internal, ok := a.inner.(*InternalAddress)
	if !ok {
		return InternalAddress{}, ErrInvalidScope
	}
	return *internal, nil
}

func (a Address) Equal(b Address) bool {
	if a.inner == nil && b.inner == nil {
		return true
	} else if a.inner == nil || b.inner == nil {
		return false
	}
	return a.Hash() == b.Hash()
}

// BytesToAddress returns Address with value b.
// If b is larger than len(h), b will be cropped from the left.
func BytesToAddress(b []byte) Address {
	if IsInChainScope(b) {
		var i InternalAddress
		i.setBytes(b)
		return Address{&i}
	} else {
		var e ExternalAddress
		e.setBytes(b)
		return Address{&e}
	}
}

func Bytes20ToAddress(b [20]byte) Address {
	return BytesToAddress(b[:])
}

func NewAddressFromData(inner AddressData) Address {
	return Address{inner: inner}
}

// EncodeRLP serializes b into the Quai RLP block format.
func (a Address) EncodeRLP(w io.Writer) error {
	if a.inner == nil {
		a.inner = &InternalAddress{}
	}
	return rlp.Encode(w, a.inner)
}

// DecodeRLP decodes the Quai
func (a *Address) DecodeRLP(s *rlp.Stream) error {
	temp := make([]byte, 0, 20)
	if err := s.Decode(&temp); err != nil {
		return err
	}
	*a = BytesToAddress(temp)
	return nil
}

// Bytes gets the string representation of the underlying address.
func (a Address) Bytes() []byte {
	if a.inner == nil {
		return []byte{}
	}
	return a.inner.Bytes()
}

// Bytes20 gets the bytes20 representation of the underlying address.
func (a Address) Bytes20() (addr AddressBytes) {
	if a.inner == nil {
		return AddressBytes{}
	}
	copy(addr[:], a.Bytes()[:]) // this is not very performant
	return addr
}

// Hash converts an address to a hash by left-padding it with zeros.
func (a Address) Hash() Hash {
	if a.inner == nil {
		return Hash{}
	}
	return a.inner.Hash()
}

// Hex returns a hex string representation of the address.
func (a Address) Hex() string {
	if a.inner == nil {
		return string([]byte{})
	}
	return a.inner.Hex()
}

// String implements fmt.Stringer.
func (a Address) String() string {
	if a.inner == nil {
		return string([]byte{})
	}
	return a.inner.String()
}

// Format implements fmt.Formatter.
// Address supports the %v, %s, %v, %x, %X and %d format verbs.
func (a Address) Format(s fmt.State, c rune) {
	if a.inner != nil {
		a.inner.Format(s, c)
	}
}

// MarshalText returns the hex representation of a.
func (a Address) MarshalText() ([]byte, error) {
	if a.inner == nil {
		return hexutil.Bytes(ZeroExternal[:]).MarshalText()
	}
	return a.inner.MarshalText()
}

// UnmarshalText parses a hash in hex syntax.
func (a *Address) UnmarshalText(input []byte) error {
	var temp [AddressLength]byte
	if err := hexutil.UnmarshalFixedText("Address", input, temp[:]); err != nil {
		return err
	}
	a.inner = Bytes20ToAddress(temp).inner
	return nil
}

// MarshalJSON marshals a subscription as its ID.
func (a *Address) MarshalJSON() ([]byte, error) {
	if a.inner == nil {
		return json.Marshal(Zero)
	}
	return json.Marshal(a.inner)
}

// UnmarshalJSON parses a hash in hex syntax.
func (a *Address) UnmarshalJSON(input []byte) error {
	var temp [AddressLength]byte
	if err := hexutil.UnmarshalFixedJSON(reflect.TypeOf(InternalAddress{}), input, temp[:]); err != nil {
		if len(input) == 0 {
			a.inner = Bytes20ToAddress(ZeroExternal).inner
			return nil
		}
		return err
	}
	a.inner = Bytes20ToAddress(temp).inner
	return nil
}

// Scan implements Scanner for database/sql.
func (a *Address) Scan(src interface{}) error {
	var temp [20]byte
	srcB, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("can't scan %T into Address", src)
	}
	if len(srcB) != AddressLength {
		return fmt.Errorf("can't scan []byte of len %d into Address, want %d", len(srcB), AddressLength)
	}
	copy(temp[:], srcB)
	a.inner = Bytes20ToAddress(temp).inner
	return nil
}

// Value implements valuer for database/sql.
func (a Address) Value() (driver.Value, error) {
	if a.inner == nil {
		return []byte{}, nil
	}
	return a.inner.Value()
}

// Location looks up the chain location which contains this address
func (a Address) Location() *Location {
	if a.inner == nil {
		return &NodeLocation
	}
	return a.inner.Location()
}

// BigToAddress returns Address with byte values of b.
// If b is larger than len(h), b will be cropped from the left.
func BigToAddress(b *big.Int) Address { return BytesToAddress(b.Bytes()) }

// HexToAddress returns Address with byte values of s.
// If s is larger than len(h), s will be cropped from the left.
func HexToAddress(s string) Address { return BytesToAddress(FromHex(s)) }

// IsHexAddress verifies whether a string can represent a valid hex-encoded
// Quai address or not.
func IsHexAddress(s string) bool {
	if has0xPrefix(s) {
		s = s[2:]
	}
	return len(s) == 2*AddressLength && isHex(s)
}

// Hex returns a hex string representation of the address.
func (a AddressBytes) Hex() string {
	return string(a.checksumHex())
}

// String implements fmt.Stringer.
func (a AddressBytes) String() string {
	return a.Hex()
}

func (a AddressBytes) checksumHex() []byte {
	buf := a.hex()

	// compute checksum
	sha := sha3.NewLegacyKeccak256()
	sha.Write(buf[2:])
	hash := sha.Sum(nil)
	for i := 2; i < len(buf); i++ {
		hashByte := hash[(i-2)/2]
		if i%2 == 0 {
			hashByte = hashByte >> 4
		} else {
			hashByte &= 0xf
		}
		if buf[i] > '9' && hashByte > 7 {
			buf[i] -= 32
		}
	}
	return buf[:]
}

func (a AddressBytes) hex() []byte {
	var buf [len(a)*2 + 2]byte
	copy(buf[:2], "0x")
	hex.Encode(buf[2:], a[:])
	return buf[:]
}
