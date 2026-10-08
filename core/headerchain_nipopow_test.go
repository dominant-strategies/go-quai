package core

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/nipopow"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/trie"
)

var _ nipopow.PrimeValidationSource = (*HeaderChain)(nil)

func testPrimeNiPoPoWHeaderChain(t *testing.T) *HeaderChain {
	t.Helper()

	db := rawdb.NewMemoryDatabase(log.Global)
	chainConfig := *params.TestChainConfig
	chainConfig.Location = common.Location{}

	headerCache, err := lru.New[common.Hash, types.WorkObject](headerCacheLimit)
	if err != nil {
		t.Fatal(err)
	}
	numberCache, err := lru.New[common.Hash, uint64](numberCacheLimit)
	if err != nil {
		t.Fatal(err)
	}

	hc := NewTestHeaderChain()
	hc.config = &chainConfig
	hc.headerDb = db
	hc.bc = NewTestBodyDb(db)
	hc.bc.chainConfig = &chainConfig
	hc.headerCache = headerCache
	hc.numberCache = numberCache
	hc.logger = log.Global
	return hc
}

func testPrimeNiPoPoWBlock(t *testing.T, number uint64, parent *types.WorkObject, committedInterlinks common.Hashes) *types.WorkObject {
	t.Helper()

	wo := types.EmptyWorkObject(common.PRIME_CTX)
	wo.WorkObjectHeader().SetNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetDifficulty(big.NewInt(1))
	wo.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetPrimaryCoinbase(common.BytesToAddress(make([]byte, 20), common.Location{}))
	wo.WorkObjectHeader().SetTime(number)

	header := wo.Header()
	header.SetNumber(new(big.Int).SetUint64(number), common.PRIME_CTX)
	header.SetParentDeltaEntropy(big.NewInt(1), common.PRIME_CTX)
	header.SetExpansionNumber(0)
	if parent != nil {
		header.SetParentHash(parent.Hash(), common.PRIME_CTX)
		wo.WorkObjectHeader().SetParentHash(parent.Hash())
	}
	header.SetInterlinkRootHash(types.DeriveSha(committedInterlinks, trie.NewStackTrie(nil)))
	wo.Body().SetInterlinkHashes(nil)
	wo.WorkObjectHeader().SetHeaderHash(header.Hash())
	return wo
}

func writePrimeNiPoPoWBlock(t *testing.T, hc *HeaderChain, wo *types.WorkObject) {
	t.Helper()
	rawdb.WriteTermini(hc.headerDb, wo.Hash(), types.EmptyTermini())
	rawdb.WriteCanonicalHash(hc.headerDb, wo.Hash(), wo.NumberU64(common.PRIME_CTX))
	rawdb.WriteWorkObject(hc.headerDb, wo.Hash(), wo, types.BlockObject, common.PRIME_CTX)
}

func TestGetPrimeNiPoPoWProofHeaderHydratesGenesisInterlinksFromRawDB(t *testing.T) {
	hc := testPrimeNiPoPoWHeaderChain(t)
	genesisInterlinks := common.Hashes{common.HexToHash("0x01"), common.HexToHash("0x02")}
	genesis := testPrimeNiPoPoWBlock(t, 0, nil, genesisInterlinks)

	rawdb.WriteGenesisHashes(hc.headerDb, common.Hashes{genesis.Hash()})
	rawdb.WriteInterlinkHashes(hc.headerDb, genesis.Hash(), genesisInterlinks)
	writePrimeNiPoPoWBlock(t, hc, genesis)

	proofHeader, err := hc.GetPrimeNiPoPoWProofHeader(genesis.Hash())
	if err != nil {
		t.Fatalf("expected genesis proof header to hydrate interlinks from rawdb: %v", err)
	}
	if !reflect.DeepEqual(proofHeader.InterlinkHashes(), genesisInterlinks) {
		t.Fatalf("unexpected genesis interlinks: want %v got %v", genesisInterlinks, proofHeader.InterlinkHashes())
	}
}

func TestGetPrimeNiPoPoWProofHeaderHydratesEmptyGenesisInterlinksWhenRawDBAbsent(t *testing.T) {
	hc := testPrimeNiPoPoWHeaderChain(t)
	genesis := testPrimeNiPoPoWBlock(t, 0, nil, nil)

	rawdb.WriteGenesisHashes(hc.headerDb, common.Hashes{genesis.Hash()})
	writePrimeNiPoPoWBlock(t, hc, genesis)

	proofHeader, err := hc.GetPrimeNiPoPoWProofHeader(genesis.Hash())
	if err != nil {
		t.Fatalf("expected empty genesis proof header to hydrate without rawdb interlinks: %v", err)
	}
	if len(proofHeader.InterlinkHashes()) != 0 {
		t.Fatalf("expected empty genesis interlinks, got %v", proofHeader.InterlinkHashes())
	}
}

func TestBuildPrimeNiPoPoWProofFromGenesisUsesRawDBInterlinks(t *testing.T) {
	hc := testPrimeNiPoPoWHeaderChain(t)
	genesisInterlinks := common.Hashes{common.HexToHash("0x01"), common.HexToHash("0x02")}
	genesis := testPrimeNiPoPoWBlock(t, 0, nil, genesisInterlinks)
	child := testPrimeNiPoPoWBlock(t, 1, genesis, genesisInterlinks)

	rawdb.WriteGenesisHashes(hc.headerDb, common.Hashes{genesis.Hash()})
	rawdb.WriteInterlinkHashes(hc.headerDb, genesis.Hash(), genesisInterlinks)
	writePrimeNiPoPoWBlock(t, hc, genesis)
	writePrimeNiPoPoWBlock(t, hc, child)

	proof, err := hc.BuildPrimeNiPoPoWProof(context.Background(), genesis.Hash(), child.Hash(), 1)
	if err != nil {
		t.Fatalf("expected proof from genesis to child to build from rawdb: %v", err)
	}
	if proof.Anchor != genesis.Hash() || proof.Tip() != child.Hash() {
		t.Fatalf("unexpected proof endpoints: anchor=%s tip=%s", proof.Anchor, proof.Tip())
	}
	if err := nipopow.VerifyPrimeProof(proof); err != nil {
		t.Fatalf("expected built proof to verify structurally: %v", err)
	}
}

func TestGetPrimeNiPoPoWProofHeaderRejectsMissingRawDBInterlinks(t *testing.T) {
	hc := testPrimeNiPoPoWHeaderChain(t)
	parentInterlinks := common.Hashes{common.HexToHash("0x01")}
	parent := testPrimeNiPoPoWBlock(t, 0, nil, parentInterlinks)
	child := testPrimeNiPoPoWBlock(t, 1, parent, parentInterlinks)

	rawdb.WriteGenesisHashes(hc.headerDb, common.Hashes{parent.Hash()})
	writePrimeNiPoPoWBlock(t, hc, parent)
	writePrimeNiPoPoWBlock(t, hc, child)

	_, err := hc.GetPrimeNiPoPoWProofHeader(child.Hash())
	if !errors.Is(err, ErrNiPoPoWInterlinkAbsent) {
		t.Fatalf("expected missing interlinks error, got %v", err)
	}
}
