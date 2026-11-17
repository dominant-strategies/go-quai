package hdwallet

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
	base58 "github.com/mr-tron/base58/base58"
)

const (
	paymentCodeOuterVersion byte = 0x47
	paymentCodeInnerVersion byte = 0x01
	paymentCodeFeatures     byte = 0x00
	paymentCodeBodyLen           = 80
	paymentCodeEncodedLen        = 81
)

type QiPaymentAddressKind string

const (
	QiPaymentAddressSend    QiPaymentAddressKind = "send"
	QiPaymentAddressReceive QiPaymentAddressKind = "receive"
)

// QiPaymentAddressInfo tracks Qi payment-code-derived addresses so the wallet can
// later recover the correct private key for signing inputs controlled by the wallet.
type QiPaymentAddressInfo struct {
	Address          string
	PubKey           string
	CounterpartyCode string
	Account          uint32
	Index            uint32
	Zone             common.Location
	Kind             QiPaymentAddressKind
}

type paymentCodeNode struct {
	body []byte
	root *hdkeychain.ExtendedKey
}

func paymentCodeDerivationWorkerCount() int {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

func (w *HDWallet) storeQiPaymentAddressInfo(info *QiPaymentAddressInfo) error {
	key, err := normalizeAddressKey(info.Address)
	if err != nil {
		return err
	}
	w.qiPaymentAddresses[key] = info
	return nil
}

func (w *HDWallet) qiPaymentAddressInfo(address string) (*QiPaymentAddressInfo, error) {
	key, err := normalizeAddressKey(address)
	if err != nil {
		return nil, err
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	info, ok := w.qiPaymentAddresses[key]
	if !ok {
		return nil, fmt.Errorf("address %s not found in qi payment address store", address)
	}
	return info, nil
}

func (w *HDWallet) getPrivateKeyForQiPaymentInfo(info *QiPaymentAddressInfo) (*ecdsa.PrivateKey, error) {
	if info.Kind != QiPaymentAddressReceive {
		return nil, fmt.Errorf("address %s is not controlled by this wallet", info.Address)
	}
	return w.GetQiPaymentReceivePrivateKey(info.CounterpartyCode, info.Account, info.Index)
}

func (w *HDWallet) bip47AccountNode(account uint32) (*HDNode, error) {
	if w.mnemonic == nil {
		return nil, fmt.Errorf("wallet mnemonic unavailable")
	}
	seed, err := w.mnemonic.ComputeSeed()
	if err != nil {
		return nil, fmt.Errorf("failed to compute seed: %w", err)
	}
	master, err := NewMasterNode(seed)
	if err != nil {
		return nil, err
	}
	// Qi payment codes use the BIP47 branch rooted at m/47'/969'/account'.
	return master.DerivePath(fmt.Sprintf("m/47'/%d'/%d'", CoinTypeQi, account))
}

func (w *HDWallet) GetQiPaymentCode(account uint32) (string, error) {
	accountNode, err := w.bip47AccountNode(account)
	if err != nil {
		return "", err
	}
	pubKey, err := accountNode.PublicKeyBytes()
	if err != nil {
		return "", err
	}
	body := make([]byte, paymentCodeBodyLen)
	body[0] = paymentCodeInnerVersion
	body[1] = paymentCodeFeatures
	copy(body[2:35], pubKey)
	copy(body[35:67], accountNode.ChainCode())
	return encodeBase58Check(append([]byte{paymentCodeOuterVersion}, body...)), nil
}

func ValidateQiPaymentCode(paymentCode string) bool {
	node, err := decodePaymentCode(paymentCode)
	return err == nil && node != nil
}

func (w *HDWallet) DeriveQiPaymentChannelSendAddress(counterpartyPaymentCode string, zone common.Location, account, startIndex uint32) (*AddressInfo, error) {
	start := time.Now()
	counterparty, err := decodePaymentCode(counterpartyPaymentCode)
	if err != nil {
		return nil, err
	}
	accountNode, err := w.bip47AccountNode(account)
	if err != nil {
		return nil, err
	}
	notificationPriv, err := derivePrivateKeyAt(accountNode, 0)
	if err != nil {
		return nil, err
	}
	btcNotifPriv, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(notificationPriv))

	type sendResult struct {
		index    uint32
		pubBytes []byte
		addr     []byte
	}

	workers := paymentCodeDerivationWorkerCount()

	// bestIndex tracks the lowest valid index found so far. Workers
	// abort once they pass this value since any result they find would
	// be higher.
	var bestIndex atomic.Uint64
	bestIndex.Store(uint64(startIndex) + uint64(MaxDerivationAttempts))

	var wg sync.WaitGroup
	results := make(chan sendResult, workers)
	attemptCounts := make([]uint32, workers)

	for wk := 0; wk < workers; wk++ {
		wg.Add(1)
		go func(workerIndex int) {
			localAttempts := uint32(0)
			defer func() {
				attemptCounts[workerIndex] = localAttempts
				wg.Done()
			}()
			workerOffset := uint32(workerIndex)
			for i := workerOffset; i < MaxDerivationAttempts; i += uint32(workers) {
				index := startIndex + i
				if uint64(index) >= bestIndex.Load() {
					return
				}
				localAttempts++
				receiverNode, err := derivePaymentCodeChild(counterparty.root, index)
				if err != nil {
					continue
				}
				receiverPub, err := receiverNode.ECPubKey()
				if err != nil {
					continue
				}
				derivedPub, err := derivePaymentPublicKeyFromPrivate(receiverPub, btcNotifPriv)
				if err != nil {
					continue
				}
				addrBytes := crypto.Keccak256(crypto.FromECDSAPub(derivedPub)[1:])[12:]
				if !IsValidAddressForZone(CoinTypeQi, addrBytes, zone) {
					continue
				}
				// Atomically update bestIndex so other workers can abort early.
				for {
					cur := bestIndex.Load()
					if uint64(index) >= cur {
						break
					}
					if bestIndex.CompareAndSwap(cur, uint64(index)) {
						break
					}
				}
				results <- sendResult{
					index:    index,
					pubBytes: crypto.CompressPubkey(derivedPub),
					addr:     addrBytes,
				}
				return
			}
		}(wk)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results and pick the lowest index.
	var best *sendResult
	for r := range results {
		if best == nil || r.index < best.index {
			best = &r
		}
	}
	totalAttempts := uint32(0)
	for _, attempts := range attemptCounts {
		totalAttempts += attempts
	}

	if best == nil {
		return nil, fmt.Errorf("no valid payment-code send address found after %d attempts", MaxDerivationAttempts)
	}

	info := &AddressInfo{
		PubKey:  "0x" + hex.EncodeToString(best.pubBytes),
		Address: "0x" + hex.EncodeToString(best.addr),
		Account: account,
		Change:  false,
		Index:   best.index,
		Zone:    zone,
		IsQi:    true,
		// In the parallel search this reflects the true number of candidate
		// indices examined across all workers, not merely the winning index span.
		DerivationAttempts:  totalAttempts,
		DerivationElapsedMs: time.Since(start).Milliseconds(),
	}

	w.mu.Lock()
	storeErr := w.storeQiPaymentAddressInfo(&QiPaymentAddressInfo{
		Address:          info.Address,
		PubKey:           info.PubKey,
		CounterpartyCode: counterpartyPaymentCode,
		Account:          account,
		Index:            best.index,
		Zone:             zone,
		Kind:             QiPaymentAddressSend,
	})
	w.mu.Unlock()
	if storeErr != nil {
		return nil, storeErr
	}
	fmt.Printf(
		"[WalletInfo] Go DeriveQiPaymentChannelSendAddress startIndex=%d resolvedIndex=%d attempts=%d elapsedMs=%d zone=%v workers=%d\n",
		startIndex,
		best.index,
		info.DerivationAttempts,
		info.DerivationElapsedMs,
		zone,
		workers,
	)
	return info, nil
}

func (w *HDWallet) DeriveQiPaymentChannelReceiveAddress(counterpartyPaymentCode string, zone common.Location, account, startIndex uint32) (*AddressInfo, error) {
	start := time.Now()
	accountNode, err := w.bip47AccountNode(account)
	if err != nil {
		return nil, err
	}
	counterparty, err := decodePaymentCode(counterpartyPaymentCode)
	if err != nil {
		return nil, err
	}
	notificationPub, err := paymentCodeNotificationPubKey(counterparty.root)
	if err != nil {
		return nil, err
	}

	type recvResult struct {
		index    uint32
		pubBytes []byte
		addr     []byte
	}

	workers := paymentCodeDerivationWorkerCount()

	var bestIndex atomic.Uint64
	bestIndex.Store(uint64(startIndex) + uint64(MaxDerivationAttempts))

	var wg sync.WaitGroup
	results := make(chan recvResult, workers)
	attemptCounts := make([]uint32, workers)

	for wk := 0; wk < workers; wk++ {
		wg.Add(1)
		go func(workerIndex int) {
			localAttempts := uint32(0)
			defer func() {
				attemptCounts[workerIndex] = localAttempts
				wg.Done()
			}()
			workerOffset := uint32(workerIndex)
			for i := workerOffset; i < MaxDerivationAttempts; i += uint32(workers) {
				index := startIndex + i
				if uint64(index) >= bestIndex.Load() {
					return
				}
				localAttempts++
				_, derivedPub, err := derivePaymentKeyPair(accountNode, notificationPub, index)
				if err != nil {
					continue
				}
				addrBytes := crypto.Keccak256(crypto.FromECDSAPub(derivedPub)[1:])[12:]
				if !IsValidAddressForZone(CoinTypeQi, addrBytes, zone) {
					continue
				}
				for {
					cur := bestIndex.Load()
					if uint64(index) >= cur {
						break
					}
					if bestIndex.CompareAndSwap(cur, uint64(index)) {
						break
					}
				}
				results <- recvResult{
					index:    index,
					pubBytes: crypto.CompressPubkey(derivedPub),
					addr:     addrBytes,
				}
				return
			}
		}(wk)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var best *recvResult
	for r := range results {
		if best == nil || r.index < best.index {
			best = &r
		}
	}
	totalAttempts := uint32(0)
	for _, attempts := range attemptCounts {
		totalAttempts += attempts
	}

	if best == nil {
		return nil, fmt.Errorf("no valid payment-code receive address found after %d attempts", MaxDerivationAttempts)
	}

	info := &AddressInfo{
		PubKey:              "0x" + hex.EncodeToString(best.pubBytes),
		Address:             "0x" + hex.EncodeToString(best.addr),
		Account:             account,
		Change:              false,
		Index:               best.index,
		Zone:                zone,
		IsQi:                true,
		DerivationAttempts:  totalAttempts,
		DerivationElapsedMs: time.Since(start).Milliseconds(),
	}

	w.mu.Lock()
	storeErr := w.storeQiPaymentAddressInfo(&QiPaymentAddressInfo{
		Address:          info.Address,
		PubKey:           info.PubKey,
		CounterpartyCode: counterpartyPaymentCode,
		Account:          account,
		Index:            best.index,
		Zone:             zone,
		Kind:             QiPaymentAddressReceive,
	})
	w.mu.Unlock()
	if storeErr != nil {
		return nil, storeErr
	}
	return info, nil
}

func (w *HDWallet) GetQiPaymentReceivePrivateKey(counterpartyPaymentCode string, account, index uint32) (*ecdsa.PrivateKey, error) {
	accountNode, err := w.bip47AccountNode(account)
	if err != nil {
		return nil, err
	}
	counterparty, err := decodePaymentCode(counterpartyPaymentCode)
	if err != nil {
		return nil, err
	}
	notificationPub, err := paymentCodeNotificationPubKey(counterparty.root)
	if err != nil {
		return nil, err
	}
	priv, _, err := derivePaymentKeyPair(accountNode, notificationPub, index)
	return priv, err
}

// GetPrivateKeyForQiInput derives the exact private key needed for one selected
// Qi input from the persisted derivation metadata attached to that input.
func (w *HDWallet) GetPrivateKeyForQiInput(input QiTxInputParam) (*ecdsa.PrivateKey, error) {
	switch input.DerivationKind {
	case "bip44External", "bip44Change":
		changeBit := uint32(0)
		if input.Change || input.DerivationKind == "bip44Change" {
			changeBit = 1
		}
		node, err := w.root.DerivePath(fmt.Sprintf("%d'/%d/%d", input.Account, changeBit, input.DerivationIndex))
		if err != nil {
			return nil, err
		}
		return node.PrivateKey()
	case "bip47Receive":
		if input.CounterpartyPaymentCode == "" {
			return nil, fmt.Errorf("missing counterparty payment code for bip47 receive input")
		}
		return w.GetQiPaymentReceivePrivateKey(input.CounterpartyPaymentCode, input.Account, input.DerivationIndex)
	case "bip47Send":
		return nil, fmt.Errorf("bip47 send addresses are not controlled by this wallet")
	default:
		return nil, fmt.Errorf("unsupported qi derivation kind %q", input.DerivationKind)
	}
}

func derivePaymentCodeChild(root *hdkeychain.ExtendedKey, index uint32) (*hdkeychain.ExtendedKey, error) {
	return root.Derive(index)
}

func paymentCodeNotificationPubKey(root *hdkeychain.ExtendedKey) (*btcec.PublicKey, error) {
	child, err := derivePaymentCodeChild(root, 0)
	if err != nil {
		return nil, err
	}
	return child.ECPubKey()
}

func derivePrivateKeyAt(root *HDNode, index uint32) (*ecdsa.PrivateKey, error) {
	child, err := root.DeriveChild(index)
	if err != nil {
		return nil, err
	}
	return child.PrivateKey()
}

func derivePaymentPublicKeyFromPrivate(derivedPub *btcec.PublicKey, notifPriv *btcec.PrivateKey) (*ecdsa.PublicKey, error) {
	sharedX := btcec.GenerateSharedSecret(notifPriv, derivedPub)
	sharedScalar := sha256.Sum256(padTo32Bytes(sharedX))
	return addScalarToPublicKey(derivedPub, sharedScalar[:])
}

func derivePaymentKeyPair(accountNode *HDNode, notificationPub *btcec.PublicKey, index uint32) (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	bNode, err := accountNode.DeriveChild(index)
	if err != nil {
		return nil, nil, err
	}
	bPriv, err := bNode.PrivateKey()
	if err != nil {
		return nil, nil, err
	}
	btcPriv, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(bPriv))
	sharedX := btcec.GenerateSharedSecret(btcPriv, notificationPub)
	sharedScalar := sha256.Sum256(padTo32Bytes(sharedX))
	derivedPriv, err := addScalarToPrivateKey(bPriv, sharedScalar[:])
	if err != nil {
		return nil, nil, err
	}
	return derivedPriv, &derivedPriv.PublicKey, nil
}

func addScalarToPrivateKey(basePriv *ecdsa.PrivateKey, scalar []byte) (*ecdsa.PrivateKey, error) {
	curveN := crypto.S256().Params().N
	sum := new(big.Int).Add(basePriv.D, new(big.Int).SetBytes(scalar))
	sum.Mod(sum, curveN)
	if sum.Sign() == 0 {
		return nil, fmt.Errorf("derived zero private key")
	}
	return crypto.ToECDSA(padTo32Bytes(sum.Bytes()))
}

func addScalarToPublicKey(basePub *btcec.PublicKey, scalar []byte) (*ecdsa.PublicKey, error) {
	var k secp.ModNScalar
	k.SetByteSlice(scalar)

	var scalarPoint secp.JacobianPoint
	secp.ScalarBaseMultNonConst(&k, &scalarPoint)

	var basePoint secp.JacobianPoint
	basePub.AsJacobian(&basePoint)

	var result secp.JacobianPoint
	secp.AddNonConst(&basePoint, &scalarPoint, &result)
	result.ToAffine()

	xBytes := result.X.Bytes()
	yBytes := result.Y.Bytes()
	return &ecdsa.PublicKey{
		Curve: crypto.S256(),
		X:     new(big.Int).SetBytes(xBytes[:]),
		Y:     new(big.Int).SetBytes(yBytes[:]),
	}, nil
}

func decodePaymentCode(paymentCode string) (*paymentCodeNode, error) {
	decoded, err := decodeBase58Check(paymentCode)
	if err != nil {
		return nil, err
	}
	if len(decoded) != paymentCodeEncodedLen {
		return nil, fmt.Errorf("invalid payment code length %d", len(decoded))
	}
	if decoded[0] != paymentCodeOuterVersion {
		return nil, fmt.Errorf("invalid payment code version 0x%02x", decoded[0])
	}
	body := decoded[1:]
	if body[0] != paymentCodeInnerVersion {
		return nil, fmt.Errorf("unsupported payment code inner version 0x%02x", body[0])
	}
	if body[1] != paymentCodeFeatures {
		return nil, fmt.Errorf("unsupported payment code features 0x%02x", body[1])
	}
	if body[2] != 0x02 && body[2] != 0x03 {
		return nil, fmt.Errorf("invalid payment code public key prefix")
	}
	pubKeyBytes := body[2:35]
	if _, err := crypto.DecompressPubkey(pubKeyBytes); err != nil {
		return nil, fmt.Errorf("invalid payment code public key: %w", err)
	}
	for _, b := range body[67:] {
		if b != 0 {
			return nil, fmt.Errorf("invalid payment code padding")
		}
	}
	root := hdkeychain.NewExtendedKey(
		chaincfg.MainNetParams.HDPublicKeyID[:],
		pubKeyBytes,
		body[35:67],
		[]byte{0, 0, 0, 0},
		0,
		0,
		false,
	)
	return &paymentCodeNode{
		body: append([]byte(nil), body...),
		root: root,
	}, nil
}

func encodeBase58Check(payload []byte) string {
	sum := sha256.Sum256(payload)
	sum = sha256.Sum256(sum[:])
	full := make([]byte, 0, len(payload)+4)
	full = append(full, payload...)
	full = append(full, sum[:4]...)
	return base58.Encode(full)
}

func decodeBase58Check(encoded string) ([]byte, error) {
	full, err := base58.Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid base58: %w", err)
	}
	if len(full) < 5 {
		return nil, fmt.Errorf("base58check payload too short")
	}
	payload := full[:len(full)-4]
	sum := sha256.Sum256(payload)
	sum = sha256.Sum256(sum[:])
	checksum := full[len(full)-4:]
	if checksum[0] != sum[0] || checksum[1] != sum[1] || checksum[2] != sum[2] || checksum[3] != sum[3] {
		return nil, fmt.Errorf("invalid base58 checksum")
	}
	return payload, nil
}

func padTo32Bytes(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}
