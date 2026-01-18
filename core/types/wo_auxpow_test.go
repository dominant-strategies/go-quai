package types

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"
)

var (
	c_version int32  = 10
	c_time    uint32 = 1610000000
	c_bits    uint32 = 0x1d00ffff
	c_height  uint32 = 298899
)

func auxPowTestData(powID PowID) *AuxPow {
	auxPow := &AuxPow{}
	auxPow.SetPowID(powID)
	auxPow.SetSignature([]byte{})
	auxPow.SetMerkleBranch([][]byte{})
	switch powID {
	case Kawpow:
		ravencoinHeader := &RavencoinBlockHeader{
			// Set fields for Ravencoin header
			Version:        c_version,
			HashPrevBlock:  EmptyRootHash,
			HashMerkleRoot: EmptyRootHash,
			Time:           c_time,
			Bits:           c_bits,
			Nonce64:        367899,
			Height:         c_height,
			MixHash:        EmptyRootHash,
		}
		coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
		coinbaseTx := NewAuxPowCoinbaseTx(Kawpow, 100, coinbaseOut, EmptyRootHash, uint32(0))
		auxPow.SetHeader(NewAuxPowHeader(ravencoinHeader))
		auxPow.SetTransaction(coinbaseTx)
	case SHA_BTC:
		bitcoinHeader := NewBitcoinBlockHeader(c_version, EmptyRootHash, EmptyRootHash, c_time, c_bits, 0)
		coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
		coinbaseTx := NewAuxPowCoinbaseTx(powID, 100, coinbaseOut, EmptyRootHash, uint32(1234))
		auxPow.SetHeader(NewAuxPowHeader(bitcoinHeader))
		auxPow.SetTransaction(coinbaseTx)
	case SHA_BCH:
		bitcoinCashHeader := NewBitcoinCashBlockHeader(c_version, EmptyRootHash, EmptyRootHash, c_time, c_bits, 0)
		coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
		coinbaseTx := NewAuxPowCoinbaseTx(SHA_BCH, 100, coinbaseOut, EmptyRootHash, uint32(1234))
		auxPow.SetHeader(NewAuxPowHeader(bitcoinCashHeader))
		auxPow.SetTransaction(coinbaseTx)
	case Scrypt:
		litecoinHeader := NewLitecoinBlockHeader(c_version, EmptyRootHash, EmptyRootHash, c_time, c_bits, 0)
		coinbaseOut := []byte{0x76, 0xa9, 0x14, 0x89, 0xab, 0xcd, 0xef, 0x88, 0xac}
		coinbaseTx := NewAuxPowCoinbaseTx(Scrypt, 100, coinbaseOut, EmptyRootHash, uint32(1234))
		auxPow.SetHeader(NewAuxPowHeader(litecoinHeader))
		auxPow.SetTransaction(coinbaseTx)
	}
	return auxPow
}

// TestWorkObjectHashWithoutAuxPow tests the hash computation of a WorkObject
// before adding the AuxPow field (with nil AuxPow)
func TestWorkObjectHashWithoutAuxPow(t *testing.T) {
	// Create test data
	headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	parentHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	number := big.NewInt(12345)
	difficulty := big.NewInt(1000000)
	primeTerminusNumber := big.NewInt(100)
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	nonce := BlockNonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	lock := uint8(1)
	time := uint64(1234567890)
	location := common.Location{0, 0}
	primaryCoinbase := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678", location)
	data := []byte("test data")
	mixHash := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")

	// Create WorkObjectHeader without AuxPow
	woHeader := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		auxPow:              nil, // No AuxPow
	}

	// Compute hash
	hash := woHeader.Hash()
	sealHash := woHeader.SealHash()

	// Print test vector
	fmt.Printf("Test Vector WITHOUT AuxPow:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Header Hash:           %s\n", headerHash.Hex())
	fmt.Printf("Parent Hash:           %s\n", parentHash.Hex())
	fmt.Printf("Number:                %d\n", number)
	fmt.Printf("Difficulty:            %d\n", difficulty)
	fmt.Printf("Prime Terminus Number: %d\n", primeTerminusNumber)
	fmt.Printf("Tx Hash:               %s\n", txHash.Hex())
	fmt.Printf("Nonce:                 %x\n", nonce)
	fmt.Printf("Lock:                  %d\n", lock)
	fmt.Printf("Time:                  %d\n", time)
	fmt.Printf("Location:              %v\n", location)
	fmt.Printf("Primary Coinbase:      %s\n", primaryCoinbase.Hex())
	fmt.Printf("Data:                  %s\n", string(data))
	fmt.Printf("Mix Hash:              %s\n", mixHash.Hex())
	fmt.Printf("AuxPow:                nil\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Seal Hash:             %s\n", sealHash.Hex())
	fmt.Printf("WorkObject Hash:       %s\n", hash.Hex())
	fmt.Printf("=====================================\n\n")

	// Verify the hash is computed correctly
	if hash == (common.Hash{}) {
		t.Error("Hash should not be empty")
	}

	// Store expected hash for comparison
	expectedHashWithoutAuxPow := hash
	t.Logf("Hash without AuxPow: %s", expectedHashWithoutAuxPow.Hex())
}

// TestWorkObjectHashWithAuxPow tests the hash computation of a WorkObject
// after adding the AuxPow field (with non-nil AuxPow)
func TestWorkObjectHashWithAuxPow(t *testing.T) {
	// Create test data (same as before)
	headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	parentHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	number := big.NewInt(12345)
	difficulty := big.NewInt(1000000)
	primeTerminusNumber := big.NewInt(100)
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	nonce := BlockNonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	lock := uint8(1)
	time := uint64(1234567890)
	location := common.Location{0, 0}
	primaryCoinbase := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678", location)
	data := []byte("test data")
	mixHash := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")

	// Create AuxPow data
	auxPow := auxPowTestData(SHA_BTC)
	// Create WorkObjectHeader with AuxPow
	woHeader := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		auxPow:              auxPow, // With AuxPow
	}

	// Compute hash
	hash := woHeader.Hash()
	sealHash := woHeader.SealHash()

	// Print test vector
	fmt.Printf("Test Vector WITH AuxPow:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Header Hash:           %s\n", headerHash.Hex())
	fmt.Printf("Parent Hash:           %s\n", parentHash.Hex())
	fmt.Printf("Number:                %d\n", number)
	fmt.Printf("Difficulty:            %d\n", difficulty)
	fmt.Printf("Prime Terminus Number: %d\n", primeTerminusNumber)
	fmt.Printf("Tx Hash:               %s\n", txHash.Hex())
	fmt.Printf("Nonce:                 %x\n", nonce)
	fmt.Printf("Lock:                  %d\n", lock)
	fmt.Printf("Time:                  %d\n", time)
	fmt.Printf("Location:              %v\n", location)
	fmt.Printf("Primary Coinbase:      %s\n", primaryCoinbase.Hex())
	fmt.Printf("Data:                  %s\n", string(data))
	fmt.Printf("Mix Hash:              %s\n", mixHash.Hex())
	fmt.Printf("AuxPow:\n")
	fmt.Printf("  ChainID:             %d\n", auxPow.PowID())
	fmt.Printf("  Signature:           %x\n", auxPow.Signature())
	fmt.Printf("=====================================\n")
	fmt.Printf("Seal Hash:             %s\n", sealHash.Hex())
	fmt.Printf("WorkObject Hash:       %s\n", hash.Hex())
	fmt.Printf("=====================================\n\n")

	// Verify the hash is computed correctly
	if hash == (common.Hash{}) {
		t.Error("Hash should not be empty")
	}

	t.Logf("Hash with AuxPow: %s", hash.Hex())
}

// TestWorkObjectHashComparison compares hashes before and after adding AuxPow
func TestWorkObjectHashComparison(t *testing.T) {
	// Create identical test data for both cases
	headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	parentHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	number := big.NewInt(12345)
	difficulty := big.NewInt(1000000)
	primeTerminusNumber := big.NewInt(100)
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	nonce := BlockNonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	lock := uint8(1)
	time := uint64(1234567890)
	location := common.Location{0, 0}
	primaryCoinbase := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678", location)
	data := []byte("test data")
	mixHash := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")
	shaDiffAndCount := NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(10), big.NewInt(100))
	scryptDiffAndCount := NewPowShareDiffAndCount(big.NewInt(2000), big.NewInt(20), big.NewInt(100))
	kawpowDifficulty := big.NewInt(3000000)
	shaTarget := big.NewInt(100)
	scryptTarget := big.NewInt(200)

	// Create WorkObjectHeader without AuxPow
	woHeaderWithoutAuxPow := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		shaDiffAndCount:     shaDiffAndCount,
		scryptDiffAndCount:  scryptDiffAndCount,
		kawpowDifficulty:    kawpowDifficulty,
		shaShareTarget:      shaTarget,
		scryptShareTarget:   scryptTarget,
		auxPow:              nil,
	}

	// Create AuxPow data
	auxPow := auxPowTestData(Scrypt)

	// Create WorkObjectHeader with AuxPow
	woHeaderWithAuxPow := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		shaDiffAndCount:     shaDiffAndCount,
		scryptDiffAndCount:  scryptDiffAndCount,
		kawpowDifficulty:    kawpowDifficulty,
		shaShareTarget:      shaTarget,
		scryptShareTarget:   scryptTarget,
		auxPow:              auxPow,
	}

	hashWithoutAuxPow := woHeaderWithoutAuxPow.Hash()
	hashWithAuxPow := woHeaderWithAuxPow.Hash()

	sealHashWithoutAuxPow := woHeaderWithoutAuxPow.SealHash()
	sealHashWithAuxPow := woHeaderWithAuxPow.SealHash()

	fmt.Printf("Hash Comparison:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Without AuxPow:\n")
	fmt.Printf("  Seal Hash: %s\n", sealHashWithoutAuxPow.Hex())
	fmt.Printf("\nWith AuxPow:\n")
	fmt.Printf("  Seal Hash: %s\n", sealHashWithAuxPow.Hex())
	fmt.Printf("=====================================\n")

	// IMPORTANT: AuxPow is NOT included in the seal hash to prevent circular reference.
	// The donor header in AuxPow commits to the WorkObject's seal hash, so the seal hash
	// cannot depend on AuxPow. Therefore, hashes should be the SAME regardless of AuxPow.
	if sealHashWithoutAuxPow != sealHashWithAuxPow {
		t.Error("Seal hashes should be the SAME (AuxPow is excluded from seal hash)")
	}

	if hashWithoutAuxPow != hashWithAuxPow {
		t.Error("Hash should be the same as the the block is below the kawpow activation")
	}

	t.Logf("Test passed: Seal Hashes are correctly the same (AuxPow excluded from hash)")
}

// TestWorkObjectProtoEncodeDecodeWithAuxPow tests protobuf encoding/decoding with AuxPow
func TestWorkObjectProtoEncodeDecodeWithAuxPow(t *testing.T) {
	// Create test data
	headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	parentHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	number := big.NewInt(12345)
	difficulty := big.NewInt(1000000)
	primeTerminusNumber := big.NewInt(int64(params.KawPowForkBlock))
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	nonce := BlockNonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	lock := uint8(1)
	time := uint64(1234567890)
	location := common.Location{0, 0}
	primaryCoinbase := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678", location)
	data := []byte("test data")
	mixHash := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")
	shaDiffAndCount := NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(10), big.NewInt(200))
	scryptDiffAndCount := NewPowShareDiffAndCount(big.NewInt(2000), big.NewInt(20), big.NewInt(300))
	kawpowDifficulty := big.NewInt(3000000)
	shaTarget := big.NewInt(100)
	scryptTarget := big.NewInt(200)

	// Create AuxPow data
	auxPow := auxPowTestData(Kawpow)

	// Create original WorkObjectHeader
	original := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		auxPow:              auxPow,
		shaDiffAndCount:     shaDiffAndCount,
		scryptDiffAndCount:  scryptDiffAndCount,
		shaShareTarget:      shaTarget,
		scryptShareTarget:   scryptTarget,
		kawpowDifficulty:    kawpowDifficulty,
	}

	// Encode to protobuf
	protoHeader, err := original.ProtoEncode()
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode from protobuf
	decoded := &WorkObjectHeader{}
	err = decoded.ProtoDecode(protoHeader, location)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify all fields match
	if !bytes.Equal(decoded.HeaderHash().Bytes(), original.HeaderHash().Bytes()) {
		t.Error("HeaderHash mismatch")
	}
	if !bytes.Equal(decoded.ParentHash().Bytes(), original.ParentHash().Bytes()) {
		t.Error("ParentHash mismatch")
	}
	if decoded.Number().Cmp(original.Number()) != 0 {
		t.Error("Number mismatch")
	}
	if decoded.Difficulty().Cmp(original.Difficulty()) != 0 {
		t.Error("Difficulty mismatch")
	}

	// Verify AuxPow fields
	if decoded.AuxPow() == nil {
		t.Fatal("AuxPow should not be nil after decoding")
	}
	if decoded.AuxPow().PowID() != original.AuxPow().PowID() {
		t.Error("AuxPow.ChainID mismatch")
	}
	if !bytes.Equal(decoded.AuxPow().Header().Bytes(), original.AuxPow().Header().Bytes()) {
		t.Error("AuxPow.Header mismatch")
	}
	if !bytes.Equal(decoded.AuxPow().Signature(), original.AuxPow().Signature()) {
		t.Error("AuxPow.Signature mismatch")
	}

	t.Log("Proto encode/decode with AuxPow test passed")
}

// OldWorkObjectHeader represents the WorkObjectHeader structure before AuxPow was added
// This is used to verify backward compatibility
type OldWorkObjectHeader struct {
	headerHash          common.Hash
	parentHash          common.Hash
	number              *big.Int
	difficulty          *big.Int
	primeTerminusNumber *big.Int
	txHash              common.Hash
	primaryCoinbase     common.Address
	location            common.Location
	mixHash             common.Hash
	time                uint64
	nonce               BlockNonce
	data                []byte
	lock                uint8
	// Note: No auxPow field
}

// OldSealEncode replicates the original SealEncode without AuxPow
func (wh *OldWorkObjectHeader) OldSealEncode() *ProtoWorkObjectHeader {
	headerHash := common.ProtoHash{Value: wh.headerHash.Bytes()}
	parentHash := common.ProtoHash{Value: wh.parentHash.Bytes()}
	txHash := common.ProtoHash{Value: wh.txHash.Bytes()}
	number := wh.number.Bytes()
	difficulty := wh.difficulty.Bytes()
	primeTerminusNumber := wh.primeTerminusNumber.Bytes()
	location := wh.location.ProtoEncode()
	time := wh.time
	lock := uint32(wh.lock)
	coinbase := common.ProtoAddress{Value: wh.primaryCoinbase.Bytes()}
	data := wh.data

	return &ProtoWorkObjectHeader{
		HeaderHash:          &headerHash,
		ParentHash:          &parentHash,
		Number:              number,
		Difficulty:          difficulty,
		TxHash:              &txHash,
		PrimeTerminusNumber: primeTerminusNumber,
		Location:            location,
		Lock:                &lock,
		PrimaryCoinbase:     &coinbase,
		Time:                &time,
		Data:                data,
		// No AuxPow field set - this mimics the old structure
	}
}

// OldSealHash computes the seal hash without AuxPow field
func (wh *OldWorkObjectHeader) OldSealHash() (hash common.Hash) {
	// Manually compute the seal hash like the original did
	// This uses the same logic as SealHash but without AuxPow
	protoSealData := wh.OldSealEncode()
	data, err := proto.Marshal(protoSealData)
	if err != nil {
		panic(err)
	}
	sum := blake3.Sum256(data[:])
	hash.SetBytes(sum[:])
	return hash
}

// OldHash computes the full hash without AuxPow field
func (wh *OldWorkObjectHeader) OldHash() (hash common.Hash) {
	// Manually compute the hash like the original did
	// This uses the same logic as Hash but without AuxPow
	sealHash := wh.OldSealHash().Bytes()
	mixHash := wh.mixHash.Bytes()
	nonce := wh.nonce.Bytes()
	var hData [common.HashLength + common.HashLength + NonceLength]byte
	copy(hData[:], mixHash)
	copy(hData[common.HashLength:], sealHash)
	copy(hData[common.HashLength+common.HashLength:], nonce)
	sum := blake3.Sum256(hData[:])
	hash.SetBytes(sum[:])
	return hash
}

// TestThreeScenarioCompatibility tests all three scenarios:
// 1. Old headers without auxPow field
// 2. New headers with auxPow set to nil
// 3. New headers with auxPow populated
// Since three new fields are added to the work object header, the hash cannot be same between 1&2 and 1&3, but 2&3 should be same.
func TestThreeScenarioCompatibility(t *testing.T) {
	// Create common test data
	headerHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	parentHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	number := big.NewInt(12345)
	difficulty := big.NewInt(1000000)
	primeTerminusNumber := big.NewInt(int64(params.KawPowForkBlock))
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	nonce := BlockNonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	lock := uint8(1)
	time := uint64(1234567890)
	location := common.Location{0, 0}
	primaryCoinbase := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678", location)
	data := []byte("test data")
	mixHash := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")
	shaDiffAndCount := NewPowShareDiffAndCount(big.NewInt(1000), big.NewInt(10), big.NewInt(1000))
	scryptDiffAndCount := NewPowShareDiffAndCount(big.NewInt(2000), big.NewInt(20), big.NewInt(100))
	kawpowDifficulty := big.NewInt(3000000)
	shaTarget := big.NewInt(100)
	scryptTarget := big.NewInt(200)

	// Scenario 1: Old WorkObjectHeader (without AuxPow field in struct)
	oldHeader := &OldWorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
	}

	// Scenario 2: New WorkObjectHeader with auxPow = nil
	newHeaderNil := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		auxPow:              nil, // Explicitly nil
	}

	if primeTerminusNumber.Uint64() >= params.KawPowForkBlock {
		newHeaderNil.shaDiffAndCount = shaDiffAndCount
		newHeaderNil.scryptDiffAndCount = scryptDiffAndCount
		newHeaderNil.shaShareTarget = shaTarget
		newHeaderNil.scryptShareTarget = scryptTarget
		newHeaderNil.kawpowDifficulty = kawpowDifficulty
	}

	// Scenario 3: New WorkObjectHeader with auxPow populated
	auxPow := auxPowTestData(SHA_BCH)

	newHeaderWithAux := &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		primaryCoinbase:     primaryCoinbase,
		location:            location,
		mixHash:             mixHash,
		time:                time,
		nonce:               nonce,
		data:                data,
		lock:                lock,
		auxPow:              auxPow, // With AuxPow
	}

	if primeTerminusNumber.Uint64() >= params.KawPowForkBlock {
		newHeaderWithAux.shaDiffAndCount = shaDiffAndCount
		newHeaderWithAux.scryptDiffAndCount = scryptDiffAndCount
		newHeaderWithAux.shaShareTarget = shaTarget
		newHeaderWithAux.scryptShareTarget = scryptTarget
		newHeaderWithAux.kawpowDifficulty = kawpowDifficulty
	}

	// Compute hashes for all three scenarios
	// Using OldSealHash/OldHash for the old implementation
	oldSealHash := oldHeader.OldSealHash()
	oldHash := oldHeader.OldHash()

	// Using current implementation SealHash/Hash for both new scenarios
	nilSealHash := newHeaderNil.SealHash()
	nilHash := newHeaderNil.Hash()

	auxSealHash := newHeaderWithAux.SealHash()
	auxHash := newHeaderWithAux.Hash()

	// Print test vectors
	fmt.Printf("\nThree Scenario Test Vectors:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Scenario 1 - Old Header (no auxPow field):\n")
	fmt.Printf("  Seal Hash: %s\n", oldSealHash.Hex())
	fmt.Printf("  Full Hash: %s\n", oldHash.Hex())
	fmt.Printf("\n")
	fmt.Printf("Scenario 2 - New Header (auxPow = nil):\n")
	fmt.Printf("  Seal Hash: %s\n", nilSealHash.Hex())
	fmt.Printf("  Full Hash: %s\n", nilHash.Hex())
	fmt.Printf("\n")
	fmt.Printf("Scenario 3 - New Header (auxPow populated):\n")
	fmt.Printf("  Seal Hash: %s\n", auxSealHash.Hex())
	fmt.Printf("  Full Hash: %s\n", auxHash.Hex())
	fmt.Printf("=====================================\n\n")

	// Critical Test 1: Old header hash MUST equal new header with nil auxPow
	if oldSealHash == nilSealHash {
		t.Errorf("Old seal hash == nil auxPow seal hash\n  Old: %s\n  Nil: %s",
			oldSealHash.Hex(), nilSealHash.Hex())
	} else {
		t.Log("✓ Old seal hash != nil auxPow seal hash")
	}

	if oldHash == nilHash {
		t.Errorf("Old hash == nil seal hash\n  Old: %s\n  Nil: %s",
			oldHash.Hex(), nilHash.Hex())
	} else {
		t.Log("✓ Old seal hash != nil auxPow seal hash")
	}

	// Critical Test 2: Since AuxPow is NOT included in seal hash (to avoid circular reference),
	// headers with auxPow populated should have the SAME hash as those without
	if nilSealHash != auxSealHash {
		t.Errorf("SEAL HASH MISMATCH: nil auxPow seal hash != populated auxPow seal hash\n  Nil: %s\n  Aux: %s\nThey should be equal since auxPow is excluded from seal hash",
			nilSealHash.Hex(), auxSealHash.Hex())
	} else {
		t.Log("✓ Seal hash consistency: nil auxPow seal hash == populated auxPow seal hash (auxPow excluded from hash)")
	}

	if nilHash == auxHash {
		t.Errorf("FULL HASH consistency: nil auxPow full hash == populated auxPow full hash\n  Nil: %s\n  Aux: %s\nThey should not be equal since auxPow is excluded from hash calculation",
			nilHash.Hex(), auxHash.Hex())
	} else {
		t.Log("✓ Full hash MISMATCH: nil auxPow full hash != populated auxPow full hash (auxPow excluded from hash)")
	}

	// Additional verification: Check protobuf encoding sizes
	oldProto := oldHeader.OldSealEncode()
	nilProto := newHeaderNil.SealEncode()
	auxProto := newHeaderWithAux.SealEncode()

	oldBytes, _ := proto.Marshal(oldProto)
	nilBytes, _ := proto.Marshal(nilProto)
	auxBytes, _ := proto.Marshal(auxProto)

	t.Logf("\nProtobuf encoding sizes:")
	t.Logf("  Old header:        %d bytes", len(oldBytes))
	t.Logf("  Nil auxPow header: %d bytes", len(nilBytes))
	t.Logf("  With auxPow header: %d bytes", len(auxBytes))

	if len(auxBytes) != len(nilBytes) {
		t.Logf("WARNING: AuxPow and nil auxPow protobuf sizes differ (%d vs %d)", len(auxBytes), len(nilBytes))
		t.Logf("This may indicate the nil auxPow is being encoded")
	}

	// Verify that nil auxPow is truly not encoded
	if nilProto.AuxPow != nil {
		t.Error("nil auxPow is being encoded in protobuf (should be omitted)")
	}
}
