package gcs

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// Reference vectors for SipHash-2-4 with key 000102...0f from the SipHash
// paper (little-endian interpretation of the 8-byte outputs).
func TestSipHash24Vectors(t *testing.T) {
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}
	k0 := binary.LittleEndian.Uint64(key[0:8])
	k1 := binary.LittleEndian.Uint64(key[8:16])

	vectors := []struct {
		inputLen int
		want     uint64
	}{
		{0, 0x726fdb47dd0e0e31},
		{1, 0x74f839c593dc67fd},
		{2, 0x0d6c8009d9a94f5a},
		{3, 0x85676696d7fb7e2d},
		{8, 0x93f5f5799a932462},
	}
	for _, vec := range vectors {
		input := make([]byte, vec.inputLen)
		for i := range input {
			input[i] = byte(i)
		}
		if got := sipHash24(k0, k1, input); got != vec.want {
			t.Errorf("sipHash24(len %d) = %#016x, want %#016x", vec.inputLen, got, vec.want)
		}
	}
}

func randomAddresses(rng *rand.Rand, count int) [][]byte {
	items := make([][]byte, count)
	for i := range items {
		addr := make([]byte, 20)
		rng.Read(addr)
		items[i] = addr
	}
	return items
}

func TestBuildAndMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var key [16]byte
	rng.Read(key[:])

	members := randomAddresses(rng, 500)
	filter, err := BuildFilter(key, members)
	if err != nil {
		t.Fatal(err)
	}
	n, err := N(filter)
	if err != nil {
		t.Fatal(err)
	}
	if n != 500 {
		t.Fatalf("N = %d, want 500", n)
	}

	// Every member must match, individually and in batches.
	for _, member := range members {
		ok, err := MatchAny(key, filter, [][]byte{member})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("member %x did not match", member)
		}
	}
	ok, err := MatchAny(key, filter, members)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("batch of members did not match")
	}

	// Non-members should essentially never match: 2000 queries at ~1/784931
	// false positive rate.
	falsePositives := 0
	for i := 0; i < 2000; i++ {
		ok, err := MatchAny(key, filter, randomAddresses(rng, 1))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			falsePositives++
		}
	}
	if falsePositives > 2 {
		t.Fatalf("false positive rate too high: %d/2000", falsePositives)
	}
}

func TestOrderAndDuplicateIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var key [16]byte
	rng.Read(key[:])

	items := randomAddresses(rng, 100)
	filter1, err := BuildFilter(key, items)
	if err != nil {
		t.Fatal(err)
	}

	shuffled := make([][]byte, len(items))
	copy(shuffled, items)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	withDuplicates := append(shuffled, items[:10]...)
	filter2, err := BuildFilter(key, withDuplicates)
	if err != nil {
		t.Fatal(err)
	}

	if string(filter1) != string(filter2) {
		t.Fatal("filters differ across item order / duplicates")
	}
}

func TestEmptyFilter(t *testing.T) {
	var key [16]byte
	filter, err := BuildFilter(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := N(filter)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("N = %d, want 0", n)
	}
	ok, err := MatchAny(key, filter, randomAddresses(rand.New(rand.NewSource(3)), 5))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("empty filter matched")
	}
}

func TestMalformedFilter(t *testing.T) {
	var key [16]byte
	targets := [][]byte{{1, 2, 3}}
	if _, err := MatchAny(key, nil, targets); err == nil {
		t.Fatal("expected error for empty input")
	}
	// Claims 1000 items but has no bitstream.
	truncated := binary.AppendUvarint(nil, 1000)
	if _, err := MatchAny(key, truncated, targets); err == nil {
		t.Fatal("expected error for truncated filter")
	}
}
