// Package sphinx implements Sphinx onion packets for routed payments.
//
// A sender constructs one fixed-size packet addressed to a route. Each hop
// decrypts exactly one layer, learning only its own instructions and the
// identity of the next hop; it cannot tell how far along the route it sits,
// how many hops remain, or who the sender and final recipient are. Every hop
// re-emits a packet of the same size, so position cannot be inferred from
// length either.
//
// The construction follows the same design as Lightning's BOLT-4: per-hop
// shared secrets from ECDH against an ephemeral key that is re-blinded at
// each hop, ChaCha20 as the stream cipher, HMAC-SHA256 for per-hop
// integrity, and a deterministic filler so that peeling a layer leaves a
// packet indistinguishable from a freshly constructed one.
//
// # Payment-point routing
//
// Each hop's instructions carry a point delta rather than a payment hash.
// A hop learns the point of its incoming PTLC from the payment itself, and
// derives the point for its outgoing PTLC by adding the delta. This is what
// lets every hop of a route be locked to a different point, so two colluding
// intermediaries cannot tell they are forwarding the same payment - a
// correlation that Lightning's shared payment hash allows.
package sphinx

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/chacha20"
)

const (
	// Version is the packet format version.
	Version byte = 0

	// MaxHops is the greatest number of hops a route may contain.
	MaxHops = 20

	// HopDataSize is the fixed size of one hop's instructions.
	HopDataSize = 81

	// hmacSize is the size of a per-hop HMAC tag.
	hmacSize = 32

	// frameSize is the space one hop occupies in the routing information:
	// its instructions plus the HMAC covering the remainder of the onion.
	frameSize = HopDataSize + hmacSize

	// routingInfoSize is the fixed size of the routing information. It is
	// independent of the actual hop count, which is what hides route length.
	routingInfoSize = MaxHops * frameSize

	// PacketSize is the total size of an onion packet on the wire.
	PacketSize = 1 + 33 + routingInfoSize + hmacSize

	// pubKeySize is the size of a compressed public key.
	pubKeySize = 33
)

var (
	ErrInvalidVersion = errors.New("sphinx: unsupported packet version")
	ErrBadHMAC        = errors.New("sphinx: HMAC mismatch, packet was tampered with or is not for this node")
	ErrTooManyHops    = errors.New("sphinx: route exceeds the maximum hop count")
	ErrEmptyRoute     = errors.New("sphinx: route has no hops")
	ErrBadPacketSize  = errors.New("sphinx: packet has the wrong size")

	keyTypeRho = []byte("rho")
	keyTypeMu  = []byte("mu")
)

// HopData is the instruction set one hop receives.
type HopData struct {
	// Amount is the value this hop should forward, in the smallest unit of
	// the hop's ledger.
	Amount uint64

	// Deadline is the PTLC deadline this hop should set on its outgoing
	// payment.
	Deadline uint64

	// NextHop identifies the node to forward to, as a compressed public
	// key. It is all zeroes at the final hop.
	NextHop [pubKeySize]byte

	// PointDelta is the scalar to add to the incoming payment point to
	// obtain the outgoing one, so that each hop is locked to a distinct
	// point. It is unused at the final hop.
	PointDelta [32]byte
}

// IsFinalHop reports whether this hop is the payment's destination.
func (h *HopData) IsFinalHop() bool {
	return h.NextHop == [pubKeySize]byte{}
}

func (h *HopData) encode() []byte {
	buf := make([]byte, HopDataSize)
	binary.BigEndian.PutUint64(buf[0:8], h.Amount)
	binary.BigEndian.PutUint64(buf[8:16], h.Deadline)
	copy(buf[16:16+pubKeySize], h.NextHop[:])
	copy(buf[16+pubKeySize:], h.PointDelta[:])
	return buf
}

func decodeHopData(buf []byte) (*HopData, error) {
	if len(buf) != HopDataSize {
		return nil, fmt.Errorf("sphinx: hop data is %d bytes, want %d", len(buf), HopDataSize)
	}
	hop := &HopData{
		Amount:   binary.BigEndian.Uint64(buf[0:8]),
		Deadline: binary.BigEndian.Uint64(buf[8:16]),
	}
	copy(hop.NextHop[:], buf[16:16+pubKeySize])
	copy(hop.PointDelta[:], buf[16+pubKeySize:])
	return hop, nil
}

// Packet is a fixed-size onion packet.
type Packet struct {
	Version      byte
	EphemeralKey *btcec.PublicKey
	RoutingInfo  [routingInfoSize]byte
	HMAC         [hmacSize]byte
}

// Serialize renders the packet for the wire. Every packet is exactly
// PacketSize bytes, whatever the route length.
func (p *Packet) Serialize() []byte {
	out := make([]byte, 0, PacketSize)
	out = append(out, p.Version)
	out = append(out, p.EphemeralKey.SerializeCompressed()...)
	out = append(out, p.RoutingInfo[:]...)
	out = append(out, p.HMAC[:]...)
	return out
}

// ParsePacket decodes a packet from the wire.
func ParsePacket(data []byte) (*Packet, error) {
	if len(data) != PacketSize {
		return nil, ErrBadPacketSize
	}
	if data[0] != Version {
		return nil, ErrInvalidVersion
	}
	ephemeral, err := btcec.ParsePubKey(data[1 : 1+pubKeySize])
	if err != nil {
		return nil, fmt.Errorf("sphinx: bad ephemeral key: %w", err)
	}
	p := &Packet{Version: data[0], EphemeralKey: ephemeral}
	copy(p.RoutingInfo[:], data[1+pubKeySize:1+pubKeySize+routingInfoSize])
	copy(p.HMAC[:], data[1+pubKeySize+routingInfoSize:])
	return p, nil
}

// sharedSecret computes the ECDH shared secret between a private key and a
// public key, hashed to a uniform 32 bytes.
func sharedSecret(priv *btcec.ModNScalar, pub *btcec.PublicKey) [32]byte {
	var pubJ, product btcec.JacobianPoint
	pub.AsJacobian(&pubJ)
	btcec.ScalarMultNonConst(priv, &pubJ, &product)
	product.ToAffine()
	point := btcec.NewPublicKey(&product.X, &product.Y)
	return sha256.Sum256(point.SerializeCompressed())
}

// blindingFactor derives the scalar that re-blinds the ephemeral key between
// hops, binding it to both the current ephemeral key and the shared secret.
func blindingFactor(ephemeral *btcec.PublicKey, secret [32]byte) btcec.ModNScalar {
	h := sha256.New()
	h.Write(ephemeral.SerializeCompressed())
	h.Write(secret[:])
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	var factor btcec.ModNScalar
	factor.SetBytes(&digest)
	return factor
}

// deriveKey derives a purpose-specific key from a shared secret.
func deriveKey(keyType []byte, secret [32]byte) [32]byte {
	mac := hmac.New(sha256.New, keyType)
	mac.Write(secret[:])
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return key
}

// stream produces a keystream of the requested length. ChaCha20 is used with
// a zero nonce because every invocation uses a distinct, single-use key
// derived from a fresh shared secret.
func stream(key [32]byte, length int) ([]byte, error) {
	nonce := make([]byte, chacha20.NonceSize)
	cipher, err := chacha20.NewUnauthenticatedCipher(key[:], nonce)
	if err != nil {
		return nil, err
	}
	out := make([]byte, length)
	cipher.XORKeyStream(out, out)
	return out, nil
}

func xorInto(dst, src []byte) {
	for i := range dst {
		if i >= len(src) {
			return
		}
		dst[i] ^= src[i]
	}
}

// computeSharedSecrets walks the route, deriving each hop's shared secret and
// re-blinding the ephemeral key as it goes. The returned ephemeral keys are
// what each hop sees.
func computeSharedSecrets(sessionKey *btcec.PrivateKey, route []*btcec.PublicKey) ([][32]byte, []*btcec.PublicKey, error) {
	secrets := make([][32]byte, len(route))
	ephemerals := make([]*btcec.PublicKey, len(route))

	var privScalar btcec.ModNScalar
	privScalar.Set(&sessionKey.Key)
	ephemeral := sessionKey.PubKey()

	for i, hop := range route {
		if hop == nil {
			return nil, nil, fmt.Errorf("sphinx: hop %d has no public key", i)
		}
		ephemerals[i] = ephemeral
		secrets[i] = sharedSecret(&privScalar, hop)

		if i == len(route)-1 {
			break
		}
		factor := blindingFactor(ephemeral, secrets[i])
		privScalar.Mul(&factor)
		if privScalar.IsZero() {
			return nil, nil, errors.New("sphinx: degenerate blinding factor")
		}
		var ephJ, blinded btcec.JacobianPoint
		ephemeral.AsJacobian(&ephJ)
		btcec.ScalarMultNonConst(&factor, &ephJ, &blinded)
		blinded.ToAffine()
		ephemeral = btcec.NewPublicKey(&blinded.X, &blinded.Y)
	}
	return secrets, ephemerals, nil
}

// generateFiller produces the deterministic padding that occupies the tail of
// the routing information. It is what makes a peeled packet indistinguishable
// from a freshly constructed one: each hop's decryption extends the
// obfuscated region by exactly one frame, and the filler is precomputed by
// the sender so the HMACs still line up.
func generateFiller(secrets [][32]byte) ([]byte, error) {
	numHops := len(secrets)
	if numHops < 2 {
		return nil, nil
	}
	filler := make([]byte, 0, (numHops-1)*frameSize)
	for i := 0; i < numHops-1; i++ {
		filler = append(filler, make([]byte, frameSize)...)
		keyStream, err := stream(deriveKey(keyTypeRho, secrets[i]), routingInfoSize+frameSize)
		if err != nil {
			return nil, err
		}
		xorInto(filler, keyStream[routingInfoSize+frameSize-len(filler):])
	}
	return filler, nil
}

// Construct builds an onion packet for a route. route lists the hops' node
// public keys in forward order, hops carries each hop's instructions, and
// assocData is authenticated but not encrypted (bind it to the payment so a
// packet cannot be replayed against a different one).
//
// sessionKey must be freshly generated for every packet.
func Construct(sessionKey *btcec.PrivateKey, route []*btcec.PublicKey, hops []*HopData, assocData []byte) (*Packet, error) {
	if len(route) == 0 {
		return nil, ErrEmptyRoute
	}
	if len(route) > MaxHops {
		return nil, ErrTooManyHops
	}
	if len(route) != len(hops) {
		return nil, fmt.Errorf("sphinx: %d hops in route but %d instruction sets", len(route), len(hops))
	}

	secrets, ephemerals, err := computeSharedSecrets(sessionKey, route)
	if err != nil {
		return nil, err
	}
	filler, err := generateFiller(secrets)
	if err != nil {
		return nil, err
	}

	// The initial routing information is a keystream rather than zeroes, so
	// that unused frames are indistinguishable from real ones.
	var routingInfo [routingInfoSize]byte
	padStream, err := stream(deriveKey([]byte("pad"), sha256.Sum256(sessionKey.Serialize())), routingInfoSize)
	if err != nil {
		return nil, err
	}
	copy(routingInfo[:], padStream)

	var nextHMAC [hmacSize]byte
	for i := len(route) - 1; i >= 0; i-- {
		// Shift the existing layers back by one frame and write this hop's
		// instructions, followed by the HMAC covering everything after it.
		copy(routingInfo[frameSize:], routingInfo[:routingInfoSize-frameSize])
		copy(routingInfo[:HopDataSize], hops[i].encode())
		copy(routingInfo[HopDataSize:frameSize], nextHMAC[:])

		keyStream, err := stream(deriveKey(keyTypeRho, secrets[i]), routingInfoSize)
		if err != nil {
			return nil, err
		}
		xorInto(routingInfo[:], keyStream)

		// The last hop's layer is where the filler comes to rest.
		if i == len(route)-1 && len(filler) > 0 {
			copy(routingInfo[routingInfoSize-len(filler):], filler)
		}

		mac := hmac.New(sha256.New, keyBytes(deriveKey(keyTypeMu, secrets[i])))
		mac.Write(routingInfo[:])
		mac.Write(assocData)
		copy(nextHMAC[:], mac.Sum(nil))
	}

	return &Packet{
		Version:      Version,
		EphemeralKey: ephemerals[0],
		RoutingInfo:  routingInfo,
		HMAC:         nextHMAC,
	}, nil
}

func keyBytes(key [32]byte) []byte { return key[:] }

// Peeled is the result of processing one layer of an onion.
type Peeled struct {
	// HopData is this hop's instructions.
	HopData *HopData

	// Next is the packet to forward to the next hop. It is nil when this
	// node is the final recipient.
	Next *Packet

	// SharedSecret is this hop's shared secret, which callers may use to
	// key an error return path.
	SharedSecret [32]byte
}

// Peel removes one layer of encryption. It authenticates the packet first, so
// a packet that was tampered with, replayed against different associated
// data, or simply not addressed to this node is rejected rather than
// misinterpreted.
func Peel(privKey *btcec.PrivateKey, packet *Packet, assocData []byte) (*Peeled, error) {
	if packet == nil || packet.EphemeralKey == nil {
		return nil, errors.New("sphinx: nil packet")
	}
	if packet.Version != Version {
		return nil, ErrInvalidVersion
	}
	var privScalar btcec.ModNScalar
	privScalar.Set(&privKey.Key)
	secret := sharedSecret(&privScalar, packet.EphemeralKey)

	mac := hmac.New(sha256.New, keyBytes(deriveKey(keyTypeMu, secret)))
	mac.Write(packet.RoutingInfo[:])
	mac.Write(assocData)
	if !hmac.Equal(mac.Sum(nil), packet.HMAC[:]) {
		return nil, ErrBadHMAC
	}

	// Extend the routing information by one frame before decrypting, so the
	// keystream reveals both this hop's layer and the filler that replaces
	// it in the forwarded packet.
	extended := make([]byte, routingInfoSize+frameSize)
	copy(extended, packet.RoutingInfo[:])
	keyStream, err := stream(deriveKey(keyTypeRho, secret), routingInfoSize+frameSize)
	if err != nil {
		return nil, err
	}
	xorInto(extended, keyStream)

	hopData, err := decodeHopData(extended[:HopDataSize])
	if err != nil {
		return nil, err
	}
	var nextHMAC [hmacSize]byte
	copy(nextHMAC[:], extended[HopDataSize:frameSize])

	result := &Peeled{HopData: hopData, SharedSecret: secret}
	if hopData.IsFinalHop() {
		// A final hop must carry a zero HMAC; anything else means the packet
		// was malformed.
		if !bytes.Equal(nextHMAC[:], make([]byte, hmacSize)) {
			return nil, errors.New("sphinx: final hop carries a non-zero forward HMAC")
		}
		return result, nil
	}

	nextEphemeral, err := blindEphemeral(packet.EphemeralKey, secret)
	if err != nil {
		return nil, err
	}
	next := &Packet{Version: Version, EphemeralKey: nextEphemeral, HMAC: nextHMAC}
	copy(next.RoutingInfo[:], extended[frameSize:])
	result.Next = next
	return result, nil
}

// blindEphemeral advances the ephemeral key for the next hop.
func blindEphemeral(ephemeral *btcec.PublicKey, secret [32]byte) (*btcec.PublicKey, error) {
	factor := blindingFactor(ephemeral, secret)
	if factor.IsZero() {
		return nil, errors.New("sphinx: degenerate blinding factor")
	}
	var ephJ, blinded btcec.JacobianPoint
	ephemeral.AsJacobian(&ephJ)
	btcec.ScalarMultNonConst(&factor, &ephJ, &blinded)
	blinded.ToAffine()
	return btcec.NewPublicKey(&blinded.X, &blinded.Y), nil
}
