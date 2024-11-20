package types

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/stretchr/testify/require"
)

func woTestData() (*WorkObject, common.Hash) {
	wo := &WorkObject{}
	wo.SetWorkObjectHeader(&WorkObjectHeader{})
	wo.woHeader.SetHeaderHash(EmptyHeader().Hash())
	wo.woHeader.SetParentHash(EmptyHeader().Hash())
	wo.woHeader.SetNumber(big.NewInt(1))
	wo.woHeader.SetDifficulty(big.NewInt(123456789))
	wo.woHeader.SetPrimeTerminusNumber(big.NewInt(42))
	wo.woHeader.SetTxHash(common.HexToHash("0x456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef3"))
	wo.woHeader.SetLocation(common.Location{0, 0})
	wo.woHeader.SetMixHash(common.HexToHash("0x56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef4"))
	wo.woHeader.SetPrimaryCoinbase(common.HexToAddress("0x123456789abcdef0123456789abcdef0123456789", common.Location{0, 0}))
	wo.woHeader.SetTime(uint64(1))
	wo.woHeader.SetNonce(EncodeNonce(uint64(1)))
	wo.woHeader.SetLock(0)

	wo.woBody = EmptyWorkObjectBody()
	return wo, wo.Hash()
}

var (
	expectedWoHash    = common.HexToHash("0xd8e3ef0d1804c06495b219308535844169ccfdbd8565770077ea12f928fc8000")
	expectedUncleHash = common.HexToHash("0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347")
)

func TestWoHash(t *testing.T) {
	_, actualHash := woTestData()
	require.Equal(t, expectedWoHash, actualHash, "Hash not equal to expected hash")
}

func TestWoSealHash(t *testing.T) {
	testWo, _ := woTestData()
	actualHash := testWo.SealHash()
	expectedHash := common.HexToHash("0x83fd7f1dbd2f62d320c2dd974e2d1b3ce8108594a7c9e3aa99dfd993ca106090")
	require.Equal(t, expectedHash, actualHash, "Seal hash not equal to expected hash")
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
		func(woh *WorkObjectHeader) common.Hash { return woh.TxHash() },
		func(woh *WorkObjectHeader, hash common.Hash) { woh.SetTxHash(hash) })
}

func FuzzMixHash(f *testing.F) {
	fuzzHash(f,
		func(woh *WorkObjectHeader) common.Hash { return woh.MixHash() },
		func(woh *WorkObjectHeader, hash common.Hash) { woh.SetMixHash(hash) })
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

func TestCalcUncleHash(t *testing.T) {
	tests := []struct {
		uncleNum          int
		expectedUncleHash common.Hash
		expectedWoHash    common.Hash
		shouldPass        bool
	}{
		{
			uncleNum:          0,
			expectedUncleHash: expectedUncleHash,
			expectedWoHash:    expectedWoHash,
			shouldPass:        true,
		},
		{
			uncleNum:          1,
			expectedUncleHash: expectedUncleHash,
			expectedWoHash:    expectedWoHash,
			shouldPass:        false,
		},
		{
			uncleNum:          5,
			expectedUncleHash: common.HexToHash("0x3c9dd26495f9a6ddf36e1443bee2ff0a3bb59b0722a765b145d01ab1f78ccd44"),
			expectedWoHash:    common.HexToHash("0x67c4f50242b43f752a32574a28633ac08d316fdbd786ccdc675e90887643adc2"),
			shouldPass:        true,
		},
	}

	// Run test cases
	for _, tt := range tests {
		t.Run(fmt.Sprintf("uncleNum=%d", tt.uncleNum), func(t *testing.T) {
			assertUncleHash(t, tt.uncleNum, tt.expectedUncleHash, tt.expectedWoHash, tt.shouldPass)
		})
	}
}

func assertUncleHash(t *testing.T, uncleNum int, expectedUncleHash common.Hash, expectedWoHash common.Hash, shouldPass bool) {
	wo, _ := woTestData()
	wo.Body().uncles = make([]*WorkObjectHeader, uncleNum)
	for i := 0; i < uncleNum; i++ {
		uncle, _ := woTestData()
		wo.Body().uncles[i] = CopyWorkObjectHeader(uncle.WorkObjectHeader())
	}

	wo.Body().Header().SetUncleHash(CalcUncleHash(wo.Body().uncles))
	wo.woHeader.SetHeaderHash(wo.Body().header.Hash())

	if shouldPass {
		require.Equal(t, expectedUncleHash, wo.Header().UncleHash(), "Uncle hashes do not create the expected root hash")
		require.Equal(t, expectedWoHash, wo.Hash(), "Uncle hashes do not create the expected WorkObject hash")
	} else {
		require.NotEqual(t, expectedUncleHash, wo.Header().UncleHash(), "Uncle hashes do not create the expected root hash")
		require.NotEqual(t, expectedWoHash, wo.Hash(), "Uncle hashes do not create the expected WorkObject hash")
	}
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
