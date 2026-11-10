package stratum

import (
	"math/big"
	"sync"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

func TestKawpowSubmissionsDoNotMutateSharedJob(t *testing.T) {
	auxHeader := types.NewAuxPowHeader(types.NewRavencoinBlockHeader(1, [32]byte{}, [32]byte{}, 1, 1, 1))
	header := &types.WorkObjectHeader{}
	header.SetNumber(big.NewInt(1))
	header.SetDifficulty(big.NewInt(1))
	header.SetPrimeTerminusNumber(big.NewInt(1))
	header.SetAuxPow(types.NewAuxPow(types.Kawpow, auxHeader, nil, nil, nil, nil))
	job := &kawpowJob{pending: types.NewWorkObject(header, nil, nil)}

	const submissions = 32
	type result struct {
		pending *types.WorkObject
		err     error
	}
	results := make([]result, submissions)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i].pending, results[i].err = copyKawpowJobPending(job)
			if results[i].err != nil {
				return
			}
			results[i].pending.AuxPow().Header().SetNonce64(uint64(i + 1))
			results[i].pending.AuxPow().Header().SetMixHash(common.Hash{byte(i + 1)})
		}(i)
	}
	wg.Wait()

	shared := job.pending.(*types.WorkObject).AuxPow().Header()
	if shared.Nonce64() != 0 || shared.MixHash() != (common.Hash{}) {
		t.Fatalf("shared job changed: nonce=%d mix=%s", shared.Nonce64(), shared.MixHash())
	}
	for i, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		got := result.pending.AuxPow().Header()
		if got.Nonce64() != uint64(i+1) || got.MixHash() != (common.Hash{byte(i + 1)}) {
			t.Fatalf("submission %d changed: nonce=%d mix=%s", i, got.Nonce64(), got.MixHash())
		}
	}
}
