package qichannel_test

import (
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/qichannel"
	"github.com/dominant-strategies/go-quai/qiwallet"
)

var (
	testLocation = common.Location{0, 0}
	testChainID  = big.NewInt(1337)
)

func newKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// openChannel sets up both parties' views of the same channel.
func openChannel(t *testing.T, capacity int64, openHeight uint64) (*qichannel.Channel, *qichannel.Channel) {
	t.Helper()
	remote := newKey(t)
	local, addr, err := qichannel.GrindFundingKey(remote.PubKey(), testLocation)
	if err != nil {
		t.Fatal(err)
	}
	if !qiwallet.InQiScope(addr, testLocation) {
		t.Fatal("ground funding address is not in Qi scope")
	}

	var fundingHash common.Hash
	fundingHash[0] = 0xAB
	funding := types.OutPoint{TxHash: fundingHash, Index: 0}
	p := qichannel.DefaultParams(testChainID, testLocation)

	half := big.NewInt(capacity / 2)
	a, err := qichannel.New(p, local, remote.PubKey(), funding, big.NewInt(capacity), openHeight, half, half)
	if err != nil {
		t.Fatal(err)
	}
	b, err := qichannel.New(p, remote, local.PubKey(), funding, big.NewInt(capacity), openHeight, half, half)
	if err != nil {
		t.Fatal(err)
	}
	// Both parties must derive the same funding address and aggregate key.
	if !a.FundingAddress.Equal(b.FundingAddress) {
		t.Fatal("parties disagree on the funding address")
	}
	if !a.AggregateKey().IsEqual(b.AggregateKey()) {
		t.Fatal("parties disagree on the aggregate key")
	}
	return a, b
}

// TestMaturityOrdering is the security property the construction rests on:
// newer states must mature strictly earlier, so an honest party can always
// publish the latest state before any stale one its counterparty holds
// becomes valid.
func TestMaturityOrdering(t *testing.T) {
	a, _ := openChannel(t, 100000, 1000)
	prev := a.MaturityHeight(0)
	for state := uint64(1); state < a.MaxStates; state++ {
		cur := a.MaturityHeight(state)
		if cur >= prev {
			t.Fatalf("state %d matures at %d, not before state %d at %d", state, cur, state-1, prev)
		}
		if prev-cur != a.SettlementDelta {
			t.Fatalf("state %d watch window is %d, want %d", state, prev-cur, a.SettlementDelta)
		}
		prev = cur
	}
	// The watch window for a state ends when the previous state matures.
	from, to := a.WatchWindow(5)
	if from != a.MaturityHeight(5) || to != a.MaturityHeight(4) {
		t.Fatal("watch window does not span until the previous state matures")
	}
	if _, openEnded := a.WatchWindow(0); openEnded != 0 {
		t.Fatal("the initial state should have an open ended window")
	}
}

func TestUpdateAccounting(t *testing.T) {
	a, _ := openChannel(t, 100000, 1000)
	if err := a.Update(big.NewInt(40000), big.NewInt(60000)); err != nil {
		t.Fatal(err)
	}
	if a.StateIndex != 1 {
		t.Fatalf("state index %d, want 1", a.StateIndex)
	}
	// Overspending the channel must be rejected.
	if err := a.Update(big.NewInt(90000), big.NewInt(90000)); err == nil {
		t.Fatal("expected overspend to be rejected")
	}
	// Exhausting the update budget must be reported, not silently allowed.
	a.StateIndex = a.MaxStates - 1
	if a.RemainingUpdates() != 0 {
		t.Fatal("expected no remaining updates")
	}
	if err := a.Update(big.NewInt(1), big.NewInt(1)); err == nil {
		t.Fatal("expected exhausted channel to reject updates")
	}
}

func settlementAddrs(t *testing.T, n int) []common.Address {
	t.Helper()
	addrs := make([]common.Address, n)
	for i := range addrs {
		_, addr, _, err := qiwallet.GrindQiAddress(testLocation, nil)
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = addr
	}
	return addrs
}

// TestSettlementCarriesLockTime checks that settlements are locktimed to
// their state's maturity and cooperative closes are not, and that the
// locktime is the encoding consensus recognizes.
func TestSettlementCarriesLockTime(t *testing.T) {
	a, _ := openChannel(t, 100000, 1000)
	balances := qichannel.Balances{
		LocalNotes: []uint8{6}, LocalAddrs: settlementAddrs(t, 1),
		RemoteNotes: []uint8{6}, RemoteAddrs: settlementAddrs(t, 1),
	}
	settlement, err := a.BuildSettlement(3, balances)
	if err != nil {
		t.Fatal(err)
	}
	lockTime, ok := core.QiTxLockTime(types.NewTx(settlement))
	if !ok {
		t.Fatal("settlement carries no locktime")
	}
	if lockTime != a.MaturityHeight(3) {
		t.Fatalf("locktime %d, want maturity %d", lockTime, a.MaturityHeight(3))
	}

	coop, err := a.BuildCooperativeClose(balances)
	if err != nil {
		t.Fatal(err)
	}
	if len(coop.Data) != 0 {
		t.Fatal("cooperative close should carry no locktime")
	}
	// Settlement spends exactly the funding output, under the aggregate key.
	if len(settlement.TxIn) != 1 || settlement.TxIn[0].PreviousOutPoint != a.FundingOutPoint {
		t.Fatal("settlement does not spend the funding output")
	}
}

func TestSettlementRejectsBadBalances(t *testing.T) {
	a, _ := openChannel(t, 2000, 1000)
	addrs := settlementAddrs(t, 2)
	// Paying out more than the capacity must fail.
	_, err := a.BuildSettlement(0, qichannel.Balances{
		LocalNotes: []uint8{10}, LocalAddrs: addrs[:1],
		RemoteNotes: []uint8{10}, RemoteAddrs: addrs[1:],
	})
	if err == nil {
		t.Fatal("expected overspending settlement to be rejected")
	}
	// Reusing an address across outputs violates the consensus rule.
	_, err = a.BuildSettlement(0, qichannel.Balances{
		LocalNotes: []uint8{6}, LocalAddrs: addrs[:1],
		RemoteNotes: []uint8{6}, RemoteAddrs: addrs[:1],
	})
	if err == nil {
		t.Fatal("expected address reuse to be rejected")
	}
}

// TestCooperativeSigning runs the full two-round MuSig2 exchange between the
// two parties and checks the result verifies under the aggregate key, which
// is what Qi consensus checks.
func TestCooperativeSigning(t *testing.T) {
	a, b := openChannel(t, 100000, 1000)
	balances := qichannel.Balances{
		LocalNotes: []uint8{6}, LocalAddrs: settlementAddrs(t, 1),
		RemoteNotes: []uint8{6}, RemoteAddrs: settlementAddrs(t, 1),
	}
	tx, err := a.BuildCooperativeClose(balances)
	if err != nil {
		t.Fatal(err)
	}
	signer := types.LatestSignerForChainID(testChainID, testLocation)

	sessionA, err := a.NewSigningSession(tx, signer)
	if err != nil {
		t.Fatal(err)
	}
	sessionB, err := b.NewSigningSession(tx, signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionA.RegisterRemoteNonce(sessionB.PublicNonce()); err != nil {
		t.Fatal(err)
	}
	if err := sessionB.RegisterRemoteNonce(sessionA.PublicNonce()); err != nil {
		t.Fatal(err)
	}
	partialA, err := sessionA.Sign()
	if err != nil {
		t.Fatal(err)
	}
	partialB, err := sessionB.Sign()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := sessionA.Combine(tx, partialA, partialB)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the way consensus does.
	digest := signer.Hash(signed)
	if !signed.GetSchnorrSignature().Verify(digest[:], a.AggregateKey()) {
		t.Fatal("signed settlement does not verify under the aggregate key")
	}
	// One party alone must not be able to produce a valid signature.
	if _, err := sessionA.Combine(tx, partialA); err == nil {
		t.Fatal("a single partial signature produced a valid settlement")
	}
}

func TestPTLCAccounting(t *testing.T) {
	a, _ := openChannel(t, 100000, 1000)
	_, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	ptlc := &qichannel.PTLC{PaymentPoint: point, Amount: big.NewInt(10000), Deadline: a.OpenHeight + 100}
	if err := a.AddPTLC(ptlc); err != nil {
		t.Fatal(err)
	}
	if a.LocalBalance.Cmp(big.NewInt(40000)) != 0 {
		t.Fatalf("payer balance %s, want 40000", a.LocalBalance)
	}
	if err := a.SettlePTLC(ptlc, true); err != nil {
		t.Fatal(err)
	}
	if a.RemoteBalance.Cmp(big.NewInt(60000)) != 0 {
		t.Fatalf("payee balance %s, want 60000", a.RemoteBalance)
	}
	// A deadline beyond the channel's own expiry is unenforceable.
	late := &qichannel.PTLC{PaymentPoint: point, Amount: big.NewInt(1), Deadline: a.ExpiryHeight() + 1}
	if err := a.AddPTLC(late); err == nil {
		t.Fatal("expected a PTLC past channel expiry to be rejected")
	}
	// A PTLC larger than the payer's balance must be rejected.
	oversized := &qichannel.PTLC{PaymentPoint: point, Amount: a.Capacity, Deadline: a.OpenHeight + 100}
	if err := a.AddPTLC(oversized); err == nil {
		t.Fatal("expected an oversized PTLC to be rejected")
	}
}

func TestBlindingRoundTrip(t *testing.T) {
	secret, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	blinded, blind, err := qichannel.BlindPoint(point)
	if err != nil {
		t.Fatal(err)
	}
	if blinded.IsEqual(point) {
		t.Fatal("blinded point equals the original")
	}
	// The secret satisfying the blinded point is t + r, and removing r
	// recovers t.
	blindedSecret := new(btcec.ModNScalar)
	blindedSecret.Set(secret).Add(blind)
	if got := qichannel.UnblindSecret(blindedSecret, blind); !got.Equals(secret) {
		t.Fatal("unblinding did not recover the payment secret")
	}
}

func TestBuildRouteStaircase(t *testing.T) {
	_, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	nodes := []*btcec.PublicKey{newKey(t).PubKey(), newKey(t).PubKey(), newKey(t).PubKey()}
	fees := []*big.Int{big.NewInt(0), big.NewInt(100), big.NewInt(50)}
	route, err := qichannel.BuildRoute(point, big.NewInt(10000), nodes, fees, 5000, qichannel.DefaultHopDelta)
	if err != nil {
		t.Fatal(err)
	}
	if err := route.Validate(qichannel.DefaultHopDelta); err != nil {
		t.Fatal(err)
	}
	// Deadlines must decrease toward the payee, amounts must decrease too.
	for i := 1; i < len(route.Hops); i++ {
		if route.Hops[i].Deadline >= route.Hops[i-1].Deadline {
			t.Fatalf("hop %d deadline is not earlier than hop %d", i, i-1)
		}
		if route.Hops[i].Amount.Cmp(route.Hops[i-1].Amount) > 0 {
			t.Fatalf("hop %d forwards more than hop %d", i, i-1)
		}
	}
	// The sender pays the delivered amount plus all downstream fees.
	if route.TotalAmount.Cmp(big.NewInt(10000+100+50)) != 0 {
		t.Fatalf("total amount %s, want 10150", route.TotalAmount)
	}
	// Every hop is locked to a distinct point (decorrelation).
	for i := range route.Hops {
		for j := i + 1; j < len(route.Hops); j++ {
			if route.Hops[i].PaymentPoint.IsEqual(route.Hops[j].PaymentPoint) {
				t.Fatalf("hops %d and %d share a payment point", i, j)
			}
		}
	}
}

func TestRouteFitsChannels(t *testing.T) {
	a, _ := openChannel(t, 100000, 1000)
	_, point, err := qichannel.NewPayment()
	if err != nil {
		t.Fatal(err)
	}
	nodes := []*btcec.PublicKey{newKey(t).PubKey()}
	// A deadline inside the channel's life is fine.
	route, err := qichannel.BuildRoute(point, big.NewInt(1000), nodes, []*big.Int{big.NewInt(0)}, a.OpenHeight+10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := route.FitsChannels([]*qichannel.Channel{a}); err != nil {
		t.Fatal(err)
	}
	// One past its expiry is not.
	late, err := qichannel.BuildRoute(point, big.NewInt(1000), nodes, []*big.Int{big.NewInt(0)}, a.ExpiryHeight()+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := late.FitsChannels([]*qichannel.Channel{a}); err == nil {
		t.Fatal("expected a route past channel expiry to be rejected")
	}
}
