package hdwallet

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
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
	accountNode, err := w.bip47AccountNode(account)
	if err != nil {
		return nil, err
	}
	counterparty, err := decodePaymentCode(counterpartyPaymentCode)
	if err != nil {
		return nil, err
	}
	notificationPriv, err := derivePrivateKeyAt(accountNode, 0)
	if err != nil {
		return nil, err
	}

	for attempts := uint32(0); attempts < MaxDerivationAttempts; attempts++ {
		index := startIndex + attempts
		// BIP47 derivation is zone-agnostic, so we scan forward until the derived
		// address lands in the requested Qi zone.
		receiverNode, err := derivePaymentCodeChild(counterparty.root, index)
		if err != nil {
			continue
		}
		receiverPub, err := receiverNode.ECPubKey()
		if err != nil {
			continue
		}
		derivedPub, err := derivePaymentPublicKeyFromPrivate(receiverPub.ToECDSA(), notificationPriv)
		if err != nil {
			continue
		}
		addrBytes := crypto.Keccak256(crypto.FromECDSAPub(derivedPub)[1:])[12:]
		if !IsValidAddressForZone(CoinTypeQi, addrBytes, zone) {
			continue
		}
		pubBytes := crypto.CompressPubkey(derivedPub)
		info := &AddressInfo{
			PubKey:  "0x" + hex.EncodeToString(pubBytes),
			Address: "0x" + hex.EncodeToString(addrBytes),
			Account: account,
			Change:  false,
			Index:   index,
			Zone:    zone,
			IsQi:    true,
		}
		// Persist the derivation metadata so later signing can recover the exact
		// private key that controls this payment-code-derived address.
		w.mu.Lock()
		storeErr := w.storeQiPaymentAddressInfo(&QiPaymentAddressInfo{
			Address:          info.Address,
			PubKey:           info.PubKey,
			CounterpartyCode: counterpartyPaymentCode,
			Account:          account,
			Index:            index,
			Zone:             zone,
			Kind:             QiPaymentAddressSend,
		})
		w.mu.Unlock()
		if storeErr != nil {
			return nil, storeErr
		}
		return info, nil
	}

	return nil, fmt.Errorf("no valid payment-code send address found after %d attempts", MaxDerivationAttempts)
}

func (w *HDWallet) DeriveQiPaymentChannelReceiveAddress(counterpartyPaymentCode string, zone common.Location, account, startIndex uint32) (*AddressInfo, error) {
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

	for attempts := uint32(0); attempts < MaxDerivationAttempts; attempts++ {
		index := startIndex + attempts
		// Receiving addresses are derived from our payment code private branch and
		// the counterparty notification pubkey, then filtered to the target zone.
		derivedPriv, derivedPub, err := derivePaymentKeyPair(accountNode, notificationPub, index)
		if err != nil {
			continue
		}
		_ = derivedPriv
		addrBytes := crypto.Keccak256(crypto.FromECDSAPub(derivedPub)[1:])[12:]
		if !IsValidAddressForZone(CoinTypeQi, addrBytes, zone) {
			continue
		}
		pubBytes := crypto.CompressPubkey(derivedPub)
		info := &AddressInfo{
			PubKey:  "0x" + hex.EncodeToString(pubBytes),
			Address: "0x" + hex.EncodeToString(addrBytes),
			Account: account,
			Change:  false,
			Index:   index,
			Zone:    zone,
			IsQi:    true,
		}
		// Only receive-side payment-code addresses are spendable by this wallet, so
		// we retain their metadata for later key recovery during input signing.
		w.mu.Lock()
		storeErr := w.storeQiPaymentAddressInfo(&QiPaymentAddressInfo{
			Address:          info.Address,
			PubKey:           info.PubKey,
			CounterpartyCode: counterpartyPaymentCode,
			Account:          account,
			Index:            index,
			Zone:             zone,
			Kind:             QiPaymentAddressReceive,
		})
		w.mu.Unlock()
		if storeErr != nil {
			return nil, storeErr
		}
		return info, nil
	}

	return nil, fmt.Errorf("no valid payment-code receive address found after %d attempts", MaxDerivationAttempts)
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

func paymentCodeNotificationPubKey(root *hdkeychain.ExtendedKey) (*ecdsa.PublicKey, error) {
	child, err := derivePaymentCodeChild(root, 0)
	if err != nil {
		return nil, err
	}
	pub, err := child.ECPubKey()
	if err != nil {
		return nil, err
	}
	return pub.ToECDSA(), nil
}

func derivePrivateKeyAt(root *HDNode, index uint32) (*ecdsa.PrivateKey, error) {
	child, err := root.DeriveChild(index)
	if err != nil {
		return nil, err
	}
	return child.PrivateKey()
}

func derivePaymentPublicKeyFromPrivate(derivedPub *ecdsa.PublicKey, notificationPriv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	curve := crypto.S256()
	sx, sy := curve.ScalarMult(derivedPub.X, derivedPub.Y, crypto.FromECDSA(notificationPriv))
	if sx == nil || sy == nil {
		return nil, fmt.Errorf("failed to compute shared secret")
	}
	sharedScalar := sha256.Sum256(padTo32Bytes(sx.Bytes()))
	return addScalarToPublicKey(derivedPub, sharedScalar[:])
}

func derivePaymentKeyPair(accountNode *HDNode, notificationPub *ecdsa.PublicKey, index uint32) (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	bNode, err := accountNode.DeriveChild(index)
	if err != nil {
		return nil, nil, err
	}
	bPriv, err := bNode.PrivateKey()
	if err != nil {
		return nil, nil, err
	}
	sx, sy := crypto.S256().ScalarMult(notificationPub.X, notificationPub.Y, crypto.FromECDSA(bPriv))
	if sx == nil || sy == nil {
		return nil, nil, fmt.Errorf("failed to compute shared secret")
	}
	sharedScalar := sha256.Sum256(padTo32Bytes(sx.Bytes()))
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

func addScalarToPublicKey(basePub *ecdsa.PublicKey, scalar []byte) (*ecdsa.PublicKey, error) {
	curve := crypto.S256()
	sx, sy := curve.ScalarBaseMult(scalar)
	if sx == nil || sy == nil {
		return nil, fmt.Errorf("failed to derive scalar point")
	}
	x, y := curve.Add(basePub.X, basePub.Y, sx, sy)
	if x == nil || y == nil {
		return nil, fmt.Errorf("failed to add scalar to public key")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
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
