package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

func unsignedSHAShareForCacheTest(t *testing.T, prime uint64, startNonce uint32) *types.WorkObjectHeader {
	t.Helper()
	wh := newTestWorkObjectHeader(prime)
	wh.SetTime(1000)
	wh.SetPrimaryCoinbase(common.HexToAddress("0x0100000000000000000000000000000000000000", common.Location{0, 0}))
	if _, err := wh.PrimaryCoinbase().InternalAddress(); err == nil {
		t.Fatal("coinbase must be outside the zone")
	}
	coinbase := types.NewAuxPowCoinbaseTx(types.SHA_BTC, 100, []byte{0x51}, wh.SealHash(), 123)
	merkle := types.CalculateMerkleRoot(types.SHA_BTC, coinbase, nil)
	target := new(big.Int).Div(common.Big2e256, big.NewInt(1000))
	for nonce := startNonce; nonce < startNonce+100000; nonce++ {
		header := types.NewBitcoinBlockHeader(1, [32]byte{1}, merkle, 1000, 0x1d00ffff, nonce)
		if new(big.Int).SetBytes(types.NewAuxPowHeader(header).PowHash().Bytes()).Cmp(target) < 0 {
			wh.SetAuxPow(types.NewAuxPow(types.SHA_BTC, types.NewAuxPowHeader(header), nil, []byte{1}, nil, coinbase))
			if !wh.IsShaOrScryptShareWithInvalidAddress() {
				t.Fatal("unsigned SHA share must use the outside-zone signature exception")
			}
			return wh
		}
	}
	t.Fatal("could not mine test SHA share")
	return nil
}

func TestAddWorkShareKeepsOneCacheEntryPerUnsignedWork(t *testing.T) {
	prime := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	hc := newSecurityTestHeaderChain(t, common.Hash{})
	hc.currentHeader.Store(newTestBlockWithUncles(newTestWorkObjectHeader(prime), nil))
	newCache := func() *lru.Cache[common.Hash, types.WorkObjectHeader] {
		cache, err := lru.New[common.Hash, types.WorkObjectHeader](3)
		if err != nil {
			t.Fatal(err)
		}
		return cache
	}
	w := &worker{hc: hc, kawpowShares: newCache(), shaShares: newCache(), scryptShares: newCache(), logger: log.Global}
	honest := unsignedSHAShareForCacheTest(t, prime, 0)
	attacker := unsignedSHAShareForCacheTest(t, prime, 10000)
	if honest.PowIdentity() == attacker.PowIdentity() {
		t.Fatal("test shares must carry distinct work")
	}
	if err := w.AddWorkShare(honest); err != nil {
		t.Fatalf("add honest share: %v", err)
	}
	for i := byte(0); i < 4; i++ {
		clone := types.CopyWorkObjectHeader(attacker)
		clone.AuxPow().SetSignature([]byte{i})
		if clone.PowIdentity() != attacker.PowIdentity() {
			t.Fatal("signature mutation changed the donor work identity")
		}
		if err := w.AddWorkShare(clone); err != nil {
			t.Fatalf("add clone %d: %v", i, err)
		}
	}
	if got := w.shaShares.Len(); got != 2 {
		t.Fatalf("one attacker work occupied %d cache entries with the honest share, want 2", got)
	}
	if !w.shaShares.Contains(honest.PowIdentity()) || !w.shaShares.Contains(attacker.PowIdentity()) {
		t.Fatal("cache lost one of the two distinct works")
	}
}

func TestEvictRejectedUnsignedShareByPowIdentity(t *testing.T) {
	prime := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	share := unsignedSHAShareForCacheTest(t, prime, 0)
	cache, err := lru.New[common.Hash, types.WorkObjectHeader](3)
	if err != nil {
		t.Fatal(err)
	}
	kawpow, _ := lru.New[common.Hash, types.WorkObjectHeader](3)
	scrypt, _ := lru.New[common.Hash, types.WorkObjectHeader](3)
	cache.Add(share.PowIdentity(), *share)
	w := &worker{kawpowShares: kawpow, shaShares: cache, scryptShares: scrypt}
	block := newTestBlockWithUncles(newTestWorkObjectHeader(prime), []*types.WorkObjectHeader{share})
	rejected := w.evictRejectedUncles(block, func(*types.WorkObject) error { return errors.New("invalid uncle") })
	if len(rejected) != 1 || rejected[0] != share.Hash() {
		t.Fatalf("rejected hashes = %v, want %s", rejected, share.Hash())
	}
	if cache.Contains(share.PowIdentity()) {
		t.Fatal("rejected work remains in the cache")
	}
}
