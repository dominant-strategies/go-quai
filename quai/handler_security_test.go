package quai

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/p2p/protocol"
)

// primeSyncTestChain is a primeSyncChain with fixed answers.
type primeSyncTestChain struct {
	current        *types.WorkObject
	sanityErr      error
	order          int
	calcOrderCalls int
}

func (c *primeSyncTestChain) CurrentHeader() *types.WorkObject { return c.current }

func (c *primeSyncTestChain) SanityCheckWorkObjectBlockViewBody(*types.WorkObject) error {
	return c.sanityErr
}

func (c *primeSyncTestChain) CalcOrder(*types.WorkObject) (*big.Int, int, error) {
	c.calcOrderCalls++
	return big.NewInt(1), c.order, nil
}

func newPrimeSyncTestBlock(number int64, parent common.Hash, difficulty *big.Int) *types.WorkObject {
	location := common.Location{0, 0}
	bodyHeader := types.EmptyHeader()
	bodyHeader.SetNumber(big.NewInt(number), common.PRIME_CTX)
	bodyHeader.SetParentHash(parent, common.PRIME_CTX)
	wh := &types.WorkObjectHeader{}
	wh.SetHeaderHash(bodyHeader.Hash())
	wh.SetParentHash(parent)
	wh.SetNumber(big.NewInt(number))
	wh.SetDifficulty(new(big.Int).Set(difficulty))
	wh.SetPrimeTerminusNumber(big.NewInt(number))
	wh.SetTxHash(types.EmptyRootHash)
	wh.SetLocation(location)
	wh.SetPrimaryCoinbase(common.HexToAddress("0x0012345678901234567890123456789012345678", location))
	wh.SetNonce(types.EncodeNonce(1))
	wh.SetData([]byte{0})
	body := types.EmptyWorkObjectBody()
	body.SetHeader(bodyHeader)
	return types.NewWorkObject(wh, body, nil)
}

// newPrimeSyncTestBatch returns a continuous chain of prime blocks that starts
// at firstNumber.
func newPrimeSyncTestBatch(firstNumber int64, difficulty *big.Int) []*types.WorkObjectBlockView {
	batch := make([]*types.WorkObjectBlockView, protocol.C_NumPrimeBlocksToDownload)
	parent := common.Hash{0xaa}
	for i := range batch {
		wo := newPrimeSyncTestBlock(firstNumber+int64(i), parent, difficulty)
		batch[i] = &types.WorkObjectBlockView{WorkObject: wo}
		parent = wo.Hash()
	}
	return batch
}

func newPrimeSyncTestChain(difficulty *big.Int) *primeSyncTestChain {
	return &primeSyncTestChain{current: newPrimeSyncTestBlock(99, common.Hash{}, difficulty), order: common.PRIME_CTX}
}

func TestValidatePrimeSyncBlocksAcceptsValidBatch(t *testing.T) {
	chain := newPrimeSyncTestChain(big.NewInt(1_000_000))
	if err := validatePrimeSyncBlocks(chain, big.NewInt(100), newPrimeSyncTestBatch(100, big.NewInt(1_000_000))); err != nil {
		t.Fatal(err)
	}
	if chain.calcOrderCalls != protocol.C_NumPrimeBlocksToDownload {
		t.Fatalf("CalcOrder called %d times, want %d", chain.calcOrderCalls, protocol.C_NumPrimeBlocksToDownload)
	}
}

// H2: a peer could answer a prime sync request with cheap blocks that have a
// trivial difficulty. They must be rejected before the PoW check and before
// anything is written.
func TestValidatePrimeSyncBlocksRejectsTrivialDifficulty(t *testing.T) {
	chain := newPrimeSyncTestChain(big.NewInt(1_000_000))
	err := validatePrimeSyncBlocks(chain, big.NewInt(100), newPrimeSyncTestBatch(100, big.NewInt(1)))
	if err == nil || !strings.Contains(err.Error(), "below the minimum") {
		t.Fatalf("got %v, want a minimum difficulty error", err)
	}
	if chain.calcOrderCalls != 0 {
		t.Fatalf("CalcOrder called %d times for a trivial difficulty batch", chain.calcOrderCalls)
	}

	// A difficulty drop inside the allowed range is still accepted
	allowed := new(big.Int).Div(big.NewInt(1_000_000), big.NewInt(c_primeSyncMaxDifficultyDrop))
	if err := validatePrimeSyncBlocks(newPrimeSyncTestChain(big.NewInt(1_000_000)), big.NewInt(100), newPrimeSyncTestBatch(100, allowed)); err != nil {
		t.Fatalf("allowed difficulty rejected: %v", err)
	}
}

func TestValidatePrimeSyncBlocksRejectsBadBatches(t *testing.T) {
	difficulty := big.NewInt(1_000_000)
	brokenChain := newPrimeSyncTestBatch(100, difficulty)
	brokenChain[5] = &types.WorkObjectBlockView{WorkObject: newPrimeSyncTestBlock(105, common.Hash{0xbb}, difficulty)}
	nilBlock := newPrimeSyncTestBatch(100, difficulty)
	nilBlock[3] = nil

	tests := []struct {
		name    string
		chain   *primeSyncTestChain
		first   int64
		batch   []*types.WorkObjectBlockView
		wantErr string
	}{
		{"short batch", newPrimeSyncTestChain(difficulty), 100, newPrimeSyncTestBatch(100, difficulty)[:3], "prime blocks"},
		{"wrong numbers", newPrimeSyncTestChain(difficulty), 100, newPrimeSyncTestBatch(500, difficulty), "has number"},
		{"broken chain", newPrimeSyncTestChain(difficulty), 100, brokenChain, "non continous"},
		{"nil block", newPrimeSyncTestChain(difficulty), 100, nilBlock, "incomplete"},
		{"failed sanity check", &primeSyncTestChain{current: newPrimeSyncTestChain(difficulty).current, sanityErr: errors.New("bad body"), order: common.PRIME_CTX}, 100, newPrimeSyncTestBatch(100, difficulty), "sanity check"},
		{"not a prime block", &primeSyncTestChain{current: newPrimeSyncTestChain(difficulty).current, order: common.ZONE_CTX}, 100, newPrimeSyncTestBatch(100, difficulty), "want prime"},
	}
	for _, tt := range tests {
		err := validatePrimeSyncBlocks(tt.chain, big.NewInt(tt.first), tt.batch)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: got %v, want an error containing %q", tt.name, err, tt.wantErr)
		}
	}
}

type primeSyncValidationBackend struct {
	NetworkingAPI
	responses []interface{}
	requested *big.Int
	rejected  int
}

func (b *primeSyncValidationBackend) RequestValidated(_ common.Location, requestData interface{}, responseDataType interface{}, validate func(interface{}) error) chan interface{} {
	b.requested = new(big.Int).Set(requestData.(*big.Int))
	if _, ok := responseDataType.([]*types.WorkObjectBlockView); !ok {
		panic("prime sync requested the wrong response type")
	}
	result := make(chan interface{}, 1)
	for _, response := range b.responses {
		if validate(response) != nil {
			b.rejected++
			continue
		}
		result <- response
		break
	}
	close(result)
	return result
}

func TestPrimeSyncValidatesResponsesBeforeSelectingPeer(t *testing.T) {
	firstNumber := big.NewInt(100)
	chain := newPrimeSyncTestChain(big.NewInt(1_000_000))
	good := newPrimeSyncTestBatch(100, big.NewInt(1_000_000))
	backend := &primeSyncValidationBackend{responses: []interface{}{
		newPrimeSyncTestBatch(500, big.NewInt(1_000_000)),
		good,
	}}
	result := requestPrimeSyncBlocks(backend, chain, common.Location{0, 0}, firstNumber)
	got, ok := <-result
	if !ok || got.([]*types.WorkObjectBlockView)[0] != good[0] {
		t.Fatalf("prime sync selected %v, want the valid batch", got)
	}
	if backend.rejected != 1 || backend.requested.Cmp(firstNumber) != 0 {
		t.Fatalf("rejected %d bad batches for request %v, want one rejection for %v", backend.rejected, backend.requested, firstNumber)
	}
	if chain.calcOrderCalls != protocol.C_NumPrimeBlocksToDownload {
		t.Fatalf("CalcOrder called %d times, want only the valid batch checked", chain.calcOrderCalls)
	}
}

// H3: many gossip workers update the per slice counters at the same time.
// Before the fix this was a concurrent map write, which the Go runtime turns
// into a fatal error that recover cannot catch. Run with -race to check.
func TestAddSliceTxCountIsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				addSliceTxCount(fmt.Sprintf("security-test-slice-%d", (worker+i)%50), 1)
			}
		}(worker)
	}
	wg.Wait()

	txCountersBySliceMu.Lock()
	defer txCountersBySliceMu.Unlock()
	for i := 0; i < 50; i++ {
		if _, ok := txCountersBySlice[fmt.Sprintf("security-test-slice-%d", i)]; !ok {
			t.Fatalf("missing counter for slice %d", i)
		}
	}
}
