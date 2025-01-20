package types

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
)

const (
	MaxDenomination = 14

	MaxOutputIndex      = math.MaxUint16
	MaxTrimDenomination = 5
)

var (
	MaxQi                   = new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(params.Ether)) // This is just a default; determine correct value later
	QuaiToQiConversionTopic = crypto.Keccak256Hash([]byte("QuaiToQiConversion"))
	QuaiCoinbaseLockupTopic = crypto.Keccak256Hash([]byte("QuaiCoinbaseLockup"))
	QiCoinbaseLockupTopic   = crypto.Keccak256Hash([]byte("QiCoinbaseLockup"))
)

// Denominations is a map of denomination to number of Qi
var Denominations map[uint8]*big.Int

var TrimDepths map[uint8]uint64

func init() {
	// Initialize denominations
	Denominations = make(map[uint8]*big.Int)
	Denominations[0] = big.NewInt(1)           // 0.001 Qi
	Denominations[1] = big.NewInt(5)           // 0.005 Qi
	Denominations[2] = big.NewInt(10)          // 0.01 Qi
	Denominations[3] = big.NewInt(50)          // 0.05 Qi
	Denominations[4] = big.NewInt(100)         // 0.1 Qi
	Denominations[5] = big.NewInt(500)         // 0.5 Qi
	Denominations[6] = big.NewInt(1000)        // 1 Qi
	Denominations[7] = big.NewInt(5000)        // 5 Qi
	Denominations[8] = big.NewInt(10000)       // 10 Qi
	Denominations[9] = big.NewInt(20000)       // 20 Qi
	Denominations[10] = big.NewInt(100000)     // 100 Qi
	Denominations[11] = big.NewInt(1000000)    // 1000 Qi
	Denominations[12] = big.NewInt(10000000)   // 10000 Qi
	Denominations[13] = big.NewInt(100000000)  // 100000 Qi
	Denominations[14] = big.NewInt(1000000000) // 1000000 Qi

	TrimDepths = make(map[uint8]uint64)
	TrimDepths[0] = 720  // 2 hours after fork starts from block 1
	TrimDepths[1] = 720  // 2 hours
	TrimDepths[2] = 1080 // 3 hours
	TrimDepths[3] = 1080 // 3 hours
	TrimDepths[4] = 2160 // 6 hours
	TrimDepths[5] = 4320 // 12 hours
}

type TxIns []TxIn

func (txIns TxIns) ProtoEncode() (*ProtoTxIns, error) {
	protoTxIns := &ProtoTxIns{}
	for _, txIn := range txIns {
		protoTxIn, err := txIn.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTxIns.TxIns = append(protoTxIns.TxIns, protoTxIn)
	}
	return protoTxIns, nil
}

func (txIns *TxIns) ProtoDecode(protoTxIns *ProtoTxIns) error {
	for _, protoTxIn := range protoTxIns.TxIns {
		decodedTxIn := &TxIn{}
		err := decodedTxIn.ProtoDecode(protoTxIn)
		if err != nil {
			return err
		}
		*txIns = append(*txIns, *decodedTxIn)
	}
	return nil
}

// TxIn defines a Qi transaction input
type TxIn struct {
	PreviousOutPoint OutPoint `json:"previousOutPoint"`
	PubKey           []byte   `json:"pubKey"`
}

func (txIn TxIn) ProtoEncode() (*ProtoTxIn, error) {
	protoTxIn := &ProtoTxIn{}

	var err error
	protoTxIn.PreviousOutPoint, err = txIn.PreviousOutPoint.ProtoEncode()
	if err != nil {
		return nil, err
	}

	// Compress the public key if it's uncompressed
	compressedPubKey, err := compressPubKeyIfNeeded(txIn.PubKey)
	if err != nil {
		return nil, err
	}
	protoTxIn.PubKey = compressedPubKey

	return protoTxIn, nil
}

func (txIn *TxIn) ProtoDecode(protoTxIn *ProtoTxIn) error {
	err := txIn.PreviousOutPoint.ProtoDecode(protoTxIn.PreviousOutPoint)
	if err != nil {
		return err
	}

	// Decompress the public key if it's compressed
	pubKey, err := decompressPubKeyIfNeeded(protoTxIn.PubKey)
	if err != nil {
		return err
	}
	txIn.PubKey = pubKey

	return nil
}

// decompressPubKeyIfNeeded decompresses the public key if it's in compressed format
func decompressPubKeyIfNeeded(pubKey []byte) ([]byte, error) {
	switch len(pubKey) {
	case 33: // Compressed public key
		uncompressedPubKey, err := crypto.DecompressPubkey(pubKey)
		if err != nil {
			return nil, err
		}
		return crypto.FromECDSAPub(uncompressedPubKey), nil
	case 65: // Uncompressed public key
		return pubKey, nil
	default:
		return nil, errors.New("invalid public key length")
	}
}

// compressPubKeyIfNeeded compresses the public key if it's in uncompressed format
func compressPubKeyIfNeeded(pubKey []byte) ([]byte, error) {
	switch len(pubKey) {
	case 65: // Uncompressed public key
		uncompressedPubKey, err := crypto.UnmarshalPubkey(pubKey)
		if err != nil {
			return nil, err
		}
		return crypto.CompressPubkey(uncompressedPubKey), nil
	case 33: // Already compressed public key
		return pubKey, nil
	default:
		return nil, errors.New("invalid public key length")
	}
}

// OutPoint defines a Qi data type that is used to track previous outputs
type OutPoint struct {
	TxHash common.Hash `json:"txHash"`
	Index  uint16      `json:"index"`
}

func (outPoint OutPoint) Key() string {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, outPoint.Index)
	bytes := append(indexBytes, outPoint.TxHash.Bytes()...)
	return common.Bytes2Hex(bytes)
}

func (outPoint OutPoint) ProtoEncode() (*ProtoOutPoint, error) {
	protoOutPoint := &ProtoOutPoint{}
	protoOutPoint.Hash = outPoint.TxHash.ProtoEncode()
	index := uint32(outPoint.Index)
	protoOutPoint.Index = &index
	return protoOutPoint, nil
}

func (outPoint *OutPoint) ProtoDecode(protoOutPoint *ProtoOutPoint) error {
	outPoint.TxHash.ProtoDecode(protoOutPoint.Hash)
	outPoint.Index = uint16(*protoOutPoint.Index)
	return nil
}

type OutpointAndDenomination struct {
	TxHash       common.Hash `json:"txHash"`
	Index        uint16      `json:"index"`
	Denomination uint8       `json:"denomination"`
	Lock         *big.Int    `json:"lock"`
}

func (outpoint *OutpointAndDenomination) UnmarshalJSON(input []byte) error {
	var dec struct {
		Txhash       *common.Hash    `json:"txHash"`
		Index        *hexutil.Uint64 `json:"index"`
		Denomination *hexutil.Uint64 `json:"denomination"`
		Lock         *hexutil.Big    `json:"lock"`
	}
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.Txhash == nil {
		return errors.New("missing txHash")
	}
	if dec.Index == nil {
		return errors.New("missing index")
	}
	if dec.Denomination == nil {
		return errors.New("missing denomination")
	}
	outpoint.TxHash = *dec.Txhash
	outpoint.Index = uint16(*dec.Index)
	outpoint.Denomination = uint8(*dec.Denomination)
	if dec.Lock != nil {
		outpoint.Lock = dec.Lock.ToInt()
	} else {
		outpoint.Lock = big.NewInt(0)
	}
	return nil
}

func (outPoint OutpointAndDenomination) Key() string {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, outPoint.Index)
	bytes := append(indexBytes, outPoint.TxHash.Bytes()...)
	return common.Bytes2Hex(bytes)
}

func (outPoint OutpointAndDenomination) ProtoEncode() (*ProtoOutPointAndDenomination, error) {
	protoOutPoint := &ProtoOutPointAndDenomination{}
	protoOutPoint.Hash = outPoint.TxHash.ProtoEncode()
	index := uint32(outPoint.Index)
	protoOutPoint.Index = &index
	denomination := uint32(outPoint.Denomination)
	protoOutPoint.Denomination = &denomination
	if outPoint.Lock != nil {
		protoOutPoint.Lock = outPoint.Lock.Bytes()
	}

	return protoOutPoint, nil
}

func (outPoint *OutpointAndDenomination) ProtoDecode(protoOutPoint *ProtoOutPointAndDenomination) error {
	outPoint.TxHash.ProtoDecode(protoOutPoint.Hash)
	outPoint.Index = uint16(*protoOutPoint.Index)
	outPoint.Denomination = uint8(*protoOutPoint.Denomination)
	if protoOutPoint.Lock != nil {
		outPoint.Lock = new(big.Int).SetBytes(protoOutPoint.Lock)
	} else {
		outPoint.Lock = big.NewInt(0)
	}
	return nil
}

// NewOutPoint returns a new Qi transaction outpoint point with the
// provided hash and index.
func NewOutPoint(txHash *common.Hash, index uint16) *OutPoint {
	return &OutPoint{
		TxHash: *txHash,
		Index:  index,
	}
}

// NewTxIn returns a new bitcoin transaction input with the provided
// previous outpoint point and signature script with a default sequence of
// MaxTxInSequenceNum.
func NewTxIn(prevOut *OutPoint, pubkey []byte, witness [][]byte) *TxIn {
	return &TxIn{
		PreviousOutPoint: *prevOut,
		PubKey:           pubkey,
	}
}

type TxOuts []TxOut

func (txOuts TxOuts) ProtoEncode() (*ProtoTxOuts, error) {
	protoTxOuts := &ProtoTxOuts{}
	for _, txOut := range txOuts {
		protoTxOut, err := txOut.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoTxOuts.TxOuts = append(protoTxOuts.TxOuts, protoTxOut)
	}
	return protoTxOuts, nil
}

func (txOuts *TxOuts) ProtoDecode(protoTxOuts *ProtoTxOuts) error {
	*txOuts = make(TxOuts, 0, len(protoTxOuts.TxOuts))
	for _, protoTxOut := range protoTxOuts.TxOuts {
		decodedTxOut := &TxOut{}
		err := decodedTxOut.ProtoDecode(protoTxOut)
		if err != nil {
			return err
		}
		*txOuts = append(*txOuts, *decodedTxOut)
	}
	return nil
}

// TxOut defines a Qi transaction output.
type TxOut struct {
	Denomination uint8
	Address      []byte
	Lock         *big.Int // Block height the entry unlocks. 0 or nil = unlocked
}

func (txOut TxOut) ProtoEncode() (*ProtoTxOut, error) {
	protoTxOut := &ProtoTxOut{}

	denomination := uint32(txOut.Denomination)
	protoTxOut.Denomination = &denomination
	protoTxOut.Address = txOut.Address
	if txOut.Lock == nil {
		protoTxOut.Lock = big.NewInt(0).Bytes()
	} else {
		protoTxOut.Lock = txOut.Lock.Bytes()
	}
	return protoTxOut, nil
}

func (txOut *TxOut) ProtoDecode(protoTxOut *ProtoTxOut) error {
	// check if protoTxOut.Denomination is above the max uint8 value
	if protoTxOut.Denomination == nil {
		return errors.New("protoTxOut.Denomination is nil")
	}
	if *protoTxOut.Denomination > math.MaxUint8 {
		return errors.New("protoTxOut.Denomination is above the max uint8 value")
	}
	txOut.Denomination = uint8(protoTxOut.GetDenomination())
	txOut.Address = protoTxOut.Address
	txOut.Lock = new(big.Int).SetBytes(protoTxOut.Lock)
	return nil
}

// NewTxOut returns a new Qi transaction output with the provided
// transaction value and address.
func NewTxOut(denomination uint8, address []byte, lock *big.Int) *TxOut {
	return &TxOut{
		Denomination: denomination,
		Address:      address,
		Lock:         lock,
	}
}

type RPCTxIn struct {
	PreviousOutPoint OutpointJSON  `json:"previousOutPoint"`
	PubKey           hexutil.Bytes `json:"pubKey"`
}

type RPCTxOut struct {
	Denomination hexutil.Uint            `json:"denomination"`
	Address      common.MixedcaseAddress `json:"address"`
	Lock         *hexutil.Big            `json:"lock"`
}

// UtxoEntry houses details about an individual transaction output in a utxo
// view such as whether or not it was contained in a coinbase tx, the height of
// the block that contains the tx, whether or not it is spent, its public key
// script, and how much it pays.
type UtxoEntry struct {
	Denomination uint8    `json:"denomination"`
	Address      []byte   `json:"address"` // The address of the output holder.
	Lock         *big.Int `json:"lock"`    // Block height the entry unlocks. 0 = unlocked
}

// SpentUtxoEntry houses details about a spent UtxoEntry.
type SpentUtxoEntry struct {
	OutPoint
	*UtxoEntry
}

func (sutxo *SpentUtxoEntry) ProtoEncode() (*ProtoSpentUTXO, error) {
	protoSpentUtxoEntry := &ProtoSpentUTXO{}

	protoOutPoint, err := sutxo.OutPoint.ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoSpentUtxoEntry.Outpoint = protoOutPoint

	protoUtxoEntry, err := sutxo.UtxoEntry.ProtoEncode()
	if err != nil {
		return nil, err
	}
	protoSpentUtxoEntry.Sutxo = protoUtxoEntry

	return protoSpentUtxoEntry, nil
}

func (sutxo *SpentUtxoEntry) ProtoDecode(protoSpentUtxoEntry *ProtoSpentUTXO) error {
	if err := sutxo.OutPoint.ProtoDecode(protoSpentUtxoEntry.Outpoint); err != nil {
		return err
	}
	sutxo.UtxoEntry = &UtxoEntry{}
	if err := sutxo.UtxoEntry.ProtoDecode(protoSpentUtxoEntry.Sutxo); err != nil {
		return err
	}
	return nil
}

// NewUtxoEntry returns a new UtxoEntry built from the arguments.
func NewUtxoEntry(txOut *TxOut) *UtxoEntry {
	return &UtxoEntry{
		Denomination: txOut.Denomination,
		Address:      txOut.Address,
		Lock:         txOut.Lock,
	}
}

type AddressUtxos struct {
	Address common.Address
	Utxos   []*UtxoEntry
}

func (utxo *UtxoEntry) ProtoEncode() (*ProtoTxOut, error) {
	protoTxOut := &ProtoTxOut{}

	denomination := uint32(utxo.Denomination)
	protoTxOut.Denomination = &denomination
	protoTxOut.Address = utxo.Address
	if utxo.Lock == nil {
		protoTxOut.Lock = big.NewInt(0).Bytes()
	} else {
		protoTxOut.Lock = utxo.Lock.Bytes()
	}
	return protoTxOut, nil
}

func (utxo *UtxoEntry) ProtoDecode(protoTxOut *ProtoTxOut) error {
	// check if protoTxOut.Denomination is above the max uint8 value
	if *protoTxOut.Denomination > math.MaxUint8 {
		return errors.New("protoTxOut.Denomination is above the max uint8 value")
	}
	utxo.Denomination = uint8(protoTxOut.GetDenomination())
	utxo.Address = protoTxOut.Address
	if protoTxOut.Lock == nil {
		utxo.Lock = nil
	} else {
		utxo.Lock = new(big.Int).SetBytes(protoTxOut.Lock)
	}
	return nil
}

func (utxo *UtxoEntry) UnmarshalJSON(input []byte) error {
	var dec struct {
		Denomination *hexutil.Uint64 `json:"denomination"`
		Address      *string         `json:"address"`
		Lock         *hexutil.Big    `json:"lock"`
	}
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.Denomination == nil {
		return errors.New("missing denomination")
	}
	if dec.Address == nil {
		return errors.New("missing address")
	}
	utxo.Denomination = uint8(*dec.Denomination)
	utxo.Address = common.HexToAddressBytes(*dec.Address).Bytes()
	if dec.Lock != nil {
		utxo.Lock = dec.Lock.ToInt()
	} else {
		utxo.Lock = big.NewInt(0)
	}
	return nil
}

func UTXOHash(txHash common.Hash, index uint16, utxo *UtxoEntry) common.Hash {
	indexBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(indexBytes, index)
	return RlpHash([]interface{}{txHash, indexBytes, utxo}) // TODO: Consider encoding to protobuf instead
}

func CoinbaseLockupHash(ownerContract common.Address, beneficiaryMiner common.Address, delegate common.Address, lockupByte byte, epoch uint32, balance *big.Int, unlockHeight uint32, elements uint16) common.Hash {
	return RlpHash([]interface{}{ownerContract, beneficiaryMiner, delegate, lockupByte, epoch, balance, unlockHeight, elements}) // TODO: Consider encoding to protobuf instead
}
