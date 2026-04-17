package quaiapi

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
)

func TestMarshalPendingWorkSharesByPow(t *testing.T) {
	workShares := []*types.WorkObjectHeader{
		newTestPendingWorkShare(1, nil),
		newTestPendingWorkShare(2, newTestAuxPow(types.Kawpow, 2)),
		newTestPendingWorkShare(3, newTestAuxPow(types.SHA_BTC, 3)),
		newTestPendingWorkShare(4, newTestAuxPow(types.SHA_BCH, 4)),
	}

	marshaled := marshalPendingWorkSharesByPow(workShares, "v1")

	if len(marshaled[types.Progpow.String()]) != 1 {
		t.Fatalf("expected 1 progpow workshare, got %d", len(marshaled[types.Progpow.String()]))
	}
	if len(marshaled[types.Kawpow.String()]) != 1 {
		t.Fatalf("expected 1 kawpow workshare, got %d", len(marshaled[types.Kawpow.String()]))
	}
	if len(marshaled[types.SHA_BTC.String()]) != 1 {
		t.Fatalf("expected 1 sha_btc workshare, got %d", len(marshaled[types.SHA_BTC.String()]))
	}
	if len(marshaled[types.SHA_BCH.String()]) != 1 {
		t.Fatalf("expected 1 sha_bch workshare, got %d", len(marshaled[types.SHA_BCH.String()]))
	}

	for pow, entries := range marshaled {
		if len(entries) == 0 {
			t.Fatalf("expected entries for %s", pow)
		}
		entry := entries[0]
		if _, ok := entry["hash"]; !ok {
			t.Fatalf("expected hash field for %s", pow)
		}
		if _, ok := entry["number"]; !ok {
			t.Fatalf("expected number field for %s", pow)
		}
		if _, ok := entry["auxpow"]; ok {
			t.Fatalf("did not expect auxpow field in v1 marshaling for %s", pow)
		}
	}
}

func newTestPendingWorkShare(number int64, auxpow *types.AuxPow) *types.WorkObjectHeader {
	return types.NewWorkObjectHeader(
		common.Hash{byte(number), 0x01},
		common.Hash{byte(number), 0x02},
		big.NewInt(number),
		big.NewInt(1000+number),
		big.NewInt(3000001),
		common.Hash{byte(number), 0x03},
		types.BlockNonce{byte(number)},
		0,
		uint64(1700000000+number),
		common.Location{0, 0},
		common.Address{},
		[]byte{byte(number)},
		auxpow,
		&types.PowShareDiffAndCount{},
		&types.PowShareDiffAndCount{},
		big.NewInt(10),
		big.NewInt(20),
		big.NewInt(30),
	)
}

func newTestAuxPow(powID types.PowID, seed byte) *types.AuxPow {
	return types.NewAuxPow(
		powID,
		types.NewBlockHeader(powID, 1, [32]byte{seed}, [32]byte{seed + 1}, 1700000000, 1, uint32(seed), 1),
		nil,
		nil,
		nil,
		nil,
	)
}

func TestValidateRewardPercentiles(t *testing.T) {
	tests := []struct {
		name        string
		percentiles []float64
		wantErr     bool
	}{
		{name: "valid empty", percentiles: nil},
		{name: "valid sorted", percentiles: []float64{0, 10, 50, 100}},
		{name: "invalid descending", percentiles: []float64{10, 5}, wantErr: true},
		{name: "invalid low", percentiles: []float64{-1}, wantErr: true},
		{name: "invalid high", percentiles: []float64{101}, wantErr: true},
	}

	for _, test := range tests {
		err := validateRewardPercentiles(test.percentiles)
		if test.wantErr && err == nil {
			t.Fatalf("%s: expected error", test.name)
		}
		if !test.wantErr && err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
	}
}

func TestFeeHistoryResultJSONTypes(t *testing.T) {
	result := &feeHistoryResult{
		OldestBlock:  (*hexutil.Big)(big.NewInt(1)),
		BaseFee:      []*hexutil.Big{(*hexutil.Big)(big.NewInt(2)), (*hexutil.Big)(big.NewInt(3))},
		GasUsedRatio: []float64{0.5},
		Reward:       [][]*hexutil.Big{{(*hexutil.Big)(big.NewInt(4))}},
	}
	if result.OldestBlock == nil || len(result.BaseFee) != 2 || len(result.Reward) != 1 {
		t.Fatal("unexpected fee history result shape")
	}
}
