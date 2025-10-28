package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/crypto/musig2"
	"github.com/dominant-strategies/go-quai/log"
	ltcchainhash "github.com/dominant-strategies/ltcd/chaincfg/chainhash"
	bchchainhash "github.com/gcash/bchd/chaincfg/chainhash"
	"google.golang.org/protobuf/proto"
)

// PowID represents a unique identifier for a proof-of-work algorithm
type PowID uint32

const (
	// Progpow is default and only there so that engine can be accessed by the same
	// type, technically progpow blocks dont have auxpow at all
	Progpow PowID = iota
	Kawpow
	SHA_BTC
	SHA_BCH
	Scrypt
)

func (p PowID) String() string {
	switch p {
	case Progpow:
		return "Progpow"
	case Kawpow:
		return "Kawpow"
	case SHA_BTC:
		return "SHA_BTC"
	case SHA_BCH:
		return "SHA_BCH"
	case Scrypt:
		return "Scrypt"
	default:
		return "Unknown"
	}
}

// AuxTemplate defines the template structure for auxiliary proof-of-work
type AuxTemplate struct {
	// === Consensus-correspondence (signed; Quai validators check against AuxPoW) ===
	powID    PowID    // must match ap.Chain (RVN)
	prevHash [32]byte // must equal donor_header.hashPrevBlock
	auxPow2  []byte

	// === Header/DAA knobs for job construction (signed) ===
	version       uint32 // header.nVersion to use
	bits          uint32 // header.nBits
	signatureTime uint32 // time at which the signature was created for this aux template
	height        uint32 // BIP34 height (needed for scriptSig + KAWPOW epoch hint)

	// CoinbaseOut is the coinbase payout
	coinbaseOut []byte // coinbase out contains the tail of the coinbase transaction including the length of the outputs

	// Mode B: LOCKED TX SET (miners get fees; template is larger & updated more often)
	merkleBranch [][]byte // siblings for coinbase index=0 up to root (little endian 32-byte hashes)

	// === Quorum signatures over CanonicalEncode(template WITHOUT Sigs) ===
	sigs []byte
}

func NewAuxTemplate() *AuxTemplate {
	return &AuxTemplate{}
}

func EmptyAuxTemplate() *AuxTemplate {
	return &AuxTemplate{
		powID:         Kawpow,
		prevHash:      [32]byte{},
		auxPow2:       nil,
		version:       536870912, // 0x20000000 (RVN)
		bits:          0,
		signatureTime: 0,
		height:        0,
		coinbaseOut:   []byte{},
		merkleBranch:  [][]byte{},
		sigs:          []byte{},
	}
}

// Getters for AuxTemplate fields
func (at *AuxTemplate) PowID() PowID           { return at.powID }
func (at *AuxTemplate) PrevHash() [32]byte     { return at.prevHash }
func (at *AuxTemplate) AuxPow2() []byte        { return at.auxPow2 }
func (at *AuxTemplate) Version() uint32        { return at.version }
func (at *AuxTemplate) Bits() uint32           { return at.bits }
func (at *AuxTemplate) SignatureTime() uint32  { return at.signatureTime }
func (at *AuxTemplate) Height() uint32         { return at.height }
func (at *AuxTemplate) CoinbaseOut() []byte    { return at.coinbaseOut }
func (at *AuxTemplate) MerkleBranch() [][]byte { return at.merkleBranch }
func (at *AuxTemplate) Sigs() []byte           { return at.sigs }

// Setters for AuxTemplate fields
func (at *AuxTemplate) SetPowID(id PowID)               { at.powID = id }
func (at *AuxTemplate) SetPrevHash(hash [32]byte)       { at.prevHash = hash }
func (at *AuxTemplate) SetAuxPow2(auxPow2 []byte)       { at.auxPow2 = auxPow2 }
func (at *AuxTemplate) SetVersion(v uint32)             { at.version = v }
func (at *AuxTemplate) SetNBits(bits uint32)            { at.bits = bits }
func (at *AuxTemplate) SetSignatureTime(time uint32)    { at.signatureTime = time }
func (at *AuxTemplate) SetHeight(h uint32)              { at.height = h }
func (at *AuxTemplate) SetCoinbaseOut(out []byte)       { at.coinbaseOut = out }
func (at *AuxTemplate) SetMerkleBranch(branch [][]byte) { at.merkleBranch = branch }
func (at *AuxTemplate) SetSigs(sigs []byte)             { at.sigs = sigs }

// ProtoEncode converts AuxTemplate to its protobuf representation
func (at *AuxTemplate) ProtoEncode() *ProtoAuxTemplate {
	if at == nil {
		return nil
	}

	powID := uint32(at.powID)
	version := at.version
	bits := at.bits
	signatureTime := at.signatureTime
	height := at.height
	coinbaseOut := at.coinbaseOut

	// Convert merkle branch
	merkleBranch := make([][]byte, len(at.merkleBranch))
	copy(merkleBranch, at.merkleBranch)

	return &ProtoAuxTemplate{
		ChainId:       &powID,
		PrevHash:      at.prevHash[:],
		AuxPow2:       at.auxPow2,
		Version:       &version,
		Bits:          &bits,
		SignatureTime: &signatureTime,
		Height:        &height,
		CoinbaseOut:   coinbaseOut,
		MerkleBranch:  merkleBranch,
		Sigs:          at.Sigs(),
	}
}

// ProtoDecode populates AuxTemplate from its protobuf representation
func (at *AuxTemplate) ProtoDecode(data *ProtoAuxTemplate) error {
	if data == nil {
		return nil
	}

	at.powID = PowID(data.GetChainId())

	// Copy PrevHash (32 bytes)
	if len(data.GetPrevHash()) == 32 {
		copy(at.prevHash[:], data.GetPrevHash())
	}

	at.auxPow2 = data.GetAuxPow2()
	at.version = data.GetVersion()
	at.bits = data.GetBits()
	at.signatureTime = data.GetSignatureTime()
	at.height = data.GetHeight()
	at.coinbaseOut = data.GetCoinbaseOut()

	// Copy merkle branch
	at.merkleBranch = make([][]byte, len(data.GetMerkleBranch()))
	for i, hash := range data.GetMerkleBranch() {
		at.merkleBranch[i] = make([]byte, len(hash))
		copy(at.merkleBranch[i], hash)
	}

	// Decode signer envelopes
	at.sigs = make([]byte, len(data.GetSigs()))
	copy(at.sigs, data.GetSigs())

	return nil
}

// Hash returns the SHA256 hash of the AuxTemplate with signature fields set to nil
// This is the same hash used for signing and verification
func (at *AuxTemplate) Hash() [32]byte {
	// Create a copy of the template without signatures for message hash calculation
	// We need to work with the protobuf representation to properly exclude signature fields
	protoTemplate := at.ProtoEncode()
	tempTemplate := proto.Clone(protoTemplate).(*ProtoAuxTemplate)
	tempTemplate.Sigs = nil

	// Since sha chains allow asic boost need to mask them before signing in the
	// case of sha chains
	versionMask := uint32(0xE0000000)
	switch at.powID {
	case SHA_BTC, SHA_BCH:
		tempTemplate.Version = proto.Uint32(*tempTemplate.Version & versionMask)
	}

	// Marshal the template without signatures to get the message hash
	templateData, err := proto.Marshal(tempTemplate)
	if err != nil {
		// Return zero hash on error - this should be handled by the caller
		return [32]byte{}
	}

	// Calculate and return the message hash
	return sha256.Sum256(templateData)
}

// VerifySignature verifies the composite MuSig2 signature over this AuxTemplate.
// It tries all possible 2-of-3 signer index combinations.
func (at *AuxTemplate) VerifySignature() bool {
	// Check Signature presence
	if len(at.sigs) == 0 {
		log.Global.Warn("Sig is empty, signature failed")
		return false
	}

	// Build the canonical message from the template (Sigs excluded internally)
	msgHash := at.Hash()
	message := msgHash[:]

	// Try all 2-of-3 combinations in both orders (order-dependent)
	combinations := [][]int{
		{0, 1}, {1, 0},
		{0, 2}, {2, 0},
		{1, 2}, {2, 1},
	}
	for _, signerIndices := range combinations {
		if err := musig2.VerifyCompositeSignature(message, at.sigs, signerIndices); err == nil {
			return true
		}
	}

	return false
}

type AuxPowHeader struct {
	inner AuxHeaderData
}

type AuxHeaderData interface {
	Serialize(w io.Writer) error
	Deserialize(r io.Reader) error
	BlockHash() common.Hash
	PowHash() common.Hash
	Copy() AuxHeaderData

	// Common blockchain header fields
	GetVersion() int32
	GetPrevBlock() [32]byte
	GetMerkleRoot() [32]byte
	GetTimestamp() uint32
	GetBits() uint32
	GetNonce() uint32
	GetHeight() uint32        // For chains that include height in header (e.g., KAWPOW)
	GetNonce64() uint64       // Only implemented for kawpow
	GetMixHash() common.Hash  // Only implemented for kawpow
	GetSealHash() common.Hash // Only implemented for kawpow, this is the hash on which PoW is done

	SetNonce(nonce uint32)

	SetNonce64(nonce uint64)        // Only implemented for kawpow
	SetMixHash(mixHash common.Hash) // Only implemented for kawpow
	SetHeight(height uint32)        // Only implemented for kawpow
}

func NewBlockHeader(powid PowID, version int32, prevBlockHash [32]byte, merkleRootHash [32]byte, time uint32, bits uint32, nonce uint32, height uint32) *AuxPowHeader {
	ah := &AuxPowHeader{}
	switch powid {
	case Kawpow:
		ah.inner = NewRavencoinBlockHeader(version, prevBlockHash, merkleRootHash, time, bits, height)
	case SHA_BTC:
		ah.inner = NewBitcoinBlockHeader(version, prevBlockHash, merkleRootHash, time, bits, nonce)
	case SHA_BCH:
		ah.inner = NewBitcoinCashBlockHeader(version, prevBlockHash, merkleRootHash, time, bits, nonce)
	case Scrypt:
		ah.inner = NewLitecoinBlockHeader(version, prevBlockHash, merkleRootHash, time, bits, nonce)
	default:
		return nil
	}
	return ah
}

func (ah *AuxPowHeader) BlockHash() common.Hash {
	if ah.inner == nil {
		return common.Hash{}
	}
	return ah.inner.BlockHash()
}

func (ah *AuxPowHeader) PowHash() common.Hash {
	if ah.inner == nil {
		return common.Hash{}
	}
	return ah.inner.PowHash()
}

// Accessor methods that delegate to the inner header
func (ah *AuxPowHeader) Version() int32 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetVersion()
}

func (ah *AuxPowHeader) PrevBlock() [32]byte {
	if ah.inner == nil {
		return [32]byte{}
	}
	return ah.inner.GetPrevBlock()
}

func (ah *AuxPowHeader) MerkleRoot() [32]byte {
	if ah.inner == nil {
		return [32]byte{}
	}
	return ah.inner.GetMerkleRoot()
}

func (ah *AuxPowHeader) Timestamp() uint32 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetTimestamp()
}

func (ah *AuxPowHeader) Bits() uint32 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetBits()
}

func (ah *AuxPowHeader) Nonce() uint32 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetNonce()
}

func (ah *AuxPowHeader) Nonce64() uint64 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetNonce64()
}

func (ah *AuxPowHeader) SealHash() common.Hash {
	if ah.inner == nil {
		return common.Hash{}
	}
	return ah.inner.GetSealHash()
}

func (ah *AuxPowHeader) MixHash() common.Hash {
	if ah.inner == nil {
		return common.Hash{}
	}
	return ah.inner.GetMixHash()
}

func (ah *AuxPowHeader) Height() uint32 {
	if ah.inner == nil {
		return 0
	}
	return ah.inner.GetHeight()
}

func (ah *AuxPowHeader) Bytes() []byte {
	if ah.inner == nil {
		return nil
	}
	var buffer bytes.Buffer
	ah.inner.Serialize(&buffer)
	return buffer.Bytes()
}

func (ah *AuxPowHeader) SetNonce64(nonce uint64) {
	if ah.inner == nil {
		return
	}
	ah.inner.SetNonce64(nonce)
}

func (ah *AuxPowHeader) SetMixHash(mixHash common.Hash) {
	if ah.inner == nil {
		return
	}
	ah.inner.SetMixHash(mixHash)
}

func (ah *AuxPowHeader) SetHeight(height uint32) {
	if ah.inner == nil {
		return
	}
	ah.inner.SetHeight(height)
}

func (ah *AuxPowHeader) SetNonce(nonce uint32) {
	if ah.inner == nil {
		return
	}
	ah.inner.SetNonce(nonce)
}

func (ah *AuxPowHeader) setInner(inner AuxHeaderData) {
	ah.inner = inner
}

func (ah *AuxPowHeader) Copy() *AuxPowHeader {
	if ah.inner == nil {
		return &AuxPowHeader{}
	}
	return &AuxPowHeader{inner: ah.inner.Copy()}
}

func NewAuxPowHeader(inner AuxHeaderData) *AuxPowHeader {
	auxHeader := new(AuxPowHeader)
	auxHeader.setInner(inner)
	return auxHeader
}

type AuxPowTx struct {
	inner AuxPowTxData
}

type AuxPowTxData interface {
	Serialize(w io.Writer) error
	SerializeNoWitness(w io.Writer) error
	Deserialize(r io.Reader) error
	DeserializeNoWitness(r io.Reader) error
	Copy() AuxPowTxData
}

func NewAuxPowCoinbaseTx(powId PowID, height uint32, coinbaseOut []byte, auxMerkleRoot common.Hash, signatureTime uint32) []byte {
	switch powId {
	case Kawpow:
		return NewRavencoinCoinbaseTx(height, coinbaseOut, auxMerkleRoot, signatureTime)
	case SHA_BTC:
		return NewBitcoinCoinbaseTx(height, coinbaseOut, auxMerkleRoot, signatureTime)
	case SHA_BCH:
		return NewBitcoinCashCoinbaseTx(height, coinbaseOut, auxMerkleRoot, signatureTime)
	case Scrypt:
		return NewLitecoinCoinbaseTx(height, coinbaseOut, auxMerkleRoot, signatureTime)
	default:
		return []byte{}
	}
}

// NewAuxPowTx creates an AuxPowTx from an AuxPowTxData implementation
func NewAuxPowTx(inner AuxPowTxData) *AuxPowTx {
	return &AuxPowTx{inner: inner}
}

func (ac *AuxPowTx) Bytes() []byte {
	if ac.inner == nil {
		return nil
	}
	var buffer bytes.Buffer
	ac.inner.SerializeNoWitness(&buffer)
	return buffer.Bytes()
}

func (ac *AuxPowTx) Copy() *AuxPowTx {
	if ac.inner == nil {
		return &AuxPowTx{}
	}
	return &AuxPowTx{inner: ac.inner.Copy()}
}

func (ac *AuxPowTx) Serialize(w io.Writer, witness bool) error {
	if ac.inner == nil {
		return errors.New("inner transaction is nil")
	}
	if witness {
		return ac.inner.Serialize(w)
	}
	return ac.inner.SerializeNoWitness(w)
}

func (ac *AuxPowTx) Deserialize(r io.Reader, witness bool) error {
	if ac.inner == nil {
		return errors.New("inner transaction is nil")
	}
	if witness {
		return ac.inner.Deserialize(r)
	}
	return ac.inner.DeserializeNoWitness(r)
}

// AuxPow represents auxiliary proof-of-work data
type AuxPow struct {
	powID        PowID         // PoW algorithm identifier
	auxPow2      []byte        // Auxiliary proof-of-work data
	header       *AuxPowHeader // donor header
	signature    []byte        // Signature proving the validity of the AuxPow
	merkleBranch [][]byte      // siblings for coinbase index=0 up to root (little endian 32-byte hashes)
	transaction  []byte        // Full coinbase transaction
}

func NewAuxPow(powID PowID, header *AuxPowHeader, auxPow2 []byte, signature []byte, merkleBranch [][]byte, transaction []byte) *AuxPow {
	return &AuxPow{
		powID:        powID,
		auxPow2:      auxPow2,
		header:       header,
		signature:    signature,
		merkleBranch: merkleBranch,
		transaction:  transaction,
	}
}

func (ap *AuxPow) ConvertToTemplate() *AuxTemplate {
	auxTemplate := &AuxTemplate{}
	auxTemplate.SetPowID(ap.powID)
	auxTemplate.SetPrevHash(ap.header.PrevBlock())
	auxTemplate.SetVersion(uint32(ap.header.Version()))
	auxTemplate.SetNBits(ap.header.Bits())
	// Set auxPow2 to empty slice if nil to match subsidy-pool's behavior
	// This ensures consistent protobuf encoding between signing and verification
	if ap.auxPow2 == nil {
		auxTemplate.SetAuxPow2([]byte{})
	} else {
		auxTemplate.SetAuxPow2(ap.auxPow2)
	}

	scriptSig := ExtractScriptSigFromCoinbaseTx(ap.transaction)

	signatureTime, err := ExtractSignatureTimeFromCoinbase(scriptSig)
	if err != nil {
		signatureTime = 0
	}
	auxTemplate.SetSignatureTime(signatureTime)
	// Height is encoded in the script sig for non kawpow chains
	switch ap.powID {
	case Kawpow:
		// For KAWPOW, set height from header
		auxTemplate.SetHeight(ap.header.Height())
	default:
		height, err := ExtractHeightFromCoinbase(scriptSig)
		if err != nil {
			height = 0
		}
		auxTemplate.SetHeight(height)
	}
	auxTemplate.SetCoinbaseOut(ExtractCoinbaseOutFromCoinbaseTx(ap.transaction))
	auxTemplate.SetMerkleBranch(ap.merkleBranch)
	auxTemplate.SetSigs(ap.signature)
	return auxTemplate
}

func (ap *AuxPow) PowID() PowID { return ap.powID }

func (ap *AuxPow) Header() *AuxPowHeader { return ap.header }

func (ap *AuxPow) Signature() []byte { return ap.signature }

func (ap *AuxPow) AuxPow2() []byte { return ap.auxPow2 }

func (ap *AuxPow) MerkleBranch() [][]byte { return ap.merkleBranch }

func (ap *AuxPow) Transaction() []byte { return ap.transaction }

func (ap *AuxPow) SetPowID(id PowID) { ap.powID = id }

func (ap *AuxPow) SetAuxPow2(auxPow2 []byte) { ap.auxPow2 = auxPow2 }

func (ap *AuxPow) SetHeader(header *AuxPowHeader) { ap.header = header }

func (ap *AuxPow) SetSignature(sig []byte) { ap.signature = sig }

func (ap *AuxPow) SetMerkleBranch(branch [][]byte) { ap.merkleBranch = branch }

func (ap *AuxPow) SetTransaction(tx []byte) { ap.transaction = tx }

func CopyAuxPow(ap *AuxPow) *AuxPow {
	if ap == nil {
		return nil
	}

	// Deep copy merkle branch
	merkleBranch := make([][]byte, len(ap.merkleBranch))
	for i, hash := range ap.merkleBranch {
		merkleBranch[i] = make([]byte, len(hash))
		copy(merkleBranch[i], hash)
	}

	// Deep copy signature
	signature := make([]byte, len(ap.signature))
	copy(signature, ap.signature)

	// Deep copy transaction
	transaction := make([]byte, len(ap.transaction))
	copy(transaction, ap.transaction)

	var header *AuxPowHeader
	if ap.header != nil {
		header = ap.header.Copy()
	}
	auxpow2 := make([]byte, len(ap.auxPow2))
	copy(auxpow2, ap.auxPow2)

	return &AuxPow{
		powID:        ap.powID,
		auxPow2:      auxpow2,
		header:       header,
		signature:    signature,
		merkleBranch: merkleBranch,
		transaction:  transaction,
	}
}

// RPCMarshal converts AuxPow to a map for RPC serialization
func (ap *AuxPow) RPCMarshal() map[string]interface{} {
	if ap == nil {
		return nil
	}

	// Convert merkle branch to hex strings
	merkleBranch := make([]hexutil.Bytes, len(ap.merkleBranch))
	for i, hash := range ap.merkleBranch {
		merkleBranch[i] = hexutil.Bytes(hash)
	}

	return map[string]interface{}{
		"powId":        hexutil.Uint64(ap.powID),
		"header":       hexutil.Bytes(ap.header.Bytes()),
		"auxpow2":      hexutil.Bytes(ap.auxPow2),
		"signature":    hexutil.Bytes(ap.signature),
		"merkleBranch": merkleBranch,
		"transaction":  hexutil.Bytes(ap.transaction),
	}
}

// UnmarshalJSON implements json.Unmarshaler for AuxPow
func (ap *AuxPow) UnmarshalJSON(data []byte) error {
	var dec struct {
		PowID        *hexutil.Uint64 `json:"powId"`
		Header       *hexutil.Bytes  `json:"header"`
		Auxpow2      *hexutil.Bytes  `json:"auxpow2"`
		Signature    *hexutil.Bytes  `json:"signature"`
		MerkleBranch []hexutil.Bytes `json:"merkleBranch"`
		Transaction  *hexutil.Bytes  `json:"transaction"`
	}

	if err := json.Unmarshal(data, &dec); err != nil {
		return err
	}

	if dec.PowID == nil {
		return errors.New("missing required fields 'powId' in AuxPow")
	}

	if dec.Header == nil {
		return errors.New("missing required fields 'header' in AuxPow")
	}

	if dec.Auxpow2 == nil {
		return errors.New("missing required fields 'auxpow2' in AuxPow")
	}

	if dec.Signature == nil {
		return errors.New("missing required fields 'signature' in AuxPow")
	}

	if dec.MerkleBranch == nil {
		return errors.New("missing required fields 'merkleBranch' in AuxPow")
	}

	ap.powID = PowID(*dec.PowID)

	switch ap.powID {
	case Kawpow:
		header := &RavencoinBlockHeader{}
		if err := header.Deserialize(bytes.NewReader(*dec.Header)); err != nil {
			return err
		}
		ap.header = NewAuxPowHeader(header)
	case SHA_BTC:
		header := &BitcoinHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(*dec.Header)); err != nil {
			return err
		}
		ap.header = NewAuxPowHeader(header)
	case SHA_BCH:
		header := &BitcoinCashHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(*dec.Header)); err != nil {
			return err
		}
		ap.header = NewAuxPowHeader(header)
	case Scrypt:
		header := &LitecoinHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(*dec.Header)); err != nil {
			return err
		}
		ap.header = NewAuxPowHeader(header)
	}

	// Decode auxpow2
	ap.auxPow2 = *dec.Auxpow2
	// Decode signature
	ap.signature = *dec.Signature
	// Decode merkle branch
	merkleBranch := make([][]byte, len(dec.MerkleBranch))
	for i, hash := range dec.MerkleBranch {
		merkleBranch[i] = hash
	}
	ap.merkleBranch = merkleBranch

	ap.transaction = *dec.Transaction

	return nil
}

// ProtoEncode converts AuxPow to its protobuf representation
func (ap *AuxPow) ProtoEncode() *ProtoAuxPow {
	if ap == nil {
		return nil
	}

	powID := uint32(ap.PowID())

	// Convert merkle branch
	merkleBranch := make([][]byte, len(ap.MerkleBranch()))
	copy(merkleBranch, ap.MerkleBranch())

	return &ProtoAuxPow{
		ChainId:      &powID,
		Auxpow2:      ap.auxPow2,
		Header:       ap.Header().Bytes(),
		Signature:    ap.Signature(),
		MerkleBranch: merkleBranch,
		Transaction:  ap.Transaction(),
	}
}

// ProtoDecode populates AuxPow from its protobuf representation
func (ap *AuxPow) ProtoDecode(data *ProtoAuxPow) error {
	if data == nil {
		return nil
	}

	ap.SetPowID(PowID(data.GetChainId()))

	switch ap.PowID() {
	case Kawpow:
		header := &RavencoinBlockHeader{}
		if err := header.Deserialize(bytes.NewReader(data.GetHeader())); err != nil {
			return err
		}
		ap.SetHeader(NewAuxPowHeader(header))
	case SHA_BTC:
		header := &BitcoinHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(data.GetHeader())); err != nil {
			return err
		}
		ap.SetHeader(NewAuxPowHeader(header))
	case SHA_BCH:
		header := &BitcoinCashHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(data.GetHeader())); err != nil {
			return err
		}
		ap.SetHeader(NewAuxPowHeader(header))
	case Scrypt:
		header := &LitecoinHeaderWrapper{}
		if err := header.Deserialize(bytes.NewReader(data.GetHeader())); err != nil {
			return err
		}
		ap.SetHeader(NewAuxPowHeader(header))
	default:
		return errors.New("unsupported powId for AuxPow header")
	}
	ap.SetSignature(data.GetSignature())

	// Decode merkle branch
	ap.merkleBranch = make([][]byte, len(data.GetMerkleBranch()))
	for i, hash := range data.GetMerkleBranch() {
		ap.merkleBranch[i] = make([]byte, len(hash))
		copy(ap.merkleBranch[i], hash)
	}

	ap.transaction = data.GetTransaction()
	ap.auxPow2 = data.GetAuxpow2()

	return nil
}

func AuxPowTxHash(PowID PowID, tx []byte) common.Hash {
	switch PowID {
	case SHA_BTC, Kawpow:
		return common.Hash(btcchainhash.DoubleHashH(tx))
	case SHA_BCH:
		return common.Hash(bchchainhash.DoubleHashH(tx))
	case Scrypt:
		return common.Hash(ltcchainhash.DoubleHashH(tx))
	}
	return common.Hash{}
}

func reverseBytesCopy(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
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
