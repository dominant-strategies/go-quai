package quaiapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/consensus/misc"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/ethdb"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
)

type testNetBackend struct {
	total    uint
	incoming uint
	outgoing uint
}

func (b testNetBackend) PeerCount() uint {
	return b.total
}

func (b testNetBackend) PeerCountByDirection() (uint, uint) {
	return b.incoming, b.outgoing
}

func TestPublicNetAPIPeerCounts(t *testing.T) {
	api := NewPublicNetAPI(1, testNetBackend{
		total:    5,
		incoming: 2,
		outgoing: 3,
	})

	if got := api.PeerCount(); got != hexutil.Uint(5) {
		t.Fatalf("expected peer count 5, got %d", got)
	}

	byDirection := api.PeerCountByDirection()
	if byDirection.Incoming != hexutil.Uint(2) {
		t.Fatalf("expected incoming peer count 2, got %d", byDirection.Incoming)
	}
	if byDirection.Outgoing != hexutil.Uint(3) {
		t.Fatalf("expected outgoing peer count 3, got %d", byDirection.Outgoing)
	}
}

func TestMarshalPendingWorkSharesByPow(t *testing.T) {
	workShares := []*types.WorkObjectHeader{
		newTestPendingWorkShare(1, nil),
		newTestPendingWorkShare(2, newTestAuxPow(types.Kawpow, 2)),
		newTestPendingWorkShare(3, newTestAuxPow(types.SHA_BTC, 3)),
		newTestPendingWorkShare(4, newTestAuxPow(types.SHA_BCH, 4)),
	}

	marshaled := marshalPendingWorkSharesByPow(workShares, "v1")

	if len(marshaled[types.Progpow.String()]) != 1 {
		t.Fatalf("expected 1 progpow workshare, got %d", len(marshaled[types.Progpow.String()]))
	}
	if len(marshaled[types.Kawpow.String()]) != 1 {
		t.Fatalf("expected 1 kawpow workshare, got %d", len(marshaled[types.Kawpow.String()]))
	}
	if len(marshaled[types.SHA_BTC.String()]) != 1 {
		t.Fatalf("expected 1 sha_btc workshare, got %d", len(marshaled[types.SHA_BTC.String()]))
	}
	if len(marshaled[types.SHA_BCH.String()]) != 1 {
		t.Fatalf("expected 1 sha_bch workshare, got %d", len(marshaled[types.SHA_BCH.String()]))
	}

	for pow, entries := range marshaled {
		if len(entries) == 0 {
			t.Fatalf("expected entries for %s", pow)
		}
		entry := entries[0]
		if _, ok := entry["hash"]; !ok {
			t.Fatalf("expected hash field for %s", pow)
		}
		if _, ok := entry["number"]; !ok {
			t.Fatalf("expected number field for %s", pow)
		}
		if _, ok := entry["auxpow"]; ok {
			t.Fatalf("did not expect auxpow field in v1 marshaling for %s", pow)
		}
	}
}

func newTestPendingWorkShare(number int64, auxpow *types.AuxPow) *types.WorkObjectHeader {
	return types.NewWorkObjectHeader(
		common.Hash{byte(number), 0x01},
		common.Hash{byte(number), 0x02},
		big.NewInt(number),
		big.NewInt(1000+number),
		big.NewInt(3000001),
		common.Hash{byte(number), 0x03},
		types.BlockNonce{byte(number)},
		0,
		uint64(1700000000+number),
		common.Location{0, 0},
		common.Address{},
		[]byte{byte(number)},
		auxpow,
		&types.PowShareDiffAndCount{},
		&types.PowShareDiffAndCount{},
		big.NewInt(10),
		big.NewInt(20),
		big.NewInt(30),
	)
}

func newTestAuxPow(powID types.PowID, seed byte) *types.AuxPow {
	return types.NewAuxPow(
		powID,
		types.NewBlockHeader(powID, 1, [32]byte{seed}, [32]byte{seed + 1}, 1700000000, 1, uint32(seed), 1),
		nil,
		nil,
		nil,
		nil,
	)
}

type outpointTestBackend struct {
	Backend
	db            ethdb.Database
	outpoints     []*types.OutpointAndDenomination
	current       *types.WorkObject
	primeTerminus *types.WorkObject
	chainConfig   *params.ChainConfig
	nodeLocation  common.Location
}

func (b *outpointTestBackend) Database() ethdb.Database {
	return b.db
}

func (b *outpointTestBackend) AddressOutpoints(context.Context, common.Address) ([]*types.OutpointAndDenomination, error) {
	return b.outpoints, nil
}

func (b *outpointTestBackend) GetOutpointsByAddressAndRange(context.Context, common.Address, uint32, uint32) ([]*types.OutpointAndDenomination, error) {
	return b.outpoints, nil
}

func (b *outpointTestBackend) CurrentBlock() *types.WorkObject {
	return b.current
}

func (b *outpointTestBackend) GetBlockByHash(hash common.Hash) *types.WorkObject {
	if b.primeTerminus != nil && b.primeTerminus.Hash() == hash {
		return b.primeTerminus
	}
	return nil
}

func (b *outpointTestBackend) ChainConfig() *params.ChainConfig {
	return b.chainConfig
}

func (b *outpointTestBackend) NodeLocation() common.Location {
	return b.nodeLocation
}

func TestUnwrapQiIsConsistentAcrossDeltaAndOutpointAPIs(t *testing.T) {
	db := rawdb.NewMemoryDatabase(log.Global)
	defer db.Close()

	blockHeight := uint64(100)
	block := types.EmptyWorkObject(common.ZONE_CTX)
	block.SetNumber(new(big.Int).SetUint64(blockHeight), common.ZONE_CTX)
	block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionLockChangeForkBlock))

	to := common.HexToAddress("0x0080000000000000000000000000000000000000", common.Location{0, 0})
	if !to.IsInQiLedgerScope() {
		t.Fatal("test destination must be in the Qi ledger scope")
	}
	value := new(big.Int).Add(types.Denominations[7], types.Denominations[0])
	unwrap := types.NewTx(&types.ExternalTx{
		OriginatingTxHash: common.Hash{1},
		ETXIndex:          1,
		Gas:               params.CallValueTransferGas,
		To:                &to,
		Value:             value,
		Sender:            common.ZeroAddress(common.Location{0, 0}),
		EtxType:           types.UnwrapQiType,
	})
	block.Body().SetTransactions([]*types.Transaction{unwrap})

	lock := new(big.Int).SetUint64(blockHeight + params.UnwrapQiLockPeriod)
	outpoint := &types.OutpointAndDenomination{
		TxHash:       unwrap.Hash(),
		Index:        0,
		Denomination: 7,
		Lock:         lock,
	}
	utxo := types.NewUtxoEntry(types.NewTxOut(outpoint.Denomination, to.Bytes(), lock))
	if err := rawdb.CreateUTXO(db, outpoint.TxHash, outpoint.Index, utxo); err != nil {
		t.Fatalf("create test UTXO: %v", err)
	}

	backend := &outpointTestBackend{db: db, outpoints: []*types.OutpointAndDenomination{outpoint}}
	api := NewPublicBlockChainQuaiAPI(backend)
	addressMap := map[common.AddressBytes]struct{}{to.Bytes20(): {}}
	deltas := map[string]map[string]map[string][]interface{}{
		to.String(): {
			"created": {},
			"deleted": {},
		},
	}
	if err := GetDeltas(api, block, addressMap, deltas); err != nil {
		t.Fatalf("get deltas: %v", err)
	}

	created := deltas[to.String()]["created"][unwrap.Hash().String()]
	if len(created) != 1 {
		t.Fatalf("expected one unwrapped output delta, got %d", len(created))
	}
	assertRPCOutpoint(t, created[0], 0, 7, lock)

	current, err := api.GetOutpointsByAddress(context.Background(), to)
	if err != nil {
		t.Fatalf("get current outpoints: %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("expected one current outpoint, got %d", len(current))
	}
	assertRPCOutpoint(t, current[0], 0, 7, lock)

	ranged, err := api.GetOutPointsByAddressAndRange(context.Background(), to, hexutil.Uint64(blockHeight), hexutil.Uint64(blockHeight))
	if err != nil {
		t.Fatalf("get ranged outpoints: %v", err)
	}
	rangedOutputs := ranged[unwrap.Hash().Hex()]
	if len(rangedOutputs) != 1 {
		t.Fatalf("expected one ranged outpoint, got %d", len(rangedOutputs))
	}
	assertRPCOutpoint(t, rangedOutputs[0], 0, 7, lock)
}

func assertRPCOutpoint(t *testing.T, output interface{}, index, denomination uint64, lock *big.Int) {
	t.Helper()
	fields, ok := output.(map[string]interface{})
	if !ok {
		t.Fatalf("expected RPC outpoint map, got %T", output)
	}
	if got := uint64(fields["index"].(hexutil.Uint64)); got != index {
		t.Fatalf("expected index %d, got %d", index, got)
	}
	if got := uint64(fields["denomination"].(hexutil.Uint64)); got != denomination {
		t.Fatalf("expected denomination %d, got %d", denomination, got)
	}
	gotLock := fields["lock"].(hexutil.Big)
	if (*big.Int)(&gotLock).Cmp(lock) != 0 {
		t.Fatalf("expected lock %s, got %s", lock, (*big.Int)(&gotLock))
	}
}

func TestQiFeeRPCsUseCanonicalConversionRate(t *testing.T) {
	db := rawdb.NewMemoryDatabase(log.Global)
	defer db.Close()

	current := types.EmptyWorkObject(common.ZONE_CTX)
	current.SetNumber(big.NewInt(100), common.ZONE_CTX)
	current.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(params.ConversionLockChangeForkBlock))
	current.WorkObjectHeader().SetDifficulty(big.NewInt(8_000_000_000_000_000))
	current.WorkObjectHeader().SetShaDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), common.Big0, common.Big0))
	current.WorkObjectHeader().SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), common.Big0, common.Big0))
	current.Header().SetBaseFee(big.NewInt(1_000))
	current.Header().SetAvgTxFees(big.NewInt(1_000_000))
	current.Header().SetTotalFees(big.NewInt(2_000_000))

	primeTerminus := types.EmptyWorkObject(common.PRIME_CTX)
	primeTerminus.Header().SetExchangeRate(big.NewInt(221_077_819_000_000_000))
	current.Header().SetPrimeTerminusHash(primeTerminus.Hash())

	backend := &outpointTestBackend{
		db:            db,
		current:       current,
		primeTerminus: primeTerminus,
		chainConfig:   &params.ChainConfig{},
		nodeLocation:  common.Location{0, 0},
	}
	api := NewPublicBlockChainQuaiAPI(backend)

	baseFee, err := api.BaseFee(context.Background(), false)
	if err != nil {
		t.Fatalf("get Qi base fee: %v", err)
	}
	expectedBaseFee := quaiToQiFeeEstimate(current, primeTerminus.ExchangeRate(), current.BaseFee())
	if (*big.Int)(baseFee).Cmp(expectedBaseFee) != 0 {
		t.Fatalf("expected Qi base fee %s, got %s", expectedBaseFee, (*big.Int)(baseFee))
	}
	if got := misc.QiToQuai(current, primeTerminus.ExchangeRate(), current.Difficulty(), (*big.Int)(baseFee)); got.Cmp(current.BaseFee()) < 0 {
		t.Fatalf("returned Qi base fee converts to %s Quai, below required %s", got, current.BaseFee())
	}

	zeroLock := hexutil.Big(*big.NewInt(0))
	args := TransactionArgs{
		TxType: types.QiTxType,
		TxIn: []types.RPCTxIn{{
			PreviousOutPoint: types.OutpointJSON{TxHash: common.Hash{2}, Index: 0},
			PubKey:           hexutil.Bytes{2},
		}},
		TxOut: []types.RPCTxOut{{
			Denomination: 0,
			Address:      common.NewMixedcaseAddress(common.HexToAddress("0x0080000000000000000000000000000000000000", common.Location{0, 0})),
			Lock:         &zeroLock,
		}},
	}
	estimated, err := api.EstimateFeeForQi(context.Background(), args)
	if err != nil {
		t.Fatalf("estimate Qi fee: %v", err)
	}
	gas, err := args.CalculateQiTxGas(0, backend.nodeLocation)
	if err != nil {
		t.Fatalf("calculate expected Qi gas: %v", err)
	}
	bufferedBaseFee := new(big.Int).Div(new(big.Int).Mul(current.BaseFee(), big.NewInt(120)), big.NewInt(100))
	requiredQuaiFee := new(big.Int).Mul(new(big.Int).SetUint64(uint64(gas)), bufferedBaseFee)
	expectedEstimate := quaiToQiFeeEstimate(current, primeTerminus.ExchangeRate(), requiredQuaiFee)
	if (*big.Int)(estimated).Cmp(expectedEstimate) != 0 {
		t.Fatalf("expected Qi fee estimate %s, got %s", expectedEstimate, (*big.Int)(estimated))
	}
	if got := misc.QiToQuai(current, primeTerminus.ExchangeRate(), current.Difficulty(), (*big.Int)(estimated)); got.Cmp(requiredQuaiFee) < 0 {
		t.Fatalf("estimated Qi fee converts to %s Quai, below required %s", got, requiredQuaiFee)
	}
}

func TestMiningQiWorkshareRewardUsesPostForkHashReward(t *testing.T) {
	block := newMiningRewardTestBlock(params.ConversionLockChangeForkBlock)
	exchangeRate := big.NewInt(221_077_819_000_000_000)
	divisor := big.NewInt(int64(params.ExpectedWorksharesPerBlock + 1))

	expectedQiShare := new(big.Int).Div(
		misc.CalculateQiReward(block.WorkObjectHeader(), block.Difficulty()),
		divisor,
	)
	got := calculateMiningQiWorkshareReward(block, exchangeRate)
	assertBigIntEqual(t, "post-fork Qi workshare reward", got, expectedQiShare)

	// The fee capacitor must not influence the post-fork Qi workshare reward.
	block.Header().SetAvgTxFees(big.NewInt(1_000_000))
	withHigherFees := calculateMiningQiWorkshareReward(block, exchangeRate)
	assertBigIntEqual(t, "fee-independent Qi workshare reward", withHigherFees, expectedQiShare)
}

func TestMiningQiWorkshareRewardIsNotAdvertisedBeforeFork(t *testing.T) {
	block := newMiningRewardTestBlock(params.ConversionLockChangeForkBlock - 1)
	exchangeRate := big.NewInt(221_077_819_000_000_000)
	if got := calculateMiningQiWorkshareReward(block, exchangeRate); got != nil {
		t.Fatalf("expected no separate pre-fork Qi workshare reward, got %s", got)
	}
}

func newMiningRewardTestBlock(primeHeight uint64) *types.WorkObject {
	block := types.EmptyWorkObject(common.ZONE_CTX)
	header := block.WorkObjectHeader()
	header.SetNumber(new(big.Int).SetUint64(primeHeight))
	header.SetPrimeTerminusNumber(new(big.Int).SetUint64(primeHeight))
	header.SetDifficulty(big.NewInt(8_000_000_000_000_000))
	header.SetShaDiffAndCount(types.NewPowShareDiffAndCount(
		new(big.Int).Mul(big.NewInt(4_500_000_000_000_000), params.InitialShaDiffMultiple),
		new(big.Int).Set(params.TargetShaShares),
		common.Big0,
	))
	header.SetScryptDiffAndCount(types.NewPowShareDiffAndCount(big.NewInt(1), common.Big0, common.Big0))
	block.Header().SetAvgTxFees(big.NewInt(101))
	block.Header().SetTotalFees(big.NewInt(200))
	return block
}

func assertBigIntEqual(t *testing.T, name string, got, want *big.Int) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("%s mismatch: got %s, want %s", name, got, want)
	}
}
