// Package qichannel implements payment channels over the Qi UTXO ledger.
//
// A channel is a single funding UTXO paying to the hash of a 2-of-2 MuSig2
// aggregate key. Because consensus verifies one aggregate Schnorr signature
// over all input public keys, that address is indistinguishable on chain from
// an ordinary single-key wallet address: opens, updates and closes all look
// like plain payments.
//
// # State ordering
//
// Both parties hold a chain of pre-signed settlement transactions, all
// spending the same funding output, each carrying a transaction-level
// locktime that decreases as the state index grows. The newest state
// therefore matures first, so an honest party can always publish it before
// any stale state its counterparty holds becomes valid. This is the
// decrementing-timelock construction; it needs no revocation secrets, no
// penalty transactions and no watchtowers, at the cost of a bounded channel
// lifetime and a defined window in which a party must act.
//
// State i matures at OpenHeight + (MaxStates-i)*SettlementDelta, so:
//
//   - the watch window for state i is [maturity(i), maturity(i-1)), one
//     SettlementDelta wide: a party closing unilaterally must publish within
//     it, or a stale state also becomes publishable and the outcome is a
//     race;
//   - the channel supports MaxStates updates and lives for
//     MaxStates*SettlementDelta blocks.
//
// Cooperative closes carry no locktime and confirm immediately, so a
// well-behaved channel never waits.
//
// # What this does not provide
//
// Perpetual (penalty-based) channels are not constructible on Qi: punishing a
// cheater requires two spending paths from one output with different timing,
// which needs a script system. Every construction here is time-bounded and
// must be settled or rolled over before expiry.
package qichannel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto/adaptor"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/qiwallet"
)

var (
	ErrChannelExhausted = errors.New("qichannel: no channel states remain; close or roll over")
	ErrBalanceMismatch  = errors.New("qichannel: balances do not sum to the channel capacity less fees")
	ErrNotEnoughKeys    = errors.New("qichannel: a channel needs exactly two participants")
)

// Params configures the timing and capacity of a channel.
type Params struct {
	ChainID  *big.Int
	Location common.Location

	// SettlementDelta is the number of blocks between successive states'
	// maturity heights, and therefore the window in which a party must
	// publish a unilateral close. Larger values are safer for parties that
	// may be briefly offline; smaller values allow more updates within a
	// given lifetime.
	SettlementDelta uint64

	// MaxStates bounds the number of channel updates. The channel's total
	// lifetime is MaxStates*SettlementDelta blocks from the opening height.
	MaxStates uint64
}

// DefaultParams returns a conservative configuration: a one-day window to
// publish a unilateral close, and a lifetime of about one year.
func DefaultParams(chainID *big.Int, location common.Location) Params {
	return Params{
		ChainID:         chainID,
		Location:        location,
		SettlementDelta: params.BlocksPerDay,
		MaxStates:       365,
	}
}

// Validate checks that the parameters describe a usable channel.
func (p Params) Validate() error {
	if p.ChainID == nil {
		return errors.New("qichannel: chain ID is required")
	}
	if p.SettlementDelta == 0 {
		return errors.New("qichannel: settlement delta must be positive")
	}
	if p.MaxStates == 0 {
		return errors.New("qichannel: max states must be positive")
	}
	return nil
}

// Lifetime returns the number of blocks a channel remains usable for.
func (p Params) Lifetime() uint64 { return p.MaxStates * p.SettlementDelta }

// Channel is one party's view of a payment channel.
type Channel struct {
	Params

	// LocalKey is this party's funding key; RemotePub is the counterparty's.
	LocalKey  *btcec.PrivateKey
	RemotePub *btcec.PublicKey

	// aggKey is the 2-of-2 MuSig2 aggregate the funding output pays to.
	aggKey *btcec.PublicKey

	FundingAddress  common.Address
	FundingOutPoint types.OutPoint
	// Capacity is the value of the funding UTXO in qits.
	Capacity *big.Int
	// OpenHeight anchors the maturity schedule.
	OpenHeight uint64

	// StateIndex is the number of updates applied; 0 is the initial state.
	StateIndex uint64
	// LocalBalance and RemoteBalance are in qits and must sum to Capacity
	// less the settlement fee and any in-flight PTLC amounts.
	LocalBalance  *big.Int
	RemoteBalance *big.Int

	// PTLCs are payments currently in flight through this channel.
	PTLCs []*PTLC
}

// FundingKeys returns the participants' public keys in the canonical order
// used for key aggregation: the aggregate is order sensitive, so both parties
// must agree. The keys are ordered lexicographically by compressed encoding.
func FundingKeys(a, b *btcec.PublicKey) []*btcec.PublicKey {
	aBytes := a.SerializeCompressed()
	bBytes := b.SerializeCompressed()
	for i := range aBytes {
		if aBytes[i] != bBytes[i] {
			if aBytes[i] < bBytes[i] {
				return []*btcec.PublicKey{a, b}
			}
			return []*btcec.PublicKey{b, a}
		}
	}
	return []*btcec.PublicKey{a, b}
}

// FundingAddressFor returns the Qi address a 2-of-2 channel between the two
// keys is funded to, and whether it is usable in the given zone. Because the
// aggregate key is determined by the participants' keys, one party must grind
// its key until the aggregate lands in the zone's Qi scope; see
// GrindFundingKey.
func FundingAddressFor(a, b *btcec.PublicKey, location common.Location) (common.Address, bool, error) {
	aggKey, err := adaptor.AggregateKeys(FundingKeys(a, b))
	if err != nil {
		return common.Address{}, false, err
	}
	addr := qiwallet.QiAddressFromKey(aggKey, location)
	return addr, qiwallet.InQiScope(addr, location), nil
}

// GrindFundingKey derives fresh local keys until the 2-of-2 aggregate with
// the counterparty lands in the zone's Qi address scope, which takes about
// 512 attempts. Only one party needs to grind.
func GrindFundingKey(remotePub *btcec.PublicKey, location common.Location) (*btcec.PrivateKey, common.Address, error) {
	const maxAttempts = 65536
	for i := 0; i < maxAttempts; i++ {
		key, err := btcec.NewPrivateKey()
		if err != nil {
			return nil, common.Address{}, err
		}
		addr, ok, err := FundingAddressFor(key.PubKey(), remotePub, location)
		if err != nil {
			return nil, common.Address{}, err
		}
		if ok {
			return key, addr, nil
		}
	}
	return nil, common.Address{}, errors.New("qichannel: no funding key found in scope")
}

// New builds a channel view for one party. The funding UTXO must already pay
// to the aggregate address, which callers obtain from GrindFundingKey.
func New(p Params, localKey *btcec.PrivateKey, remotePub *btcec.PublicKey,
	funding types.OutPoint, capacity *big.Int, openHeight uint64,
	localBalance, remoteBalance *big.Int) (*Channel, error) {

	if err := p.Validate(); err != nil {
		return nil, err
	}
	if localKey == nil || remotePub == nil {
		return nil, ErrNotEnoughKeys
	}
	aggKey, err := adaptor.AggregateKeys(FundingKeys(localKey.PubKey(), remotePub))
	if err != nil {
		return nil, err
	}
	addr := qiwallet.QiAddressFromKey(aggKey, p.Location)
	if !qiwallet.InQiScope(addr, p.Location) {
		return nil, fmt.Errorf("qichannel: aggregate address %s is not in Qi scope for %v", addr, p.Location)
	}
	return &Channel{
		Params:          p,
		LocalKey:        localKey,
		RemotePub:       remotePub,
		aggKey:          aggKey,
		FundingAddress:  addr,
		FundingOutPoint: funding,
		Capacity:        new(big.Int).Set(capacity),
		OpenHeight:      openHeight,
		LocalBalance:    new(big.Int).Set(localBalance),
		RemoteBalance:   new(big.Int).Set(remoteBalance),
	}, nil
}

// AggregateKey returns the 2-of-2 key the funding output is addressed to.
func (c *Channel) AggregateKey() *btcec.PublicKey { return c.aggKey }

// MaturityHeight returns the height at which the settlement transaction for
// a state becomes valid. Later states mature earlier, so the newest state can
// always be published before any stale one.
func (c *Channel) MaturityHeight(state uint64) uint64 {
	if state >= c.MaxStates {
		state = c.MaxStates - 1
	}
	return c.OpenHeight + (c.MaxStates-state)*c.SettlementDelta
}

// WatchWindow returns the height range in which a unilateral close of the
// given state must be published: from its own maturity until the previous
// state matures and becomes publishable too. For the initial state there is
// no earlier state, so the window is open ended.
func (c *Channel) WatchWindow(state uint64) (from uint64, to uint64) {
	from = c.MaturityHeight(state)
	if state == 0 {
		return from, 0
	}
	return from, c.MaturityHeight(state - 1)
}

// ExpiryHeight is the height by which the channel must be closed or rolled
// over: past it, no further state can be published inside its own window.
func (c *Channel) ExpiryHeight() uint64 { return c.OpenHeight + c.Lifetime() }

// RemainingUpdates reports how many further updates the channel supports.
func (c *Channel) RemainingUpdates() uint64 {
	if c.StateIndex+1 >= c.MaxStates {
		return 0
	}
	return c.MaxStates - 1 - c.StateIndex
}

// Balances describes how a settlement splits the channel.
type Balances struct {
	// LocalNotes and RemoteNotes are the denominations paid to each party,
	// and Addrs the fresh addresses receiving them (one per note).
	LocalNotes  []uint8
	LocalAddrs  []common.Address
	RemoteNotes []uint8
	RemoteAddrs []common.Address
}

// EncodeLockTime renders an absolute height as the Qi transaction Data field
// that carries a transaction-level locktime.
func EncodeLockTime(height uint64) []byte {
	data := make([]byte, params.QiTxLockTimeDataLength)
	binary.BigEndian.PutUint64(data, height)
	return data
}

// BuildSettlement constructs the unsigned settlement transaction for a state:
// a single-input spend of the funding output paying out the balances, with a
// locktime set from the state's maturity height. The fee is the difference
// between the channel capacity and the paid-out notes.
//
// Both parties build this identically and sign it with SignSettlement; each
// keeps the fully signed transaction as its unilateral exit for that state.
func (c *Channel) BuildSettlement(state uint64, balances Balances) (*types.QiTx, error) {
	if state >= c.MaxStates {
		return nil, ErrChannelExhausted
	}
	return c.buildSpend(balances, EncodeLockTime(c.MaturityHeight(state)))
}

// BuildCooperativeClose constructs a settlement with no locktime, which
// confirms as soon as it is mined. This is the normal way to close: both
// parties are online and agree on the final split, so no delay is needed and
// the transaction is indistinguishable from an ordinary payment.
func (c *Channel) BuildCooperativeClose(balances Balances) (*types.QiTx, error) {
	return c.buildSpend(balances, nil)
}

func (c *Channel) buildSpend(balances Balances, data []byte) (*types.QiTx, error) {
	if len(balances.LocalNotes) != len(balances.LocalAddrs) {
		return nil, fmt.Errorf("qichannel: %d local notes but %d addresses", len(balances.LocalNotes), len(balances.LocalAddrs))
	}
	if len(balances.RemoteNotes) != len(balances.RemoteAddrs) {
		return nil, fmt.Errorf("qichannel: %d remote notes but %d addresses", len(balances.RemoteNotes), len(balances.RemoteAddrs))
	}
	paid := new(big.Int).Add(qiwallet.NotesValue(balances.LocalNotes), qiwallet.NotesValue(balances.RemoteNotes))
	if paid.Cmp(c.Capacity) > 0 {
		return nil, ErrBalanceMismatch
	}

	seen := map[common.AddressBytes]struct{}{c.FundingAddress.Bytes20(): {}}
	txOut := make([]types.TxOut, 0, len(balances.LocalNotes)+len(balances.RemoteNotes))
	appendOutputs := func(notes []uint8, addrs []common.Address) error {
		for i, note := range notes {
			addr := addrs[i]
			if !qiwallet.InQiScope(addr, c.Location) {
				return fmt.Errorf("qichannel: settlement address %s is not in Qi scope", addr)
			}
			if _, dup := seen[addr.Bytes20()]; dup {
				return fmt.Errorf("qichannel: address %s reused within the settlement", addr)
			}
			seen[addr.Bytes20()] = struct{}{}
			txOut = append(txOut, types.TxOut{Denomination: note, Address: addr.Bytes()})
		}
		return nil
	}
	if err := appendOutputs(balances.LocalNotes, balances.LocalAddrs); err != nil {
		return nil, err
	}
	if err := appendOutputs(balances.RemoteNotes, balances.RemoteAddrs); err != nil {
		return nil, err
	}

	return &types.QiTx{
		ChainID: c.ChainID,
		TxIn: []types.TxIn{{
			PreviousOutPoint: c.FundingOutPoint,
			PubKey:           c.aggKey.SerializeCompressed(),
		}},
		TxOut: txOut,
		Data:  data,
	}, nil
}

// Update advances the channel to the next state with a new balance split.
// The caller is responsible for exchanging signatures on the new settlement
// before treating the update as complete: until both parties hold a signed
// settlement for state N+1, state N remains the enforceable one.
func (c *Channel) Update(localBalance, remoteBalance *big.Int) error {
	if c.RemainingUpdates() == 0 {
		return ErrChannelExhausted
	}
	total := new(big.Int).Add(localBalance, remoteBalance)
	for _, ptlc := range c.PTLCs {
		total.Add(total, ptlc.Amount)
	}
	if total.Cmp(c.Capacity) > 0 {
		return ErrBalanceMismatch
	}
	c.StateIndex++
	c.LocalBalance = new(big.Int).Set(localBalance)
	c.RemoteBalance = new(big.Int).Set(remoteBalance)
	return nil
}

// SigningSession drives the two-round MuSig2 protocol both parties run to
// sign a settlement (or any other channel transaction). Each party creates a
// session, exchanges public nonces, produces a partial signature, and
// combines the two into the single aggregate signature consensus verifies.
type SigningSession struct {
	channel  *Channel
	nonces   *musig2.Nonces
	pubKeys  []*btcec.PublicKey
	digest   [32]byte
	combined [musig2.PubNonceSize]byte
	haveAgg  bool
}

// NewSigningSession starts a signing session for a transaction. Every session
// generates a fresh nonce, which must never be reused across transactions or
// across attempts at the same transaction.
func (c *Channel) NewSigningSession(tx *types.QiTx, signer types.Signer) (*SigningSession, error) {
	nonces, err := musig2.GenNonces(musig2.WithPublicKey(c.LocalKey.PubKey()))
	if err != nil {
		return nil, err
	}
	digest := signer.Hash(types.NewTx(tx))
	var msg [32]byte
	copy(msg[:], digest[:])
	return &SigningSession{
		channel: c,
		nonces:  nonces,
		pubKeys: FundingKeys(c.LocalKey.PubKey(), c.RemotePub),
		digest:  msg,
	}, nil
}

// PublicNonce returns the nonce to send to the counterparty.
func (s *SigningSession) PublicNonce() [musig2.PubNonceSize]byte { return s.nonces.PubNonce }

// RegisterRemoteNonce aggregates the counterparty's nonce with the local one.
func (s *SigningSession) RegisterRemoteNonce(remote [musig2.PubNonceSize]byte) error {
	combined, err := musig2.AggregateNonces([][musig2.PubNonceSize]byte{s.nonces.PubNonce, remote})
	if err != nil {
		return err
	}
	s.combined = combined
	s.haveAgg = true
	return nil
}

// Sign produces this party's partial signature.
func (s *SigningSession) Sign() (*musig2.PartialSignature, error) {
	if !s.haveAgg {
		return nil, errors.New("qichannel: remote nonce not registered")
	}
	return musig2.Sign(s.nonces.SecNonce, s.channel.LocalKey, s.combined, s.pubKeys, s.digest)
}

// SignAdaptor produces this party's partial signature locked to an adaptor
// point, for a PTLC claim that may only be completed by revealing the
// payment secret.
func (s *SigningSession) SignAdaptor(point *btcec.PublicKey) (*musig2.PartialSignature, error) {
	if !s.haveAgg {
		return nil, errors.New("qichannel: remote nonce not registered")
	}
	return adaptor.PreSignPartial(s.nonces.SecNonce, s.channel.LocalKey, s.combined, s.pubKeys, s.digest, point)
}

// Combine aggregates both partial signatures into the final signature and
// attaches it to the transaction.
func (s *SigningSession) Combine(tx *types.QiTx, partials ...*musig2.PartialSignature) (*types.Transaction, error) {
	if len(partials) == 0 {
		return nil, errors.New("qichannel: no partial signatures")
	}
	sum := new(btcec.ModNScalar)
	for _, partial := range partials {
		if partial == nil || partial.S == nil {
			return nil, errors.New("qichannel: nil partial signature")
		}
		sum.Add(partial.S)
	}
	var rX btcec.FieldVal
	rX.SetByteSlice(schnorr.SerializePubKey(partials[0].R))
	sig := schnorr.NewSignature(&rX, sum)
	if !sig.Verify(s.digest[:], s.channel.aggKey) {
		return nil, errors.New("qichannel: combined signature does not verify under the aggregate key")
	}
	signed := *tx
	signed.Signature = sig
	return types.NewTx(&signed), nil
}

// CombineAdaptor aggregates partial adaptor signatures into a single adaptor
// signature over the channel's aggregate key.
func (s *SigningSession) CombineAdaptor(point *btcec.PublicKey, partials ...*musig2.PartialSignature) (*adaptor.Signature, error) {
	return adaptor.CombinePartials(partials, s.pubKeys, s.digest, point)
}

// Digest returns the signing digest for the session's transaction.
func (s *SigningSession) Digest() [32]byte { return s.digest }
