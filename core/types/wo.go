package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/params"
	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"
)

var ObjectPool = sync.Pool{
	New: func() interface{} {
		return new(interface{})
	},
}

type WorkObject struct {
	woHeader *WorkObjectHeader
	woBody   *WorkObjectBody
	tx       *Transaction

	// caches
	appendTime                atomic.Value
	stateProcessTime          atomic.Value
	pendingHeaderCreationTime atomic.Value

	// These fields are used to track
	// inter-peer block relay.
	ReceivedAt   time.Time
	ReceivedFrom interface{}
}

type WorkObjectHeader struct {
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
	auxPow              *AuxPow // New field for auxiliary proof-of-work
	scryptDiffAndCount  *PowShareDiffAndCount
	shaDiffAndCount     *PowShareDiffAndCount
	shaShareTarget      *big.Int
	scryptShareTarget   *big.Int
	kawpowDifficulty    *big.Int
	PowHash             atomic.Value
	PowDigest           atomic.Value
}

type WorkObjects []*WorkObject

type WorkObjectView int

// Work object types
const (
	BlockObject WorkObjectView = iota
	BlockObjects
	PEtxObject
	HeaderObject
	WorkShareObject
	WorkShareTxObject
)

type PowShareDiffAndCount struct {
	difficulty *big.Int
	count      *big.Int
	uncled     *big.Int
}

func NewPowShareDiffAndCount(difficulty *big.Int, count *big.Int, uncled *big.Int) *PowShareDiffAndCount {
	return &PowShareDiffAndCount{
		difficulty: difficulty,
		count:      count,
		uncled:     uncled,
	}
}

func (p *PowShareDiffAndCount) RPCMarshal() map[string]interface{} {
	if p == nil {
		return nil
	}
	result := make(map[string]interface{})
	if p.difficulty != nil {
		result["difficulty"] = (*hexutil.Big)(p.Difficulty())
	}
	if p.count != nil {
		result["count"] = (*hexutil.Big)(p.Count())
	}
	if p.uncled != nil {
		result["uncled"] = (*hexutil.Big)(p.Uncled())
	}
	return result
}

func (p *PowShareDiffAndCount) UnmarshalJSON(data []byte) error {

	var dec struct {
		Difficulty *hexutil.Big `json:"difficulty"`
		Count      *hexutil.Big `json:"count"`
		Uncled     *hexutil.Big `json:"uncled"`
	}

	if err := json.Unmarshal(data, &dec); err != nil {
		return err
	}

	if dec.Difficulty != nil {
		p.SetDifficulty((*big.Int)(dec.Difficulty))
	}
	if dec.Count != nil {
		p.SetCount((*big.Int)(dec.Count))
	}
	if dec.Uncled != nil {
		p.SetUncled((*big.Int)(dec.Uncled))
	}
	return nil
}

func (p *PowShareDiffAndCount) Cmp(other *PowShareDiffAndCount) bool {
	if p == nil || other == nil {
		return false
	}
	if p.difficulty == nil || other.difficulty == nil {
		return false
	}
	if p.count == nil || other.count == nil {
		return false
	}
	if p.uncled == nil || other.uncled == nil {
		return false
	}
	return p.difficulty.Cmp(other.difficulty) == 0 && p.count.Cmp(other.count) == 0 && p.uncled.Cmp(other.uncled) == 0
}

func (p *PowShareDiffAndCount) Clone() *PowShareDiffAndCount {
	var difficulty *big.Int
	if p.difficulty != nil {
		difficulty = new(big.Int).Set(p.difficulty)
	}
	var count *big.Int
	if p.count != nil {
		count = new(big.Int).Set(p.count)
	}
	var uncled *big.Int
	if p.uncled != nil {
		uncled = new(big.Int).Set(p.uncled)
	}
	return &PowShareDiffAndCount{
		difficulty: difficulty,
		count:      count,
		uncled:     uncled,
	}
}

func (p *PowShareDiffAndCount) Difficulty() *big.Int {
	return p.difficulty
}

func (p *PowShareDiffAndCount) Count() *big.Int {
	return p.count
}

func (p *PowShareDiffAndCount) Uncled() *big.Int {
	return p.uncled
}

func (p *PowShareDiffAndCount) SetDifficulty(difficulty *big.Int) {
	p.difficulty = difficulty
}

func (p *PowShareDiffAndCount) SetCount(count *big.Int) {
	p.count = count
}

func (p *PowShareDiffAndCount) SetUncled(uncled *big.Int) {
	p.uncled = uncled
}

func (p *PowShareDiffAndCount) ProtoEncode() *ProtoPowShareDiffAndCount {
	if p == nil {
		return nil
	}
	if p.difficulty == nil || p.count == nil || p.uncled == nil {
		return nil
	}
	protoShare := &ProtoPowShareDiffAndCount{}

	// Encode zero explicitly so it round-trips as 0 instead of nil.
	if p.difficulty.Sign() == 0 {
		protoShare.Difficulty = []byte{0}
	} else {
		protoShare.Difficulty = p.difficulty.Bytes()
	}
	if p.count.Sign() == 0 {
		protoShare.Count = []byte{0}
	} else {
		protoShare.Count = p.count.Bytes()
	}
	if p.uncled.Sign() == 0 {
		protoShare.Uncled = []byte{0}
	} else {
		protoShare.Uncled = p.uncled.Bytes()
	}
	return protoShare
}

func (p *PowShareDiffAndCount) ProtoDecode(protoShare *ProtoPowShareDiffAndCount) {
	if p == nil {
		return
	}
	if protoShare == nil {
		p.difficulty = nil
		p.count = nil
		p.uncled = nil
		return
	}
	if protoShare.GetDifficulty() != nil {
		p.difficulty = new(big.Int).SetBytes(protoShare.GetDifficulty())
	} else {
		p.difficulty = nil
	}
	if protoShare.GetCount() != nil {
		p.count = new(big.Int).SetBytes(protoShare.GetCount())
	} else {
		p.count = nil
	}
	if protoShare.GetUncled() != nil {
		p.uncled = new(big.Int).SetBytes(protoShare.GetUncled())
	} else {
		p.uncled = nil
	}
}

type WorkShareValidity int

const (
	Valid WorkShareValidity = iota
	Sub
	Invalid
	Block
)

func (wo *WorkObject) Hash() common.Hash {
	return wo.WorkObjectHeader().Hash()
}

func (wo *WorkObject) WorkObjectHeaderHash() common.Hash {
	return wo.WorkObjectHeader().Hash()
}

func (wo *WorkObject) SealHash() common.Hash {
	return wo.WorkObjectHeader().SealHash()
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Getters ///////////////
////////////////////////////////////////////////////////////

func (wo *WorkObject) WorkObjectHeader() *WorkObjectHeader {
	return wo.woHeader
}

func (wo *WorkObject) Body() *WorkObjectBody {
	return wo.woBody
}

func (wo *WorkObject) Tx() *Transaction {
	return wo.tx
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Setters ///////////////
////////////////////////////////////////////////////////////

func (wo *WorkObject) SetWorkObjectHeader(header *WorkObjectHeader) {
	wo.woHeader = header
}

func (wo *WorkObject) SetBody(body *WorkObjectBody) {
	wo.woBody = body
}

func (wo *WorkObject) SetTx(tx *Transaction) {
	wo.tx = tx
}

func (wo *WorkObject) SetAppendTime(appendTime time.Duration) {
	wo.appendTime.Store(appendTime)
}

func (wo *WorkObject) SetStateProcessTime(stateProcessTimes time.Duration) {
	wo.stateProcessTime.Store(stateProcessTimes)
}

func (wo *WorkObject) SetPendingHeaderCreationTime(pendingHeaderCreationTime time.Duration) {
	wo.pendingHeaderCreationTime.Store(pendingHeaderCreationTime)
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Generic Getters ///////////////
////////////////////////////////////////////////////////////

// GetAppendTime returns the appendTime of the block
// The appendTime is computed on the first call and cached thereafter.
func (wo *WorkObject) GetAppendTime() time.Duration {
	if appendTime := wo.appendTime.Load(); appendTime != nil {
		if val, ok := appendTime.(time.Duration); ok {
			return val
		}
	}
	return -1
}

// GetStateProcessTime returns the stateProcessTIme of the block
// The stateProcessTime is computed on the first call and cached thereafter.
func (wo *WorkObject) GetStateProcessTime() time.Duration {
	if stateProcessTime := wo.stateProcessTime.Load(); stateProcessTime != nil {
		if val, ok := stateProcessTime.(time.Duration); ok {
			return val
		}
	}
	return -1
}

// GetPendingHeaderCreationTime returns the pendingHeaderTime of the block
// The pendingHeaderTime is computed on the first call and cached thereafter.
func (wo *WorkObject) GetPendingHeaderCreationTime() time.Duration {
	if pendingHeaderCreationTime := wo.appendTime.Load(); pendingHeaderCreationTime != nil {
		if val, ok := pendingHeaderCreationTime.(time.Duration); ok {
			return val
		}
	}
	return -1
}

// Size returns the true RLP encoded storage size of the block, either by encoding
// and returning it, or returning a previsouly cached value.
func (wo *WorkObject) Size() common.StorageSize {
	protoWorkObject, err := wo.ProtoEncode(BlockObject)
	if err != nil {
		return common.StorageSize(0)
	}
	data, err := proto.Marshal(protoWorkObject)
	if err != nil {
		return common.StorageSize(0)
	}
	return common.StorageSize(len(data))
}

func (wo *WorkObject) HeaderHash() common.Hash {
	return wo.WorkObjectHeader().HeaderHash()
}

func (wo *WorkObject) Difficulty() *big.Int {
	return wo.WorkObjectHeader().Difficulty()
}

func (wo *WorkObject) PrimeTerminusNumber() *big.Int {
	return wo.WorkObjectHeader().PrimeTerminusNumber()
}

func (wo *WorkObject) TxHash() common.Hash {
	return wo.WorkObjectHeader().TxHash()
}

func (wo *WorkObject) PrimaryCoinbase() common.Address {
	return wo.WorkObjectHeader().PrimaryCoinbase()
}

func (wo *WorkObject) Data() []byte {
	return wo.WorkObjectHeader().Data()
}

func (wo *WorkObject) MixHash() common.Hash {
	return wo.WorkObjectHeader().MixHash()
}

func (wo *WorkObject) Nonce() BlockNonce {
	return wo.WorkObjectHeader().Nonce()
}

func (wo *WorkObject) Lock() uint8 {
	return wo.WorkObjectHeader().Lock()
}

func (wo *WorkObject) Location() common.Location {
	return wo.WorkObjectHeader().Location()
}

func (wo *WorkObject) Time() uint64 {
	return wo.WorkObjectHeader().Time()
}

// New fields that were added on the kawpow fork block
func (wo *WorkObject) AuxPow() *AuxPow {
	return wo.WorkObjectHeader().AuxPow()
}

func (wo *WorkObject) ShaDiffAndCount() *PowShareDiffAndCount {
	return wo.WorkObjectHeader().ShaDiffAndCount()
}

func (wo *WorkObject) ScryptDiffAndCount() *PowShareDiffAndCount {
	return wo.WorkObjectHeader().ScryptDiffAndCount()
}

func (wo *WorkObject) ShaShareTarget() *big.Int {
	return wo.WorkObjectHeader().ShaShareTarget()
}

func (wo *WorkObject) ScryptShareTarget() *big.Int {
	return wo.WorkObjectHeader().ScryptShareTarget()
}

func (wo *WorkObject) KawpowDifficulty() *big.Int {
	return wo.WorkObjectHeader().KawpowDifficulty()
}

func (wo *WorkObject) Header() *Header {
	if wo.Body() == nil {
		return nil
	}
	return wo.Body().Header()
}

func (wo *WorkObject) ParentHash(nodeCtx int) common.Hash {
	if nodeCtx == common.ZONE_CTX {
		return wo.WorkObjectHeader().ParentHash()
	} else {
		return wo.Body().Header().ParentHash(nodeCtx)
	}
}

func (wo *WorkObject) Number(nodeCtx int) *big.Int {
	if nodeCtx == common.ZONE_CTX {
		return wo.WorkObjectHeader().Number()
	} else {
		return wo.Body().Header().Number(nodeCtx)
	}
}

func (wo *WorkObject) NumberU64(nodeCtx int) uint64 {
	if nodeCtx == common.ZONE_CTX {
		return wo.WorkObjectHeader().NumberU64()
	} else {
		return wo.Body().Header().NumberU64(nodeCtx)
	}
}

func (wo *WorkObject) NonceU64() uint64 {
	return wo.WorkObjectHeader().Nonce().Uint64()
}

func (wo *WorkObject) QuaiStateSize() *big.Int {
	return wo.Header().QuaiStateSize()
}

func (wo *WorkObject) UncledEntropy() *big.Int {
	return wo.Header().UncledEntropy()
}

func (wo *WorkObject) EVMRoot() common.Hash {
	return wo.Header().EVMRoot()
}

func (wo *WorkObject) ParentEntropy(nodeCtx int) *big.Int {
	return wo.Header().ParentEntropy(nodeCtx)
}

func (wo *WorkObject) EtxRollupHash() common.Hash {
	return wo.Header().EtxRollupHash()
}

func (wo *WorkObject) EtxSetRoot() common.Hash {
	return wo.Header().EtxSetRoot()
}

func (wo *WorkObject) BaseFee() *big.Int {
	return wo.Header().BaseFee()
}

func (wo *WorkObject) StateLimit() uint64 {
	return wo.Header().StateLimit()
}

func (wo *WorkObject) StateUsed() uint64 {
	return wo.Header().StateUsed()
}

func (wo *WorkObject) GasUsed() uint64 {
	return wo.Header().GasUsed()
}

func (wo *WorkObject) GasLimit() uint64 {
	return wo.Header().GasLimit()
}

func (wo *WorkObject) ManifestHash(nodeCtx int) common.Hash {
	return wo.Header().ManifestHash(nodeCtx)
}

func (wo *WorkObject) ParentDeltaEntropy(nodeCtx int) *big.Int {
	return wo.Header().ParentDeltaEntropy(nodeCtx)
}

func (wo *WorkObject) ParentUncledDeltaEntropy(nodeCtx int) *big.Int {
	return wo.Header().ParentUncledDeltaEntropy(nodeCtx)
}

func (wo *WorkObject) UncleHash() common.Hash {
	return wo.Header().UncleHash()
}

func (wo *WorkObject) OutboundEtxHash() common.Hash {
	return wo.Header().OutboundEtxHash()
}

func (wo *WorkObject) ReceiptHash() common.Hash {
	return wo.Header().ReceiptHash()
}

func (wo *WorkObject) Extra() []byte {
	return wo.Header().Extra()
}

func (wo *WorkObject) UTXORoot() common.Hash {
	return wo.Header().UTXORoot()
}

func (wo *WorkObject) EfficiencyScore() uint16 {
	return wo.Header().EfficiencyScore()
}

func (wo *WorkObject) ThresholdCount() uint16 {
	return wo.Header().ThresholdCount()
}

func (wo *WorkObject) ExpansionNumber() uint8 {
	return wo.Header().ExpansionNumber()
}

func (wo *WorkObject) EtxEligibleSlices() common.Hash {
	return wo.Header().EtxEligibleSlices()
}

func (wo *WorkObject) InterlinkRootHash() common.Hash {
	return wo.Header().InterlinkRootHash()
}

func (wo *WorkObject) PrimeTerminusHash() common.Hash {
	return wo.Header().PrimeTerminusHash()
}
func (wo *WorkObject) Transactions() Transactions {
	return wo.Body().Transactions()
}

func (wo *WorkObject) OutboundEtxs() Transactions {
	return wo.Body().OutboundEtxs()
}

func (wo *WorkObject) Uncles() []*WorkObjectHeader {
	return wo.Body().Uncles()
}

func (wo *WorkObject) Manifest() BlockManifest {
	return wo.Body().Manifest()
}

func (wo *WorkObject) InterlinkHashes() common.Hashes {
	return wo.Body().InterlinkHashes()
}

func (wo *WorkObject) ExchangeRate() *big.Int {
	return wo.Header().ExchangeRate()
}

func (wo *WorkObject) AvgTxFees() *big.Int {
	return wo.Header().AvgTxFees()
}

func (wo *WorkObject) TotalFees() *big.Int {
	return wo.Header().TotalFees()
}

func (wo *WorkObject) KQuaiDiscount() *big.Int {
	return wo.Header().KQuaiDiscount()
}

func (wo *WorkObject) ConversionFlowAmount() *big.Int {
	return wo.Header().ConversionFlowAmount()
}

func (wo *WorkObject) MinerDifficulty() *big.Int {
	return wo.Header().MinerDifficulty()
}

func (wo *WorkObject) PrimeStateRoot() common.Hash {
	return wo.Header().PrimeStateRoot()
}

func (wo *WorkObject) RegionStateRoot() common.Hash {
	return wo.Header().RegionStateRoot()
}

func (wo *WorkObject) QiTransactions() []*Transaction {
	qiTxs := make([]*Transaction, 0)
	for _, t := range wo.Transactions() {
		if t.Type() == QiTxType {
			qiTxs = append(qiTxs, t)
		}
	}
	return qiTxs
}

func (wo *WorkObject) QiTransactionsWithoutCoinbase() []*Transaction {
	// TODO: cache the UTXO loop
	qiTxs := make([]*Transaction, 0)
	for _, t := range wo.Transactions() {
		if t.Type() == QiTxType {
			qiTxs = append(qiTxs, t)
		}
	}
	return qiTxs
}

func (wo *WorkObject) TransactionsInfo() map[string]interface{} {
	txInfo := make(map[string]interface{})
	txInfo["hash"] = wo.Hash()
	var inputs, outputs int
	var quai, qi, etxOutBound, etxInbound, coinbaseOutboundEtx, coinbaseInboundEtx, conversionOutboundEtx, conversionInboundEtx int
	var txCount int
	for _, tx := range wo.Transactions() {
		txCount++
		if tx.Type() == QuaiTxType {
			quai++
		} else if tx.Type() == QiTxType {
			qi++
			inputs += len(tx.TxIn())
			outputs += len(tx.TxOut())
		} else if tx.Type() == ExternalTxType {
			etxInbound++
			if IsCoinBaseTx(tx) {
				coinbaseInboundEtx++
			} else if IsConversionTx(tx) {
				conversionInboundEtx++
			}
		}
	}
	for _, etx := range wo.OutboundEtxs() {
		etxOutBound++
		if IsCoinBaseTx(etx) {
			coinbaseOutboundEtx++
		} else if IsConversionTx(etx) {
			conversionOutboundEtx++
		}
	}
	txInfo["txs"] = txCount
	txInfo["quai"] = quai
	txInfo["qi"] = qi
	txInfo["net input/outputs"] = outputs - inputs
	txInfo["etxInbound"] = etxInbound
	txInfo["etxOutbound"] = etxOutBound
	txInfo["coinbaseEtxOutbound"] = coinbaseOutboundEtx
	txInfo["coinbaseEtxInbound"] = coinbaseInboundEtx
	txInfo["conversionEtxOutbound"] = conversionOutboundEtx
	txInfo["conversionEtxInbound"] = conversionInboundEtx
	txInfo["baseFee"] = wo.BaseFee()
	return txInfo
}

func (wo *WorkObject) ParentHashArray() []common.Hash {
	parentHashArray := make([]common.Hash, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		parentHashArray[i] = wo.ParentHash(i)
	}
	return parentHashArray
}

func (wo *WorkObject) NumberArray() []*big.Int {
	numArray := make([]*big.Int, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		numArray[i] = wo.Number(i)
	}
	return numArray
}

func (wo *WorkObject) SetMixHash(mixHash common.Hash) {
	wo.woHeader.mixHash = mixHash
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Generic Setters ///////////////
////////////////////////////////////////////////////////////

func (wo *WorkObject) SetParentHash(val common.Hash, nodeCtx int) {
	if nodeCtx == common.ZONE_CTX {
		wo.WorkObjectHeader().SetParentHash(val)
	} else {
		wo.Body().Header().SetParentHash(val, nodeCtx)
	}
}

func (wo *WorkObject) SetNumber(val *big.Int, nodeCtx int) {
	if nodeCtx == common.ZONE_CTX {
		wo.WorkObjectHeader().SetNumber(val)
	} else {
		wo.Body().Header().SetNumber(val, nodeCtx)
	}
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Header Getters ///////////////
////////////////////////////////////////////////////////////

// KawpowActivationHappened checks if the AuxPow field is non-nil, indicating
// that Kawpow activation has occurred.
func (wh *WorkObjectHeader) KawpowActivationHappened() bool {
	return wh.primeTerminusNumber.Uint64() >= params.KawPowForkBlock
}

func (wh *WorkObjectHeader) IsKawPowBlock() bool {
	return wh.primeTerminusNumber.Uint64() >= params.KawPowForkBlock &&
		wh.auxPow != nil &&
		wh.AuxPow().PowID() == Kawpow
}

func (wh *WorkObjectHeader) IsShaOrScryptShareWithInvalidAddress() bool {
	shaScryptShare := wh.AuxPow() != nil && (wh.AuxPow().PowID() == SHA_BTC || wh.AuxPow().PowID() == SHA_BCH || wh.AuxPow().PowID() == Scrypt)
	_, err := wh.primaryCoinbase.InternalAddress()
	return err != nil && shaScryptShare
}

// IsTransitionProgPowBlock checks if the block is within the transition period
func (wh *WorkObjectHeader) IsTransitionProgPowBlock() bool {
	return wh.primeTerminusNumber.Uint64() >= params.KawPowForkBlock &&
		wh.primeTerminusNumber.Uint64() < params.KawPowForkBlock+params.KawPowTransitionPeriod &&
		wh.auxPow == nil
}

func (wh *WorkObjectHeader) HeaderHash() common.Hash {
	return wh.headerHash
}

func (wh *WorkObjectHeader) ParentHash() common.Hash {
	return wh.parentHash
}

func (wh *WorkObjectHeader) Number() *big.Int {
	return wh.number
}

func (wh *WorkObjectHeader) NumberU64() uint64 {
	return wh.number.Uint64()
}

func (wh *WorkObjectHeader) PrimeTerminusNumber() *big.Int {
	return wh.primeTerminusNumber
}

func (wh *WorkObjectHeader) Difficulty() *big.Int {
	return wh.difficulty
}

func (wh *WorkObjectHeader) TxHash() common.Hash {
	return wh.txHash
}

func (wh *WorkObjectHeader) PrimaryCoinbase() common.Address {
	return wh.primaryCoinbase
}

func (wh *WorkObjectHeader) Location() common.Location {
	return wh.location
}

func (wh *WorkObjectHeader) MixHash() common.Hash {
	return wh.mixHash
}

func (wh *WorkObjectHeader) Nonce() BlockNonce {
	return wh.nonce
}

func (wh *WorkObjectHeader) Lock() uint8 {
	return wh.lock
}

func (wh *WorkObjectHeader) NonceU64() uint64 {
	return wh.nonce.Uint64()
}

func (wh *WorkObjectHeader) Time() uint64 {
	return wh.time
}

func (wh *WorkObjectHeader) Data() []byte {
	return wh.data
}

func (wh *WorkObjectHeader) AuxPow() *AuxPow {
	return wh.auxPow
}

func (wh *WorkObjectHeader) ScryptDiffAndCount() *PowShareDiffAndCount {
	if wh.scryptDiffAndCount == nil {
		return &PowShareDiffAndCount{}
	}
	return wh.scryptDiffAndCount.Clone()
}

func (wh *WorkObjectHeader) ShaDiffAndCount() *PowShareDiffAndCount {
	if wh.shaDiffAndCount == nil {
		return &PowShareDiffAndCount{}
	}
	return wh.shaDiffAndCount.Clone()
}

func (wh *WorkObjectHeader) ShaShareTarget() *big.Int {
	return wh.shaShareTarget
}

func (wh *WorkObjectHeader) ScryptShareTarget() *big.Int {
	return wh.scryptShareTarget
}

func (wh *WorkObjectHeader) KawpowDifficulty() *big.Int {
	return wh.kawpowDifficulty
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Header Setters ///////////////
////////////////////////////////////////////////////////////

func (wh *WorkObjectHeader) SetHeaderHash(headerHash common.Hash) {
	wh.headerHash = headerHash
}

func (wh *WorkObjectHeader) SetParentHash(parentHash common.Hash) {
	wh.parentHash = parentHash
}

func (wh *WorkObjectHeader) SetNumber(number *big.Int) {
	wh.number = number
}

func (wh *WorkObjectHeader) SetPrimeTerminusNumber(primeTerminusNumber *big.Int) {
	wh.primeTerminusNumber = primeTerminusNumber
}

func (wh *WorkObjectHeader) SetDifficulty(difficulty *big.Int) {
	wh.difficulty = difficulty
}

func (wh *WorkObjectHeader) SetTxHash(txHash common.Hash) {
	wh.txHash = txHash
}

func (wh *WorkObjectHeader) SetPrimaryCoinbase(coinbase common.Address) {
	wh.primaryCoinbase = coinbase
}

func (wh *WorkObjectHeader) SetLocation(location common.Location) {
	wh.location = location
}

func (wh *WorkObjectHeader) SetMixHash(mixHash common.Hash) {
	wh.mixHash = mixHash
}

func (wh *WorkObjectHeader) SetNonce(nonce BlockNonce) {
	wh.nonce = nonce
}

func (wh *WorkObjectHeader) SetLock(lock uint8) {
	wh.lock = lock
}

func (wh *WorkObjectHeader) SetTime(val uint64) {
	wh.time = val
}

func (wh *WorkObjectHeader) SetData(val []byte) {
	wh.data = val
}

func (wh *WorkObjectHeader) SetAuxPow(auxPow *AuxPow) {
	wh.auxPow = auxPow
}

func (wh *WorkObjectHeader) SetScryptDiffAndCount(val *PowShareDiffAndCount) {
	wh.scryptDiffAndCount = val.Clone()
}

func (wh *WorkObjectHeader) SetShaDiffAndCount(val *PowShareDiffAndCount) {
	wh.shaDiffAndCount = val.Clone()
}

func (wh *WorkObjectHeader) SetShaShareTarget(val *big.Int) {
	wh.shaShareTarget = val
}

func (wh *WorkObjectHeader) SetScryptShareTarget(val *big.Int) {
	wh.scryptShareTarget = val
}

func (wh *WorkObjectHeader) SetKawpowDifficulty(val *big.Int) {
	wh.kawpowDifficulty = val
}

type WorkObjectBody struct {
	header          *Header
	transactions    Transactions
	outboundEtxs    Transactions
	uncles          []*WorkObjectHeader
	manifest        BlockManifest
	interlinkHashes common.Hashes
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Body Setters ///////////////
////////////////////////////////////////////////////////////

func (wb *WorkObjectBody) SetHeader(header *Header) {
	wb.header = header
}

func (wb *WorkObjectBody) SetTransactions(transactions []*Transaction) {
	wb.transactions = transactions
}

func (wb *WorkObjectBody) SetOutboundEtxs(transactions []*Transaction) {
	wb.outboundEtxs = transactions
}

func (wb *WorkObjectBody) SetUncles(uncles []*WorkObjectHeader) {
	wb.uncles = uncles
}

func (wb *WorkObjectBody) SetManifest(manifest BlockManifest) {
	wb.manifest = manifest
}

func (wb *WorkObjectBody) SetInterlinkHashes(interlinkHashes common.Hashes) {
	wb.interlinkHashes = interlinkHashes
}

////////////////////////////////////////////////////////////
/////////////////// Work Object Body Getters ///////////////
////////////////////////////////////////////////////////////

func (wb *WorkObjectBody) Header() *Header {
	return wb.header
}

func (wb *WorkObjectBody) Transactions() []*Transaction {
	return wb.transactions
}

func (wb *WorkObjectBody) OutboundEtxs() []*Transaction {
	return wb.outboundEtxs
}

func (wb *WorkObjectBody) Uncles() []*WorkObjectHeader {
	return wb.uncles
}

func (wb *WorkObjectBody) Manifest() BlockManifest {
	return wb.manifest
}

func (wb *WorkObjectBody) InterlinkHashes() common.Hashes {
	return wb.interlinkHashes
}

func (wb *WorkObjectBody) ExternalTransactions() []*Transaction {
	etxs := make([]*Transaction, 0)
	for _, t := range wb.Transactions() {
		if t.Type() == ExternalTxType {
			etxs = append(etxs, t)
		}
	}
	return etxs
}

func CalcUncleHash(uncles []*WorkObjectHeader) (hash common.Hash) {
	if len(uncles) == 0 {
		return EmptyUncleHash
	}
	protoUncles := &ProtoWorkObjectHeaders{}
	for _, unc := range uncles {
		protoUncle, err := unc.ProtoEncode()
		if err != nil {
			return common.Hash{}
		}
		protoUncles.WoHeaders = append(protoUncles.WoHeaders, protoUncle)
	}

	data, err := proto.Marshal(protoUncles)
	if err != nil {
		return common.Hash{}
	}
	sum := blake3.Sum256(data[:])
	hash.SetBytes(sum[:])
	return hash
}

////////////////////////////////////////////////////////////
/////////////////// New Object Creation Methods ////////////
////////////////////////////////////////////////////////////

func NewWorkObject(woHeader *WorkObjectHeader, woBody *WorkObjectBody, tx *Transaction) *WorkObject {
	return &WorkObject{
		woHeader: woHeader,
		woBody:   woBody,
		tx:       tx,
	}
}

func NewWorkObjectWithHeaderAndTx(header *WorkObjectHeader, tx *Transaction) *WorkObject {
	return &WorkObject{woHeader: CopyWorkObjectHeader(header), tx: tx}
}

func (wo *WorkObject) WithBody(header *Header, txs []*Transaction, etxs []*Transaction, uncles []*WorkObjectHeader, manifest BlockManifest, interlinkHashes common.Hashes) *WorkObject {
	woBody := &WorkObjectBody{
		header:          CopyHeader(header),
		transactions:    make([]*Transaction, len(txs)),
		uncles:          make([]*WorkObjectHeader, len(uncles)),
		outboundEtxs:    make([]*Transaction, len(etxs)),
		manifest:        make(BlockManifest, len(manifest)),
		interlinkHashes: make(common.Hashes, len(interlinkHashes)),
	}
	copy(woBody.transactions, txs)
	copy(woBody.uncles, uncles)
	copy(woBody.outboundEtxs, etxs)
	copy(woBody.manifest, manifest)
	copy(woBody.interlinkHashes, interlinkHashes)
	for i := range uncles {
		woBody.uncles[i] = CopyWorkObjectHeader(uncles[i])
	}

	newWo := &WorkObject{
		woHeader: CopyWorkObjectHeader(wo.woHeader),
		woBody:   woBody,
		tx:       wo.tx,
	}
	return newWo
}

func EmptyWorkObjectBody() *WorkObjectBody {
	woBody := &WorkObjectBody{}
	woBody.SetHeader(EmptyHeader())
	woBody.SetTransactions([]*Transaction{})
	woBody.SetOutboundEtxs([]*Transaction{})
	woBody.SetUncles([]*WorkObjectHeader{})
	woBody.SetManifest(BlockManifest{})
	woBody.SetInterlinkHashes(common.Hashes{})
	return woBody
}

func NewWorkObjectBody(header *Header, txs []*Transaction, etxs []*Transaction, uncles []*WorkObjectHeader, manifest BlockManifest, receipts []*Receipt, hasher TrieHasher, nodeCtx int) (*WorkObjectBody, error) {
	b := &WorkObjectBody{}
	b.SetHeader(CopyHeader(header))

	if len(txs) == 0 {
		b.Header().SetTxHash(EmptyRootHash)
	} else {
		b.Header().SetTxHash(DeriveSha(Transactions(txs), hasher))
		b.transactions = make(Transactions, len(txs))
		copy(b.transactions, txs)
	}

	if len(receipts) == 0 {
		b.Header().SetReceiptHash(EmptyRootHash)
	} else {
		b.Header().SetReceiptHash(DeriveSha(Receipts(receipts), hasher))
	}

	if len(uncles) == 0 {
		b.Header().SetUncleHash(EmptyUncleHash)
	} else {
		b.Header().SetUncleHash(CalcUncleHash(uncles))
		b.uncles = make([]*WorkObjectHeader, len(uncles))
		for i := range uncles {
			b.uncles[i] = CopyWorkObjectHeader(uncles[i])
		}
	}

	if len(etxs) == 0 {
		b.Header().SetOutboundEtxHash(EmptyRootHash)
	} else {
		b.Header().SetOutboundEtxHash(DeriveSha(Transactions(etxs), hasher))
		b.outboundEtxs = make(Transactions, len(etxs))
		copy(b.outboundEtxs, etxs)
	}

	// Since the subordinate's manifest lives in our body, we still need to check
	// that the manifest matches the subordinate's manifest hash, but we do not set
	// the subordinate's manifest hash.
	subManifestHash := EmptyRootHash
	if len(manifest) != 0 {
		subManifestHash = DeriveSha(manifest, hasher)
		b.manifest = make(BlockManifest, len(manifest))
		copy(b.manifest, manifest)
	}
	if nodeCtx < common.ZONE_CTX && subManifestHash != b.Header().ManifestHash(nodeCtx+1) {
		return nil, fmt.Errorf("attempted to build block with invalid subordinate manifest")
	}

	return b, nil
}

func NewWorkObjectWithHeader(header *WorkObject, tx *Transaction, nodeCtx int, woType WorkObjectView) *WorkObject {
	woHeader := NewWorkObjectHeader(header.Hash(),
		header.ParentHash(common.ZONE_CTX),
		header.WorkObjectHeader().number,
		header.WorkObjectHeader().difficulty,
		header.WorkObjectHeader().PrimeTerminusNumber(),
		header.WorkObjectHeader().txHash,
		header.WorkObjectHeader().nonce,
		header.WorkObjectHeader().lock,
		header.WorkObjectHeader().time,
		header.Location(),
		header.PrimaryCoinbase(),
		header.WorkObjectHeader().data,
		header.WorkObjectHeader().AuxPow(),
		header.WorkObjectHeader().ScryptDiffAndCount(),
		header.WorkObjectHeader().ShaDiffAndCount(),
		header.WorkObjectHeader().ShaShareTarget(),
		header.WorkObjectHeader().ScryptShareTarget(),
		header.WorkObjectHeader().KawpowDifficulty(),
	)
	woBody, _ := NewWorkObjectBody(header.Body().Header(), nil, nil, nil, nil, nil, nil, nodeCtx)
	return NewWorkObject(woHeader, woBody, tx)
}

func CopyWorkObject(wo *WorkObject) *WorkObject {
	newWo := &WorkObject{
		woHeader: CopyWorkObjectHeader(wo.woHeader),
		woBody:   CopyWorkObjectBody(wo.woBody),
		tx:       wo.tx,
	}
	return newWo
}
func (wo *WorkObject) RPCMarshalWorkObject(rpcVersion string) map[string]interface{} {
	result := map[string]interface{}{
		"woHeader": wo.woHeader.RPCMarshalWorkObjectHeader(rpcVersion),
	}
	if wo.woBody != nil {
		result["woBody"] = wo.woBody.RPCMarshalWorkObjectBody(rpcVersion)
	}
	if wo.tx != nil {
		result["tx"] = wo.tx
	}
	return result
}

// RPCMarshalHeader returns a flattened header and woHeader as part of the
// header response
func (wo *WorkObject) RPCMarshalHeader(rpcVersion string) map[string]interface{} {
	result := make(map[string]interface{})
	if wo.woBody != nil && wo.woBody.header != nil {
		result = wo.woBody.Header().RPCMarshalHeader()
	}
	result["woHeader"] = wo.woHeader.RPCMarshalWorkObjectHeader(rpcVersion)
	return result
}

func (wo *WorkObject) ProtoEncode(woType WorkObjectView) (*ProtoWorkObject, error) {
	switch woType {
	case PEtxObject:
		header, err := wo.woHeader.ProtoEncode()
		if err != nil {
			return nil, err
		}
		bodyHeader, err := wo.woBody.header.ProtoEncode()
		if err != nil {
			return nil, fmt.Errorf("error encoding work object body header, err %s", err)
		}
		return &ProtoWorkObject{
			WoHeader: header,
			WoBody:   &ProtoWorkObjectBody{Header: bodyHeader},
		}, nil
	default:
		header, err := wo.woHeader.ProtoEncode()
		if err != nil {
			return nil, err
		}
		body, err := wo.woBody.ProtoEncode(woType)
		if err != nil {
			return nil, err
		}
		if wo.tx == nil || wo.tx.inner == nil {
			return &ProtoWorkObject{
				WoHeader: header,
				WoBody:   body,
			}, nil
		} else {
			tx, err := wo.tx.ProtoEncode()
			if err != nil {
				return nil, err
			}
			return &ProtoWorkObject{
				WoHeader: header,
				WoBody:   body,
				Tx:       tx,
			}, nil
		}
	}
}

func (wo *WorkObjectHeaderView) ProtoEncode() (*ProtoWorkObjectHeaderView, error) {
	protoWo, err := wo.WorkObject.ProtoEncode(HeaderObject)
	if err != nil {
		return nil, err
	}
	return &ProtoWorkObjectHeaderView{
		WorkObject: protoWo,
	}, nil
}

func (wo *WorkObjectBlockView) ProtoEncode() (*ProtoWorkObjectBlockView, error) {
	protoWo, err := wo.WorkObject.ProtoEncode(BlockObject)
	if err != nil {
		return nil, err
	}
	return &ProtoWorkObjectBlockView{
		WorkObject: protoWo,
	}, nil
}

func (wo *WorkObjectShareView) ProtoEncode() (*ProtoWorkObjectShareView, error) {
	protoWo, err := wo.WorkObject.ProtoEncode(WorkShareTxObject)
	if err != nil {
		return nil, err
	}
	return &ProtoWorkObjectShareView{
		WorkObject: protoWo,
	}, nil
}

func (wo *WorkObjectHeaderView) ProtoDecode(data *ProtoWorkObjectHeaderView, location common.Location) error {
	decodeWo := new(WorkObject)
	err := decodeWo.ProtoDecode(data.GetWorkObject(), location, HeaderObject)
	if err != nil {
		return err
	}
	wo.WorkObject = decodeWo
	return nil
}

func (wob *WorkObjectBlockView) ProtoDecode(data *ProtoWorkObjectBlockView, location common.Location) error {
	decodeWo := new(WorkObject)
	err := decodeWo.ProtoDecode(data.GetWorkObject(), location, BlockObject)
	if err != nil {
		return err
	}
	wob.WorkObject = decodeWo
	return nil
}

func (wos *WorkObjectShareView) ProtoDecode(data *ProtoWorkObjectShareView, location common.Location) error {
	decodeWo := new(WorkObject)
	err := decodeWo.ProtoDecode(data.GetWorkObject(), location, WorkShareTxObject)
	if err != nil {
		return err
	}
	wos.WorkObject = decodeWo
	return nil
}

func (wo *WorkObject) ProtoDecode(data *ProtoWorkObject, location common.Location, woType WorkObjectView) error {
	switch woType {
	case PEtxObject:
		wo.woHeader = new(WorkObjectHeader)
		err := wo.woHeader.ProtoDecode(data.GetWoHeader(), location)
		if err != nil {
			return err
		}
		wo.woBody = new(WorkObjectBody)
		bodyHeader := new(Header)
		bodyHeader.ProtoDecode(data.GetWoBody().Header, location)
		wo.woBody.SetHeader(bodyHeader)
	default:
		wo.woHeader = new(WorkObjectHeader)
		err := wo.woHeader.ProtoDecode(data.GetWoHeader(), location)
		if err != nil {
			return err
		}
		wo.woBody = new(WorkObjectBody)
		err = wo.woBody.ProtoDecode(data.GetWoBody(), location, woType)
		if err != nil {
			return err
		}
		if data.Tx != nil {
			wo.tx = new(Transaction)
			err = wo.tx.ProtoDecode(data.GetTx(), location)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func NewWorkObjectHeader(headerHash common.Hash, parentHash common.Hash, number *big.Int, difficulty *big.Int, primeTerminusNumber *big.Int, txHash common.Hash, nonce BlockNonce, lock uint8, time uint64, location common.Location, primaryCoinbase common.Address, data []byte, auxpow *AuxPow, scryptDiffAndCount, shaDiffAndCount *PowShareDiffAndCount, shaShareTarget, scryptShareTarget, kawpowDifficulty *big.Int) *WorkObjectHeader {
	return &WorkObjectHeader{
		headerHash:          headerHash,
		parentHash:          parentHash,
		number:              number,
		difficulty:          difficulty,
		primeTerminusNumber: primeTerminusNumber,
		txHash:              txHash,
		nonce:               nonce,
		lock:                lock,
		time:                time,
		location:            location,
		primaryCoinbase:     primaryCoinbase,
		data:                data,
		auxPow:              auxpow,
		scryptDiffAndCount:  scryptDiffAndCount.Clone(),
		shaDiffAndCount:     shaDiffAndCount.Clone(),
		shaShareTarget:      shaShareTarget,
		scryptShareTarget:   scryptShareTarget,
		kawpowDifficulty:    kawpowDifficulty,
	}
}

func CopyWorkObjectHeader(wh *WorkObjectHeader) *WorkObjectHeader {
	if wh == nil {
		return nil
	}
	cpy := *wh
	cpy.SetHeaderHash(wh.HeaderHash())
	cpy.SetParentHash(wh.ParentHash())
	cpy.SetNumber(new(big.Int).Set(wh.Number()))
	cpy.SetDifficulty(new(big.Int).Set(wh.Difficulty()))
	cpy.SetTxHash(wh.TxHash())
	cpy.SetNonce(wh.Nonce())
	cpy.SetMixHash(wh.MixHash())
	cpy.SetLocation(wh.Location())
	cpy.SetTime(wh.Time())
	cpy.SetPrimeTerminusNumber(wh.primeTerminusNumber)
	cpy.SetLock(wh.Lock())
	cpy.SetPrimaryCoinbase(wh.PrimaryCoinbase())
	cpy.SetData(wh.Data())

	if wh.ScryptDiffAndCount() != nil {
		cpy.SetScryptDiffAndCount(wh.ScryptDiffAndCount())
	}
	if wh.ShaDiffAndCount() != nil {
		cpy.SetShaDiffAndCount(wh.ShaDiffAndCount())
	}

	if wh.ShaShareTarget() != nil {
		cpy.SetShaShareTarget(wh.ShaShareTarget())
	}
	if wh.ScryptShareTarget() != nil {
		cpy.SetScryptShareTarget(wh.ScryptShareTarget())
	}
	if wh.KawpowDifficulty() != nil {
		cpy.SetKawpowDifficulty(wh.KawpowDifficulty())
	}

	// Deep copy AuxPow if present
	if wh.auxPow != nil {
		cpy.auxPow = CopyAuxPow(wh.auxPow)
	}

	return &cpy
}

func (wh *WorkObjectHeader) RPCMarshalWorkObjectHeader(rpcVersion string) map[string]interface{} {
	result := map[string]interface{}{
		"hash":                wh.Hash(),
		"headerHash":          wh.HeaderHash(),
		"parentHash":          wh.ParentHash(),
		"number":              (*hexutil.Big)(wh.Number()),
		"difficulty":          (*hexutil.Big)(wh.Difficulty()),
		"primeTerminusNumber": (*hexutil.Big)(wh.PrimeTerminusNumber()),
		"nonce":               wh.Nonce(),
		"location":            hexutil.Bytes(wh.Location()),
		"txHash":              wh.TxHash(),
		"timestamp":           hexutil.Uint64(wh.Time()),
		"mixHash":             wh.MixHash(),
		"lock":                hexutil.Uint64(wh.Lock()),
		"primaryCoinbase":     wh.PrimaryCoinbase().Hex(),
		"data":                hexutil.Bytes(wh.Data()),
	}

	// For v1 RPC, do not include AuxPow and related fields
	if rpcVersion == "v1" {
		return result
	}

	if rpcVersion == "v2" {
		// Include AuxPow if present
		if wh.AuxPow() != nil {
			result["auxpow"] = wh.AuxPow().RPCMarshal()
		}
		if wh.ShaDiffAndCount() != nil {
			result["shaDiffAndCount"] = wh.ShaDiffAndCount().RPCMarshal()
		}
		if wh.ScryptDiffAndCount() != nil {
			result["scryptDiffAndCount"] = wh.ScryptDiffAndCount().RPCMarshal()
		}
		if wh.ShaShareTarget() != nil {
			result["shaShareTarget"] = (*hexutil.Big)(wh.ShaShareTarget())
		}
		if wh.ScryptShareTarget() != nil {
			result["scryptShareTarget"] = (*hexutil.Big)(wh.ScryptShareTarget())
		}
		if wh.KawpowDifficulty() != nil {
			result["kawpowDifficulty"] = (*hexutil.Big)(wh.KawpowDifficulty())
		}
	}

	return result
}

func (wh *WorkObjectHeader) Hash() common.Hash {
	// If the prime terminus number is below the KawPow fork block, then use the
	// normal hashing
	if !wh.KawpowActivationHappened() || wh.IsTransitionProgPowBlock() {
		return wh.WoProgpowHash()
	} else {
		return wh.WoCustomPowHash()
	}
}

func (wh *WorkObjectHeader) WoProgpowHash() (hash common.Hash) {
	sealHash := wh.SealHash().Bytes()
	mixHash := wh.MixHash().Bytes()
	nonce := wh.Nonce().Bytes()
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	var hData [common.HashLength + common.HashLength + NonceLength]byte
	copy(hData[:], mixHash)
	copy(hData[common.HashLength:], sealHash)
	copy(hData[common.HashLength+common.HashLength:], nonce)
	sum := blake3.Sum256(hData[:])
	hash.SetBytes(sum[:])
	return hash
}

func (wh *WorkObjectHeader) WoCustomPowHash() (hash common.Hash) {
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	protoAuxPow := wh.AuxPow().ProtoEncode()
	data, _ := proto.Marshal(protoAuxPow)
	sum := blake3.Sum256(data[:])
	hash.SetBytes(sum[:])
	return hash
}

func (wh *WorkObjectHeader) SealHash() (hash common.Hash) {
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	protoSealData := wh.SealEncode()
	primaryCoinbase := wh.PrimaryCoinbase().Bytes()
	// After the kawpow activation update the seal hash to be
	// without the primary coinbase and then hash the coinbase
	// with the sealhash
	if wh.KawpowActivationHappened() {
		protoSealData.PrimaryCoinbase = nil
	}
	data, err := proto.Marshal(protoSealData)
	if err != nil {
		log.Global.Error("Failed to marshal seal data ", "err", err)
	}

	sum := blake3.Sum256(data[:])

	if wh.KawpowActivationHappened() {
		// Encode the primary coinbase separately
		sum = blake3.Sum256(append(sum[:], primaryCoinbase...))
	}

	hash.SetBytes(sum[:])
	return hash
}

func (wh *WorkObjectHeader) SealEncode() *ProtoWorkObjectHeader {
	// Omit MixHash, PowHash, and AuxPow from seal hash calculation
	// AuxPow is excluded to prevent circular reference since the donor header
	// in AuxPow will commit to this seal hash
	headerHash := common.ProtoHash{Value: wh.HeaderHash().Bytes()}
	parentHash := common.ProtoHash{Value: wh.ParentHash().Bytes()}
	txHash := common.ProtoHash{Value: wh.TxHash().Bytes()}
	number := wh.Number().Bytes()
	difficulty := wh.Difficulty().Bytes()
	primeTerminusNumber := wh.PrimeTerminusNumber().Bytes()
	location := wh.Location().ProtoEncode()
	time := wh.Time()
	lock := uint32(wh.Lock())
	coinbase := common.ProtoAddress{Value: wh.PrimaryCoinbase().Bytes()}
	data := wh.Data()

	protoWh := &ProtoWorkObjectHeader{
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
		// AuxPow explicitly NOT included to avoid circular reference
	}

	// After kawpow activation the scrypt and sha share diff count should
	// be part of the seal
	if wh.KawpowActivationHappened() {
		protoWh.ScryptDiffAndCount = wh.scryptDiffAndCount.ProtoEncode()
		protoWh.ShaDiffAndCount = wh.shaDiffAndCount.ProtoEncode()
		protoWh.ShaShareTarget = wh.ShaShareTarget().Bytes()
		protoWh.ScryptShareTarget = wh.ScryptShareTarget().Bytes()
		protoWh.KawpowDifficulty = wh.KawpowDifficulty().Bytes()
	}

	return protoWh
}

func (wh *WorkObjectHeader) ProtoEncode() (*ProtoWorkObjectHeader, error) {
	hash := common.ProtoHash{Value: wh.HeaderHash().Bytes()}
	parentHash := common.ProtoHash{Value: wh.ParentHash().Bytes()}
	txHash := common.ProtoHash{Value: wh.TxHash().Bytes()}
	number := wh.Number().Bytes()
	difficulty := wh.Difficulty().Bytes()
	primeTerminusNumber := wh.PrimeTerminusNumber().Bytes()
	location := wh.Location().ProtoEncode()
	nonce := wh.Nonce().Uint64()
	mixHash := common.ProtoHash{Value: wh.MixHash().Bytes()}
	lock := uint32(wh.Lock())
	coinbase := common.ProtoAddress{Value: wh.PrimaryCoinbase().Bytes()}
	data := wh.Data()

	protoWh := &ProtoWorkObjectHeader{
		HeaderHash:          &hash,
		ParentHash:          &parentHash,
		Number:              number,
		Difficulty:          difficulty,
		PrimeTerminusNumber: primeTerminusNumber,
		TxHash:              &txHash,
		Location:            location,
		Nonce:               &nonce,
		Lock:                &lock,
		MixHash:             &mixHash,
		Time:                &wh.time,
		PrimaryCoinbase:     &coinbase,
		Data:                data,
	}

	// Include AuxPow if present
	var auxPow *ProtoAuxPow
	// kawpow fork happened?
	if wh.KawpowActivationHappened() {
		if wh.auxPow != nil {
			auxPow = wh.auxPow.ProtoEncode()
			protoWh.AuxPow = auxPow
		}
		protoWh.ScryptDiffAndCount = wh.scryptDiffAndCount.ProtoEncode()
		protoWh.ShaDiffAndCount = wh.shaDiffAndCount.ProtoEncode()
		protoWh.ShaShareTarget = wh.ShaShareTarget().Bytes()
		protoWh.ScryptShareTarget = wh.ScryptShareTarget().Bytes()
		protoWh.KawpowDifficulty = wh.KawpowDifficulty().Bytes()
	}

	return protoWh, nil
}

func (wh *WorkObjectHeader) ProtoDecode(data *ProtoWorkObjectHeader, location common.Location) error {
	if data.HeaderHash == nil || data.ParentHash == nil || data.Number == nil || data.Difficulty == nil || data.PrimeTerminusNumber == nil || data.TxHash == nil || data.Nonce == nil || data.Location == nil || data.Time == nil || data.PrimaryCoinbase == nil {
		err := errors.New("failed to decode work object header")
		return err
	}
	wh.SetHeaderHash(common.BytesToHash(data.GetHeaderHash().Value))
	wh.SetParentHash(common.BytesToHash(data.GetParentHash().Value))
	wh.SetNumber(new(big.Int).SetBytes(data.GetNumber()))
	wh.SetDifficulty(new(big.Int).SetBytes(data.Difficulty))
	wh.SetPrimeTerminusNumber(new(big.Int).SetBytes(data.GetPrimeTerminusNumber()))
	wh.SetTxHash(common.BytesToHash(data.GetTxHash().Value))
	wh.SetNonce(uint64ToByteArr(data.GetNonce()))
	wh.SetLock(uint8(data.GetLock()))
	wh.SetLocation(data.GetLocation().GetValue())
	wh.SetMixHash(common.BytesToHash(data.GetMixHash().Value))
	wh.SetTime(data.GetTime())
	wh.SetPrimaryCoinbase(common.BytesToAddress(data.GetPrimaryCoinbase().GetValue(), location))
	wh.SetData(data.GetData())

	if new(big.Int).SetBytes(data.GetPrimeTerminusNumber()).Uint64() >= params.KawPowForkBlock {

		// if the scryptDiffAndCount or the shaDiffAndCount doesnt
		// exist after the fork, throw an error
		if data.GetShaDiffAndCount() == nil {
			return errors.New("sha diff and count is nil")
		}
		if data.GetScryptDiffAndCount() == nil {
			return errors.New("scrypt diff and count is nil")
		}

		wh.shaDiffAndCount = &PowShareDiffAndCount{}
		wh.shaDiffAndCount.ProtoDecode(data.GetShaDiffAndCount())

		wh.scryptDiffAndCount = &PowShareDiffAndCount{}
		wh.scryptDiffAndCount.ProtoDecode(data.GetScryptDiffAndCount())

		// Decode AuxPow if present
		if data.AuxPow != nil && data.AuxPow.GetHeader() != nil && data.AuxPow.GetTransaction() != nil {
			wh.auxPow = &AuxPow{}
			if err := wh.auxPow.ProtoDecode(data.AuxPow); err != nil {
				return err
			}
		}

		if data.GetShaShareTarget() == nil {
			return errors.New("sha share target is nil")
		}
		if data.GetScryptShareTarget() == nil {
			return errors.New("scrypt share target is nil")
		}
		if data.GetKawpowDifficulty() == nil {
			return errors.New("kawpow difficulty is nil")
		}

		wh.SetShaShareTarget(new(big.Int).SetBytes(data.GetShaShareTarget()))
		wh.SetScryptShareTarget(new(big.Int).SetBytes(data.GetScryptShareTarget()))
		wh.SetKawpowDifficulty(new(big.Int).SetBytes(data.GetKawpowDifficulty()))
	}

	return nil
}

func NewWoBody(header *Header, txs []*Transaction, etxs []*Transaction, uncles []*WorkObjectHeader, manifest BlockManifest, interlinkHashes common.Hashes) *WorkObjectBody {
	woBody := &WorkObjectBody{
		header:          CopyHeader(header),
		transactions:    make([]*Transaction, len(txs)),
		uncles:          make([]*WorkObjectHeader, len(uncles)),
		outboundEtxs:    make([]*Transaction, len(etxs)),
		manifest:        make(BlockManifest, len(manifest)),
		interlinkHashes: make(common.Hashes, len(interlinkHashes)),
	}
	copy(woBody.transactions, txs)
	copy(woBody.uncles, uncles)
	copy(woBody.outboundEtxs, etxs)
	copy(woBody.manifest, manifest)
	copy(woBody.interlinkHashes, interlinkHashes)
	for i := range uncles {
		woBody.uncles[i] = CopyWorkObjectHeader(uncles[i])
	}
	return woBody
}

func CopyWorkObjectBody(wb *WorkObjectBody) *WorkObjectBody {
	cpy := &WorkObjectBody{header: CopyHeader(wb.header)}
	cpy.transactions = make(Transactions, len(wb.Transactions()))
	copy(cpy.transactions, wb.Transactions())
	cpy.outboundEtxs = make(Transactions, len(wb.OutboundEtxs()))
	copy(cpy.outboundEtxs, wb.OutboundEtxs())
	cpy.uncles = make([]*WorkObjectHeader, len(wb.uncles))
	copy(cpy.uncles, wb.Uncles())
	cpy.manifest = make(BlockManifest, len(wb.Manifest()))
	copy(cpy.manifest, wb.Manifest())
	cpy.interlinkHashes = make(common.Hashes, len(wb.InterlinkHashes()))
	copy(cpy.interlinkHashes, wb.InterlinkHashes())

	return cpy
}

func (wb *WorkObjectBody) ProtoEncode(woType WorkObjectView) (*ProtoWorkObjectBody, error) {
	switch woType {
	case WorkShareTxObject:
		var err error
		var protoHeader *ProtoHeader
		if wb.header != nil {
			protoHeader, err = wb.header.ProtoEncode()
			if err != nil {
				return nil, err
			}
		}
		// Only encode the txs field in the body
		protoTransactions, err := wb.transactions.ProtoEncode()
		if err != nil {
			return nil, err
		}
		return &ProtoWorkObjectBody{
			Header:       protoHeader,
			Transactions: protoTransactions,
		}, nil

	default:
		header, err := wb.header.ProtoEncode()
		if err != nil {
			return nil, err
		}

		protoTransactions, err := wb.transactions.ProtoEncode()
		if err != nil {
			return nil, err
		}

		protoEtxs, err := wb.outboundEtxs.ProtoEncode()
		if err != nil {
			return nil, err
		}

		protoUncles := &ProtoWorkObjectHeaders{}
		for _, unc := range wb.uncles {
			protoUncle, err := unc.ProtoEncode()
			if err != nil {
				return nil, err
			}
			protoUncles.WoHeaders = append(protoUncles.WoHeaders, protoUncle)
		}

		protoManifest, err := wb.manifest.ProtoEncode()
		if err != nil {
			return nil, err
		}

		protoInterlinkHashes := wb.interlinkHashes.ProtoEncode()

		return &ProtoWorkObjectBody{
			Header:          header,
			Transactions:    protoTransactions,
			OutboundEtxs:    protoEtxs,
			Uncles:          protoUncles,
			Manifest:        protoManifest,
			InterlinkHashes: protoInterlinkHashes,
		}, nil
	}
}

func (wb *WorkObjectBody) ProtoDecode(data *ProtoWorkObjectBody, location common.Location, woType WorkObjectView) error {
	var err error
	switch woType {
	case WorkShareObject:
		wb.header = &Header{}
		err := wb.header.ProtoDecode(data.GetHeader(), location)
		if err != nil {
			return err
		}
		wb.uncles = make([]*WorkObjectHeader, len(data.GetUncles().GetWoHeaders()))
		for i, protoUncle := range data.GetUncles().GetWoHeaders() {
			uncle := &WorkObjectHeader{}
			err = uncle.ProtoDecode(protoUncle, location)
			if err != nil {
				return err
			}
			wb.uncles[i] = uncle
		}
	case WorkShareTxObject:
		wb.header = &Header{}
		if data.GetHeader() != nil {
			err := wb.header.ProtoDecode(data.GetHeader(), location)
			if err != nil {
				return err
			}
		}
		wb.transactions = Transactions{}
		err = wb.transactions.ProtoDecode(data.GetTransactions(), location)
		if err != nil {
			return err
		}
	default:
		// Only decode the header if its specified
		if data.Header != nil {
			wb.header = &Header{}
			err := wb.header.ProtoDecode(data.GetHeader(), location)
			if err != nil {
				return err
			}
		}
		wb.transactions = Transactions{}
		err = wb.transactions.ProtoDecode(data.GetTransactions(), location)
		if err != nil {
			return err
		}
		wb.outboundEtxs = Transactions{}
		err = wb.outboundEtxs.ProtoDecode(data.GetOutboundEtxs(), location)
		if err != nil {
			return err
		}
		wb.uncles = make([]*WorkObjectHeader, len(data.GetUncles().GetWoHeaders()))
		for i, protoUncle := range data.GetUncles().GetWoHeaders() {
			uncle := &WorkObjectHeader{}
			err = uncle.ProtoDecode(protoUncle, location)
			if err != nil {
				return err
			}
			wb.uncles[i] = uncle
		}
		wb.manifest = BlockManifest{}
		err = wb.manifest.ProtoDecode(data.GetManifest())
		if err != nil {
			return err
		}
		wb.interlinkHashes = common.Hashes{}
		wb.interlinkHashes.ProtoDecode(data.GetInterlinkHashes())
	}

	return nil
}

func (wb *WorkObjectBody) ProtoDecodeHeader(data *ProtoWorkObjectBody, location common.Location) error {
	wb.header = &Header{}
	return wb.header.ProtoDecode(data.GetHeader(), location)
}

func (wb *WorkObjectBody) RPCMarshalWorkObjectBody(rpcVersion string) map[string]interface{} {
	result := map[string]interface{}{
		"header":          wb.header.RPCMarshalHeader(),
		"transactions":    wb.Transactions(),
		"outboundEtxs":    wb.OutboundEtxs(),
		"manifest":        wb.Manifest(),
		"interlinkHashes": wb.InterlinkHashes(),
	}

	workedUncles := make([]map[string]interface{}, len(wb.Uncles()))
	for i, uncle := range wb.Uncles() {
		workedUncles[i] = uncle.RPCMarshalWorkObjectHeader(rpcVersion)
	}
	result["uncles"] = workedUncles

	return result
}

////////////////////////////////////////////////////////////
///////////////////// Work Object Views ////////////////////
////////////////////////////////////////////////////////////

type WorkObjectBlockView struct {
	*WorkObject
}

type WorkObjectHeaderView struct {
	*WorkObject
}

type WorkObjectShareView struct {
	*WorkObject
}

////////////////////////////////////////////////////////////
////////////// View Conversion/Getter Methods //////////////
////////////////////////////////////////////////////////////

func (wo *WorkObject) ConvertToHeaderView() *WorkObjectHeaderView {
	newWo := CopyWorkObject(wo)

	newWo.Body().SetTransactions(Transactions{})
	newWo.Body().SetManifest(BlockManifest{})
	newWo.Body().SetInterlinkHashes(common.Hashes{})
	return &WorkObjectHeaderView{
		WorkObject: newWo,
	}
}

func (wo *WorkObject) ConvertToBlockView() *WorkObjectBlockView {
	return &WorkObjectBlockView{
		WorkObject: wo,
	}
}

func (wo *WorkObject) ConvertToPEtxView() *WorkObject {
	return wo.WithBody(wo.Header(), nil, nil, nil, nil, nil)
}

func (wo *WorkObject) ConvertToWorkObjectShareView(txs Transactions) *WorkObjectShareView {
	return &WorkObjectShareView{
		WorkObject: wo.WithBody(wo.Header(), txs, nil, nil, nil, nil),
	}
}
