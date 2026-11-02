package core

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func TestWorkShareDataRejectedBeforeRelayOrCache(t *testing.T) {
	hc := newSecurityTestHeaderChain(t, common.Hash{})
	validator := NewBlockValidator(hc.config, hc, nil)
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "header data field is empty"},
		{"invalid lockup byte", []byte{5}, "lock byte in header data"},
		{"external lockup contract", append([]byte{0}, bytes.Repeat([]byte{0xff}, common.AddressLength)...), "out-of-scope lockup contract"},
		{"external beneficiary", append(make([]byte, 1+common.AddressLength), bytes.Repeat([]byte{0xff}, common.AddressLength)...), "out-of-scope beneficiary"},
		{"valid maximum lockup byte", []byte{3}, ""},
		{"valid lockup byte distinct from Lock", []byte{1}, ""},
	}
	for _, era := range []struct {
		name     string
		newShare func() *types.WorkObjectHeader
	}{
		{"before KawPow", func() *types.WorkObjectHeader { return newTestWorkObjectHeader(1) }},
		{"post Singularity", func() *types.WorkObjectHeader {
			share := newTestShaShare(params.SingularityForkBlock + 1)
			share.SetNumber(new(big.Int).SetUint64(params.SingularityForkBlock + 1))
			return share
		}},
	} {
		for _, tc := range tests {
			t.Run(era.name+"/"+tc.name, func(t *testing.T) {
				share := era.newShare()
				share.SetData(tc.data)
				wo := types.NewWorkObject(share, types.EmptyWorkObjectBody(), nil)
				err := validator.SanityCheckWorkObjectShareViewBody(wo)
				if tc.want == "" {
					if err != nil {
						t.Fatalf("valid share rejected: %v", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want %q", err, tc.want)
				}
				if tc.want != "" {
					if _, _, _, err := (&Slice{hc: hc}).ReceiveWorkShare(share); err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("RPC admission got %v, want %q", err, tc.want)
					}
					if err := (&worker{hc: hc}).AddWorkShare(share); err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("cache admission got %v, want %q", err, tc.want)
					}
					blockHeader := newTestWorkObjectHeader(share.PrimeTerminusNumber().Uint64())
					blockHeader.SetNonce(types.EncodeNonce(2))
					block := newTestBlockWithUncles(blockHeader, []*types.WorkObjectHeader{share})
					if err := hc.VerifyUncles(block); err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("block validation got %v, want %q", err, tc.want)
					}
				}
			})
		}
	}
}
