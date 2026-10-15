package core

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

// The test difficulty is 2^100 and the PoW hash is 2^153, so the intrinsic
// entropy is 3 bits above the zone threshold. With expansion number 0, an
// honest header with zero parent delta entropy is a region block.
var (
	calcOrderTestDifficulty = new(big.Int).Lsh(big.NewInt(1), 100)
	calcOrderTestPowHash    = common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 153))
)

func activateConsensusFixForkForTest(t *testing.T) {
	t.Helper()
	setForkForTest(t, &params.ConsensusFixForkBlock, 1)
}

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
	activateConsensusFixForkForTest(t)
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

func TestCalcOrderSharedForkBoundaryDoesNotCarryPoisonedOrder(t *testing.T) {
	setForkForTest(t, &params.ConsensusFixForkBlock, 2)
	honest := newCalcOrderTestBlock(types.EmptyHeader())
	forged := types.NewWorkObject(honest.WorkObjectHeader(), types.EmptyWorkObjectBody(), nil)
	forged.Body().SetHeader(forgedBodyHeader())
	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	if _, order, err := hc.CalcOrder(forged); err != nil || order != common.PRIME_CTX {
		t.Fatalf("historical order changed: order=%d err=%v", order, err)
	}
	if _, _, cached := hc.CheckInCalcOrderCache(honest.Hash()); cached {
		t.Fatal("pre-fork forged order survived in the cache")
	}
	setForkForTest(t, &params.ConsensusFixForkBlock, 1)
	if _, _, err := hc.CalcOrder(forged); !errors.Is(err, ErrHeaderHashMismatch) {
		t.Fatalf("post-fork forged body accepted: %v", err)
	}
	if _, order, err := hc.CalcOrder(honest); err != nil || order != common.REGION_CTX {
		t.Fatalf("honest order changed after fork: order=%d err=%v", order, err)
	}
}

// C2: the gossip sanity checks must reject a forged body header before the
// block is cached, stored or relayed.
func TestSanityCheckRejectsForgedBodyHeader(t *testing.T) {
	activateConsensusFixForkForTest(t)
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

// The commitment checks use real coinbase and merkle encoding. Only the PoW
// engine is mocked, so these tests do not need to mine a donor block.
func newCalcOrderKawpowBlock() *types.WorkObject {
	wh := newTestKawpowHeader(params.KawPowForkBlock + params.KawPowTransitionPeriod + 1)
	wh.SetDifficulty(new(big.Int).Set(calcOrderTestDifficulty))
	wh.SetTime(123)
	body := types.EmptyWorkObjectBody()
	wh.SetHeaderHash(body.Header().Hash())
	coinbase := types.NewAuxPowCoinbaseTx(types.Kawpow, 100, []byte{0x51}, wh.SealHash(), 123)
	wh.AuxPow().SetTransaction(coinbase)
	wh.AuxPow().SetHeader(types.NewAuxPowHeader(&types.RavencoinBlockHeader{
		Version: 4, Time: 123, Bits: 0x1d00ffff, Height: 100, Nonce64: 7,
		HashMerkleRoot: types.CalculateMerkleRoot(types.Kawpow, coinbase, wh.AuxPow().MerkleBranch()),
	}))
	return types.NewWorkObject(wh, body, nil)
}

func forgeCalcOrderKawpowBody(honest *types.WorkObject, rewriteCoinbase bool) *types.WorkObject {
	forged := types.CopyWorkObject(honest)
	forged.Body().SetHeader(forgedBodyHeader())
	forged.WorkObjectHeader().SetHeaderHash(forged.Header().Hash())
	if rewriteCoinbase {
		// Restore the seal commitment but retain the original donor merkle root.
		forged.AuxPow().SetTransaction(types.NewAuxPowCoinbaseTx(types.Kawpow, 100, []byte{0x51}, forged.SealHash(), 123))
	}
	return forged
}

func TestCalcOrderRejectsForgedAuxPowBinding(t *testing.T) {
	activateConsensusFixForkForTest(t)
	for _, rewriteCoinbase := range []bool{false, true} {
		for _, warmCache := range []bool{false, true} {
			name := fmt.Sprintf("rewrite_coinbase=%t/warm_cache=%t", rewriteCoinbase, warmCache)
			t.Run(name, func(t *testing.T) {
				honest := newCalcOrderKawpowBlock()
				forged := forgeCalcOrderKawpowBody(honest, rewriteCoinbase)
				if !rewriteCoinbase && forged.Hash() != honest.Hash() {
					t.Fatal("body substitution must retain the honest AuxPow hash")
				}
				if forged.AuxPow().ConvertToTemplate().Hash() != honest.AuxPow().ConvertToTemplate().Hash() {
					t.Fatal("the signed donor template must remain unchanged")
				}
				hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
				if warmCache {
					if _, _, err := hc.CalcOrder(honest); err != nil {
						t.Fatal(err)
					}
				}
				wantError := "seal hash"
				if rewriteCoinbase {
					wantError = "merkle root"
				}
				if _, _, err := hc.CalcOrder(forged); err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("got %v, want a %s binding error", err, wantError)
				}
				if !warmCache || rewriteCoinbase {
					if _, _, cached := hc.CheckInCalcOrderCache(forged.Hash()); cached {
						t.Fatal("forged order was cached")
					}
				}
				if _, order, err := hc.CalcOrder(honest); err != nil || order != common.REGION_CTX {
					t.Fatalf("honest order changed: order=%d err=%v", order, err)
				}
			})
		}
	}
}

func TestCalcOrderAuxPowSharedForkBoundary(t *testing.T) {
	height := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	setForkForTest(t, &params.ConsensusFixForkBlock, height+1)
	honest := newCalcOrderKawpowBlock()
	forged := forgeCalcOrderKawpowBody(honest, false)
	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	if _, _, err := hc.CalcOrder(forged); err != nil {
		t.Fatalf("historical early order check changed: %v", err)
	}
	if _, _, cached := hc.CheckInCalcOrderCache(forged.Hash()); cached {
		t.Fatal("pre-fork forged AuxPow order survived in the cache")
	}
	setForkForTest(t, &params.ConsensusFixForkBlock, height)
	if _, _, err := hc.CalcOrder(forged); err == nil || !strings.Contains(err.Error(), "seal hash") {
		t.Fatalf("post-fork forged AuxPow binding accepted: %v", err)
	}
	if _, order, err := hc.CalcOrder(honest); err != nil || order != common.REGION_CTX {
		t.Fatalf("honest order changed after fork: order=%d err=%v", order, err)
	}
}

func TestSanityCheckRejectsForgedAuxPowBinding(t *testing.T) {
	activateConsensusFixForkForTest(t)
	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	v := &BlockValidator{config: hc.config, hc: hc}
	for name, check := range map[string]func(*types.WorkObject) error{
		"block view": v.SanityCheckWorkObjectBlockViewBody, "header view": v.SanityCheckWorkObjectHeaderViewBody,
	} {
		t.Run(name, func(t *testing.T) {
			honest := newCalcOrderKawpowBlock()
			if err := check(honest); err != nil {
				t.Fatalf("honest body rejected: %v", err)
			}
			for _, rewriteCoinbase := range []bool{false, true} {
				if err := check(forgeCalcOrderKawpowBody(honest, rewriteCoinbase)); err == nil {
					t.Fatal("forged AuxPow binding accepted")
				}
			}
		})
	}
}

func TestWriteBlockRejectsForgedAuxPowBeforeStorage(t *testing.T) {
	for _, afterFork := range []bool{false, true} {
		for _, rewriteCoinbase := range []bool{false, true} {
			t.Run(fmt.Sprintf("after_fork=%t/rewrite_coinbase=%t", afterFork, rewriteCoinbase), func(t *testing.T) {
				if afterFork {
					activateConsensusFixForkForTest(t)
				} else {
					setForkForTest(t, &params.ConsensusFixForkBlock, params.KawPowForkBlock+params.KawPowTransitionPeriod+2)
				}
				hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
				hc.numberCache, _ = lru.New[common.Hash, uint64](10)
				c := &Core{sl: &Slice{hc: hc}, logger: log.Global}
				c.appendQueue, _ = lru.New[common.Hash, blockNumberAndRetryCounter](10)
				honest := newCalcOrderKawpowBlock()
				if _, _, err := hc.CalcOrder(honest); err != nil {
					t.Fatal(err)
				}
				forged := forgeCalcOrderKawpowBody(honest, rewriteCoinbase)
				c.WriteBlock(forged)
				if hc.bc.blockCache.Contains(forged.Hash()) || rawdb.ReadHeaderNumber(hc.headerDb, forged.Hash()) != nil {
					t.Fatal("forged candidate was stored")
				}
				if c.appendQueue.Len() != 0 {
					t.Fatal("forged candidate reached the append queue")
				}
				c.WriteBlock(honest)
				if !hc.bc.blockCache.Contains(honest.Hash()) || rawdb.ReadHeaderNumber(hc.headerDb, honest.Hash()) == nil {
					t.Fatal("honest candidate was not stored")
				}
			})
		}
	}
}

func TestCalcOrderRejectsUnregisteredZeroNumber(t *testing.T) {
	activateConsensusFixForkForTest(t)
	block := newCalcOrderKawpowBlock()
	block.WorkObjectHeader().SetNumber(big.NewInt(0))
	// Even a consistent commitment must not turn an arbitrary object into genesis.
	coinbase := types.NewAuxPowCoinbaseTx(types.Kawpow, 100, []byte{0x51}, block.SealHash(), 123)
	block.AuxPow().SetTransaction(coinbase)
	block.AuxPow().SetHeader(types.NewBlockHeader(types.Kawpow, 4, [32]byte{}, types.CalculateMerkleRoot(types.Kawpow, coinbase, nil), 123, 0x1d00ffff, 7, 100))
	hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
	if _, _, err := hc.CalcOrder(block); err == nil {
		t.Fatal("unregistered zero-number object accepted as genesis")
	}
}

func TestCalcOrderPreservesGenesisAndTransitionBlocks(t *testing.T) {
	activateConsensusFixForkForTest(t)
	t.Run("registered genesis", func(t *testing.T) {
		block := newCalcOrderTestBlock(types.EmptyHeader())
		block.WorkObjectHeader().SetNumber(big.NewInt(0))
		hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
		hc.AddGenesisHash(block.Hash())
		if entropy, order, err := hc.CalcOrder(block); err != nil || order != common.PRIME_CTX || entropy.Sign() != 0 {
			t.Fatalf("registered genesis rejected: entropy=%v order=%d err=%v", entropy, order, err)
		}
	})
	t.Run("registered post-KawPow expansion block", func(t *testing.T) {
		block := newCalcOrderKawpowBlock()
		hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
		hc.AddGenesisHash(block.Hash())
		if _, order, err := hc.CalcOrder(block); err != nil || order != common.REGION_CTX {
			t.Fatalf("bound expansion block rejected: order=%d err=%v", order, err)
		}
		if _, _, err := hc.CalcOrder(forgeCalcOrderKawpowBody(block, false)); err == nil {
			t.Fatal("registered hash bypassed binding validation")
		}
	})
	for _, height := range []uint64{params.KawPowForkBlock - 1, params.KawPowForkBlock, params.KawPowForkBlock + params.KawPowTransitionPeriod - 1} {
		t.Run(fmt.Sprintf("ProgPow at %d", height), func(t *testing.T) {
			block := newCalcOrderTestBlock(types.EmptyHeader())
			block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(height))
			if block.Hash() != block.WorkObjectHeader().WoProgpowHash() {
				t.Fatal("transition ProgPow block must hash its own seal and nonce")
			}
			hc := newSecurityTestHeaderChain(t, calcOrderTestPowHash)
			if _, order, err := hc.CalcOrder(block); err != nil || order != common.REGION_CTX {
				t.Fatalf("bound ProgPow block rejected: order=%d err=%v", order, err)
			}
			forged := types.CopyWorkObject(block)
			forged.Body().SetHeader(forgedBodyHeader())
			forged.WorkObjectHeader().SetHeaderHash(forged.Header().Hash())
			if forged.Hash() == block.Hash() {
				t.Fatal("distinct transition ProgPow seals share a block hash")
			}
			if _, order, err := hc.CalcOrder(forged); err != nil || order != common.PRIME_CTX {
				t.Fatalf("distinct transition ProgPow order was reused: order=%d err=%v", order, err)
			}
		})
	}
}

func TestAuxPowBindingPreservesScryptCommitment(t *testing.T) {
	header := newTestWorkObjectHeader(params.KawPowForkBlock + params.KawPowTransitionPeriod + 1)
	dogeHash := common.Hash{1}
	commitment := types.CreateAuxMerkleRoot(dogeHash, header.SealHash())
	coinbase := types.NewAuxPowCoinbaseTx(types.Scrypt, 100, []byte{0x51}, commitment, 123)
	root := types.CalculateMerkleRoot(types.Scrypt, coinbase, nil)
	header.SetAuxPow(types.NewAuxPow(types.Scrypt, types.NewBlockHeader(types.Scrypt, 1, [32]byte{}, root, 123, 0x1d00ffff, 7, 100), dogeHash.Bytes(), nil, nil, coinbase))
	if err := verifyAuxPowBinding(header); err != nil {
		t.Fatalf("valid Scrypt commitment rejected: %v", err)
	}
	header.AuxPow().SetAuxPow2([]byte{1})
	if err := verifyAuxPowBinding(header); err == nil {
		t.Fatal("short Scrypt AuxPow2 accepted")
	}
}
