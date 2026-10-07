package state

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

func testStateDB(t *testing.T) *StateDB {
	t.Helper()

	db := NewDatabase(rawdb.NewMemoryDatabase(log.Global))
	etxDb := NewDatabase(rawdb.NewMemoryDatabase(log.Global))
	state, err := New(common.Hash{}, common.Hash{}, big.NewInt(0), db, etxDb, nil, common.Location{0, 0}, log.Global)
	require.NoError(t, err)
	return state
}

func testUnlockBlock(primeTerminus uint64) *types.WorkObject {
	wo := &types.WorkObject{}
	wo.SetWorkObjectHeader(&types.WorkObjectHeader{})
	wo.WorkObjectHeader().SetNumber(big.NewInt(1))
	wo.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(primeTerminus))
	return wo
}

func testGenesisAccount(addrHex string, amount int64) params.GenesisAccount {
	account := params.GenesisAccount{
		Address:         common.HexToAddress(addrHex, common.Location{0, 0}),
		BalanceSchedule: orderedmap.New[uint64, *big.Int](),
	}
	account.BalanceSchedule.Set(0, big.NewInt(amount))
	return account
}

func testMonthlyGenesisAccount(addrHex string, amount int64, months uint64) params.GenesisAccount {
	account := params.GenesisAccount{
		Address:         common.HexToAddress(addrHex, common.Location{0, 0}),
		BalanceSchedule: orderedmap.New[uint64, *big.Int](),
	}
	for month := uint64(0); month < months; month++ {
		account.BalanceSchedule.Set(month*params.BlocksPerMonth, big.NewInt(amount))
	}
	return account
}

func testUnlockBlockAtMonth(month uint64, primeTerminus uint64) *types.WorkObject {
	wo := &types.WorkObject{}
	wo.SetWorkObjectHeader(&types.WorkObjectHeader{})
	if month == 0 {
		wo.WorkObjectHeader().SetNumber(big.NewInt(1))
	} else {
		wo.WorkObjectHeader().SetNumber(new(big.Int).SetUint64(month * params.BlocksPerMonth))
	}
	wo.WorkObjectHeader().SetPrimeTerminusNumber(new(big.Int).SetUint64(primeTerminus))
	return wo
}

func TestAddLockedBalancesSkipsForfeitureAddressesAfterFork(t *testing.T) {
	state := testStateDB(t)
	forfeited := testGenesisAccount("0x0000000000000000000000000000000000000001", 10)
	retained := testGenesisAccount("0x0000000000000000000000000000000000000002", 20)

	forfeitureAddresses := map[common.AddressBytes]bool{
		forfeited.Address.Bytes20(): true,
	}

	require.NoError(t, state.AddLockedBalances(
		testUnlockBlock(params.SingularityForkBlock),
		[]params.GenesisAccount{forfeited, retained},
		forfeitureAddresses,
		nil,
		log.Global,
	))

	forfeitedInternal, err := forfeited.Address.InternalAddress()
	require.NoError(t, err)
	retainedInternal, err := retained.Address.InternalAddress()
	require.NoError(t, err)

	require.Zero(t, state.GetBalance(forfeitedInternal).Cmp(big.NewInt(0)))
	require.Zero(t, state.GetBalance(retainedInternal).Cmp(big.NewInt(20)))
}

func TestAddLockedBalancesIncludesForfeitureAddressesBeforeFork(t *testing.T) {
	state := testStateDB(t)
	account := testGenesisAccount("0x0000000000000000000000000000000000000003", 15)

	forfeitureAddresses := map[common.AddressBytes]bool{
		account.Address.Bytes20(): true,
	}

	require.NoError(t, state.AddLockedBalances(
		testUnlockBlock(params.SingularityForkBlock-1),
		[]params.GenesisAccount{account},
		forfeitureAddresses,
		nil,
		log.Global,
	))

	internal, err := account.Address.InternalAddress()
	require.NoError(t, err)
	require.Zero(t, state.GetBalance(internal).Cmp(big.NewInt(15)))
}

func TestAddLockedBalancesSkipsForfeitureAddressesOnEveryMonthlyUnlockForYears(t *testing.T) {
	const totalMonths = 36

	state := testStateDB(t)
	forfeitedA := testMonthlyGenesisAccount("0x0000000000000000000000000000000000000011", 3, totalMonths)
	forfeitedB := testMonthlyGenesisAccount("0x0000000000000000000000000000000000000012", 5, totalMonths)
	retained := testMonthlyGenesisAccount("0x0000000000000000000000000000000000000013", 7, totalMonths)

	forfeitureAddresses := map[common.AddressBytes]bool{
		forfeitedA.Address.Bytes20(): true,
		forfeitedB.Address.Bytes20(): true,
	}

	forfeitedAInternal, err := forfeitedA.Address.InternalAddress()
	require.NoError(t, err)
	forfeitedBInternal, err := forfeitedB.Address.InternalAddress()
	require.NoError(t, err)
	retainedInternal, err := retained.Address.InternalAddress()
	require.NoError(t, err)

	expectedRetained := big.NewInt(0)
	for month := uint64(0); month < totalMonths; month++ {
		require.NoError(t, state.AddLockedBalances(
			testUnlockBlockAtMonth(month, params.SingularityForkBlock+month),
			[]params.GenesisAccount{forfeitedA, forfeitedB, retained},
			forfeitureAddresses,
			nil,
			log.Global,
		))

		expectedRetained.Add(expectedRetained, big.NewInt(7))
		require.Zero(t, state.GetBalance(forfeitedAInternal).Cmp(big.NewInt(0)), "forfeited account A unlocked at month %d", month)
		require.Zero(t, state.GetBalance(forfeitedBInternal).Cmp(big.NewInt(0)), "forfeited account B unlocked at month %d", month)
		require.Zero(t, state.GetBalance(retainedInternal).Cmp(expectedRetained), "retained account mismatch at month %d", month)
	}
}

func TestAddLockedBalancesSkipsAddressFromForfeitureFile(t *testing.T) {
	forfeitureAddresses, err := params.LoadForfeitureAddresses(filepath.Join("..", "..", "params", "forfeiture_addresses.json"))
	require.NoError(t, err)

	// This address is taken directly from params/forfeiture_addresses.json.
	forfeited := testMonthlyGenesisAccount("0x00001e9ddc9759d9734f85fe17311b35a27cc9d2", 9, 24)
	retained := testMonthlyGenesisAccount("0x0000000000000000000000000000000000000042", 4, 24)

	state := testStateDB(t)

	forfeitedInternal, err := forfeited.Address.InternalAddress()
	require.NoError(t, err)
	retainedInternal, err := retained.Address.InternalAddress()
	require.NoError(t, err)

	expectedRetained := big.NewInt(0)
	for month := uint64(0); month < 24; month++ {
		require.NoError(t, state.AddLockedBalances(
			testUnlockBlockAtMonth(month, params.SingularityForkBlock+month),
			[]params.GenesisAccount{forfeited, retained},
			forfeitureAddresses,
			nil,
			log.Global,
		))

		expectedRetained.Add(expectedRetained, big.NewInt(4))
		require.Zero(t, state.GetBalance(forfeitedInternal).Cmp(big.NewInt(0)), "file-backed forfeiture address unlocked at month %d", month)
		require.Zero(t, state.GetBalance(retainedInternal).Cmp(expectedRetained), "retained account mismatch at month %d", month)
	}
}

func TestAddLockedBalancesFounderForfeitureAtFork(t *testing.T) {
	oldAddresses, err := params.LoadForfeitureAddresses(filepath.Join("..", "..", "params", "forfeiture_addresses.json"))
	require.NoError(t, err)
	founderAddresses, err := params.LoadForfeitureAddresses(filepath.Join("..", "..", "params", "founder_forfeiture_addresses.json"))
	require.NoError(t, err)

	old := testMonthlyGenesisAccount("0x00001e9ddc9759d9734f85fe17311b35a27cc9d2", 3, 3)
	founder := testMonthlyGenesisAccount("0x00002beae967fcd44e76a1270481875082f2759d", 5, 3)
	retained := testMonthlyGenesisAccount("0x0000000000000000000000000000000000000042", 7, 3)
	require.True(t, oldAddresses[old.Address.Bytes20()])
	require.True(t, founderAddresses[founder.Address.Bytes20()])
	require.False(t, oldAddresses[founder.Address.Bytes20()])

	state := testStateDB(t)
	accounts := []params.GenesisAccount{old, founder, retained}
	require.NoError(t, state.AddLockedBalances(testUnlockBlockAtMonth(0, params.FounderForfeitureForkBlock-1), accounts, oldAddresses, founderAddresses, log.Global))
	require.NoError(t, state.AddLockedBalances(testUnlockBlockAtMonth(1, params.FounderForfeitureForkBlock), accounts, oldAddresses, founderAddresses, log.Global))

	oldInternal, err := old.Address.InternalAddress()
	require.NoError(t, err)
	founderInternal, err := founder.Address.InternalAddress()
	require.NoError(t, err)
	retainedInternal, err := retained.Address.InternalAddress()
	require.NoError(t, err)
	require.Zero(t, state.GetBalance(oldInternal).Sign())
	require.Zero(t, state.GetBalance(founderInternal).Cmp(big.NewInt(5)))
	require.Zero(t, state.GetBalance(retainedInternal).Cmp(big.NewInt(14)))
}
