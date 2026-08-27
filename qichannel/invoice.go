package qichannel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/common/hexutil"
)

// Ledger identifies which ledger an invoice is denominated on. The payment
// layer is ledger agnostic: a route may traverse Qi channels and Quai
// contract-channels interchangeably, because atomicity comes from the
// payment point rather than from anything ledger specific.
type Ledger uint8

const (
	LedgerQi Ledger = iota
	LedgerQuai
)

func (l Ledger) String() string {
	switch l {
	case LedgerQi:
		return "qi"
	case LedgerQuai:
		return "quai"
	default:
		return "unknown"
	}
}

func parseLedger(s string) (Ledger, error) {
	switch s {
	case "qi":
		return LedgerQi, nil
	case "quai":
		return LedgerQuai, nil
	default:
		return 0, fmt.Errorf("qichannel: unknown ledger %q", s)
	}
}

// Invoice is a request for payment. The payee generates a payment secret,
// keeps it, and publishes the corresponding point: any route that ends in a
// PTLC locked to this point pays this invoice, and settling it necessarily
// reveals the secret back up the route.
//
// The invoice carries no payment hash, unlike Lightning: the point IS the
// identifier, and because each hop blinds it differently, the point never
// appears on the wire between intermediaries.
type Invoice struct {
	// PaymentPoint is T = t*G, where t is the secret the payee holds.
	PaymentPoint *btcec.PublicKey
	// Destination is the payee's node key, used for routing and onion
	// construction.
	Destination *btcec.PublicKey
	// Amount is the requested value, in the smallest unit of Ledger (qits
	// for Qi).
	Amount *big.Int
	// Ledger is the ledger the payee wants to be paid on.
	Ledger Ledger
	// ExpiryHeight is the height after which the payee will no longer
	// accept the payment; senders must choose a final PTLC deadline
	// comfortably below it.
	ExpiryHeight uint64
	// Memo is an optional free-form description.
	Memo string
}

// NewInvoice creates an invoice along with the payment secret the payee must
// retain to claim it.
func NewInvoice(destination *btcec.PublicKey, amount *big.Int, ledger Ledger, expiryHeight uint64, memo string) (*Invoice, *PaymentSecret, error) {
	if destination == nil || amount == nil || amount.Sign() <= 0 {
		return nil, nil, errors.New("qichannel: destination and a positive amount are required")
	}
	secret, point, err := NewPayment()
	if err != nil {
		return nil, nil, err
	}
	return &Invoice{
		PaymentPoint: point,
		Destination:  destination,
		Amount:       amount,
		Ledger:       ledger,
		ExpiryHeight: expiryHeight,
		Memo:         memo,
	}, secret, nil
}

// Validate checks that an invoice is complete and not already expired at the
// given height.
func (inv *Invoice) Validate(currentHeight uint64) error {
	if inv == nil || inv.PaymentPoint == nil || inv.Destination == nil {
		return errors.New("qichannel: incomplete invoice")
	}
	if inv.Amount == nil || inv.Amount.Sign() <= 0 {
		return errors.New("qichannel: invoice amount must be positive")
	}
	if inv.ExpiryHeight != 0 && currentHeight >= inv.ExpiryHeight {
		return fmt.Errorf("qichannel: invoice expired at height %d", inv.ExpiryHeight)
	}
	return nil
}

// Encode renders an invoice as a compact, self-describing string:
//
//	qichan:<ledger>:<point>:<destination>:<amount>:<expiry>[:<memo>]
//
// Points and keys are hex-encoded compressed public keys, and amount and
// expiry are hex quantities. This is deliberately simple; a production
// deployment would want a checksummed, human-readable encoding.
func (inv *Invoice) Encode() (string, error) {
	if err := inv.Validate(0); err != nil {
		return "", err
	}
	if strings.ContainsRune(inv.Memo, ':') {
		return "", errors.New("qichannel: memo may not contain a colon")
	}
	var expiry [8]byte
	binary.BigEndian.PutUint64(expiry[:], inv.ExpiryHeight)
	parts := []string{
		"qichan",
		inv.Ledger.String(),
		hexutil.Encode(inv.PaymentPoint.SerializeCompressed()),
		hexutil.Encode(inv.Destination.SerializeCompressed()),
		hexutil.Encode(inv.Amount.Bytes()),
		hexutil.Encode(expiry[:]),
	}
	if inv.Memo != "" {
		parts = append(parts, inv.Memo)
	}
	return strings.Join(parts, ":"), nil
}

// DecodeInvoice parses an encoded invoice.
func DecodeInvoice(encoded string) (*Invoice, error) {
	parts := strings.Split(encoded, ":")
	if len(parts) < 6 || len(parts) > 7 || parts[0] != "qichan" {
		return nil, errors.New("qichannel: malformed invoice")
	}
	ledger, err := parseLedger(parts[1])
	if err != nil {
		return nil, err
	}
	pointBytes, err := hexutil.Decode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad payment point: %w", err)
	}
	point, err := btcec.ParsePubKey(pointBytes)
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad payment point: %w", err)
	}
	destBytes, err := hexutil.Decode(parts[3])
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad destination: %w", err)
	}
	destination, err := btcec.ParsePubKey(destBytes)
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad destination: %w", err)
	}
	amountBytes, err := hexutil.Decode(parts[4])
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad amount: %w", err)
	}
	expiryBytes, err := hexutil.Decode(parts[5])
	if err != nil {
		return nil, fmt.Errorf("qichannel: bad expiry: %w", err)
	}
	if len(expiryBytes) != 8 {
		return nil, errors.New("qichannel: bad expiry length")
	}
	inv := &Invoice{
		PaymentPoint: point,
		Destination:  destination,
		Amount:       new(big.Int).SetBytes(amountBytes),
		Ledger:       ledger,
		ExpiryHeight: binary.BigEndian.Uint64(expiryBytes),
	}
	if len(parts) == 7 {
		inv.Memo = parts[6]
	}
	return inv, inv.Validate(0)
}

// RouteFor builds a route that pays this invoice through the given nodes,
// leaving safety margin between the final PTLC deadline and the invoice
// expiry.
func (inv *Invoice) RouteFor(nodes []*btcec.PublicKey, fees []*big.Int, finalDeadline, hopDelta uint64) (*Route, error) {
	if inv.ExpiryHeight != 0 && finalDeadline >= inv.ExpiryHeight {
		return nil, fmt.Errorf("qichannel: final deadline %d is not before the invoice expiry %d", finalDeadline, inv.ExpiryHeight)
	}
	return BuildRoute(inv.PaymentPoint, inv.Amount, nodes, fees, finalDeadline, hopDelta)
}
