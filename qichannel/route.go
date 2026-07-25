package qichannel

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
)

// DefaultHopDelta is the safety margin between consecutive hops' PTLC
// deadlines: the number of blocks a routing node has to settle upstream after
// being settled downstream, including time to get a transaction confirmed.
const DefaultHopDelta uint64 = 144

// Hop is one leg of a route.
type Hop struct {
	// Node identifies the peer this hop forwards to.
	Node *btcec.PublicKey
	// Amount is the value forwarded on this hop in qits, including the fees
	// of all downstream hops.
	Amount *big.Int
	// Fee is what this hop's forwarding node keeps.
	Fee *big.Int
	// Deadline is the PTLC deadline on this hop.
	Deadline uint64
	// PaymentPoint is the (blinded) point this hop is locked to.
	PaymentPoint *btcec.PublicKey
	// Blind is the offset added to the payment point for this hop, known
	// only to the sender.
	Blind *PaymentSecret
}

// Route is a fully constructed path from sender to payee.
type Route struct {
	Hops []Hop
	// TotalAmount is what the sender pays, including all forwarding fees.
	TotalAmount *big.Int
	// FinalDeadline is the PTLC deadline on the last hop.
	FinalDeadline uint64
}

// BuildRoute constructs the amount and deadline schedule for a payment.
//
// Deadlines form a staircase: the hop closest to the payee has the earliest
// deadline, and each hop upstream gets hopDelta more blocks, so an
// intermediary always has time to claim upstream after being claimed
// downstream. Amounts accumulate in the other direction: each hop carries the
// amount delivered plus the fees of every hop below it.
//
// Each hop is locked to a distinct blinded payment point, so intermediaries
// cannot correlate hops of the same payment.
func BuildRoute(destinationPoint *btcec.PublicKey, amount *big.Int, nodes []*btcec.PublicKey,
	fees []*big.Int, finalDeadline uint64, hopDelta uint64) (*Route, error) {

	if destinationPoint == nil || amount == nil {
		return nil, errors.New("qichannel: destination point and amount are required")
	}
	if len(nodes) == 0 {
		return nil, errors.New("qichannel: a route needs at least one hop")
	}
	if len(fees) != len(nodes) {
		return nil, fmt.Errorf("qichannel: %d hops but %d fees", len(nodes), len(fees))
	}
	if hopDelta == 0 {
		hopDelta = DefaultHopDelta
	}

	hops := make([]Hop, len(nodes))
	// Walk from the payee backwards: amounts accumulate fees and deadlines
	// grow by hopDelta with each step upstream.
	runningAmount := new(big.Int).Set(amount)
	deadline := finalDeadline
	for i := len(nodes) - 1; i >= 0; i-- {
		blinded, blind, err := BlindPoint(destinationPoint)
		if err != nil {
			return nil, err
		}
		if i != len(nodes)-1 {
			runningAmount = new(big.Int).Add(runningAmount, fees[i+1])
			deadline += hopDelta
		}
		hops[i] = Hop{
			Node:         nodes[i],
			Amount:       new(big.Int).Set(runningAmount),
			Fee:          new(big.Int).Set(fees[i]),
			Deadline:     deadline,
			PaymentPoint: blinded,
			Blind:        blind,
		}
	}
	return &Route{
		Hops:          hops,
		TotalAmount:   new(big.Int).Set(hops[0].Amount),
		FinalDeadline: finalDeadline,
	}, nil
}

// Validate checks a route's internal consistency: deadlines must decrease
// toward the payee by at least hopDelta, and amounts must not increase.
func (r *Route) Validate(hopDelta uint64) error {
	if len(r.Hops) == 0 {
		return errors.New("qichannel: empty route")
	}
	if hopDelta == 0 {
		hopDelta = DefaultHopDelta
	}
	for i := 1; i < len(r.Hops); i++ {
		prev, cur := r.Hops[i-1], r.Hops[i]
		if prev.Deadline < cur.Deadline+hopDelta {
			return fmt.Errorf("qichannel: hop %d deadline %d leaves less than %d blocks over hop %d's %d",
				i-1, prev.Deadline, hopDelta, i, cur.Deadline)
		}
		if cur.Amount.Cmp(prev.Amount) > 0 {
			return fmt.Errorf("qichannel: hop %d forwards more than hop %d", i, i-1)
		}
	}
	return nil
}

// FitsChannels checks that every hop's deadline falls inside the usable
// lifetime of the channel that will carry it. Because channels here are time
// bounded, routing capacity decays as a channel approaches expiry, and a
// route must be rejected rather than stranded.
func (r *Route) FitsChannels(channels []*Channel) error {
	if len(channels) != len(r.Hops) {
		return fmt.Errorf("qichannel: %d hops but %d channels", len(r.Hops), len(channels))
	}
	for i, hop := range r.Hops {
		if channels[i] == nil {
			return fmt.Errorf("qichannel: no channel for hop %d", i)
		}
		if expiry := channels[i].ExpiryHeight(); hop.Deadline >= expiry {
			return fmt.Errorf("qichannel: hop %d deadline %d does not fit channel expiring at %d", i, hop.Deadline, expiry)
		}
		if channels[i].RemainingUpdates() == 0 {
			return fmt.Errorf("qichannel: channel for hop %d has no updates remaining", i)
		}
	}
	return nil
}
