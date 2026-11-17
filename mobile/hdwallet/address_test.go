package hdwallet

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
)

func TestLocationFromAddress(t *testing.T) {
	tests := []struct {
		name     string
		addr     []byte
		wantReg  int
		wantZone int
	}{
		{
			name:     "Cyprus1 (region=0, zone=0)",
			addr:     append([]byte{0x00, 0x01}, make([]byte, 18)...),
			wantReg:  0,
			wantZone: 0,
		},
		{
			name:     "region=1, zone=2",
			addr:     append([]byte{0x12, 0x01}, make([]byte, 18)...),
			wantReg:  1,
			wantZone: 2,
		},
		{
			name:     "region=15, zone=15",
			addr:     append([]byte{0xFF, 0x01}, make([]byte, 18)...),
			wantReg:  15,
			wantZone: 15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := common.LocationFromAddressBytes(tt.addr)
			if loc.Region() != tt.wantReg {
				t.Errorf("region = %d, want %d", loc.Region(), tt.wantReg)
			}
			if loc.Zone() != tt.wantZone {
				t.Errorf("zone = %d, want %d", loc.Zone(), tt.wantZone)
			}
		})
	}
}

func TestIsQiQuaiAddress(t *testing.T) {
	quaiAddr := make([]byte, 20)
	quaiAddr[1] = 0x7F // 127 = Quai
	if !IsQuaiAddress(quaiAddr) {
		t.Error("expected Quai address")
	}
	if IsQiAddress(quaiAddr) {
		t.Error("should not be Qi address")
	}

	qiAddr := make([]byte, 20)
	qiAddr[1] = 0x80 // 128 = Qi
	if !IsQiAddress(qiAddr) {
		t.Error("expected Qi address")
	}
	if IsQuaiAddress(qiAddr) {
		t.Error("should not be Quai address")
	}

	// Boundary: 0 is Quai
	zeroAddr := make([]byte, 20)
	if !IsQuaiAddress(zeroAddr) {
		t.Error("byte[1]=0 should be Quai")
	}

	// Boundary: 255 is Qi
	maxAddr := make([]byte, 20)
	maxAddr[1] = 0xFF
	if !IsQiAddress(maxAddr) {
		t.Error("byte[1]=255 should be Qi")
	}
}

func TestIsValidAddressForZone(t *testing.T) {
	zone00 := common.Location{0, 0} // BytePrefix = 0x00

	// Quai address in zone (0,0)
	quaiAddr := make([]byte, 20)
	quaiAddr[0] = 0x00
	quaiAddr[1] = 0x50 // Quai (< 128)
	if !IsValidAddressForZone(CoinTypeQuai, quaiAddr, zone00) {
		t.Error("should be valid Quai address for zone 0,0")
	}
	if IsValidAddressForZone(CoinTypeQi, quaiAddr, zone00) {
		t.Error("Quai address should not be valid for Qi coin type")
	}

	// Qi address in zone (0,0)
	qiAddr := make([]byte, 20)
	qiAddr[0] = 0x00
	qiAddr[1] = 0x90 // Qi (> 127)
	if !IsValidAddressForZone(CoinTypeQi, qiAddr, zone00) {
		t.Error("should be valid Qi address for zone 0,0")
	}
	if IsValidAddressForZone(CoinTypeQuai, qiAddr, zone00) {
		t.Error("Qi address should not be valid for Quai coin type")
	}

	// Wrong zone
	zone12 := common.Location{1, 2} // BytePrefix = 0x12
	if IsValidAddressForZone(CoinTypeQuai, quaiAddr, zone12) {
		t.Error("zone mismatch should return false")
	}

	// Address in zone (1,2)
	addr12 := make([]byte, 20)
	addr12[0] = 0x12
	addr12[1] = 0x30 // Quai
	if !IsValidAddressForZone(CoinTypeQuai, addr12, zone12) {
		t.Error("should be valid for zone 1,2")
	}
}

func TestIsValidAddressForZone_EdgeCases(t *testing.T) {
	zone := common.Location{0, 0}

	// Too short address
	if IsValidAddressForZone(CoinTypeQuai, []byte{0x00}, zone) {
		t.Error("short address should be invalid")
	}

	// Empty zone
	if IsValidAddressForZone(CoinTypeQuai, make([]byte, 20), common.Location{}) {
		t.Error("empty zone should be invalid")
	}

	// Invalid coin type
	addr := make([]byte, 20)
	addr[1] = 0x50
	if IsValidAddressForZone(999, addr, zone) {
		t.Error("invalid coin type should be invalid")
	}
}
