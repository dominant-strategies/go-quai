package main

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/nipopow"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/trie"
)

func TestParseRangesAcceptsNamedNumberPairs(t *testing.T) {
	ranges, err := parseRanges("recent=10:20,older=30:40")
	if err != nil {
		t.Fatalf("parseRanges returned error: %v", err)
	}
	want := []nipopow.ValidationRange{
		{Name: "recent", AnchorNumber: 10, TipNumber: 20},
		{Name: "older", AnchorNumber: 30, TipNumber: 40},
	}
	if len(ranges) != len(want) {
		t.Fatalf("range count mismatch: want %d got %d", len(want), len(ranges))
	}
	for i := range want {
		if ranges[i] != want[i] {
			t.Fatalf("range %d mismatch: want %+v got %+v", i, want[i], ranges[i])
		}
	}
}

func TestSelectTailRangesSkipsUnsafeLengths(t *testing.T) {
	ranges := selectTailRanges(100, 16, 64, []uint64{0, 8, 16, 32, 65, 200})
	want := []nipopow.ValidationRange{
		{Name: "tail-16", AnchorNumber: 85, TipNumber: 100},
		{Name: "tail-32", AnchorNumber: 69, TipNumber: 100},
	}
	if len(ranges) != len(want) {
		t.Fatalf("range count mismatch: want %d got %d (%+v)", len(want), len(ranges), ranges)
	}
	for i := range want {
		if ranges[i] != want[i] {
			t.Fatalf("range %d mismatch: want %+v got %+v", i, want[i], ranges[i])
		}
	}
}

func TestRunValidationAgainstRawDBReportsSuccessfulRange(t *testing.T) {
	db := rawdb.NewMemoryDatabase(log.Global)
	blocks := writeLinearPrimeChain(t, db, 8)

	report, err := runValidation(context.Background(), db, validationConfig{
		M:       4,
		Ranges:  []nipopow.ValidationRange{{Name: "fixture", AnchorNumber: 0, TipNumber: 7}},
		Limits:  nipopow.BuildLimits{MaxChainLength: 16, MaxProofHeaders: 16, MaxM: 4},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("runValidation returned error: %v", err)
	}
	if !report.OK {
		t.Fatalf("expected OK report: %+v", report)
	}
	if report.HeadHash != blocks[7].Hash() || report.HeadNumber != 7 {
		t.Fatalf("unexpected head: number=%d hash=%s", report.HeadNumber, report.HeadHash)
	}
	if report.Report == nil || len(report.Report.Results) != 1 {
		t.Fatalf("missing validation result: %+v", report.Report)
	}
	result := report.Report.Results[0]
	if !result.OK || result.Anchor != blocks[0].Hash() || result.Tip != blocks[7].Hash() || result.ProofHeaders == 0 {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func writeLinearPrimeChain(t *testing.T, db ethdb.Database, length int) []*types.WorkObject {
	t.Helper()
	blocks := make([]*types.WorkObject, 0, length)
	committedInterlinks := common.Hashes{common.HexToHash("0x01")}
	for i := 0; i < length; i++ {
		var parent *types.WorkObject
		if i > 0 {
			parent = blocks[i-1]
		}
		block := testPrimeBlock(t, uint64(i), parent, committedInterlinks)
		if i == 0 {
			rawdb.WriteGenesisHashes(db, common.Hashes{block.Hash()})
		}
		rawdb.WriteInterlinkHashes(db, block.Hash(), committedInterlinks)
		rawdb.WriteTermini(db, block.Hash(), types.EmptyTermini())
		rawdb.WriteCanonicalHash(db, block.Hash(), uint64(i))
		rawdb.WriteWorkObject(db, block.Hash(), block, types.BlockObject, common.PRIME_CTX)
		blocks = append(blocks, block)
	}
	rawdb.WriteHeadBlockHash(db, blocks[len(blocks)-1].Hash())
	rawdb.WriteHeadHeaderHash(db, blocks[len(blocks)-1].Hash())
	return blocks
}

func testPrimeBlock(t *testing.T, number uint64, parent *types.WorkObject, committedInterlinks common.Hashes) *types.WorkObject {
	t.Helper()
	wo := types.EmptyWorkObject(common.PRIME_CTX)
	wo.WorkObjectHeader().SetNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetDifficulty(big.NewInt(1))
	wo.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(number))
	wo.WorkObjectHeader().SetPrimaryCoinbase(common.BytesToAddress(make([]byte, 20), common.Location{}))
	wo.WorkObjectHeader().SetTime(number + 1)

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
