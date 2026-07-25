package qiwallet

import (
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"
	"sort"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// OwnedUTXO describes a spendable UTXO held by the wallet.
type OwnedUTXO struct {
	OutPoint       types.OutPoint
	Denomination   uint8
	Address        common.Address
	PubKey         []byte   // public key that spends this UTXO (compressed or uncompressed)
	CreationHeight uint64   // block height the UTXO was created at; 0 if unknown
	Lock           *big.Int // unlock height; nil or 0 if unlocked
}

// FeeFunc returns the fee in qits a transaction with the given input and
// output counts must pay. Wallets typically derive this from
// quai_estimateFeeForQi or from the intrinsic gas formula, the base fee and
// the Quai-to-Qi exchange rate.
type FeeFunc func(numInputs, numOutputs int) *big.Int

// Selection is the result of coin selection: which UTXOs to spend and the
// denominations of the payment and change outputs. The implied fee is the
// input total minus the value of all output notes.
type Selection struct {
	Inputs       []OwnedUTXO
	PaymentNotes []uint8
	ChangeNotes  []uint8
	FeeQits      *big.Int
}

// notePool tracks available note counts per denomination, supporting the
// cashier's change-making discipline: notes may only be broken downward.
// Any multiset of outputs formed this way satisfies the consensus
// no-combining rule (core.CheckDenominations) with respect to the inputs
// the pool was built from.
type notePool struct {
	counts map[uint8]uint64
}

func newNotePool(inputs []OwnedUTXO) *notePool {
	pool := &notePool{counts: make(map[uint8]uint64)}
	for _, input := range inputs {
		pool.counts[input.Denomination]++
	}
	return pool
}

func (p *notePool) clone() *notePool {
	clone := &notePool{counts: make(map[uint8]uint64, len(p.counts))}
	for d, c := range p.counts {
		clone.counts[d] = c
	}
	return clone
}

// makeChange forms exactly target qits of output notes from the pool,
// breaking larger notes downward as needed, and removes the used value.
func (p *notePool) makeChange(target *big.Int) ([]uint8, error) {
	if target.Sign() < 0 {
		return nil, errors.New("negative change target")
	}
	remaining := new(big.Int).Set(target)
	notes := make([]uint8, 0)
	for remaining.Sign() > 0 {
		// Take the largest available note that fits under the remainder.
		taken := false
		for d := types.MaxDenomination; d >= 0; d-- {
			if p.counts[uint8(d)] == 0 {
				continue
			}
			if types.Denominations[uint8(d)].Cmp(remaining) <= 0 {
				p.counts[uint8(d)]--
				remaining.Sub(remaining, types.Denominations[uint8(d)])
				notes = append(notes, uint8(d))
				taken = true
				break
			}
		}
		if taken {
			continue
		}
		// No available note fits: break the smallest available note downward.
		broke := false
		for d := uint8(1); d <= types.MaxDenomination; d++ {
			if p.counts[d] > 0 {
				p.counts[d]--
				p.counts[d-1] += denominationRatio(d)
				broke = true
				break
			}
		}
		if !broke {
			return nil, errors.New("insufficient note value for change target")
		}
	}
	return notes, nil
}

// checkNoCombining mirrors consensus CheckDenominations: walking from the
// largest denomination down, outputs at each level must be covered by
// inputs at that level plus value carried down from above.
func checkNoCombining(inputs, outputs map[uint]uint64) error {
	carries := make(map[uint]uint64)
	for i := types.MaxDenomination; i >= 1; i-- {
		totalInputs := inputs[uint(i)] + carries[uint(i)]
		if outputs[uint(i)] > totalInputs {
			return fmt.Errorf("outputs combine smaller denominations into larger one at denomination %d", i)
		}
		diff := new(big.Int).SetUint64(totalInputs - outputs[uint(i)])
		carries[uint(i-1)] += diff.Mul(diff, new(big.Int).Div(types.Denominations[uint8(i)], types.Denominations[uint8(i-1)])).Uint64()
	}
	return nil
}

// ExpiryUrgencyWindow is the default look-ahead used by SelectUTXOs to pull
// soon-to-be-trimmed notes into a transaction: one day of blocks.
const ExpiryUrgencyWindow = 17280

// SelectUTXOs chooses inputs covering amount (in qits) plus fee, and builds
// payment and change denominations that satisfy the consensus no-combining
// rule by construction.
//
// Selection policy: notes expiring within ExpiryUrgencyWindow blocks are
// included first whenever their value exceeds the marginal fee of an extra
// input (rescuing them from being burned by the trimmer); remaining value
// is covered largest-note-first. Change absorbs everything above amount and
// fee; the fee is whatever feeFor demands for the final shape, with any
// sub-note remainder overpaid to the miner.
func SelectUTXOs(utxos []OwnedUTXO, amount *big.Int, currentHeight uint64, feeFor FeeFunc, maxNotes int, rng *mrand.Rand) (*Selection, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if feeFor == nil {
		return nil, errors.New("feeFor must be provided")
	}
	if rng == nil {
		rng = NewRand()
	}

	// Filter to currently spendable notes.
	spendable := make([]OwnedUTXO, 0, len(utxos))
	for _, utxo := range utxos {
		if utxo.Lock != nil && utxo.Lock.Sign() != 0 && utxo.Lock.Cmp(new(big.Int).SetUint64(currentHeight)) > 0 {
			continue
		}
		expiry := ExpiryHeight(utxo.Denomination, utxo.CreationHeight, utxo.Lock)
		if expiry != 0 && expiry <= currentHeight {
			continue // already trimmed or about to be; do not rely on it
		}
		spendable = append(spendable, utxo)
	}

	urgent := make([]OwnedUTXO, 0)
	rest := make([]OwnedUTXO, 0, len(spendable))
	for _, utxo := range spendable {
		expiry := ExpiryHeight(utxo.Denomination, utxo.CreationHeight, utxo.Lock)
		if expiry != 0 && expiry-currentHeight <= ExpiryUrgencyWindow {
			urgent = append(urgent, utxo)
		} else {
			rest = append(rest, utxo)
		}
	}
	sort.SliceStable(urgent, func(i, j int) bool {
		return ExpiryHeight(urgent[i].Denomination, urgent[i].CreationHeight, urgent[i].Lock) <
			ExpiryHeight(urgent[j].Denomination, urgent[j].CreationHeight, urgent[j].Lock)
	})
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Denomination > rest[j].Denomination })

	selected := make([]OwnedUTXO, 0, 8)
	total := big.NewInt(0)
	addInput := func(utxo OwnedUTXO) {
		selected = append(selected, utxo)
		total.Add(total, types.Denominations[utxo.Denomination])
	}

	// Rescue expiring notes when their value beats the marginal input fee.
	estimatedOutputs := 4
	if greedy, err := DecomposeGreedy(amount); err == nil {
		estimatedOutputs = len(greedy) + 2
	}
	for _, utxo := range urgent {
		marginalFee := new(big.Int).Sub(feeFor(len(selected)+1, estimatedOutputs), feeFor(len(selected), estimatedOutputs))
		if types.Denominations[utxo.Denomination].Cmp(marginalFee) > 0 {
			addInput(utxo)
		}
	}

	// Cover amount plus fee, largest notes first.
	nextRest := 0
	for {
		need := new(big.Int).Add(amount, feeFor(len(selected), estimatedOutputs))
		if total.Cmp(need) >= 0 {
			break
		}
		if nextRest >= len(rest) {
			return nil, fmt.Errorf("insufficient funds: have %s qits spendable, need %s plus fees", total.String(), amount.String())
		}
		addInput(rest[nextRest])
		nextRest++
	}

	// Build outputs from the selected notes by making change downward, then
	// iterate the fee/change fixpoint: change value depends on the fee,
	// which depends on the output count.
	buildPayment := func() (*notePool, []uint8, error) {
		pool := newNotePool(selected)
		payment, err := pool.makeChange(amount)
		if err != nil {
			return nil, nil, err
		}
		return pool, payment, nil
	}
	pool, payment, err := buildPayment()
	if err != nil {
		return nil, err
	}

	var change []uint8
	changeValue := big.NewInt(0)
	for iteration := 0; iteration < 12; iteration++ {
		fee := feeFor(len(selected), len(payment)+len(change))
		newChangeValue := new(big.Int).Sub(total, amount)
		newChangeValue.Sub(newChangeValue, fee)
		if newChangeValue.Sign() < 0 {
			// The fee outgrew our margin; pull in one more input and rebuild.
			if nextRest >= len(rest) {
				return nil, errors.New("insufficient funds to cover the fee")
			}
			addInput(rest[nextRest])
			nextRest++
			pool, payment, err = buildPayment()
			if err != nil {
				return nil, err
			}
			change = nil
			changeValue = big.NewInt(0)
			continue
		}
		if iteration > 0 && newChangeValue.Cmp(changeValue) == 0 {
			break
		}
		changePool := pool.clone()
		change, err = changePool.makeChange(newChangeValue)
		if err != nil {
			return nil, err
		}
		changeValue = newChangeValue
	}
	if len(payment)+len(change) > maxNotes {
		return nil, fmt.Errorf("transaction needs %d output notes, more than the maximum of %d", len(payment)+len(change), maxNotes)
	}

	// Privacy shaping: randomly split change notes downward, within a modest
	// budget, so change does not follow the canonical greedy shape. Done
	// before the final fee check so the fee covers the final output count.
	if budget := min(maxNotes-len(payment), len(change)+4); len(change) > 0 && budget > len(change) {
		change = SplitNotes(change, budget, rng)
	}

	// Final feasibility: drop change notes (smallest first, their value goes
	// to the fee) until the implied fee covers the requirement for the final
	// transaction shape.
	for {
		impliedFee := new(big.Int).Sub(total, amount)
		impliedFee.Sub(impliedFee, changeValue)
		required := feeFor(len(selected), len(payment)+len(change))
		if impliedFee.Cmp(required) >= 0 {
			break
		}
		if len(change) == 0 {
			return nil, errors.New("unable to satisfy fee requirement")
		}
		smallest := 0
		for i := range change {
			if change[i] < change[smallest] {
				smallest = i
			}
		}
		changeValue.Sub(changeValue, types.Denominations[change[smallest]])
		change = append(change[:smallest], change[smallest+1:]...)
	}

	// Sanity: the combined outputs must satisfy the consensus rule.
	inputCounts := make(map[uint]uint64)
	for _, utxo := range selected {
		inputCounts[uint(utxo.Denomination)]++
	}
	outputCounts := make(map[uint]uint64)
	for _, note := range payment {
		outputCounts[uint(note)]++
	}
	for _, note := range change {
		outputCounts[uint(note)]++
	}
	if err := checkNoCombining(inputCounts, outputCounts); err != nil {
		return nil, fmt.Errorf("internal error: selection violates no-combining rule: %w", err)
	}

	fee := new(big.Int).Sub(total, amount)
	fee.Sub(fee, changeValue)
	return &Selection{
		Inputs:       selected,
		PaymentNotes: payment,
		ChangeNotes:  change,
		FeeQits:      fee,
	}, nil
}

// BuildQiTx assembles an unsigned QiTx from a selection. paymentAddrs must
// contain one fresh address per payment note (provided by the payee) and
// changeAddrs one fresh address per change note (owned by this wallet).
// Output order is shuffled so change position carries no information. The
// within-transaction address reuse ban is enforced here so an invalid
// combination fails before signing.
func BuildQiTx(chainID *big.Int, location common.Location, sel *Selection, paymentAddrs, changeAddrs []common.Address, rng *mrand.Rand) (*types.QiTx, error) {
	if len(paymentAddrs) != len(sel.PaymentNotes) {
		return nil, fmt.Errorf("need %d payment addresses, got %d", len(sel.PaymentNotes), len(paymentAddrs))
	}
	if len(changeAddrs) != len(sel.ChangeNotes) {
		return nil, fmt.Errorf("need %d change addresses, got %d", len(sel.ChangeNotes), len(changeAddrs))
	}
	if rng == nil {
		rng = NewRand()
	}

	seen := make(map[common.AddressBytes]struct{})
	txIns := make([]types.TxIn, 0, len(sel.Inputs))
	for _, input := range sel.Inputs {
		if len(input.PubKey) == 0 {
			return nil, fmt.Errorf("input %v has no public key", input.OutPoint)
		}
		txIns = append(txIns, types.TxIn{PreviousOutPoint: input.OutPoint, PubKey: input.PubKey})
		seen[input.Address.Bytes20()] = struct{}{}
	}

	type output struct {
		note uint8
		addr common.Address
	}
	outputs := make([]output, 0, len(sel.PaymentNotes)+len(sel.ChangeNotes))
	for i, note := range sel.PaymentNotes {
		outputs = append(outputs, output{note, paymentAddrs[i]})
	}
	for i, note := range sel.ChangeNotes {
		outputs = append(outputs, output{note, changeAddrs[i]})
	}
	rng.Shuffle(len(outputs), func(a, b int) { outputs[a], outputs[b] = outputs[b], outputs[a] })

	txOuts := make([]types.TxOut, 0, len(outputs))
	for _, out := range outputs {
		if !out.addr.IsInQiLedgerScope() {
			return nil, fmt.Errorf("output address %s is not in Qi ledger scope", out.addr.String())
		}
		if _, exists := seen[out.addr.Bytes20()]; exists {
			return nil, fmt.Errorf("address %s reused within the transaction", out.addr.String())
		}
		seen[out.addr.Bytes20()] = struct{}{}
		txOuts = append(txOuts, types.TxOut{Denomination: out.note, Address: out.addr.Bytes(), Lock: nil})
	}

	return &types.QiTx{
		ChainID: chainID,
		TxIn:    txIns,
		TxOut:   txOuts,
	}, nil
}
