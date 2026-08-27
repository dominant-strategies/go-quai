package qichannel

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto/adaptor"
	"github.com/dominant-strategies/go-quai/qiwallet"
)

// PTLC is a point timelocked contract: an amount held in a channel that the
// payee can claim by revealing the discrete log of PaymentPoint, and that
// otherwise reverts to the payer after Deadline.
//
// PTLCs replace Lightning's HTLCs. The claim condition lives entirely in an
// adaptor signature rather than in a script, which is what makes them
// expressible on Qi at all - and, as a bonus, lets every hop of a route use a
// different point, so intermediaries cannot correlate a payment across hops
// the way a shared payment hash allows.
type PTLC struct {
	// PaymentPoint is T = t*G; claiming requires revealing t.
	PaymentPoint *btcec.PublicKey
	// Amount is the value held, in qits.
	Amount *big.Int
	// Deadline is the height after which the payer may reclaim the amount.
	Deadline uint64
	// Incoming reports whether this node is the payee (true) or payer.
	Incoming bool
}

// PaymentSecret is the scalar that unlocks a PTLC.
type PaymentSecret = btcec.ModNScalar

// NewPayment generates a fresh payment secret and its point. The payee
// generates this, keeps the secret, and publishes the point in an invoice.
func NewPayment() (*PaymentSecret, *btcec.PublicKey, error) {
	return adaptor.NewSecret()
}

// BlindPoint returns T + r*G together with r, for decorrelating a payment
// across hops: each hop is locked to a different point, so two colluding
// intermediaries cannot tell they are forwarding the same payment.
func BlindPoint(point *btcec.PublicKey) (*btcec.PublicKey, *PaymentSecret, error) {
	blind, blindPoint, err := adaptor.NewSecret()
	if err != nil {
		return nil, nil, err
	}
	blinded, err := adaptor.AddPoints(point, blindPoint)
	if err != nil {
		return nil, nil, err
	}
	return blinded, blind, nil
}

// UnblindSecret recovers the underlying payment secret from the secret that
// satisfied a blinded point: t = t' - r.
func UnblindSecret(blindedSecret, blind *PaymentSecret) *PaymentSecret {
	var negated btcec.ModNScalar
	negated.Set(blind)
	negated.Negate()
	secret := new(btcec.ModNScalar)
	secret.Set(blindedSecret).Add(&negated)
	return secret
}

// AddPTLC registers an in-flight payment against the channel, reserving its
// amount from the payer's balance.
func (c *Channel) AddPTLC(ptlc *PTLC) error {
	if ptlc == nil || ptlc.PaymentPoint == nil || ptlc.Amount == nil {
		return errors.New("qichannel: incomplete PTLC")
	}
	if ptlc.Deadline >= c.ExpiryHeight() {
		return fmt.Errorf("qichannel: PTLC deadline %d is beyond the channel expiry %d", ptlc.Deadline, c.ExpiryHeight())
	}
	payer := c.LocalBalance
	if ptlc.Incoming {
		payer = c.RemoteBalance
	}
	if payer.Cmp(ptlc.Amount) < 0 {
		return fmt.Errorf("qichannel: PTLC amount %s exceeds the payer's balance %s", ptlc.Amount, payer)
	}
	payer.Sub(payer, ptlc.Amount)
	c.PTLCs = append(c.PTLCs, ptlc)
	return nil
}

// SettlePTLC removes a PTLC and credits its amount to the payee, which is
// what both parties do off-chain once the payment secret is revealed.
func (c *Channel) SettlePTLC(ptlc *PTLC, claimed bool) error {
	for i, existing := range c.PTLCs {
		if existing != ptlc {
			continue
		}
		c.PTLCs = append(c.PTLCs[:i], c.PTLCs[i+1:]...)
		payee, payer := c.RemoteBalance, c.LocalBalance
		if ptlc.Incoming {
			payee, payer = c.LocalBalance, c.RemoteBalance
		}
		if claimed {
			payee.Add(payee, ptlc.Amount)
		} else {
			payer.Add(payer, ptlc.Amount)
		}
		return nil
	}
	return errors.New("qichannel: PTLC not found in this channel")
}

// PTLCOutput describes the on-chain output a PTLC occupies when a channel is
// closed unilaterally while the payment is still in flight. It pays to a
// 2-of-2 aggregate, and is resolved by one of two pre-signed spends: a claim
// (adaptor locked to the payment point) or a refund (locktimed to the
// deadline).
type PTLCOutput struct {
	OutPoint     types.OutPoint
	Denomination uint8
	AggregateKey *btcec.PublicKey
}

// BuildPTLCClaim constructs the unsigned transaction the payee uses to claim
// a PTLC output. It carries no locktime, so it is valid immediately: the only
// thing standing between the payee and the funds is the adaptor signature,
// which requires the payment secret. Publishing it reveals that secret, which
// is exactly what lets the upstream hop claim in turn.
func (c *Channel) BuildPTLCClaim(out PTLCOutput, payeeNotes []uint8, payeeAddrs []common.Address) (*types.QiTx, error) {
	return c.buildPTLCSpend(out, payeeNotes, payeeAddrs, nil)
}

// BuildPTLCRefund constructs the unsigned transaction the payer uses to
// reclaim a PTLC output once its deadline passes. Its locktime is the
// deadline, so it cannot confirm earlier - which is what guarantees the payee
// a window in which only the claim path is available.
func (c *Channel) BuildPTLCRefund(out PTLCOutput, deadline uint64, payerNotes []uint8, payerAddrs []common.Address) (*types.QiTx, error) {
	return c.buildPTLCSpend(out, payerNotes, payerAddrs, EncodeLockTime(deadline))
}

func (c *Channel) buildPTLCSpend(out PTLCOutput, notes []uint8, addrs []common.Address, data []byte) (*types.QiTx, error) {
	if len(notes) != len(addrs) {
		return nil, fmt.Errorf("qichannel: %d notes but %d addresses", len(notes), len(addrs))
	}
	if out.AggregateKey == nil {
		return nil, errors.New("qichannel: PTLC output has no aggregate key")
	}
	seen := make(map[common.AddressBytes]struct{})
	txOut := make([]types.TxOut, 0, len(notes))
	for i, note := range notes {
		addr := addrs[i]
		if !qiwallet.InQiScope(addr, c.Location) {
			return nil, fmt.Errorf("qichannel: address %s is not in Qi scope", addr)
		}
		if _, dup := seen[addr.Bytes20()]; dup {
			return nil, fmt.Errorf("qichannel: address %s reused within the transaction", addr)
		}
		seen[addr.Bytes20()] = struct{}{}
		txOut = append(txOut, types.TxOut{Denomination: note, Address: addr.Bytes()})
	}
	return &types.QiTx{
		ChainID: c.ChainID,
		TxIn: []types.TxIn{{
			PreviousOutPoint: out.OutPoint,
			PubKey:           out.AggregateKey.SerializeCompressed(),
		}},
		TxOut: txOut,
		Data:  data,
	}, nil
}

// ClaimWithSecret completes an adaptor-signed claim transaction with the
// payment secret, producing a broadcastable transaction.
func ClaimWithSecret(tx *types.QiTx, sig *adaptor.Signature, secret *PaymentSecret) (*types.Transaction, error) {
	final, err := sig.Adapt(secret)
	if err != nil {
		return nil, err
	}
	claimed := *tx
	claimed.Signature = final
	return types.NewTx(&claimed), nil
}

// SecretFromClaim recovers the payment secret from a claim transaction that
// the payee has published, given the adaptor signature it was completed from.
// A routing node calls this on the downstream settlement to learn what it
// needs to settle upstream.
func SecretFromClaim(sig *adaptor.Signature, claimed *types.Transaction) (*PaymentSecret, error) {
	if claimed.GetSchnorrSignature() == nil {
		return nil, errors.New("qichannel: claim transaction is unsigned")
	}
	return sig.Extract(claimed.GetSchnorrSignature())
}
