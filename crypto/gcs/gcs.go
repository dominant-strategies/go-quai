// Package gcs implements a Golomb-coded set: a compact probabilistic filter
// following the construction and parameters of BIP-158 compact block filters.
//
// A filter commits to a set of byte strings. A wallet can download the filter
// for a block, test its own addresses against it locally, and fetch only the
// blocks that match, so it never reveals which addresses it is scanning for.
// False positives occur at a rate of roughly 1/M and only cost the wallet an
// unnecessary block download; false negatives cannot occur.
package gcs

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sort"
)

const (
	// DefaultP is the Golomb-Rice coding parameter (BIP-158).
	DefaultP = 19
	// DefaultM is the false positive rate parameter, ~1/M (BIP-158).
	DefaultM = 784931

	// maxItems bounds the number of committed items accepted when decoding a
	// serialized filter, as a defense against maliciously large inputs.
	maxItems = 1 << 24
)

var (
	ErrEmptyFilter     = errors.New("gcs: empty serialized filter")
	ErrMalformedFilter = errors.New("gcs: malformed serialized filter")
)

// BuildFilter constructs a serialized Golomb-coded set over the given items
// using the provided SipHash key. Duplicate and empty items are ignored. The
// serialization is a uvarint item count followed by the Golomb-Rice coded
// bitstream of sorted deltas.
func BuildFilter(key [16]byte, items [][]byte) ([]byte, error) {
	// Deduplicate items as byte strings so the committed count always equals
	// the number of encoded deltas. Colliding mapped values are kept and
	// encode as zero deltas, which decode correctly.
	unique := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		unique[string(item)] = struct{}{}
	}
	n := uint64(len(unique))
	if n > maxItems {
		return nil, ErrMalformedFilter
	}
	if n == 0 {
		return binary.AppendUvarint(nil, 0), nil
	}
	values := make([]uint64, 0, n)
	for item := range unique {
		values = append(values, hashToRange(key, []byte(item), n))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })

	out := binary.AppendUvarint(nil, n)
	w := &bitWriter{bytes: out}
	var last uint64
	for _, v := range values {
		delta := v - last
		last = v
		// Quotient in unary, then P remainder bits.
		for q := delta >> DefaultP; q > 0; q-- {
			w.writeBit(1)
		}
		w.writeBit(0)
		w.writeBits(delta, DefaultP)
	}
	return w.bytes, nil
}

// MatchAny reports whether any of the targets may be committed in the
// serialized filter. A true result may be a false positive at rate ~1/M; a
// false result is definitive.
func MatchAny(key [16]byte, filter []byte, targets [][]byte) (bool, error) {
	if len(filter) == 0 {
		return false, ErrEmptyFilter
	}
	n, offset := binary.Uvarint(filter)
	if offset <= 0 {
		return false, ErrMalformedFilter
	}
	if n == 0 || len(targets) == 0 {
		return false, nil
	}
	if n > maxItems {
		return false, ErrMalformedFilter
	}

	targetValues := make([]uint64, 0, len(targets))
	for _, target := range targets {
		if len(target) == 0 {
			continue
		}
		targetValues = append(targetValues, hashToRange(key, target, n))
	}
	sort.Slice(targetValues, func(i, j int) bool { return targetValues[i] < targetValues[j] })

	r := &bitReader{bytes: filter[offset:]}
	var value uint64
	ti := 0
	for i := uint64(0); i < n; i++ {
		delta, err := r.readDelta()
		if err != nil {
			return false, err
		}
		value += delta
		for ti < len(targetValues) && targetValues[ti] < value {
			ti++
		}
		if ti == len(targetValues) {
			return false, nil
		}
		if targetValues[ti] == value {
			return true, nil
		}
	}
	return false, nil
}

// N returns the number of items committed in a serialized filter.
func N(filter []byte) (uint64, error) {
	if len(filter) == 0 {
		return 0, ErrEmptyFilter
	}
	n, offset := binary.Uvarint(filter)
	if offset <= 0 {
		return 0, ErrMalformedFilter
	}
	return n, nil
}

// hashToRange maps an item uniformly into [0, n*M) via SipHash-2-4 and a
// 128-bit multiply-shift reduction.
func hashToRange(key [16]byte, item []byte, n uint64) uint64 {
	k0 := binary.LittleEndian.Uint64(key[0:8])
	k1 := binary.LittleEndian.Uint64(key[8:16])
	hi, _ := bits.Mul64(sipHash24(k0, k1, item), n*DefaultM)
	return hi
}

type bitWriter struct {
	bytes []byte
	nbits uint // number of bits used in the final byte, 0 means byte-aligned
}

func (w *bitWriter) writeBit(bit uint64) {
	if w.nbits == 0 {
		w.bytes = append(w.bytes, 0)
		w.nbits = 8
	}
	w.nbits--
	if bit != 0 {
		w.bytes[len(w.bytes)-1] |= 1 << w.nbits
	}
}

// writeBits writes the low count bits of v, most significant first.
func (w *bitWriter) writeBits(v uint64, count uint) {
	for i := int(count) - 1; i >= 0; i-- {
		w.writeBit((v >> uint(i)) & 1)
	}
}

type bitReader struct {
	bytes []byte
	pos   uint // bit position
}

func (r *bitReader) readBit() (uint64, error) {
	byteIdx := r.pos >> 3
	if byteIdx >= uint(len(r.bytes)) {
		return 0, ErrMalformedFilter
	}
	bit := uint64(r.bytes[byteIdx]>>(7-(r.pos&7))) & 1
	r.pos++
	return bit, nil
}

func (r *bitReader) readBits(count uint) (uint64, error) {
	var v uint64
	for i := uint(0); i < count; i++ {
		bit, err := r.readBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | bit
	}
	return v, nil
}

// readDelta reads one Golomb-Rice coded value: a unary quotient followed by
// P remainder bits.
func (r *bitReader) readDelta() (uint64, error) {
	var q uint64
	for {
		bit, err := r.readBit()
		if err != nil {
			return 0, err
		}
		if bit == 0 {
			break
		}
		q++
		if q > 1<<(64-DefaultP) {
			return 0, ErrMalformedFilter
		}
	}
	rem, err := r.readBits(DefaultP)
	if err != nil {
		return 0, err
	}
	return q<<DefaultP | rem, nil
}

// sipHash24 computes SipHash-2-4 of data under the key (k0, k1).
func sipHash24(k0, k1 uint64, data []byte) uint64 {
	v0 := k0 ^ 0x736f6d6570736575
	v1 := k1 ^ 0x646f72616e646f6d
	v2 := k0 ^ 0x6c7967656e657261
	v3 := k1 ^ 0x7465646279746573

	b := uint64(len(data)) << 56
	for ; len(data) >= 8; data = data[8:] {
		m := binary.LittleEndian.Uint64(data[:8])
		v3 ^= m
		v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
		v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
		v0 ^= m
	}
	for i, by := range data {
		b |= uint64(by) << (8 * uint(i))
	}
	v3 ^= b
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0 ^= b
	v2 ^= 0xff
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	return v0 ^ v1 ^ v2 ^ v3
}

func sipRound(v0, v1, v2, v3 uint64) (uint64, uint64, uint64, uint64) {
	v0 += v1
	v1 = bits.RotateLeft64(v1, 13)
	v1 ^= v0
	v0 = bits.RotateLeft64(v0, 32)
	v2 += v3
	v3 = bits.RotateLeft64(v3, 16)
	v3 ^= v2
	v0 += v3
	v3 = bits.RotateLeft64(v3, 21)
	v3 ^= v0
	v2 += v1
	v1 = bits.RotateLeft64(v1, 17)
	v1 ^= v2
	v2 = bits.RotateLeft64(v2, 32)
	return v0, v1, v2, v3
}
