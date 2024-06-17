package types

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func woTestData() (*WorkObject, common.Hash) {
	wo := &WorkObject{
		woHeader: &WorkObjectHeader{
			headerHash: common.HexToHash("0x123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"),
			parentHash: common.HexToHash("0x23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef1"),
			number:     big.NewInt(1),
			difficulty: big.NewInt(123456789),
			txHash:     common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3"),
			location:   common.Location{0, 0},
			mixHash:    common.HexToHash("0x56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef4"),
			time:       uint64(1),
			nonce:      EncodeNonce(uint64(1)),
		},
	}
	return wo, wo.Hash()
}

func TestWoHash(t *testing.T) {
	_, hash := woTestData()
	correctHash := common.HexToHash("0xce1b26f11a4694a6be74e23b92ce133de912799bc7e067fa05f3bb19fb356d2c")
	require.Equal(t, hash, correctHash, "Hash not equal to expected hash")
}

func FuzzHeaderHash(f *testing.F) {
	fuzzHash(f,
		func(woh *WorkObjectHeader) common.Hash { return woh.headerHash },
		func(woh *WorkObjectHeader, hash common.Hash) { woh.headerHash = hash })
}

func FuzzParentHash(f *testing.F) {
	fuzzHash(f,
		func(woh *WorkObjectHeader) common.Hash { return woh.parentHash },
		func(woh *WorkObjectHeader, hash common.Hash) { woh.parentHash = hash })
}

func FuzzDifficultyHash(f *testing.F) {
	fuzzBigInt(f,
		func(woh *WorkObjectHeader) *big.Int { return woh.difficulty },
		func(woh *WorkObjectHeader, val *big.Int) { woh.difficulty = val })
}

func FuzzNumberHash(f *testing.F) {
	fuzzBigInt(f,
		func(woh *WorkObjectHeader) *big.Int { return woh.number },
		func(woh *WorkObjectHeader, val *big.Int) { woh.number = val })
}

func FuzzTxHash(f *testing.F) {
	fuzzHash(f,
		func(woh *WorkObjectHeader) common.Hash { return woh.txHash },
		func(woh *WorkObjectHeader, hash common.Hash) { woh.txHash = hash })
}

func TestLocationHash(t *testing.T) {
	wo, hash := woTestData()

	locations := []common.Location{
		{0, 1},
		{0, 2},
		{0, 3},
		{1, 0},
		{1, 1},
		{1, 2},
		{1, 3},
		{2, 0},
		{2, 1},
		{2, 2},
		{2, 3},
		{3, 0},
		{3, 1},
		{3, 2},
		{3, 3},
	}

	for _, loc := range locations {
		woCopy := *wo
		woCopy.woHeader.location = loc
		require.NotEqual(t, woCopy.Hash(), hash, "Hash equal for location \noriginal: %v, modified: %v", wo.woHeader.location, loc)
	}
}

func FuzzTimeHash(f *testing.F) {
	fuzzUint64Field(f,
		func(woh *WorkObjectHeader) uint64 { return woh.time },
		func(woh *WorkObjectHeader, time uint64) { woh.time = time })
}

func FuzzNonceHash(f *testing.F) {
	fuzzUint64Field(f,
		func(woh *WorkObjectHeader) uint64 { return woh.nonce.Uint64() },
		func(woh *WorkObjectHeader, nonce uint64) { woh.nonce = EncodeNonce(nonce) })
}

func fuzzHash(f *testing.F, getField func(*WorkObjectHeader) common.Hash, setField func(*WorkObjectHeader, common.Hash)) {
	wo, _ := woTestData()
	f.Add(testByte)
	f.Add(getField(wo.woHeader).Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		localWo, hash := woTestData()
		sc := common.BytesToHash(b)
		if getField(localWo.woHeader) != sc {
			setField(localWo.woHeader, sc)
			require.NotEqual(t, localWo.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", getField(wo.woHeader).Bytes(), b)
		}
	})
}

func fuzzUint64Field(f *testing.F, getVal func(*WorkObjectHeader) uint64, setVal func(*WorkObjectHeader, uint64)) {
	wo, _ := woTestData()
	f.Add(testUInt64)
	f.Add(getVal(wo.woHeader))
	f.Fuzz(func(t *testing.T, i uint64) {
		localWo, hash := woTestData()
		if getVal(localWo.woHeader) != i {
			setVal(localWo.woHeader, i)
			require.NotEqual(t, localWo.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", getVal(wo.woHeader), i)
		}
	})
}

func fuzzBigInt(f *testing.F, getVal func(*WorkObjectHeader) *big.Int, setVal func(*WorkObjectHeader, *big.Int)) {
	wo, _ := woTestData()
	f.Add(testInt64)
	f.Add(getVal(wo.woHeader).Int64())
	f.Fuzz(func(t *testing.T, i int64) {
		localWo, hash := woTestData()
		bi := big.NewInt(i)
		if getVal(localWo.woHeader).Cmp(bi) != 0 {
			setVal(localWo.woHeader, bi)
			require.NotEqual(t, localWo.Hash(), hash, "Hash collision\noriginal: %v, modified: %v", getVal(wo.woHeader), bi)
		}
	})
}
