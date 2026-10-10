package common

import (
	"errors"
	"reflect"
	"testing"
)

func TestIsInQuaiLedgerScope(t *testing.T) {
	address := HexToAddress("0x8ECc962Ba5481F7B42f2BAaBD55afAB6a27Fc197", Location{0, 0})
	_, err := address.InternalAndQuaiAddress()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	tests := []struct {
		name     string
		address  Address
		expected bool
	}{
		{name: "Quai address", address: HexToAddress("0x8ECc962Ba5481F7B42f2BAaBD55afAB6a27Fc197", Location{0, 0}), expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.address.InternalAndQuaiAddress()
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
			if result.IsInQuaiLedgerScope() != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result.IsInQuaiLedgerScope())
			}
		})
	}
}
func TestInternalAddress(t *testing.T) {
	// Test cases
	tests := []struct {
		name          string
		address       Address
		expected      InternalAddress
		expectedError error
	}{
		{
			name:          "Nil inner field",
			address:       Address{inner: nil},
			expected:      InternalAddress{},
			expectedError: ErrNilInner,
		},
		{
			name:          "Incorrect type in inner field",
			address:       HexToAddress("0x0103e45Aa16163f2663015B6695894D918866D19", Location{0, 0}),
			expected:      InternalAddress{},
			expectedError: ErrExternalAddress,
		},
		{
			name:          "Nil InternalAddress pointer",
			address:       Address{inner: (*InternalAddress)(nil)},
			expected:      InternalAddress{},
			expectedError: ErrNilInner,
		},
		{
			name:          "Valid InternalAddress",
			address:       HexToAddress("0x0003e45Aa16163f2663015B6695894D918866D19", Location{0, 0}),
			expected:      InternalAddress(HexToAddressBytes("0x0003e45Aa16163f2663015B6695894D918866D19")),
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.address.InternalAddress()
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Expected result %+v, got %+v", tt.expected, result)
			}
		})
	}
}
