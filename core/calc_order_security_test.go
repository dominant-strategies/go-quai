package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// The test difficulty is 2^100 and the PoW hash is 2^153, so the intrinsic
// entropy is 3 bits above the zone threshold. With expansion number 0, an
// honest header with zero parent delta entropy is a region block.
var (
	calcOrderTestDifficulty = new(big.Int).Lsh(big.NewInt(1), 100)
	calcOrderTestPowHash    = common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 153))
)

// newCalcOrderTestBlock returns a zone block before the KawPow fork whose
// work object header commits to the given body header.
func newCalcOrderTestBlock(bodyHeader *types.Header) *types.WorkObject {
	wh := newTestWorkObjectHeader(1)
	wh.SetDifficulty(new(big.Int).Set(calcOrderTestDifficulty))
	wh.SetHeaderHash(bodyHeader.Hash())
	body := types.EmptyWorkObjectBody()
	body.SetHeader(bodyHeader)
	return types.NewWorkObject(wh, body, nil)
}

// forgedBodyHeader returns a body header that sets a high region parent delta
// entropy, which would make the block a prime block.
func forgedBodyHeader() *types.Header {
	forged := types.EmptyHeader()
	forged.SetParentDeltaEntropy(new(big.Int).Lsh(big.NewInt(200), common.MantBits), common.REGION_CTX)
	return forged
}

// C2: a peer replays the work object header of a real block with a forged body
// header. The work object hash does not cover the body header, so before the
// fix the forged order was cached under the real block hash.
func TestCalcOrderRejectsForgedBodyHeader(t *testing.T) {
	honest := newCalcOrderTestBlock(types.EmptyHeader())

	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	_, honestOrder, err := hc.CalcOrder(honest)
	if err != nil {
		t.Fatal(err)
	}

	// Test setup: a body header with these values, bound by its own header
	// hash, has a different order. So the forged body would change the order.
	_, forgedOrder, err := newSecurityTestHeaderChain(t, calcOrderTestPowHash).CalcOrder(newCalcOrderTestBlock(forgedBodyHeader()))
	if err != nil {
		t.Fatal(err)
	}
	if forgedOrder == honestOrder {
		t.Fatalf("test setup: forged body must change the order, both are %d", honestOrder)
	}

	// The attack: same work object header as the honest block, forged body.
	forged := types.NewWorkObject(honest.WorkObjectHeader(), types.EmptyWorkObjectBody(), nil)
	forged.Body().SetHeader(forgedBodyHeader())
	if forged.Hash() != honest.Hash() {
		t.Fatal("test setup: forged block must have the honest block hash")
	}

	hc = newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	if _, _, err := hc.CalcOrder(forged); !errors.Is(err, ErrHeaderHashMismatch) {
		t.Fatalf("got %v, want %v", err, ErrHeaderHashMismatch)
	}
	if _, _, cached := hc.CheckInCalcOrderCache(honest.Hash()); cached {
		t.Fatal("forged body header must not be cached")
	}
	_, order, err := hc.CalcOrder(honest)
	if err != nil {
		t.Fatal(err)
	}
	if order != honestOrder {
		t.Fatalf("honest block order poisoned: got %d, want %d", order, honestOrder)
	}
}

// C2: the gossip sanity checks must reject a forged body header before the
// block is cached, stored or relayed.
func TestSanityCheckRejectsForgedBodyHeader(t *testing.T) {
	honest := newCalcOrderTestBlock(types.EmptyHeader())
	forged := types.NewWorkObject(honest.WorkObjectHeader(), types.EmptyWorkObjectBody(), nil)
	forged.Body().SetHeader(forgedBodyHeader())

	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	v := &BlockValidator{config: hc.config, hc: hc}
	for name, check := range map[string]func(*types.WorkObject) error{
		"block view":  v.SanityCheckWorkObjectBlockViewBody,
		"header view": v.SanityCheckWorkObjectHeaderViewBody,
	} {
		if err := check(forged); !errors.Is(err, ErrHeaderHashMismatch) {
			t.Fatalf("%s: got %v, want %v", name, err, ErrHeaderHashMismatch)
		}
		if err := check(honest); errors.Is(err, ErrHeaderHashMismatch) {
			t.Fatalf("%s: honest block rejected: %v", name, err)
		}
	}
}
