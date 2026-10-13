package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/params"
)

func TestRegularQiETXDenominationRejectsTruncationAndOutOfRange(t *testing.T) {
	for _, value := range []*big.Int{nil, big.NewInt(-1), big.NewInt(15), big.NewInt(270), new(big.Int).Lsh(big.NewInt(1), 64)} {
		if _, err := regularQiETXDenomination(value); err == nil {
			t.Fatalf("accepted invalid denomination %v", value)
		}
	}
	for _, value := range []int64{0, 14} {
		got, err := regularQiETXDenomination(big.NewInt(value))
		if err != nil || got != uint8(value) {
			t.Fatalf("valid denomination %d: got %d, %v", value, got, err)
		}
	}
}

func TestWorkerConsumesInvalidRegularQiETXAfterSharedFork(t *testing.T) {
	height := params.SecurityHardeningForkBlock + 10
	setForkForTest(t, &params.ConsensusFixForkBlock, height)
	to := common.HexToAddress("0x0080000000000000000000000000000000000000", common.Location{0, 0})
	sender := common.ZeroAddress(common.Location{0, 1})
	if !to.IsInQiLedgerScope() || sender.Location().Equal(*to.Location()) {
		t.Fatal("test requires a cross-zone Qi ETX")
	}
	for _, tc := range []struct {
		name       string
		height     uint64
		value      int64
		wantStatus uint64
		wantUTXO   int
	}{
		{"invalid_after_shared_fork", height, 15, types.ReceiptStatusFailed, 0},
		{"wrapped_value_after_shared_fork", height, 270, types.ReceiptStatusFailed, 0},
		{"negative_value_after_shared_fork", height, -1, types.ReceiptStatusFailed, 0},
		{"valid_after_shared_fork", height, 14, types.ReceiptStatusSuccessful, 1},
		{"historical_truncation", params.SecurityHardeningForkBlock - 1, 270, types.ReceiptStatusSuccessful, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := types.EmptyWorkObject(common.ZONE_CTX)
			block.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(tc.height))
			env := &environment{wo: block, gasPool: new(types.GasPool).AddGas(1_000_000)}
			tx := types.NewTx(&types.ExternalTx{
				OriginatingTxHash: common.Hash{1}, ETXIndex: 1, Gas: params.CallValueTransferGas,
				To: &to, Sender: sender, Value: big.NewInt(tc.value), EtxType: types.DefaultType,
			})
			if _, _, err := (&worker{}).commitTransaction(env, block, tx); err != nil {
				t.Fatal(err)
			}
			if len(env.receipts) != 1 || env.receipts[0].Status != tc.wantStatus || len(env.utxosCreate) != tc.wantUTXO {
				t.Fatalf("receipt/UTXO: receipts=%v UTXOs=%d", env.receipts, len(env.utxosCreate))
			}
			if env.wo.GasUsed() != params.CallValueTransferGas || env.gasPool.Gas() != 1_000_000-params.CallValueTransferGas {
				t.Fatalf("gas accounting: used=%d available=%d", env.wo.GasUsed(), env.gasPool.Gas())
			}
		})
	}
}
