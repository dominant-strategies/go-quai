package common

import (
	"database/sql/driver"
	"fmt"
	"reflect"

	"io"
	"math/big"

	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/rlp"
)

type Address struct {
	inner AddressData
}

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

// IsInChainScope checks if an address is a valid account in our node's sharded address space
func (a Address) IsInChainScope() bool {
	if a.inner == nil || a.inner.Hash() == ZeroAddr.Hash() {
		return true
	}
	return NodeLocation.ContainsAddress(a)
}

func (a Address) InternalAddress() (*InternalAddress, error) {
	if a.inner == nil {
		return &InternalAddress{}, nil
	}
	internal, ok := a.inner.(*InternalAddress)
	if !ok {
		return nil, ErrInvalidScope
	}
	return internal, nil
}

func (a Address) Equals(b Address) bool {
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

// EncodeRLP serializes b into the Ethereum RLP block format.
func (a Address) EncodeRLP(w io.Writer) error {
	if a.inner == nil {
		a.inner = &InternalAddress{}
	}
	return rlp.Encode(w, a.inner)
}

// DecodeRLP decodes the Ethereum
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

// Hash converts an address to a hash by left-padding it with zeros.
func (a Address) Hash() Hash {
	if a.inner == nil {
		return Hash{}
	}
	return a.inner.Hash()
}

// Hex returns an EIP55-compliant hex string representation of the address.
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
		return []byte{}, nil
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

// UnmarshalJSON parses a hash in hex syntax.
func (a *Address) UnmarshalJSON(input []byte) error {
	var temp [AddressLength]byte
	if err := hexutil.UnmarshalFixedJSON(reflect.TypeOf(InternalAddress{}), input, temp[:]); err != nil {
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
// Ethereum address or not.
func IsHexAddress(s string) bool {
	if has0xPrefix(s) {
		s = s[2:]
	}
	return len(s) == 2*AddressLength && isHex(s)
}
