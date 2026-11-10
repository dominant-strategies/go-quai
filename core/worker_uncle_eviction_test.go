package core

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

func TestCommitUncleEvictsShareRejectedByVerifyUncles(t *testing.T) {
	location := common.Location{0, 0}
	// The fixed PoW hash meets the work-share target, but not the full-block
	// target. AddWorkShare therefore accepts the share before VerifyUncles runs.
	hc := newSecurityTestHeaderChain(t, common.Hash{1})
	hc.powConfig = params.PowConfig{PowMode: params.ModeNormal, NodeLocation: location}
	hc.numberCache, _ = lru.New[common.Hash, uint64](10)

	makeBlock := func(number int64, parent common.Hash) *types.WorkObject {
		header := newTestWorkObjectHeader(1)
		header.SetNumber(big.NewInt(number))
		header.SetParentHash(parent)
		header.SetNonce(types.EncodeNonce(uint64(number)))
		return newTestBlockWithUncles(header, nil)
	}
	block7 := makeBlock(7, common.Hash{1})
	block8 := makeBlock(8, block7.Hash())
	block9 := makeBlock(9, block8.Hash())
	for _, block := range []*types.WorkObject{block7, block8, block9} {
		hc.numberCache.Add(block.Hash(), block.NumberU64(common.ZONE_CTX))
		hc.headerCache.Add(block.Hash(), *block)
		hc.bc.blockCache.Add(block.Hash(), *block)
		rawdb.WriteTermini(hc.headerDb, block.Hash(), types.EmptyTermini())
	}
	hc.AddGenesisHash(block7.Hash())
	candidate := makeBlock(10, block9.Hash())
	share := newTestWorkObjectHeader(1)
	share.SetNumber(big.NewInt(9))
	share.SetParentHash(block8.Hash())
	share.SetNonce(types.EncodeNonce(99))
	share.SetDifficulty(big.NewInt(2000))
	key := share.PowIdentity()
	kawpow, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	sha, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	scrypt, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	hc.currentHeader.Store(block9)
	w := &worker{hc: hc, kawpowShares: kawpow, shaShares: sha, scryptShares: scrypt, logger: log.Global}
	if err := w.AddWorkShare(share); err != nil {
		t.Fatalf("share admission failed: %v", err)
	}
	if !kawpow.Contains(key) {
		t.Fatal("AddWorkShare did not cache the valid work share")
	}
	env := &environment{wo: candidate, ancestors: mapset.NewSet(), family: mapset.NewSet(), uncles: make(map[common.Hash]*types.WorkObjectHeader)}
	err := w.commitUncle(env, share)
	if err == nil || !strings.Contains(err.Error(), "invalid difficulty") {
		t.Fatalf("commitUncle returned %v, want invalid difficulty error", err)
	}
	if kawpow.Contains(key) {
		t.Fatal("share rejected by per-share VerifyUncles remains cached")
	}
}

func TestCandidateDependentUncleFailureKeepsShare(t *testing.T) {
	for _, err := range []error{consensus.ErrDuplicateUncle, consensus.ErrUncleIsAncestor, consensus.ErrDanglingUncle, consensus.ErrUnknownAncestor, consensus.ErrTooManyUncles, errWorkShareParentNotFound} {
		if shouldEvictWorkShareOnVerifyError(err) {
			t.Fatalf("candidate-dependent failure %v would evict a share", err)
		}
	}
	if !shouldEvictWorkShareOnVerifyError(errors.New("invalid share difficulty")) {
		t.Fatal("permanent share failure would remain cached")
	}
}

func TestFinalizeAssembleEvictsRejectedUncle(t *testing.T) {
	location := common.Location{0, 0}
	config := *params.TestChainConfig
	config.Location = location
	hc := NewTestHeaderChain()
	hc.config = &config
	hc.bc = &BodyDb{chainConfig: &config}
	hc.powConfig = params.PowConfig{PowMode: params.ModeNormal, NodeLocation: location}
	hc.headerDb = rawdb.NewMemoryDatabase(log.Global)
	kawpow, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	sha, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	scrypt, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	w := &worker{hc: hc, kawpowShares: kawpow, shaShares: sha, scryptShares: scrypt, logger: log.Global}

	invalid := newTestWorkObjectHeader(1)
	invalid.SetData([]byte{5})
	kawpow.Add(invalid.Hash(), *invalid)
	block := types.NewWorkObject(newTestWorkObjectHeader(1), types.EmptyWorkObjectBody(), nil)
	_, err := w.FinalizeAssemble(block, nil, nil, nil, []*types.WorkObjectHeader{invalid}, nil, nil, nil, 0, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "lock byte in header data") {
		t.Fatalf("FinalizeAssemble returned %v, want invalid uncle data error", err)
	}
	if kawpow.Contains(invalid.Hash()) {
		t.Fatal("uncle that invalidated the final candidate remains cached")
	}
}

func TestEvictRejectedUnclesKeepsValidShares(t *testing.T) {
	kawpow, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	sha, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	scrypt, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	w := &worker{kawpowShares: kawpow, shaShares: sha, scryptShares: scrypt}

	first := newTestWorkObjectHeader(1)
	bad := newTestWorkObjectHeader(1)
	bad.SetNonce(types.EncodeNonce(2))
	last := newTestWorkObjectHeader(1)
	last.SetNonce(types.EncodeNonce(3))
	kawpow.Add(first.Hash(), *first)
	sha.Add(bad.Hash(), *bad)
	scrypt.Add(last.Hash(), *last)

	block := types.NewWorkObject(newTestWorkObjectHeader(1), types.EmptyWorkObjectBody(), nil)
	block.Body().SetUncles([]*types.WorkObjectHeader{first, bad, last})
	verify := func(candidate *types.WorkObject) error {
		for _, uncle := range candidate.Uncles() {
			if uncle.Hash() == bad.Hash() {
				return errors.New("bad uncle")
			}
		}
		return nil
	}
	rejected := w.evictRejectedUncles(block, verify)
	if len(rejected) != 1 || rejected[0] != bad.Hash() {
		t.Fatalf("rejected = %v, want only %s", rejected, bad.Hash())
	}
	if sha.Contains(bad.Hash()) {
		t.Fatal("rejected share remains in the cache")
	}
	if !kawpow.Contains(first.Hash()) || !scrypt.Contains(last.Hash()) {
		t.Fatal("valid shares were removed with the rejected share")
	}
}

func TestEvictRejectedUnclesFindsInteraction(t *testing.T) {
	kawpow, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	sha, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	scrypt, _ := lru.New[common.Hash, types.WorkObjectHeader](4)
	w := &worker{kawpowShares: kawpow, shaShares: sha, scryptShares: scrypt}

	first := newTestWorkObjectHeader(1)
	second := newTestWorkObjectHeader(1)
	second.SetNonce(types.EncodeNonce(2))
	kawpow.Add(first.Hash(), *first)
	kawpow.Add(second.Hash(), *second)
	block := types.NewWorkObject(newTestWorkObjectHeader(1), types.EmptyWorkObjectBody(), nil)
	block.Body().SetUncles([]*types.WorkObjectHeader{first, second})
	verify := func(candidate *types.WorkObject) error {
		if len(candidate.Uncles()) == 2 {
			return errors.New("uncle pair conflicts")
		}
		return nil
	}
	rejected := w.evictRejectedUncles(block, verify)
	if len(rejected) != 1 || rejected[0] != second.Hash() {
		t.Fatalf("rejected = %v, want the share that completed the invalid pair", rejected)
	}
	if !kawpow.Contains(first.Hash()) || kawpow.Contains(second.Hash()) {
		t.Fatal("cache does not retain only the valid share")
	}
}
