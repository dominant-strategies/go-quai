package common

import (
	"bytes"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"reflect"

	"github.com/dominant-strategies/go-quai/common/hexutil"
	"golang.org/x/crypto/sha3"
)

type ExternalAddress [AddressLength]byte

// Bytes gets the string representation of the underlying address.
func (a ExternalAddress) Bytes() []byte { return a[:] }

// Hash converts an address to a hash by left-padding it with zeros.
func (a ExternalAddress) Hash() Hash { return BytesToHash(a[:]) }

// Hex returns a hex string representation of the address.
func (a ExternalAddress) Hex() string {
	return string(a.checksumHex())
}

// String implements fmt.Stringer.
func (a ExternalAddress) String() string {
	return a.Hex()
}

func (a *ExternalAddress) checksumHex() []byte {
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

func (a ExternalAddress) hex() []byte {
	var buf [len(a)*2 + 2]byte
	copy(buf[:2], "0x")
	hex.Encode(buf[2:], a[:])
	return buf[:]
}

// Format implements fmt.Formatter.
// Address supports the %v, %s, %v, %x, %X and %d format verbs.
func (a ExternalAddress) Format(s fmt.State, c rune) {
	switch c {
	case 'v', 's':
		s.Write(a.checksumHex())
	case 'q':
		q := []byte{'"'}
		s.Write(q)
		s.Write(a.checksumHex())
		s.Write(q)
	case 'x', 'X':
		// %x disables the checksum.
		hex := a.hex()
		if !s.Flag('#') {
			hex = hex[2:]
		}
		if c == 'X' {
			hex = bytes.ToUpper(hex)
		}
		s.Write(hex)
	case 'd':
		fmt.Fprint(s, ([len(a)]byte)(a))
	default:
		fmt.Fprintf(s, "%%!%c(address=%x)", c, a)
	}
}

// SetBytes sets the address to the value of b.
// If b is larger than len(a), b will be cropped from the left.
func (a *ExternalAddress) setBytes(b []byte) {
	if len(b) > len(a) {
		b = b[len(b)-AddressLength:]
	}
	copy(a[AddressLength-len(b):], b)
}

// MarshalText returns the hex representation of a.
func (a ExternalAddress) MarshalText() ([]byte, error) {
	return hexutil.Bytes(a[:]).MarshalText()
}

// UnmarshalText parses a hash in hex syntax.
func (a *ExternalAddress) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("Address", input, a[:])
}

// UnmarshalJSON parses a hash in hex syntax.
func (a *ExternalAddress) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(reflect.TypeOf(ExternalAddress{}), input, a[:])
}

// Scan implements Scanner for database/sql.
func (a *ExternalAddress) Scan(src interface{}) error {
	srcB, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("can't scan %T into Address", src)
	}
	if len(srcB) != AddressLength {
		return fmt.Errorf("can't scan []byte of len %d into Address, want %d", len(srcB), AddressLength)
	}
	copy(a[:], srcB)
	return nil
}

// Value implements valuer for database/sql.
func (a ExternalAddress) Value() (driver.Value, error) {
	return a[:], nil
}

func (a ExternalAddress) Location() *Location {
	// Assuming AddressLength is at least 1 byte
	if len(a) < 1 {
		return nil // or handle error appropriately
	}

	// Extract the first byte (shard identifier) from the address
	shardIdentifier := a[0]

	// Extract the region (first 4 bits) and zone (last 4 bits)
	region := (shardIdentifier & 0xF0) >> 4
	zone := shardIdentifier & 0x0F

	// If region or zone exceeds the Width, return nil
	if region >= Width || zone >= Width {
		return nil
	}

	// Convert region and zone to byte slice and return
	loc := Location{byte(region), byte(zone)}
	return &loc
}
