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
	require.NoError(t, state.AddLockedBalances(testUnlockBlockAtMonth(0, params.PolarisForkBlock-1), accounts, oldAddresses, founderAddresses, log.Global))
	require.NoError(t, state.AddLockedBalances(testUnlockBlockAtMonth(1, params.PolarisForkBlock), accounts, oldAddresses, founderAddresses, log.Global))
	require.NoError(t, state.AddLockedBalances(testUnlockBlockAtMonth(2, params.PolarisForkBlock+1), accounts, oldAddresses, founderAddresses, log.Global))

	oldInternal, err := old.Address.InternalAddress()
	require.NoError(t, err)
	founderInternal, err := founder.Address.InternalAddress()
	require.NoError(t, err)
	retainedInternal, err := retained.Address.InternalAddress()
	require.NoError(t, err)
	require.Zero(t, state.GetBalance(oldInternal).Sign())
	require.Zero(t, state.GetBalance(founderInternal).Cmp(big.NewInt(5)))
	require.Zero(t, state.GetBalance(retainedInternal).Cmp(big.NewInt(21)))
}

// Run every monthly unlock in the checked-in Orchard allocation schedule through
// both forfeiture forks. Mainnet zone headers at months 13, 14 and 21 carry Prime
// termini 1,470,244, 1,589,198 and 2,408,223. Polaris was active before the
// future month-22 unlock. Only these fork stages matter to AddLockedBalances;
// exact Prime numbers within a stage do not.
func TestAddLockedBalancesFounderForfeitureAllMonths(t *testing.T) {
	primeTerminusAtMonth := func(month uint64) uint64 {
		switch {
		case month < 14:
			return params.SingularityForkBlock - 1
		case month < 22:
			return params.PolarisForkBlock - 1
		default:
			return params.PolarisForkBlock
		}
	}
	allocs, err := params.VerifyGenesisAllocs(filepath.Join("..", "..", "params", "genesis_alloc.json"), params.AllocHash)
	require.NoError(t, err)
	oldAddresses, err := params.LoadForfeitureAddresses(filepath.Join("..", "..", "params", "forfeiture_addresses.json"))
	require.NoError(t, err)
	founderAddresses, err := params.LoadForfeitureAddresses(filepath.Join("..", "..", "params", "founder_forfeiture_addresses.json"))
	require.NoError(t, err)

	expectedByAddress := make(map[common.AddressBytes]*big.Int, len(allocs))
	founderMatched := make(map[common.AddressBytes]struct{})
	founderWithheld := new(big.Int)
	oldWithheld := new(big.Int)
	blockOneUnlocked := new(big.Int)
	maxMonth := uint64(0)
	for _, account := range allocs {
		key := account.Address.Bytes20()
		if expectedByAddress[key] == nil {
			expectedByAddress[key] = new(big.Int)
		}
		for unlock := account.BalanceSchedule.Oldest(); unlock != nil; unlock = unlock.Next() {
			month := unlock.Key / params.BlocksPerMonth
			if month > maxMonth {
				maxMonth = month
			}
			primeTerminus := primeTerminusAtMonth(month)
			if month == 0 {
				blockOneUnlocked.Add(blockOneUnlocked, unlock.Value)
			}
			if oldAddresses[key] && primeTerminus >= params.SingularityForkBlock {
				oldWithheld.Add(oldWithheld, unlock.Value)
			} else if founderAddresses[key] && primeTerminus >= params.PolarisForkBlock {
				founderWithheld.Add(founderWithheld, unlock.Value)
			} else {
				expectedByAddress[key].Add(expectedByAddress[key], unlock.Value)
			}
			if founderAddresses[key] {
				founderMatched[key] = struct{}{}
			}
		}
	}
	require.Equal(t, uint64(72), maxMonth)
	require.Len(t, founderMatched, 359)

	state := testStateDB(t)
	for month := uint64(0); month <= maxMonth; month++ {
		require.NoError(t, state.AddLockedBalances(
			testUnlockBlockAtMonth(month, primeTerminusAtMonth(month)),
			allocs, oldAddresses, founderAddresses, log.Global,
		), "month %d", month)
	}

	finalUnlocked := new(big.Int)
	seen := make(map[common.AddressBytes]struct{}, len(expectedByAddress))
	for _, account := range allocs {
		key := account.Address.Bytes20()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		internal, err := account.Address.InternalAddress()
		require.NoError(t, err)
		balance := state.GetBalance(internal)
		require.Zero(t, balance.Cmp(expectedByAddress[key]), "wrong final unlock for %s", account.Address)
		finalUnlocked.Add(finalUnlocked, balance)
	}
	// These totals include the Orchard fixture's four seeded accounts, each with
	// 10^30 wei at block 1. VerifyGenesisAllocs above pins the complete fixture.
	require.Equal(t, "4001096383689133997684558250502", finalUnlocked.String())
	require.Equal(t, "236456538365850624693750162", founderWithheld.String())
	require.Equal(t, "1667159984000000000000000036", oldWithheld.String())
	require.Equal(t, "4000477922715957243240952000700", blockOneUnlocked.String())

	quai := new(big.Rat).SetFrac(finalUnlocked, big.NewInt(params.Ether)).FloatString(18)
	founderQuai := new(big.Rat).SetFrac(founderWithheld, big.NewInt(params.Ether)).FloatString(18)
	blockOneQuai := new(big.Rat).SetFrac(blockOneUnlocked, big.NewInt(params.Ether)).FloatString(18)
	t.Logf("full genesis schedule through forfeiture forks: months=0..%d founder_addresses=%d block_1_unlocked=%s QUAI final_unlocked=%s wei (%s QUAI) founder_withheld=%s wei (%s QUAI) old_withheld=%s wei",
		maxMonth, len(founderMatched), blockOneQuai, finalUnlocked, quai, founderWithheld, founderQuai, oldWithheld)
}
