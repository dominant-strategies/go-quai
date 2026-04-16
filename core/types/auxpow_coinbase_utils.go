package types

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/dominant-strategies/go-quai/common"
	ltcblockchain "github.com/dominant-strategies/ltcd/blockchain"
	ltcchainhash "github.com/dominant-strategies/ltcd/chaincfg/chainhash"
	bchblockchain "github.com/gcash/bchd/blockchain"
	bchchainhash "github.com/gcash/bchd/chaincfg/chainhash"
)

// ExtractSealHashFromCoinbase extracts the seal hash from the coinbase scriptSig format.
// Format:
//
//	OP_PUSH<n> <height(variable bytes)> ← BIP34 height (minimal encoding, 0-5 bytes)
//	OP_PUSH44  <fabe6d6d | aux_merkle_root(32 LE) | merkle_size(4 LE) | merkle_nonce(4 LE)>
//	OP_PUSH42  <extraNonce1(4) + extraNonce2(8) + extraData(30)>
//  OP_PUSH4   <signature_time>

func ExtractSealHashFromCoinbase(scriptSig []byte) (common.Hash, error) {
	if len(scriptSig) == 0 {
		return common.Hash{}, errors.New("coinbase scriptSig empty")
	}

	cursor := 0

	// 1. Parse and skip height (variable length - BIP34 minimal encoding)
	heightData, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return common.Hash{}, fmt.Errorf("decode coinbase height: %w", err)
	}
	// Height can be 0-5 bytes (BIP34 allows minimal encoding)
	if len(heightData) > 5 {
		return common.Hash{}, fmt.Errorf("invalid height length: expected 0-5 bytes, got %d", len(heightData))
	}
	cursor += consumed

	// 2. Parse AuxPoW commitment as a single push
	// It must contain exactly: 4-byte magic, 32-byte root, 4-byte size (LE), 4-byte nonce (LE)
	payload, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return common.Hash{}, fmt.Errorf("decode auxpow commitment: %w", err)
	}
	cursor += consumed
	if len(payload) != 44 {
		return common.Hash{}, fmt.Errorf("invalid auxpow commitment length: expected 44, got %d", len(payload))
	}

	expectedMagic := []byte{0xfa, 0xbe, 0x6d, 0x6d}
	if !bytes.Equal(payload[0:4], expectedMagic) {
		return common.Hash{}, fmt.Errorf("invalid magic marker: expected %x, got %x", expectedMagic, payload[0:4])
	}

	sealHashData := payload[4 : 4+common.HashLength]

	return common.BytesToHash(sealHashData), nil
}

func ExtractMerkleSizeAndNonceFromCoinbase(scriptSig []byte) (uint32, uint32, error) {

	if len(scriptSig) == 0 {
		return 0, 0, errors.New("coinbase scriptSig empty")
	}

	cursor := 0

	// 1. Parse and skip height (variable length - BIP34 minimal encoding)
	heightData, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, 0, fmt.Errorf("decode coinbase height: %w", err)
	}
	// Height can be 0-5 bytes (BIP34 allows minimal encoding)
	if len(heightData) > 5 {
		return 0, 0, fmt.Errorf("invalid height length: expected 0-5 bytes, got %d", len(heightData))
	}
	cursor += consumed

	// 2. Parse AuxPoW commitment as a single push
	// It must contain exactly: 4-byte magic, 32-byte root, 4-byte size (LE), 4-byte nonce (LE)
	payload, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, 0, fmt.Errorf("decode auxpow commitment: %w", err)
	}
	cursor += consumed
	if len(payload) != 44 {
		return 0, 0, fmt.Errorf("invalid auxpow commitment length: expected 44, got %d", len(payload))
	}

	expectedMagic := []byte{0xfa, 0xbe, 0x6d, 0x6d}
	if !bytes.Equal(payload[0:4], expectedMagic) {
		return 0, 0, fmt.Errorf("invalid magic marker: expected %x, got %x", expectedMagic, payload[0:4])
	}

	merkleSize := binary.LittleEndian.Uint32(payload[4+common.HashLength : 4+common.HashLength+4])
	merkleNonce := binary.LittleEndian.Uint32(payload[4+common.HashLength+4 : 4+common.HashLength+8])

	return merkleSize, merkleNonce, nil
}

// ExtractSignatureTimeFromCoinbase extracts the signature time from the coinbase scriptSig format.
// Format:
//
//	OP_PUSH<n> <height(variable bytes)> ← BIP34 height (minimal encoding, 0-5 bytes)
//	OP_PUSH44  <fabe6d6d | aux_merkle_root(32 LE) | merkle_size(4 LE) | merkle_nonce(4 LE)>
//	OP_PUSH42  <extraNonce1(4) + extraNonce2(8) + extraData(30)>
//  OP_PUSH4   <signature_time>

func ExtractSignatureTimeFromCoinbase(scriptSig []byte) (uint32, error) {
	if len(scriptSig) == 0 {
		return 0, errors.New("coinbase scriptSig empty")
	}

	cursor := 0

	// 1. Parse and skip height (variable length - BIP34 minimal encoding)
	heightData, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, fmt.Errorf("decode coinbase height: %w", err)
	}
	// Height can be 0-5 bytes (BIP34 allows minimal encoding)
	if len(heightData) > 5 {
		return 0, fmt.Errorf("invalid height length: expected 0-5 bytes, got %d", len(heightData))
	}
	cursor += consumed

	// 2. Parse AuxPoW commitment as a single push and skip it
	payload, consumed, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, fmt.Errorf("decode magic marker: %w", err)
	}
	if len(payload) != 44 {
		return 0, fmt.Errorf("invalid auxpow commitment length: expected 44, got %d", len(payload))
	}
	expectedMagic := []byte{0xfa, 0xbe, 0x6d, 0x6d}
	if !bytes.Equal(payload[0:4], expectedMagic) {
		return 0, fmt.Errorf("invalid magic marker: expected %x, got %x", expectedMagic, payload[0:4])
	}
	cursor += consumed

	// 3. Skip extranonces push (extraNonce1(4) + extraNonce2(8) + extraData(30) = 42 bytes)
	_, consumed, err = parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, fmt.Errorf("decode extranonces: %w", err)
	}
	cursor += consumed

	if cursor >= len(scriptSig) {
		return 0, errors.New("scriptSig too short to contain signature time")
	}

	// 4. Parse signature time (4 bytes)
	signatureTimeData, _, err := parseScriptPush(scriptSig[cursor:])
	if err != nil {
		return 0, fmt.Errorf("decode signature time: %w", err)
	}
	if len(signatureTimeData) != 4 {
		return 0, fmt.Errorf("invalid signature time length: expected 4, got %d", len(signatureTimeData))
	}

	return binary.LittleEndian.Uint32(signatureTimeData), nil
}

func parseScriptPush(script []byte) ([]byte, int, error) {
	if len(script) == 0 {
		return nil, 0, errors.New("empty script segment")
	}

	opcode := script[0]
	read := 1
	var dataLen int

	switch {
	case opcode <= 75:
		dataLen = int(opcode)
	default:
		return nil, 0, fmt.Errorf("unsupported opcode 0x%x in coinbase script", opcode)
	}

	if len(script) < read+dataLen {
		return nil, 0, errors.New("coinbase push exceeds script bounds")
	}

	return script[read : read+dataLen], read + dataLen, nil
}

// BuildCoinbaseScriptSigWithNonce creates a scriptSig for AuxPow coinbase with the Bitcoin standard format
// Format:
//
//		OP_PUSH<n> <height(variable bytes)> ← BIP34 height (MINIMAL encoding)
//		OP_PUSH44  <fabe6d6d | AuxMerkleRoot(32 LE) | merkle_size(4 LE) | merkle_nonce(4 LE)>
//		OP_PUSH42  <extraNonce1(4) + extraNonce2(8) + extraData(30)>
//	    OP_PUSH4   <signature_time> ← time from the aux template
func BuildCoinbaseScriptSigWithNonce(blockHeight uint32, extraNonce1 uint32, extraNonce2 uint64, auxMerkleRoot common.Hash, merkleSize uint32, signatureTime uint32) []byte {
	var buf bytes.Buffer

	// 1. BIP34: Block height (minimal encoding - variable length)
	// Must use minimal/compact encoding for BIP34 compliance
	heightBytes := encodeHeightForBIP34(blockHeight)
	if len(heightBytes) <= 75 {
		buf.WriteByte(byte(len(heightBytes))) // Direct push for <= 75 bytes
	} else if len(heightBytes) <= 255 {
		buf.WriteByte(0x4c) // OP_PUSHDATA1
		buf.WriteByte(byte(len(heightBytes)))
	}
	buf.Write(heightBytes)

	// 2. AuxPoW commitment as a single PUSHDATA with payload:
	//    magic(4) | auxMerkleRoot(32, LE) | merkleSize(4, LE) | merkleNonce(4, LE)
	payload := make([]byte, 0, 44)
	payload = append(payload, 0xfa, 0xbe, 0x6d, 0x6d)
	payload = append(payload, auxMerkleRoot[:]...)
	msBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(msBytes, merkleSize)
	payload = append(payload, msBytes...)
	mnBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(mnBytes, 0)
	payload = append(payload, mnBytes...)

	// Write single push for commitment
	// Length is 44, fits in single-byte push opcode
	buf.WriteByte(0x2c) // OP_PUSH44 (44 decimal = 0x2c hex)
	buf.Write(payload)

	// 3. Combined extra nonces and extraData (42 bytes total, little-endian)
	// Bitcoin standard: extraNonce1 (4 bytes) + extraNonce2 (8 bytes) + extraData (30 bytes) in a single push
	en1 := make([]byte, 4)
	binary.LittleEndian.PutUint32(en1, extraNonce1)
	en2 := make([]byte, 8)
	binary.LittleEndian.PutUint64(en2, extraNonce2)
	extraData := make([]byte, 30)
	buf.WriteByte(0x2a) // OP_PUSH42 (42 decimal = 0x2a hex)
	buf.Write(en1)
	buf.Write(en2)
	// extraData: 30 bytes of zeros for now (can be used for additional miner data)
	buf.Write(extraData)

	// 4. Signature time (4 bytes, little-endian)
	st := make([]byte, 4)
	binary.LittleEndian.PutUint32(st, signatureTime)
	buf.WriteByte(0x04)
	buf.Write(st)

	return buf.Bytes()
}

// VerifyMerkleProof verifies a merkle proof for a transaction at index 0 (coinbase)
// merkleBranch contains the sibling hashes from leaf to root
func CalculateMerkleRoot(powId PowID, coinbaseTx []byte, merkleBranch [][]byte) [common.HashLength]byte {

	switch powId {
	case Kawpow, SHA_BTC:
		// Start with the transaction hash
		currentHash := chainhash.Hash(AuxPowTxHash(powId, coinbaseTx))

		// For coinbase (index 0), we always take the right branch
		// and our hash goes on the left
		for _, siblingBytes := range merkleBranch {
			var sibling chainhash.Hash
			copy(sibling[:], siblingBytes)

			// Since we're at index 0 (coinbase), we're always the left child
			currentHash = btcblockchain.HashMerkleBranches(&currentHash, &sibling)
		}
		return currentHash
	case Scrypt:
		// Start with the transaction hash
		currentHash := ltcchainhash.Hash(AuxPowTxHash(powId, coinbaseTx))

		// For coinbase (index 0), we always take the right branch
		// and our hash goes on the left
		for _, siblingBytes := range merkleBranch {
			var sibling ltcchainhash.Hash
			copy(sibling[:], siblingBytes)

			// Since we're at index 0 (coinbase), we're always the left child
			currentHash = ltcblockchain.HashMerkleBranches(&currentHash, &sibling)
		}
		return currentHash
	case SHA_BCH:
		// Start with the transaction hash
		currentHash := bchchainhash.Hash(AuxPowTxHash(powId, coinbaseTx))

		// For coinbase (index 0), we always take the right branch
		// and our hash goes on the left
		for _, siblingBytes := range merkleBranch {
			var sibling bchchainhash.Hash
			copy(sibling[:], siblingBytes)

			// Since we're at index 0 (coinbase), we're always the left child
			currentHash = *bchblockchain.HashMerkleBranches(&currentHash, &sibling)
		}
		return currentHash
	default:
		return [common.HashLength]byte{}
	}
}

// encodeHeightForBIP34 encodes block height in minimal little-endian format
// This matches Bitcoin's CScriptNum serialization used for BIP34 compliance.
// The height must be encoded with the minimum number of bytes required.
func encodeHeightForBIP34(height uint32) []byte {
	if height == 0 {
		return []byte{}
	}

	// Convert to little-endian bytes, removing leading zeros
	result := make([]byte, 0, 4)
	for height > 0 {
		result = append(result, byte(height&0xff))
		height >>= 8
	}

	// If the most significant bit is set, add a zero byte to indicate positive number
	// This is part of Bitcoin's CScriptNum format to distinguish positive/negative
	if len(result) > 0 && (result[len(result)-1]&0x80) != 0 {
		result = append(result, 0x00)
	}

	return result
}

// readVarInt decodes Bitcoin-style VarInts and returns the decoded value along with the number of bytes consumed.
func readVarInt(buf *bytes.Reader) (uint64, int, error) {
	b, err := buf.ReadByte()
	if err != nil {
		return 0, 0, err
	}

	switch b {
	case 0xfd:
		var v uint16
		if err := binary.Read(buf, binary.LittleEndian, &v); err != nil {
			return 0, 0, err
		}
		return uint64(v), 3, nil
	case 0xfe:
		var v uint32
		if err := binary.Read(buf, binary.LittleEndian, &v); err != nil {
			return 0, 0, err
		}
		return uint64(v), 5, nil
	case 0xff:
		var v uint64
		if err := binary.Read(buf, binary.LittleEndian, &v); err != nil {
			return 0, 0, err
		}
		return v, 9, nil
	default:
		return uint64(b), 1, nil
	}
}

// ExtractScriptSigFromCoinbaseTx extracts scriptSig from the first input.
func ExtractScriptSigFromCoinbaseTx(coinbaseTx []byte) []byte {
	r := bytes.NewReader(coinbaseTx)

	// Skip version (4 bytes)
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		return nil
	}

	// Read input count (value unused, only advance reader)
	if _, _, err := readVarInt(r); err != nil {
		return nil
	}

	// Skip prev_txid (32) + prev_vout (4)
	if _, err := r.Seek(36, io.SeekCurrent); err != nil {
		return nil
	}

	// Read scriptSig length
	scriptLen, _, err := readVarInt(r)
	if err != nil {
		return nil
	}

	if scriptLen == 0 {
		return []byte{}
	}

	if scriptLen > uint64(r.Len()) {
		return nil
	}

	// Read scriptSig
	script := make([]byte, scriptLen)
	if _, err := io.ReadFull(r, script); err != nil {
		return nil
	}
	return script
}

// ExtractCoinbaseOutFromCoinbaseTx extracts all the outputs including the outputs length (varint + serialized outputs).
func ExtractCoinbaseOutFromCoinbaseTx(coinbaseTx []byte) []byte {
	r := bytes.NewReader(coinbaseTx)

	// Skip version
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		return nil
	}

	// Skip input count + first input (we can reuse above but inline for simplicity)
	if _, _, err := readVarInt(r); err != nil {
		return nil
	}
	if _, err := r.Seek(36, io.SeekCurrent); err != nil { // prev_txid + vout
		return nil
	}

	scriptLen, _, err := readVarInt(r)
	if err != nil {
		return nil
	}
	if int64(scriptLen) < 0 || scriptLen > uint64(r.Len()) {
		return nil
	}
	if _, err := r.Seek(int64(scriptLen), io.SeekCurrent); err != nil { // skip script
		return nil
	}
	if _, err := r.Seek(4, io.SeekCurrent); err != nil { // skip sequence
		return nil
	}

	// Record the start of the outputs (includes the outputs count varint)
	// Return everything from outputs to the end (includes outputs + locktime)
	// Note: Locktime is part of the signed template data and cannot be modified by miners
	outputsStart := int(r.Size()) - r.Len()

	return coinbaseTx[outputsStart:]
}

// ValidatePrevOutPointIndexAndSequenceOfCoinbase validates the tx in part of the coinbase transaction
// 1) Number of inputs must be 1
// 2) prev_txid must be all zeros
// 3) prev_vout must be 0xffffffff
// 4) sequence must be 0xffffffff
func ValidatePrevOutPointIndexAndSequenceOfCoinbase(coinbaseTx []byte) error {
	r := bytes.NewReader(coinbaseTx)

	// Skip version
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		return err
	}

	// Input count also has to be 1 for a valid coinbase transaction
	inputCount, _, err := readVarInt(r)
	if err != nil {
		return err
	}
	if inputCount != 1 {
		return errors.New("coinbase transaction must have exactly one input")
	}

	// Read prev_txid (32 bytes)
	prevTxid := make([]byte, 32)
	if _, err := io.ReadFull(r, prevTxid); err != nil {
		return err
	}
	for _, b := range prevTxid {
		if b != 0 {
			return errors.New("prev_txid in coinbase input must be all zeros")
		}
	}

	// Read prev_vout (4 bytes)
	var prevVout uint32
	if err := binary.Read(r, binary.LittleEndian, &prevVout); err != nil {
		return err
	}
	if prevVout != 0xffffffff {
		return errors.New("prev_vout in coinbase input must be 0xffffffff")
	}

	// Skip scriptSig
	scriptLen, _, err := readVarInt(r)
	if err != nil {
		return err
	}
	if int64(scriptLen) < 0 || scriptLen > uint64(r.Len()) {
		return errors.New("invalid scriptSig length in coinbase input")
	}
	if _, err := r.Seek(int64(scriptLen), io.SeekCurrent); err != nil {
		return err
	}

	// Read sequence (4 bytes)
	var sequence uint32
	if err := binary.Read(r, binary.LittleEndian, &sequence); err != nil {
		return err
	}
	if sequence != 0xffffffff {
		return errors.New("sequence in coinbase input must be 0xffffffff")
	}

	return nil
}

// ExtractHeightFromCoinbase extracts the block height from the coinbase scriptSig.
// The height is encoded at the beginning of the scriptSig using BIP34 minimal encoding.
// Format:
//
//	OP_PUSH<n> <height(variable bytes)> ← BIP34 height (minimal encoding, 0-5 bytes)
//	... (rest of scriptSig)
//
// Returns the decoded height and an error if the scriptSig is invalid.
func ExtractHeightFromCoinbase(scriptSig []byte) (uint32, error) {
	if len(scriptSig) == 0 {
		return 0, errors.New("coinbase scriptSig empty")
	}

	// Parse the height push operation
	heightData, _, err := parseScriptPush(scriptSig)
	if err != nil {
		return 0, fmt.Errorf("failed to parse height from scriptSig: %w", err)
	}

	// Height can be 0-5 bytes (BIP34 allows minimal encoding)
	if len(heightData) > 5 {
		return 0, fmt.Errorf("invalid height length: expected 0-5 bytes, got %d", len(heightData))
	}

	// Empty height data means height 0
	if len(heightData) == 0 {
		return 0, nil
	}

	// Decode the height from minimal little-endian format
	var height uint32
	for i := 0; i < len(heightData); i++ {
		height |= uint32(heightData[i]) << (8 * uint(i))
	}

	// Handle negative flag (last byte with high bit set followed by 0x00)
	// In BIP34, heights are always positive, but we need to handle the encoding
	if len(heightData) > 0 && heightData[len(heightData)-1] == 0x00 && len(heightData) > 1 {
		// Remove the sign byte and recalculate
		height = 0
		for i := 0; i < len(heightData)-1; i++ {
			height |= uint32(heightData[i]) << (8 * uint(i))
		}
	}

	return height, nil
}

// CreateAuxMerkleRoot creates an aux work merkle root for merged mining with multiple chains.
// According to the Bitcoin merged mining specification:
// - Each chain has a chain_id that determines its slot in the merkle tree
// - The merkle tree must have a power-of-two size (merkle_size)
// - A merkle_nonce is used to resolve slot collisions (though the algorithm is broken)
// - Block hashes are inserted in reversed byte order
// - The final merkle root is reversed before insertion into the coinbase
//
// For Dogecoin + Quai merged mining:
// - Dogecoin chain_id = 98
// - Quai chain_id = 9
// - merkle_size = smallest power of 2 that fits both chains without collision
// - merkle_nonce = 0
//
// Parameters:
//   - dogeHash: Dogecoin block hash (32 bytes)
//   - quaiSealHash: Quai seal hash (32 bytes)
//
// Returns:
//   - auxMerkleRoot: The merkle root to insert into the Litecoin coinbase (32 bytes, byte-reversed)
func CreateAuxMerkleRoot(dogeHash common.Hash, quaiSealHash common.Hash) common.Hash {
	// Chain IDs for merged mining
	const (
		dogeChainID uint32 = 98 // Dogecoin's chain ID
		quaiChainID uint32 = 9  // Quai's chain ID
	)

	// For merkle_size=2, the slot calculation simplifies to: slot ≡ (merkle_nonce + chain_id) mod 2
	// Since Dogecoin (98) is even and Quai (9) is odd, they have different parity
	// and will never collide at size=2 regardless of merkle_nonce.
	merkleNonce := uint32(0)
	merkleSize := uint32(2)

	// Calculate slot positions using the merged mining algorithm
	// Both chains use the SAME merkle_nonce (as per the spec - there's only one nonce in the coinbase)
	dogeSlot := CalculateMerkleSlot(dogeChainID, merkleNonce, merkleSize)
	quaiSlot := CalculateMerkleSlot(quaiChainID, merkleNonce, merkleSize)

	// Create the leaf level of the merkle tree
	// Fill with zeros and insert the chain hashes in reversed order
	leaves := make([][]byte, merkleSize)
	for i := range leaves {
		leaves[i] = make([]byte, 32) // Fill with zeros
	}

	// Reverse the bytes of each chain's block hash before inserting
	// (This is required by the merged mining spec)
	dogeHashReversed := dogeHash.Reverse().Bytes()
	quaiHashReversed := quaiSealHash.Reverse().Bytes()

	leaves[dogeSlot] = dogeHashReversed
	leaves[quaiSlot] = quaiHashReversed

	// Build the merkle tree by hashing pairs up to the root
	currentLevel := leaves
	for len(currentLevel) > 1 {
		nextLevel := make([][]byte, len(currentLevel)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			left := currentLevel[i]
			right := currentLevel[i+1]

			// Double SHA256 of concatenated hashes
			combined := append(left, right...)
			hash1 := doubleSHA256(combined)
			nextLevel[i/2] = hash1[:]
		}
		currentLevel = nextLevel
	}

	// The root is the last remaining hash
	merkleRoot := currentLevel[0]

	// Reverse the bytes of the merkle root before returning
	// (Required by the merged mining spec for coinbase insertion)
	merkleRootReversed := common.BytesToHash(merkleRoot).Reverse().Bytes()

	return common.BytesToHash(merkleRootReversed)
}

// calculateMerkleSlot calculates the slot position for a chain in the aux merkle tree
// using the algorithm from the Bitcoin merged mining specification.
func CalculateMerkleSlot(chainID uint32, merkleNonce uint32, merkleSize uint32) uint32 {
	// This is the exact algorithm from the merged mining spec
	rand := merkleNonce
	rand = rand*1103515245 + 12345
	rand += chainID
	rand = rand*1103515245 + 12345
	return rand % merkleSize
}

// doubleSHA256 performs double SHA256 hashing
func doubleSHA256(data []byte) [32]byte {
	first := chainhash.DoubleHashB(data)
	var result [32]byte
	copy(result[:], first)
	return result
}

// HasWitnessCommitment detects if coinbase output bytes contain a witness commitment.
// The witness commitment is an OP_RETURN output with the following format:
//
//	OP_RETURN (0x6a) 0x24 0xaa 0x21 0xa9 0xed <32-byte commitment>
//
// This output must be the LAST output in the coinbase transaction.
// See Litecoin validation.cpp:3718-3720 and BIP 141 for specification.
//
// The coinbaseOut parameter should be the serialized outputs section starting
// with the output count varint.
func HasWitnessCommitment(coinbaseOut []byte) bool {
	if len(coinbaseOut) == 0 {
		return false
	}

	// Parse the output count
	r := bytes.NewReader(coinbaseOut)
	outputCount, _, err := readVarInt(r)
	if err != nil || outputCount == 0 {
		return false
	}

	// We need to find the LAST output to check for witness commitment
	// Parse through all outputs to find the last one
	var lastOutputScript []byte

	for i := uint64(0); i < outputCount; i++ {
		// Read output value (8 bytes)
		var value [8]byte
		if _, err := io.ReadFull(r, value[:]); err != nil {
			return false
		}

		// Read script length
		scriptLen, _, err := readVarInt(r)
		if err != nil {
			return false
		}

		// Read script
		script := make([]byte, scriptLen)
		if _, err := io.ReadFull(r, script); err != nil {
			return false
		}

		// Keep track of the last output's script
		lastOutputScript = script
	}

	// Check if the last output is a witness commitment
	// Format: OP_RETURN (0x6a) 0x24 0xaa 0x21 0xa9 0xed <32-byte commitment>
	// Minimum length: 1 + 1 + 4 + 32 = 38 bytes
	if len(lastOutputScript) < 38 {
		return false
	}

	// Check the witness commitment pattern
	return lastOutputScript[0] == 0x6a && // OP_RETURN
		lastOutputScript[1] == 0x24 && // Push 36 bytes
		lastOutputScript[2] == 0xaa &&
		lastOutputScript[3] == 0x21 &&
		lastOutputScript[4] == 0xa9 &&
		lastOutputScript[5] == 0xed
	// The remaining 32 bytes are the actual commitment
}

func ExtractCoinb1AndCoinb2FromAuxPowTx(txBytes []byte) ([]byte, []byte, error) {
	// Helpers to parse CompactSize varints and script pushes
	readVarInt := func(b []byte) (val uint64, size int, err error) {
		if len(b) == 0 {
			return 0, 0, errors.New("short varint")
		}
		prefix := b[0]
		switch prefix {
		case 0xFF:
			if len(b) < 9 {
				return 0, 0, errors.New("short varint 0xff")
			}
			return binary.LittleEndian.Uint64(b[1:9]), 9, nil
		case 0xFE:
			if len(b) < 5 {
				return 0, 0, errors.New("short varint 0xfe")
			}
			return uint64(binary.LittleEndian.Uint32(b[1:5])), 5, nil
		case 0xFD:
			if len(b) < 3 {
				return 0, 0, errors.New("short varint 0xfd")
			}
			return uint64(binary.LittleEndian.Uint16(b[1:3])), 3, nil
		default:
			return uint64(prefix), 1, nil
		}
	}

	// Returns (dataStartOffset, dataLen, headerLen) for the next push
	parsePushHeader := func(script []byte) (int, int, int, error) {
		if len(script) == 0 {
			return 0, 0, 0, errors.New("empty script segment")
		}
		opcode := script[0]
		if opcode <= 75 {
			return 1, int(opcode), 1, nil
		}
		if opcode == 0x4c { // OP_PUSHDATA1
			if len(script) < 2 {
				return 0, 0, 0, errors.New("short OP_PUSHDATA1")
			}
			return 2, int(script[1]), 2, nil
		}
		if opcode == 0x4d { // OP_PUSHDATA2
			if len(script) < 3 {
				return 0, 0, 0, errors.New("short OP_PUSHDATA2")
			}
			ln := int(binary.LittleEndian.Uint16(script[1:3]))
			return 3, ln, 3, nil
		}
		return 0, 0, 0, fmt.Errorf("unsupported opcode 0x%x in coinbase script", opcode)
	}

	// Walk the transaction encoding to find scriptSig and split positions
	pos := 0
	// version (4 bytes)
	if len(txBytes) < pos+4 {
		return nil, nil, errors.New("tx too short for version")
	}
	pos += 4
	// input count (varint)
	vinCount, sz, err := readVarInt(txBytes[pos:])
	if err != nil {
		return nil, nil, fmt.Errorf("parse vin count: %w", err)
	}
	pos += sz
	if vinCount == 0 {
		return nil, nil, errors.New("coinbase tx has zero vin")
	}
	// prevout (32 + 4)
	if len(txBytes) < pos+36 {
		return nil, nil, errors.New("tx too short for prevout")
	}
	pos += 36
	// scriptSig length (varint)
	scriptLenU64, sz2, err := readVarInt(txBytes[pos:])
	if err != nil {
		return nil, nil, fmt.Errorf("parse scriptsig length: %w", err)
	}
	pos += sz2
	scriptLen := int(scriptLenU64)
	if scriptLen < 0 || len(txBytes) < pos+scriptLen {
		return nil, nil, errors.New("tx too short for scriptsig bytes")
	}
	scriptStart := pos
	script := txBytes[scriptStart : scriptStart+scriptLen]

	// Parse pushes per our format
	// 1) height
	_, l, hdr, err := parsePushHeader(script)
	if err != nil {
		return nil, nil, fmt.Errorf("parse height push: %w", err)
	}
	cur := hdr + l
	// 2) AuxPoW commitment as a single push: magic(4) | root(32) | size(4 LE) | nonce(4 LE)
	if cur >= len(script) {
		return nil, nil, errors.New("unexpected end after height push")
	}
	off, l, hdr, err := parsePushHeader(script[cur:])
	if err != nil {
		return nil, nil, fmt.Errorf("parse auxpow commitment push: %w", err)
	}
	if l != 44 || cur+off+l > len(script) {
		return nil, nil, errors.New("invalid auxpow commitment length; expected 44 bytes")
	}
	payload := script[cur+off : cur+off+l]
	if !bytes.Equal(payload[0:4], []byte{0xfa, 0xbe, 0x6d, 0x6d}) {
		return nil, nil, errors.New("unexpected magic marker in auxpow commitment")
	}

	cur += hdr + l
	// 6) combined extranonces + extraData push
	if cur >= len(script) {
		return nil, nil, errors.New("unexpected end before extranonce push")
	}
	dataStartOff, dataLen, _, err := parsePushHeader(script[cur:])
	if err != nil {
		return nil, nil, fmt.Errorf("parse extranonce push: %w", err)
	}
	if dataLen < 1 || cur+dataStartOff+dataLen > len(script) {
		return nil, nil, errors.New("invalid extranonce push bounds")
	}

	pushDataAbsStart := scriptStart + cur + dataStartOff
	pushDataAbsEnd := pushDataAbsStart + dataLen

	if pushDataAbsStart < 0 || pushDataAbsEnd > len(txBytes) || pushDataAbsStart > pushDataAbsEnd {
		return nil, nil, errors.New("computed coinbase split out of bounds")
	}
	// Only carve out extranonce1 (4B) and extranonce2 (8B). Keep the trailing 32B zeros in coinb2.
	coinb1 := txBytes[:pushDataAbsStart]
	coinb2Start := pushDataAbsStart + 4 + 8
	if coinb2Start < 0 || coinb2Start > len(txBytes) {
		return nil, nil, errors.New("computed coinb2 start out of bounds")
	}
	coinb2 := txBytes[coinb2Start:]
	return coinb1, coinb2, nil
}
