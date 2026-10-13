package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	lru "github.com/hashicorp/golang-lru/v2"
)

// fixedPowEngine returns the same PoW hash for every header.
type fixedPowEngine struct {
	consensus.Engine
	powHash common.Hash
}

func (e fixedPowEngine) ComputePowHash(*types.WorkObjectHeader) (common.Hash, error) {
	return e.powHash, nil
}

func newSecurityTestHeaderChain(t *testing.T, powHash common.Hash) *HeaderChain {
	t.Helper()
	db := rawdb.NewMemoryDatabase(log.Global)
	config := &params.ChainConfig{Location: common.Location{0, 0}}
	bodyDb := NewTestBodyDb(db)
	bodyDb.chainConfig = config
	bodyDb.logger = log.Global
	headerCache, err := lru.New[common.Hash, types.WorkObject](10)
	if err != nil {
		t.Fatal(err)
	}
	calcOrderCache, err := lru.New[common.Hash, calcOrderResponse](10)
	if err != nil {
		t.Fatal(err)
	}
	engine := fixedPowEngine{powHash: powHash}
	return &HeaderChain{
		config:         config,
		bc:             bodyDb,
		headerDb:       db,
		headerCache:    headerCache,
		calcOrderCache: calcOrderCache,
		engine:         []consensus.Engine{engine, engine},
		logger:         log.Global,
	}
}

func setForkForTest(t *testing.T, fork *uint64, value uint64) {
	t.Helper()
	previous := *fork
	*fork = value
	t.Cleanup(func() { *fork = previous })
}

func newTestWorkObjectHeader(primeTerminusNumber uint64) *types.WorkObjectHeader {
	location := common.Location{0, 0}
	wh := &types.WorkObjectHeader{}
	wh.SetHeaderHash(types.EmptyRootHash)
	wh.SetParentHash(common.Hash{0x01})
	wh.SetNumber(big.NewInt(10))
	wh.SetDifficulty(big.NewInt(1000))
	wh.SetPrimeTerminusNumber(new(big.Int).SetUint64(primeTerminusNumber))
	wh.SetTxHash(types.EmptyRootHash)
	wh.SetLocation(location)
	wh.SetMixHash(common.Hash{})
	wh.SetPrimaryCoinbase(common.HexToAddress("0x0012345678901234567890123456789012345678", location))
	wh.SetTime(1)
	wh.SetNonce(types.EncodeNonce(1))
	wh.SetLock(0)
	wh.SetData([]byte{0})
	wh.SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(1), big.NewInt(0)))
	wh.SetShaDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(1), big.NewInt(0)))
	wh.SetShaShareTarget(big.NewInt(1000))
	wh.SetScryptShareTarget(big.NewInt(1000))
	wh.SetKawpowDifficulty(big.NewInt(1000))
	return wh
}

// newTestShaShare returns a SHA workshare whose coinbase commits to its seal
// hash, as a mined share does.
func newTestShaShare(primeTerminusNumber uint64) *types.WorkObjectHeader {
	wh := newTestWorkObjectHeader(primeTerminusNumber)
	btcHeader := types.NewBitcoinBlockHeader(1, [32]byte{1}, [32]byte{2}, 1000, 0x1d00ffff, 7)
	coinbaseTx := types.NewAuxPowCoinbaseTx(types.SHA_BTC, 100, []byte{0x51}, wh.SealHash(), 123)
	wh.SetAuxPow(types.NewAuxPow(types.SHA_BTC, types.NewAuxPowHeader(btcHeader), []byte{}, []byte{0x01, 0x02}, [][]byte{}, coinbaseTx))
	return wh
}

// newTestKawpowHeader returns a Kawpow block header with an empty AuxPow2.
func newTestKawpowHeader(primeTerminusNumber uint64) *types.WorkObjectHeader {
	wh := newTestWorkObjectHeader(primeTerminusNumber)
	ravencoinHeader := &types.RavencoinBlockHeader{
		Version:        4,
		HashPrevBlock:  types.EmptyRootHash,
		HashMerkleRoot: types.EmptyRootHash,
		Time:           34545,
		Bits:           0x1d00ffff,
		Nonce64:        367899,
		Height:         298899,
		MixHash:        types.EmptyRootHash,
	}
	coinbaseTx := types.NewAuxPowCoinbaseTx(types.Kawpow, 100, []byte{0x51}, wh.SealHash(), 123)
	wh.SetAuxPow(types.NewAuxPow(types.Kawpow, types.NewAuxPowHeader(ravencoinHeader), []byte{}, []byte{0x03, 0x04}, [][]byte{}, coinbaseTx))
	return wh
}

func newTestBlockWithUncles(wh *types.WorkObjectHeader, uncles []*types.WorkObjectHeader) *types.WorkObject {
	body := types.EmptyWorkObjectBody()
	body.SetHeader(types.EmptyHeader())
	body.SetUncles(uncles)
	return types.NewWorkObject(wh, body, nil)
}

// C1: one SHA share is re-encoded with other AuxPow signature bytes. Both
// copies carry the same work but have different hashes.
func TestVerifyUnclesRejectsSameWorkUnderDifferentHashes(t *testing.T) {
	primeTerminusNumber := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	share := newTestShaShare(primeTerminusNumber)
	copied := types.CopyWorkObjectHeader(share)
	copied.AuxPow().SetSignature([]byte{0xde, 0xad, 0xbe, 0xef})
	if share.Hash() == copied.Hash() {
		t.Fatal("test setup: the copies must have different hashes")
	}
	if share.AuxPow().Header().PowHash() != copied.AuxPow().Header().PowHash() {
		t.Fatal("test setup: the copies must carry the same work")
	}

	block := newTestBlockWithUncles(newTestWorkObjectHeader(primeTerminusNumber), []*types.WorkObjectHeader{share, copied})
	hc := newSecurityTestHeaderChain(t, common.HexToHash("0xff"))

	// Before the fork, the hash check alone does not catch the copy. The block
	// fails later for an unrelated reason (the test share has no real work).
	setForkForTest(t, &params.WorkShareIdentityForkBlock, primeTerminusNumber+1)
	if err := hc.VerifyUncles(block); errors.Is(err, consensus.ErrDuplicateUncle) {
		t.Fatalf("before the fork the copy must not be detected by hash: %v", err)
	}

	setForkForTest(t, &params.WorkShareIdentityForkBlock, primeTerminusNumber)
	if err := hc.VerifyUncles(block); !errors.Is(err, consensus.ErrDuplicateUncle) {
		t.Fatalf("got %v, want %v", err, consensus.ErrDuplicateUncle)
	}
}

// C1: the block's own header is re-encoded with a nil AuxPow2 and included as
// an uncle of itself. The hash differs, so the hash-based ancestor check does
// not catch it.
func TestVerifyUnclesRejectsReEncodedAncestor(t *testing.T) {
	primeTerminusNumber := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	blockHeader := newTestKawpowHeader(primeTerminusNumber)
	reEncoded := types.CopyWorkObjectHeader(blockHeader)
	reEncoded.AuxPow().SetAuxPow2(nil)
	if blockHeader.Hash() == reEncoded.Hash() {
		t.Fatal("test setup: nil and empty AuxPow2 must hash differently")
	}

	block := newTestBlockWithUncles(blockHeader, []*types.WorkObjectHeader{reEncoded})
	hc := newSecurityTestHeaderChain(t, common.HexToHash("0xff"))

	setForkForTest(t, &params.WorkShareIdentityForkBlock, primeTerminusNumber+1)
	if err := hc.VerifyUncles(block); errors.Is(err, consensus.ErrUncleIsAncestor) {
		t.Fatalf("before the fork the re-encoded ancestor must not be detected by hash: %v", err)
	}

	setForkForTest(t, &params.WorkShareIdentityForkBlock, primeTerminusNumber)
	if err := hc.VerifyUncles(block); !errors.Is(err, consensus.ErrUncleIsAncestor) {
		t.Fatalf("got %v, want %v", err, consensus.ErrUncleIsAncestor)
	}
}

// Different work must still be accepted by the identity check.
func TestVerifyUnclesKeepsDistinctWork(t *testing.T) {
	primeTerminusNumber := params.KawPowForkBlock + params.KawPowTransitionPeriod + 1
	share := newTestShaShare(primeTerminusNumber)
	other := types.CopyWorkObjectHeader(share)
	other.AuxPow().Header().SetNonce(8)

	block := newTestBlockWithUncles(newTestWorkObjectHeader(primeTerminusNumber), []*types.WorkObjectHeader{share, other})
	hc := newSecurityTestHeaderChain(t, common.HexToHash("0xff"))
	setForkForTest(t, &params.WorkShareIdentityForkBlock, primeTerminusNumber)
	err := hc.VerifyUncles(block)
	if errors.Is(err, consensus.ErrDuplicateUncle) || errors.Is(err, consensus.ErrUncleIsAncestor) {
		t.Fatalf("distinct work rejected as duplicate: %v", err)
	}
}
