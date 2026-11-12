package core

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

type uncleShareZoneDB struct{ ethdb.Database }

func (uncleShareZoneDB) Location() common.Location { return common.Location{0, 0} }

func TestVerifyUnclesRejectsForgedKawpowShareFields(t *testing.T) {
	location := common.Location{0, 0}
	primeHeight := params.InclusionDepthChangeBlock + params.InclusionDepthUpdatePeriod + 1
	hc := newSecurityTestHeaderChain(t, common.Hash{})
	hc.headerDb = uncleShareZoneDB{hc.headerDb}
	hc.bc.db = hc.headerDb
	hc.powConfig = params.PowConfig{PowMode: params.ModeNormal, NodeLocation: location}
	numberCache, err := lru.New[common.Hash, uint64](16)
	if err != nil {
		t.Fatal(err)
	}
	hc.numberCache = numberCache

	var coinbase common.Address
	for i := 0; i < 100000; i++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		candidate := crypto.PubkeyToAddress(key.PublicKey, location)
		if candidate.IsInQuaiLedgerScope() && candidate.Location().Equal(location) {
			coinbase = candidate
			break
		}
	}
	if _, err := coinbase.InternalAndQuaiAddress(); err != nil {
		t.Fatalf("could not find Quai coinbase: %v", err)
	}

	parent := types.EmptyWorkObject(common.ZONE_CTX)
	parent.WorkObjectHeader().SetLocation(location)
	parent.SetNumber(big.NewInt(1), common.ZONE_CTX)
	parent.WorkObjectHeader().SetDifficulty(big.NewInt(100))
	parent.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(primeHeight))
	parent.WorkObjectHeader().SetKawpowDifficulty(big.NewInt(100))
	parent.WorkObjectHeader().SetShaShareTarget(new(big.Int).Set(params.MaxShaShares))
	parent.WorkObjectHeader().SetScryptShareTarget(new(big.Int).Set(params.MaxShaShares))
	parent.WorkObjectHeader().SetData([]byte{0})
	parent.WorkObjectHeader().SetPrimaryCoinbase(coinbase)
	parent.WorkObjectHeader().SetHeaderHash(parent.Header().Hash())
	rawdb.WriteWorkObject(hc.headerDb, parent.Hash(), parent, types.BlockObject, common.ZONE_CTX)
	rawdb.WriteHeaderNumber(hc.headerDb, parent.Hash(), 1)
	rawdb.WriteTermini(hc.headerDb, parent.Hash(), types.EmptyTermini())
	hc.headerCache.Add(parent.Hash(), *parent)
	hc.calcOrderCache.Add(parent.Hash(), calcOrderResponse{intrinsicEntropy: big.NewInt(0), order: common.ZONE_CTX})

	template := types.DefaultKawpowAuxTemplate()
	base := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	base.SetLocation(location)
	base.SetParentHash(parent.Hash())
	base.SetNumber(big.NewInt(2))
	base.SetPrimeTerminusNumber(new(big.Int).SetUint64(primeHeight))
	base.SetDifficulty(big.NewInt(100))
	base.SetKawpowDifficulty(big.NewInt(100))
	base.SetShaShareTarget(new(big.Int).Set(params.MaxShaShares))
	base.SetScryptShareTarget(new(big.Int).Set(params.MaxShaShares))
	base.SetShaDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), big.NewInt(1), big.NewInt(0)))
	base.SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), big.NewInt(1), big.NewInt(0)))
	base.SetTime(uint64(template.SignatureTime()))
	base.SetData([]byte{0})
	base.SetPrimaryCoinbase(coinbase)
	canonical := types.NewWorkObject(base, types.EmptyWorkObjectBody(), nil)
	if want := hc.CalculateShareTarget(parent, canonical); base.ShaShareTarget().Cmp(want) != 0 || base.ScryptShareTarget().Cmp(want) != 0 {
		t.Fatalf("canonical share targets do not match parent-derived target %v", want)
	}
	if want := hc.CalculateKawpowDifficulty(parent, canonical); base.KawpowDifficulty().Cmp(want) != 0 {
		t.Fatalf("canonical Kawpow difficulty does not match parent-derived value %v", want)
	}

	cases := []struct {
		name   string
		change func(*types.WorkObjectHeader)
		want   string
	}{
		{"sha target", func(h *types.WorkObjectHeader) { h.SetShaShareTarget(big.NewInt(0)) }, "invalid sha share target"},
		{"scrypt target", func(h *types.WorkObjectHeader) { h.SetScryptShareTarget(big.NewInt(0)) }, "invalid scrypt share target"},
		{"kawpow difficulty", func(h *types.WorkObjectHeader) { h.SetKawpowDifficulty(new(big.Int).Lsh(big.NewInt(1), 256)) }, "invalid kawpow difficulty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uncle := *base
			tc.change(&uncle)
			coinbaseTx := types.NewAuxPowCoinbaseTx(types.Kawpow, template.Height(), template.CoinbaseOut(), uncle.SealHash(), template.SignatureTime())
			merkle := types.CalculateMerkleRoot(types.Kawpow, coinbaseTx, template.MerkleBranch())
			donor := types.NewRavencoinBlockHeader(int32(template.Version()), template.PrevHash(), [32]byte(merkle), template.SignatureTime(), template.Bits(), template.Height())
			uncle.SetAuxPow(types.NewAuxPow(types.Kawpow, types.NewAuxPowHeader(donor), template.AuxPow2(), template.Sigs(), template.MerkleBranch(), coinbaseTx))
			if err := verifyAuxPowBinding(&uncle); err != nil {
				t.Fatalf("invalid test AuxPoW binding: %v", err)
			}

			block := types.EmptyWorkObject(common.ZONE_CTX)
			block.WorkObjectHeader().SetLocation(location)
			block.SetNumber(big.NewInt(2), common.ZONE_CTX)
			block.SetParentHash(parent.Hash(), common.ZONE_CTX)
			block.WorkObjectHeader().SetParentHash(parent.Hash())
			block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(primeHeight))
			block.WorkObjectHeader().SetData([]byte{0})
			block.Body().SetUncles([]*types.WorkObjectHeader{&uncle})
			block.Header().SetUncleHash(types.CalcUncleHash(block.Uncles()))
			block.WorkObjectHeader().SetHeaderHash(block.Header().Hash())
			if err := hc.VerifyUncles(block); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyUncles error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyUnclesSkipsKawpowTargetsForPreForkUncle(t *testing.T) {
	location := common.Location{0, 0}
	hc := newSecurityTestHeaderChain(t, common.Hash{})
	hc.powConfig = params.PowConfig{PowMode: params.ModeNormal, NodeLocation: location}
	parent := types.EmptyWorkObject(common.ZONE_CTX)
	parent.WorkObjectHeader().SetLocation(location)
	parent.SetNumber(big.NewInt(1), common.ZONE_CTX)
	parent.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.KawPowForkBlock - 1))
	parent.WorkObjectHeader().SetDifficulty(big.NewInt(100))
	parent.WorkObjectHeader().SetData([]byte{0})
	parent.WorkObjectHeader().SetHeaderHash(parent.Header().Hash())
	hc.headerCache.Add(parent.Hash(), *parent)
	hc.calcOrderCache.Add(parent.Hash(), calcOrderResponse{intrinsicEntropy: big.NewInt(0), order: common.ZONE_CTX})

	uncle := types.EmptyWorkObject(common.ZONE_CTX).WorkObjectHeader()
	uncle.SetLocation(location)
	uncle.SetParentHash(parent.Hash())
	uncle.SetNumber(big.NewInt(2))
	uncle.SetPrimeTerminusNumber(new(big.Int).SetUint64(params.KawPowForkBlock - 1))
	uncle.SetDifficulty(big.NewInt(100))
	uncle.SetData([]byte{0})
	uncle.SetPrimaryCoinbase(common.HexToAddress("0x00000000000000000000000000000000000000aa", location))
	block := types.EmptyWorkObject(common.ZONE_CTX)
	block.WorkObjectHeader().SetLocation(location)
	block.SetNumber(big.NewInt(2), common.ZONE_CTX)
	block.SetParentHash(parent.Hash(), common.ZONE_CTX)
	block.WorkObjectHeader().SetParentHash(parent.Hash())
	block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.KawPowForkBlock))
	block.WorkObjectHeader().SetData([]byte{0})
	block.Body().SetUncles([]*types.WorkObjectHeader{uncle})
	block.Header().SetUncleHash(types.CalcUncleHash(block.Uncles()))
	block.WorkObjectHeader().SetHeaderHash(block.Header().Hash())

	// The synthetic full-seal sibling is rejected later. It must reach that
	// check without trying to derive post-fork targets from a pre-fork parent.
	if err := hc.VerifyUncles(block); !errors.Is(err, consensus.ErrDanglingUncle) {
		t.Fatalf("pre-fork uncle in post-fork block: got %v, want dangling sibling", err)
	}
}
