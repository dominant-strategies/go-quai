package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

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
