package hdwallet

import (
	"github.com/dominant-strategies/go-quai/common"
)

// LocationFromAddress extracts the Location (region, zone) from a 20-byte address.
// Byte[0] upper nibble = region, lower nibble = zone.
// Mirrors common.LocationFromAddressBytes.
func LocationFromAddress(addr []byte) common.Location {
	return common.LocationFromAddressBytes(addr)
}

// IsQiAddress returns true if the address is in the Qi ledger scope.
// Byte[1] > 127 (high bit set) means Qi.
func IsQiAddress(addr []byte) bool {
	if len(addr) < 2 {
		return false
	}
	return addr[1] > 127
}

// IsQuaiAddress returns true if the address is in the Quai ledger scope.
// Byte[1] <= 127 (high bit clear) means Quai.
func IsQuaiAddress(addr []byte) bool {
	if len(addr) < 2 {
		return false
	}
	return addr[1] <= 127
}

// IsValidAddressForZone checks if an address belongs to the target zone AND
// has the correct ledger scope for the given coin type.
// CoinTypeQuai (994) requires Quai ledger scope, CoinTypeQi (969) requires Qi.
func IsValidAddressForZone(coinType uint32, addr []byte, zone common.Location) bool {
	if len(addr) < 2 || len(zone) < 2 {
		return false
	}
	// Check zone match: addr[0] must equal the zone's byte prefix
	if addr[0] != zone.BytePrefix() {
		return false
	}
	// Check ledger scope
	switch coinType {
	case CoinTypeQuai:
		return IsQuaiAddress(addr)
	case CoinTypeQi:
		return IsQiAddress(addr)
	default:
		return false
	}
}
