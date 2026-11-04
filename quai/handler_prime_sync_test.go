package quai

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

type recordingPrimeBlockStore struct {
	stored     *types.WorkObject
	canonical  common.Hash
	replayErr  error
	writes     int
	replays    int
	readHash   common.Hash
	readNumber uint64
}

func (s *recordingPrimeBlockStore) GetBlockByHash(hash common.Hash) *types.WorkObject {
	s.readHash = hash
	return s.stored
}

func (s *recordingPrimeBlockStore) GetCanonicalHash(number uint64) common.Hash {
	s.readNumber = number
	return s.canonical
}

func (s *recordingPrimeBlockStore) ReemitChainEventForHC(_ *types.WorkObject) error {
	s.replays++
	return s.replayErr
}

func (s *recordingPrimeBlockStore) WriteBlock(_ *types.WorkObject) {
	s.writes++
}

func TestApplyPrimeBlockRequiresStoredBodyAtCanonicalBoundary(t *testing.T) {
	block := types.EmptyWorkObject(common.PRIME_CTX)
	block.Header().SetNumber(big.NewInt(42), common.PRIME_CTX)
	blockHash := block.WorkObjectHeader().Hash()
	store := &recordingPrimeBlockStore{canonical: blockHash}

	alreadyCanonical, replayed, err := applyPrimeBlock(store, block)
	if err != nil || alreadyCanonical || replayed || store.writes != 1 || store.replays != 0 || store.readHash != blockHash {
		t.Fatalf("canonical header without body: already=%v replayed=%v err=%v writes=%d replays=%d readHash=%s", alreadyCanonical, replayed, err, store.writes, store.replays, store.readHash)
	}
}

func TestApplyPrimeBlockStopsAtStoredCanonicalBlock(t *testing.T) {
	block := types.EmptyWorkObject(common.PRIME_CTX)
	block.Header().SetNumber(big.NewInt(42), common.PRIME_CTX)
	store := &recordingPrimeBlockStore{stored: block, canonical: block.WorkObjectHeader().Hash()}

	alreadyCanonical, replayed, err := applyPrimeBlock(store, block)
	if err != nil || !alreadyCanonical || replayed || store.writes != 0 || store.replays != 0 || store.readNumber != 42 {
		t.Fatalf("stored canonical block: already=%v replayed=%v err=%v writes=%d replays=%d number=%d", alreadyCanonical, replayed, err, store.writes, store.replays, store.readNumber)
	}
}

func TestApplyPrimeBlockReplaysStoredNoncanonicalBlock(t *testing.T) {
	block := types.EmptyWorkObject(common.PRIME_CTX)
	store := &recordingPrimeBlockStore{stored: block, replayErr: errors.New("replay failed")}

	alreadyCanonical, replayed, err := applyPrimeBlock(store, block)
	if alreadyCanonical || !replayed || !errors.Is(err, store.replayErr) || store.writes != 0 || store.replays != 1 {
		t.Fatalf("stored noncanonical block: already=%v replayed=%v err=%v writes=%d replays=%d", alreadyCanonical, replayed, err, store.writes, store.replays)
	}
}
