package sphinx_test

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/dominant-strategies/go-quai/crypto/sphinx"
)

func newKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// buildRoute creates n hop keys and the matching instructions, with each hop
// pointing at its successor and the final hop marked by a zero NextHop.
func buildRoute(t *testing.T, n int) ([]*btcec.PrivateKey, []*btcec.PublicKey, []*sphinx.HopData) {
	t.Helper()
	keys := make([]*btcec.PrivateKey, n)
	pubs := make([]*btcec.PublicKey, n)
	for i := range keys {
		keys[i] = newKey(t)
		pubs[i] = keys[i].PubKey()
	}
	hops := make([]*sphinx.HopData, n)
	for i := range hops {
		hop := &sphinx.HopData{
			Amount:   uint64(1000 * (n - i)),
			Deadline: uint64(5000 + 144*(n-i)),
		}
		if i < n-1 {
			copy(hop.NextHop[:], pubs[i+1].SerializeCompressed())
		}
		for j := range hop.PointDelta {
			hop.PointDelta[j] = byte(i*7 + j)
		}
		hops[i] = hop
	}
	return keys, pubs, hops
}

// TestRouteRoundTrip walks a packet along routes of every supported length,
// checking each hop recovers exactly its own instructions and that the final
// hop is recognized as the destination.
func TestRouteRoundTrip(t *testing.T) {
	assocData := []byte("payment-point-commitment")
	for n := 1; n <= sphinx.MaxHops; n++ {
		keys, pubs, hops := buildRoute(t, n)
		packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
		if err != nil {
			t.Fatalf("%d hops: %v", n, err)
		}

		current := packet
		for i := 0; i < n; i++ {
			peeled, err := sphinx.Peel(keys[i], current, assocData)
			if err != nil {
				t.Fatalf("%d hops, hop %d: %v", n, i, err)
			}
			if peeled.HopData.Amount != hops[i].Amount || peeled.HopData.Deadline != hops[i].Deadline {
				t.Fatalf("%d hops, hop %d: instructions do not match", n, i)
			}
			if peeled.HopData.NextHop != hops[i].NextHop {
				t.Fatalf("%d hops, hop %d: next hop does not match", n, i)
			}
			if peeled.HopData.PointDelta != hops[i].PointDelta {
				t.Fatalf("%d hops, hop %d: point delta does not match", n, i)
			}

			isLast := i == n-1
			if peeled.HopData.IsFinalHop() != isLast {
				t.Fatalf("%d hops, hop %d: final-hop flag is %v", n, i, peeled.HopData.IsFinalHop())
			}
			if isLast {
				if peeled.Next != nil {
					t.Fatalf("%d hops: final hop produced a forward packet", n)
				}
				break
			}
			if peeled.Next == nil {
				t.Fatalf("%d hops, hop %d: no forward packet", n, i)
			}
			current = peeled.Next
		}
	}
}

// TestPacketSizeConstant is the property that hides route length: every
// packet, at every position along every route, is byte-identical in size.
func TestPacketSizeConstant(t *testing.T) {
	assocData := []byte("assoc")
	for _, n := range []int{1, 2, 5, 12, sphinx.MaxHops} {
		keys, pubs, hops := buildRoute(t, n)
		packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(packet.Serialize()); got != sphinx.PacketSize {
			t.Fatalf("%d hops: packet is %d bytes, want %d", n, got, sphinx.PacketSize)
		}
		current := packet
		for i := 0; i < n-1; i++ {
			peeled, err := sphinx.Peel(keys[i], current, assocData)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(peeled.Next.Serialize()); got != sphinx.PacketSize {
				t.Fatalf("%d hops, after hop %d: packet is %d bytes, want %d", n, i, got, sphinx.PacketSize)
			}
			current = peeled.Next
		}
	}
}

func TestSerializationRoundTrip(t *testing.T) {
	keys, pubs, hops := buildRoute(t, 4)
	assocData := []byte("assoc")
	packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sphinx.ParsePacket(packet.Serialize())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed.Serialize(), packet.Serialize()) {
		t.Fatal("packet did not survive serialization")
	}
	// A parsed packet must still peel correctly.
	if _, err := sphinx.Peel(keys[0], parsed, assocData); err != nil {
		t.Fatal(err)
	}
	if _, err := sphinx.ParsePacket(packet.Serialize()[:sphinx.PacketSize-1]); err == nil {
		t.Fatal("expected a short packet to be rejected")
	}
}

// TestWrongNodeCannotPeel checks that a hop that is not the addressee cannot
// read the layer: authentication fails rather than yielding garbage.
func TestWrongNodeCannotPeel(t *testing.T) {
	keys, pubs, hops := buildRoute(t, 3)
	assocData := []byte("assoc")
	packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	// The second hop cannot peel the outermost layer.
	if _, err := sphinx.Peel(keys[1], packet, assocData); err != sphinx.ErrBadHMAC {
		t.Fatalf("expected an HMAC failure, got %v", err)
	}
	// Neither can an unrelated node.
	if _, err := sphinx.Peel(newKey(t), packet, assocData); err != sphinx.ErrBadHMAC {
		t.Fatalf("expected an HMAC failure, got %v", err)
	}
}

func TestTamperDetection(t *testing.T) {
	keys, pubs, hops := buildRoute(t, 3)
	assocData := []byte("assoc")
	packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	// Flipping a bit anywhere in the routing information must be caught.
	for _, offset := range []int{0, 100, 1000, len(packet.RoutingInfo) - 1} {
		tampered := *packet
		tampered.RoutingInfo[offset] ^= 0x01
		if _, err := sphinx.Peel(keys[0], &tampered, assocData); err != sphinx.ErrBadHMAC {
			t.Fatalf("tampering at offset %d was not detected: %v", offset, err)
		}
	}
	// So must replaying the packet against different associated data, which
	// is what binds an onion to one specific payment.
	if _, err := sphinx.Peel(keys[0], packet, []byte("different-payment")); err != sphinx.ErrBadHMAC {
		t.Fatalf("expected associated-data mismatch to be caught, got %v", err)
	}
}

// TestHopLearnsNothingExtra checks that a hop's forwarded packet does not
// contain its own instructions any more: what it passes on is opaque to it.
func TestHopLearnsNothingExtra(t *testing.T) {
	keys, pubs, hops := buildRoute(t, 4)
	assocData := []byte("assoc")
	packet, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	peeled, err := sphinx.Peel(keys[0], packet, assocData)
	if err != nil {
		t.Fatal(err)
	}
	// No later hop's plaintext instructions should appear in the forwarded
	// packet: everything downstream is still encrypted.
	for i := 1; i < len(hops); i++ {
		encoded := hops[i].NextHop[:]
		if hops[i].IsFinalHop() {
			continue
		}
		if bytes.Contains(peeled.Next.Serialize(), encoded) {
			t.Fatalf("hop %d's next-hop identity is readable in the forwarded packet", i)
		}
	}
	// A hop also cannot peel the packet it forwards.
	if _, err := sphinx.Peel(keys[0], peeled.Next, assocData); err != sphinx.ErrBadHMAC {
		t.Fatal("a hop was able to peel its own forwarded packet")
	}
}

func TestConstructRejectsBadRoutes(t *testing.T) {
	_, pubs, hops := buildRoute(t, 2)
	if _, err := sphinx.Construct(newKey(t), nil, nil, nil); err != sphinx.ErrEmptyRoute {
		t.Fatalf("expected ErrEmptyRoute, got %v", err)
	}
	if _, err := sphinx.Construct(newKey(t), pubs, hops[:1], nil); err == nil {
		t.Fatal("expected a hop-count mismatch to be rejected")
	}
	tooMany := make([]*btcec.PublicKey, sphinx.MaxHops+1)
	tooManyHops := make([]*sphinx.HopData, sphinx.MaxHops+1)
	for i := range tooMany {
		tooMany[i] = newKey(t).PubKey()
		tooManyHops[i] = &sphinx.HopData{}
	}
	if _, err := sphinx.Construct(newKey(t), tooMany, tooManyHops, nil); err != sphinx.ErrTooManyHops {
		t.Fatalf("expected ErrTooManyHops, got %v", err)
	}
}

// TestDistinctSessionsDiffer checks that two packets for the same route are
// unlinkable: a fresh session key must change every byte a hop sees.
func TestDistinctSessionsDiffer(t *testing.T) {
	_, pubs, hops := buildRoute(t, 3)
	assocData := []byte("assoc")
	first, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sphinx.Construct(newKey(t), pubs, hops, assocData)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Serialize(), second.Serialize()) {
		t.Fatal("two sessions produced identical packets")
	}
	if first.EphemeralKey.IsEqual(second.EphemeralKey) {
		t.Fatal("two sessions shared an ephemeral key")
	}
}
