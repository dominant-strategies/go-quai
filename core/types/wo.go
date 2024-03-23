package types

import (
	"errors"
	"io"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/log"
	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"
)

type WorkObject struct {
	woHeader *WorkObjectHeader
	woBody   *WorkObjectBody
	tx       *Transaction

	// caches
	size       atomic.Value
	appendTime atomic.Value

	// These fields are used to track
	// inter-peer block relay.
	ReceivedAt   time.Time
	ReceivedFrom interface{}
}

type WorkObjectHeader struct {
	headerHash common.Hash
	parentHash common.Hash
	number     *big.Int
	difficulty *big.Int
	txHash     common.Hash
	location   common.Location
	mixHash    common.Hash
	nonce      BlockNonce
}

type WorkObjectBody struct {
	header          *Header
	transactions    Transactions
	extTransactions Transactions
	uncles          []*WorkObject
	manifest        BlockManifest
}

// Work object types
const (
	BlockObject = iota
	TxObject
	PhObject
)

type WorkObjects []*WorkObject

func (wo *WorkObject) Header() *Header {
	return wo.woBody.header
}

func (wo *WorkObject) WorkObjectHeader() *WorkObjectHeader {
	return wo.woHeader
}

func (wo *WorkObject) Body() *WorkObjectBody {
	return wo.woBody
}

func (wo *WorkObject) Hash() common.Hash {
	return wo.woHeader.Hash()
}

func (wo *WorkObject) SealHash() common.Hash {
	return wo.woHeader.SealHash()
}

func (wo *WorkObject) NumberU64(nodeCtx int) uint64 {
	if nodeCtx != common.ZONE_CTX {
		return wo.Header().NumberU64(nodeCtx)
	} else {
		return wo.woHeader.NumberU64()
	}
}

func (wo *WorkObject) Transactions() Transactions {
	return wo.woBody.transactions
}

func (wo *WorkObject) ExtTransactions() Transactions {
	return wo.woBody.ExtTransactions()
}

func (wo *WorkObject) Uncles() []*WorkObject {
	return wo.woBody.uncles
}

func (wo *WorkObject) Manifest() BlockManifest {
	return wo.woBody.manifest
}

func (wo *WorkObject) MixHash() common.Hash {
	return wo.woHeader.mixHash
}

func (wo *WorkObject) ParentHash(nodeCtx int) common.Hash {
	if nodeCtx == common.ZONE_CTX {
		return wo.woHeader.parentHash
	} else {
		return wo.woBody.header.parentHash[nodeCtx]
	}
}

func (wo *WorkObject) Number(nodeCtx int) *big.Int {
	if wo.woBody.header != nil && nodeCtx != common.ZONE_CTX {
		return wo.woBody.header.Number(nodeCtx)
	} else {
		return wo.woHeader.number
	}
}

func (wo *WorkObject) Difficulty() *big.Int {
	return wo.woHeader.difficulty
}

func (wo *WorkObject) TxHash() common.Hash {
	return wo.woHeader.txHash
}

func (wo *WorkObject) TransactionsHash() common.Hash {
	return wo.woBody.header.TxHash()
}

func (wo *WorkObject) Nonce() BlockNonce {
	return wo.woHeader.nonce
}

func (wo *WorkObject) Nonce64() uint64 {
	return wo.woHeader.nonce.Uint64()
}

func (wo *WorkObject) HeaderHash() common.Hash {
	return wo.woHeader.headerHash
}

func (wo *WorkObject) Tx() *Transaction {
	return wo.tx
}

func (wo *WorkObject) Location() common.Location {
	return wo.woHeader.location
}

func (wo *WorkObject) EVMRoot() common.Hash {
	return wo.woBody.header.EVMRoot()
}

func (wo *WorkObject) SubManifest() BlockManifest {
	return wo.woBody.manifest
}

func (wo *WorkObject) ParentEntropy(nodeCtx int) *big.Int {
	return wo.woBody.header.ParentEntropy(nodeCtx)
}

func (wo *WorkObject) EtxRollupHash() common.Hash {
	return wo.woBody.header.EtxRollupHash()
}

func (wo *WorkObject) EtxSetHash() common.Hash {
	return wo.woBody.header.EtxSetHash()
}

func (wo *WorkObject) BaseFee() *big.Int {
	return wo.woBody.header.BaseFee()
}

func (wo *WorkObject) GasUsed() uint64 {
	return wo.woBody.header.GasUsed()
}

func (wo *WorkObject) GasLimit() uint64 {
	return wo.woBody.header.GasLimit()
}

func (wo *WorkObject) Time() uint64 {
	return wo.woBody.header.Time()
}

func (wo *WorkObject) Coinbase() common.Address {
	return wo.woBody.header.Coinbase()
}

func (wo *WorkObject) ManifestHash(nodeCtx int) common.Hash {
	return wo.woBody.header.ManifestHash(nodeCtx)
}

func (wo *WorkObject) ParentDeltaS(nodeCtx int) *big.Int {
	return wo.woBody.header.ParentDeltaS(nodeCtx)
}

func (wo *WorkObject) UncleHash() common.Hash {
	return wo.woBody.header.UncleHash()
}

func (wo *WorkObject) EtxHash() common.Hash {
	return wo.woBody.header.EtxHash()
}

func (wo *WorkObject) ReceiptHash() common.Hash {
	return wo.woBody.header.ReceiptHash()
}

func (wo *WorkObject) Extra() []byte {
	return wo.woBody.header.Extra()
}

func (wo *WorkObject) UTXORoot() common.Hash {
	return wo.woBody.header.UTXORoot()
}

func (wo *WorkObject) SetTx(tx *Transaction) {
	wo.tx = tx
}

func (wo *WorkObject) NumberArray() []*big.Int {
	numArray := make([]*big.Int, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		numArray[i] = wo.Number(i)
	}
	return numArray
}

func (wo *WorkObject) SetWorkObjectHeader(woHeader *WorkObjectHeader) {
	wo.woHeader = woHeader
}

func (wo *WorkObject) SetBody(woBody *WorkObjectBody) {
	wo.woBody = woBody
}

func (wo *WorkObject) SetMixHash(mixHash common.Hash) {
	wo.woHeader.mixHash = mixHash
}

func (wo *WorkObject) SetHeader(header *Header) {
	wo.woBody.header = header
}

func (wo *WorkObject) SetTransactions(transactions []*Transaction) {
	wo.woBody.transactions = transactions
}

func (wo *WorkObject) SetExtTransactions(transactions []*Transaction) {
	wo.woBody.extTransactions = transactions
}

func (wo *WorkObject) SetUncles(uncles []*WorkObject) {
	wo.woBody.uncles = uncles
}

func (wo *WorkObject) SetManifest(manifest BlockManifest) {
	wo.woBody.manifest = manifest
}

func (wo *WorkObject) SetEtxSetHash(hash common.Hash) {
	wo.woBody.header.SetEtxSetHash(hash)
}

func (wo *WorkObject) SetParentHash(parentHash common.Hash, nodeCtx int) {
	if nodeCtx == common.ZONE_CTX {
		wo.woHeader.parentHash = parentHash
	} else {
		wo.woBody.header.SetParentHash(parentHash, nodeCtx)
	}
}

func (wo *WorkObject) SetNumber(number *big.Int, nodeCtx int) {
	if nodeCtx == common.ZONE_CTX {
		wo.woHeader.number = number
	} else {
		wo.woBody.header.SetNumber(number, nodeCtx)
	}
}

func (wo *WorkObject) SetDifficulty(difficulty *big.Int) {
	wo.woHeader.difficulty = difficulty
}

func (wo *WorkObject) SetNonce(nonce BlockNonce) {
	wo.woHeader.nonce = nonce
}

func (wo *WorkObject) SetHeaderHash(headerHash common.Hash) {
	wo.woHeader.headerHash = headerHash
}

func (wo *WorkObject) SetLocation(location common.Location) {
	wo.woHeader.SetLocation(location)
}

func (wo *WorkObject) SetEVMRoot(root common.Hash) {
	wo.woBody.header.SetEVMRoot(root)
}

func (wo *WorkObject) SetParentEntropy(entropy *big.Int, nodeCtx int) {
	wo.woBody.header.SetParentEntropy(entropy, nodeCtx)
}

func (wo *WorkObject) SetEtxRollupHash(hash common.Hash) {
	wo.woBody.header.SetEtxRollupHash(hash)
}

func (wo *WorkObject) SetBaseFee(fee *big.Int) {
	wo.woBody.header.SetBaseFee(fee)
}

func (wo *WorkObject) SetGasUsed(gasUsed uint64) {
	wo.woBody.header.SetGasUsed(gasUsed)
}

func (wo *WorkObject) SetGasLimit(gasLimit uint64) {
	wo.woBody.header.SetGasLimit(gasLimit)
}

func (wo *WorkObject) SetTime(time uint64) {
	wo.woBody.header.SetTime(time)
}

func (wo *WorkObject) SetCoinbase(coinbase common.Address) {
	wo.woBody.header.SetCoinbase(coinbase)
}

func (wo *WorkObject) SetManifestHash(hash common.Hash, nodeCtx int) {
	wo.woBody.header.SetManifestHash(hash, nodeCtx)
}

func (wo *WorkObject) SetParentDeltaS(deltaS *big.Int, nodeCtx int) {
	wo.woBody.header.SetParentDeltaS(deltaS, nodeCtx)
}

func (wo *WorkObject) SetUncleHash(hash common.Hash) {
	wo.woBody.header.SetUncleHash(hash)
}

func (wo *WorkObject) SetEtxHash(hash common.Hash) {
	wo.woBody.header.SetEtxHash(hash)
}

func (wo *WorkObject) SetReceiptHash(hash common.Hash) {
	wo.woBody.header.SetReceiptHash(hash)
}

func (wo *WorkObject) SetExtra(extra []byte) {
	wo.woBody.header.SetExtra(extra)
}

func (wo *WorkObject) SetTransactionsRoot(root common.Hash) {
	wo.woBody.header.SetTxHash(root)
}

func (wo *WorkObject) SetUTXORoot(root common.Hash) {
	wo.woBody.header.SetUTXORoot(root)
}

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

func (wo *WorkObject) SetAppendTime(appendTime time.Duration) {
	wo.appendTime.Store(appendTime)
}

func (wo *WorkObject) QiTransactions() []*Transaction {
	return wo.woBody.QiTransactions()
}

func (wo *WorkObject) QuaiTransactions() []*Transaction {
	return wo.woBody.QuaiTransactions()
}

func (wo *WorkObject) EncodeRLP(w io.Writer) error {
	return wo.woBody.header.EncodeRLP(w)
}

func (wo *WorkObject) Size() common.StorageSize {
	if size := wo.size.Load(); size != nil {
		if val, ok := size.(common.StorageSize); ok {
			return val
		}
	}
	return -1
}

func NewWorkObject(woHeader *WorkObjectHeader, woBody *WorkObjectBody, tx *Transaction) *WorkObject {
	newWo := &WorkObject{
		woHeader: woHeader,
		woBody:   woBody,
		tx:       tx,
	}
	newWo.SetHeader(woBody.Header())
	newWo.SetTransactions(woBody.Transactions())
	newWo.SetExtTransactions(woBody.ExtTransactions())
	newWo.SetUncles(woBody.Uncles())
	newWo.SetManifest(woBody.Manifest())
	return newWo
}

func NewBlock(header *WorkObject, txs []*Transaction, uncles []*WorkObject, etxs []*Transaction, subManifest BlockManifest, receipts []*Receipt, hasher TrieHasher, nodeCtx int) *WorkObject {
	b := &WorkObject{
		woHeader: CopyWorkObjectHeader(header.woHeader),
		woBody:   &WorkObjectBody{},
		tx:       NewEmptyTx(),
	}

	b.woBody.header = CopyHeader(header.Header())

	if len(txs) == 0 {
		b.woBody.header.SetTxHash(EmptyRootHash)
	} else {
		b.woBody.header.SetTxHash(DeriveSha(Transactions(txs), hasher))
		b.woBody.transactions = make(Transactions, len(txs))
		copy(b.woBody.transactions, txs)
	}

	if len(receipts) == 0 {
		b.woBody.header.SetReceiptHash(EmptyRootHash)
	} else {
		b.woBody.header.SetReceiptHash(DeriveSha(Receipts(receipts), hasher))
	}

	if len(uncles) == 0 {
		b.woBody.header.SetUncleHash(EmptyUncleHash)
	} else {
		b.woBody.header.SetUncleHash(CalcUncleHash(uncles))
		b.woBody.uncles = make([]*WorkObject, len(uncles))
		for i := range uncles {
			b.woBody.uncles[i] = CopyWorkObject(uncles[i])
		}
	}

	if len(etxs) == 0 {
		b.woBody.header.SetEtxHash(EmptyRootHash)
	} else {
		b.woBody.header.SetEtxHash(DeriveSha(Transactions(etxs), hasher))
		b.woBody.extTransactions = make(Transactions, len(etxs))
		copy(b.woBody.extTransactions, etxs)
	}

	// Since the subordinate's manifest lives in our body, we still need to check
	// that the manifest matches the subordinate's manifest hash, but we do not set
	// the subordinate's manifest hash.
	subManifestHash := EmptyRootHash
	if len(subManifest) != 0 {
		subManifestHash = DeriveSha(subManifest, hasher)
		b.woBody.manifest = make(BlockManifest, len(subManifest))
		copy(b.woBody.manifest, subManifest)
	}
	if nodeCtx < common.ZONE_CTX && subManifestHash != b.Header().ManifestHash(nodeCtx+1) {
		log.Global.Error("attempted to build block with invalid subordinate manifest")
		return nil
	}

	return b
}

func NewWorkObjectWithHeader(header *WorkObject, tx *Transaction, nodeCtx int) *WorkObject {
	woHeader := NewWorkObjectHeader(header.Hash(), header.ParentHash(common.ZONE_CTX), header.Number(common.ZONE_CTX), header.woHeader.difficulty, header.woHeader.txHash, header.woHeader.nonce, header.Location())
	woBody := NewWorkObjectBody(header, nil, nil, nil, nil, nil, nil, nodeCtx)
	return NewWorkObject(woHeader, woBody, tx)
}

func CopyWorkObject(wo *WorkObject) *WorkObject {
	newWo := &WorkObject{
		woHeader: CopyWorkObjectHeader(wo.woHeader),
		woBody:   CopyWorkObjectBody(wo.woBody),
		tx:       wo.tx,
	}
	newWo.SetHeader(wo.Header())
	return newWo
}
func (wo *WorkObject) RPCMarshalWorkObject() map[string]interface{} {
	result := map[string]interface{}{
		"woHeader": wo.woHeader.RPCMarshalWorkObjectHeader(),
		"woBody":   wo.woBody.RPCMarshalWorkObjectBody(),
		"tx":       wo.tx,
	}
	return result
}

func (wo *WorkObject) ProtoEncode() (*ProtoWorkObject, error) {
	header, err := wo.woHeader.ProtoEncode()
	if err != nil {
		return nil, err
	}
	body, err := wo.woBody.ProtoEncode()
	if err != nil {
		return nil, err
	}
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

func (wo *WorkObject) ProtoDecode(data *ProtoWorkObject) error {
	wo.woHeader = new(WorkObjectHeader)
	err := wo.woHeader.ProtoDecode(data.GetWoHeader())
	if err != nil {
		return err
	}
	wo.woBody = new(WorkObjectBody)
	err = wo.woBody.ProtoDecode(data.GetWoBody(), wo.woHeader.Location())
	if err != nil {
		return err
	}
	wo.tx = new(Transaction)
	err = wo.tx.ProtoDecode(data.GetTx(), wo.woHeader.Location())
	if err != nil {
		return err
	}
	return nil
}

// Work Object Header functions
func (wh *WorkObjectHeader) SetHeaderHash(headerHash common.Hash) {
	wh.headerHash = headerHash
}

func (wh *WorkObjectHeader) SetParentHash(parentHash common.Hash) {
	wh.parentHash = parentHash
}

func (wh *WorkObjectHeader) SetNumber(number *big.Int) {
	wh.number = number
}

func (wh *WorkObjectHeader) SetDifficulty(difficulty *big.Int) {
	wh.difficulty = difficulty
}

func (wh *WorkObjectHeader) SetTxHash(txHash common.Hash) {
	wh.txHash = txHash
}

func (wh *WorkObjectHeader) SetNonce(nonce BlockNonce) {
	wh.nonce = nonce
}

func (wh *WorkObjectHeader) SetMixHash(mixHash common.Hash) {
	wh.mixHash = mixHash
}

func (wh *WorkObjectHeader) SetLocation(location common.Location) {
	wh.location = location
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

func (wh *WorkObjectHeader) Difficulty() *big.Int {
	return wh.difficulty
}

func (wh *WorkObjectHeader) TxHash() common.Hash {
	return wh.txHash
}

func (wh *WorkObjectHeader) Nonce() BlockNonce {
	return wh.nonce
}

func (wh *WorkObjectHeader) MixHash() common.Hash {
	return wh.mixHash
}

func (wh *WorkObjectHeader) Location() common.Location {
	return wh.location
}

func NewWorkObjectHeader(headerHash common.Hash, parentHash common.Hash, number *big.Int, difficulty *big.Int, txHash common.Hash, nonce BlockNonce, location common.Location) *WorkObjectHeader {
	return &WorkObjectHeader{
		headerHash: headerHash,
		parentHash: parentHash,
		number:     number,
		difficulty: difficulty,
		txHash:     txHash,
		nonce:      nonce,
		location:   location,
	}
}

func CopyWorkObjectHeader(wh *WorkObjectHeader) *WorkObjectHeader {
	cpy := *wh
	cpy.SetHeaderHash(wh.HeaderHash())
	cpy.SetParentHash(wh.ParentHash())
	cpy.SetNumber(new(big.Int).Set(wh.Number()))
	cpy.SetDifficulty(new(big.Int).Set(wh.Difficulty()))
	cpy.SetTxHash(wh.TxHash())
	cpy.SetNonce(wh.Nonce())
	cpy.SetMixHash(wh.MixHash())
	cpy.SetLocation(wh.Location())
	return &cpy
}

func (wh *WorkObjectHeader) RPCMarshalWorkObjectHeader() map[string]interface{} {
	result := map[string]interface{}{
		"headerHash": wh.HeaderHash(),
		"parentHash": wh.ParentHash(),
		"number":     (*hexutil.Big)(wh.Number()),
		"difficulty": (*hexutil.Big)(wh.Difficulty()),
		"nonce":      wh.Nonce(),
		"location":   wh.Location(),
		"txHash":     wh.TxHash(),
		"mixHash":    wh.MixHash(),
	}
	return result
}

func (wh *WorkObjectHeader) Hash() (hash common.Hash) {
	sealHash := wh.SealHash().Bytes()
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	var hData [40]byte
	copy(hData[:], wh.Nonce().Bytes())
	copy(hData[len(wh.nonce):], sealHash)
	sum := blake3.Sum256(hData[:])
	hash.SetBytes(sum[:])
	return hash
}

func (wh *WorkObjectHeader) SealHash() (hash common.Hash) {
	hasherMu.Lock()
	defer hasherMu.Unlock()
	hasher.Reset()
	protoSealData := wh.SealEncode()
	data, err := proto.Marshal(protoSealData)
	if err != nil {
		log.Global.Error("Failed to marshal seal data ", "err", err)
	}
	sum := blake3.Sum256(data[:])
	hash.SetBytes(sum[:])
	return hash
}

func (wh *WorkObjectHeader) SealEncode() *ProtoWorkObjectHeader {
	hash := common.ProtoHash{Value: wh.HeaderHash().Bytes()}
	parentHash := common.ProtoHash{Value: wh.ParentHash().Bytes()}
	txHash := common.ProtoHash{Value: wh.TxHash().Bytes()}
	number := wh.Number().Bytes()
	difficulty := wh.Difficulty().Bytes()
	location := wh.Location().ProtoEncode()
	mixHash := common.ProtoHash{Value: wh.MixHash().Bytes()}

	return &ProtoWorkObjectHeader{
		HeaderHash: &hash,
		ParentHash: &parentHash,
		Number:     number,
		Difficulty: difficulty,
		TxHash:     &txHash,
		Location:   location,
		MixHash:    &mixHash,
	}
}

func (wh *WorkObjectHeader) ProtoEncode() (*ProtoWorkObjectHeader, error) {
	hash := common.ProtoHash{Value: wh.HeaderHash().Bytes()}
	parentHash := common.ProtoHash{Value: wh.ParentHash().Bytes()}
	txHash := common.ProtoHash{Value: wh.TxHash().Bytes()}
	number := wh.Number().Bytes()
	difficulty := wh.Difficulty().Bytes()
	location := wh.Location().ProtoEncode()
	nonce := wh.Nonce().Uint64()
	mixHash := common.ProtoHash{Value: wh.MixHash().Bytes()}

	return &ProtoWorkObjectHeader{
		HeaderHash: &hash,
		ParentHash: &parentHash,
		Number:     number,
		Difficulty: difficulty,
		TxHash:     &txHash,
		Location:   location,
		Nonce:      &nonce,
		MixHash:    &mixHash,
	}, nil
}

func (wh *WorkObjectHeader) ProtoDecode(data *ProtoWorkObjectHeader) error {
	if data.HeaderHash == nil || data.ParentHash == nil || data.Number == nil || data.Difficulty == nil || data.TxHash == nil || data.Nonce == nil || data.Location == nil {
		err := errors.New("failed to decode work object header")
		log.Global.WithField("err", err).Warn()
		return err
	}
	wh.SetHeaderHash(common.BytesToHash(data.GetHeaderHash().Value))
	wh.SetParentHash(common.BytesToHash(data.GetParentHash().Value))
	wh.SetNumber(new(big.Int).SetBytes(data.GetNumber()))
	wh.SetDifficulty(new(big.Int).SetBytes(data.Difficulty))
	wh.SetTxHash(common.BytesToHash(data.GetTxHash().Value))
	wh.SetNonce(uint64ToByteArr(data.GetNonce()))
	wh.SetLocation(data.GetLocation().GetValue())

	return nil
}

func (wb *WorkObjectBody) Header() *Header {
	return wb.header
}

func (wb *WorkObjectBody) Transactions() Transactions {
	return wb.transactions
}

func (wb *WorkObjectBody) ExtTransactions() Transactions {
	return wb.extTransactions
}

func (wb *WorkObjectBody) Uncles() []*WorkObject {
	return wb.uncles
}

func (wb *WorkObjectBody) Manifest() BlockManifest {
	return wb.manifest
}

func (wb *WorkObjectBody) SetHeader(header *Header) {
	wb.header = header
}

func (wb *WorkObjectBody) SetTransactions(txs Transactions) {
	wb.transactions = make(Transactions, len(txs))
	copy(wb.transactions, txs)
}

func (wb *WorkObjectBody) SetExtTransactions(etxs Transactions) {
	wb.extTransactions = make(Transactions, len(etxs))
	copy(wb.extTransactions, etxs)
}

func (wb *WorkObjectBody) SetUncles(uncles []*WorkObject) {
	wb.uncles = make([]*WorkObject, len(uncles))
	copy(wb.uncles, uncles)
}

func (wb *WorkObjectBody) SetManifest(manifest BlockManifest) {
	wb.manifest = make([]common.Hash, len(manifest))
	copy(wb.manifest, manifest)
}

func NewWorkObjectBody(header *WorkObject, txs []*Transaction, etxs []*Transaction, uncles []*WorkObject, subManifest BlockManifest, receipts []*Receipt, hasher TrieHasher, nodeCtx int) *WorkObjectBody {
	wb := &WorkObjectBody{header: CopyHeader(header.Body().header)}

	// TODO: panic if len(txs) != len(receipts)
	if len(txs) == 0 {
		wb.header.SetTxHash(EmptyRootHash)
	} else {
		wb.header.SetTxHash(DeriveSha(Transactions(txs), hasher))
		wb.transactions = make(Transactions, len(txs))
		copy(wb.transactions, txs)
	}

	if len(receipts) == 0 {
		wb.header.SetReceiptHash(EmptyRootHash)
	} else {
		wb.header.SetReceiptHash(DeriveSha(Receipts(receipts), hasher))
	}

	if len(uncles) == 0 {
		wb.header.SetUncleHash(EmptyUncleHash)
	} else {
		wb.header.SetUncleHash(CalcUncleHash(uncles))
		wb.uncles = make([]*WorkObject, len(uncles))
		for i := range uncles {
			wb.uncles[i] = CopyWorkObject(uncles[i])
		}
	}

	if len(etxs) == 0 {
		wb.header.SetEtxHash(EmptyRootHash)
	} else {
		wb.header.SetEtxHash(DeriveSha(Transactions(etxs), hasher))
		wb.extTransactions = make(Transactions, len(etxs))
		copy(wb.extTransactions, etxs)
	}

	// Since the subordinate's manifest lives in our body, we still need to check
	// that the manifest matches the subordinate's manifest hash, but we do not set
	// the subordinate's manifest hash.
	subManifestHash := EmptyRootHash
	if len(subManifest) != 0 {
		subManifestHash = DeriveSha(subManifest, hasher)
		wb.manifest = make(BlockManifest, len(subManifest))
		copy(wb.manifest, subManifest)
	}
	//fmt.Println("context:", nodeCtx, "subManifestHash", subManifestHash, "wb.Header().ManifestHash(nodeCtx+1)", wb.Header().ManifestHash(nodeCtx+1))
	if nodeCtx < common.ZONE_CTX && subManifestHash != wb.Header().ManifestHash(nodeCtx+1) {
		log.Global.Error("attempted to build block with invalid subordinate manifest")
		return nil
	}

	return wb
}

func CopyWorkObjectBody(wb *WorkObjectBody) *WorkObjectBody {
	cpy := &WorkObjectBody{header: CopyHeader(wb.header)}
	cpy.SetTransactions(wb.Transactions())
	cpy.SetExtTransactions(wb.ExtTransactions())
	cpy.SetUncles(wb.Uncles())
	cpy.SetManifest(wb.Manifest())

	return cpy
}

func CopyWorkObjectBodyFromParts(header *Header, transactions []*Transaction, extTransactions []*Transaction, uncles []*WorkObject, manifest BlockManifest) *WorkObjectBody {
	cpy := &WorkObjectBody{header: CopyHeader(header)}
	cpy.SetTransactions(transactions)
	cpy.SetExtTransactions(extTransactions)
	cpy.SetUncles(uncles)
	cpy.SetManifest(manifest)

	return cpy
}
func (wb *WorkObjectBody) ProtoEncode() (*ProtoWorkObjectBody, error) {
	header, err := wb.header.ProtoEncode()
	if err != nil {
		return nil, err
	}

	protoTransactions, err := wb.transactions.ProtoEncode()
	if err != nil {
		return nil, err
	}

	protoExtTransactions, err := wb.extTransactions.ProtoEncode()
	if err != nil {
		return nil, err
	}

	protoUncles := &ProtoWorkObjects{}
	for _, unc := range wb.uncles {
		protoUncle, err := unc.ProtoEncode()
		if err != nil {
			return nil, err
		}
		protoUncles.WorkObjects = append(protoUncles.WorkObjects, protoUncle)
	}

	protoManifest, err := wb.manifest.ProtoEncode()
	if err != nil {
		return nil, err
	}

	return &ProtoWorkObjectBody{
		Header:          header,
		Transactions:    protoTransactions,
		ExtTransactions: protoExtTransactions,
		Uncles:          protoUncles,
		Manifest:        protoManifest,
	}, nil
}

func (wb *WorkObjectBody) ProtoDecode(data *ProtoWorkObjectBody, location common.Location) error {
	wb.header = &Header{}
	err := wb.header.ProtoDecode(data.GetHeader(), location)
	if err != nil {
		return err
	}
	wb.transactions = Transactions{}
	err = wb.transactions.ProtoDecode(data.GetTransactions(), location)
	if err != nil {
		return err
	}
	wb.extTransactions = Transactions{}
	err = wb.extTransactions.ProtoDecode(data.GetExtTransactions(), location)
	if err != nil {
		return err
	}
	wb.uncles = make([]*WorkObject, len(data.GetUncles().GetWorkObjects()))
	for i, protoUncle := range data.GetUncles().GetWorkObjects() {
		uncle := &WorkObject{}
		err = uncle.ProtoDecode(protoUncle)
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

	return nil
}

func (wb *WorkObjectBody) RPCMarshalWorkObjectBody() map[string]interface{} {
	result := map[string]interface{}{
		"header":          wb.header.RPCMarshalHeader(),
		"transactions":    wb.Transactions(),
		"extTransactions": wb.ExtTransactions(),
		"uncles":          wb.Uncles(),
		"manifest":        wb.Manifest(),
	}
	return result
}

func (wb *WorkObjectBody) QiTransactions() []*Transaction {
	// TODO: cache the UTXO loop
	qiTxs := make([]*Transaction, 0)
	for _, t := range wb.Transactions() {
		if t.Type() == QiTxType {
			qiTxs = append(qiTxs, t)
		}
	}
	return qiTxs
}

func (wb *WorkObjectBody) QuaiTransactions() []*Transaction {
	quaiTxs := make([]*Transaction, 0)
	for _, t := range wb.Transactions() {
		if t.Type() != QiTxType {
			quaiTxs = append(quaiTxs, t)
		}
	}
	return quaiTxs
}

func CalcUncleHash(uncles []*WorkObject) common.Hash {
	if len(uncles) == 0 {
		return EmptyUncleHash
	}
	return RlpHash(uncles)
}
