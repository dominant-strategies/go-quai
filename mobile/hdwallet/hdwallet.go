package hdwallet

import (
	"crypto/ecdsa"
	"fmt"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/crypto"
)

// Coin type constants matching quais.js AllowedCoinType.
const (
	CoinTypeQuai uint32 = 994
	CoinTypeQi   uint32 = 969
)

// MaxDerivationAttempts matches quais.js MAX_ADDRESS_DERIVATION_ATTEMPTS.
const MaxDerivationAttempts = 10_000_000

// AddressInfo represents a derived address with its metadata.
type AddressInfo struct {
	PubKey  string          `json:"pubKey"`  // hex compressed public key
	Address string          `json:"address"` // 0x-prefixed hex address
	Account uint32          `json:"account"`
	Index   uint32          `json:"index"`
	Zone    common.Location `json:"zone"`
	IsQi    bool            `json:"isQi"`
}

// HDWallet is the top-level HD wallet supporting zone-aware address derivation.
type HDWallet struct {
	mu       sync.RWMutex
	root     *HDNode   // derived to m/44'/<coinType>'
	mnemonic *Mnemonic
	coinType uint32

	// Track derived addresses and the next index per account/change
	addresses  map[string]*AddressInfo // address hex -> info
	nextIndex  map[accountKey]uint32   // track next derivation index
}

type accountKey struct {
	account uint32
	change  bool
}

// NewHDWallet creates a wallet from a mnemonic for the given coin type.
func NewHDWallet(mnemonic *Mnemonic, coinType uint32) (*HDWallet, error) {
	if coinType != CoinTypeQuai && coinType != CoinTypeQi {
		return nil, fmt.Errorf("unsupported coin type %d (must be %d or %d)", coinType, CoinTypeQuai, CoinTypeQi)
	}

	seed, err := mnemonic.ComputeSeed()
	if err != nil {
		return nil, fmt.Errorf("failed to compute seed: %w", err)
	}

	master, err := NewMasterNode(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to create master node: %w", err)
	}

	// Derive to m/44'/<coinType>'
	root, err := master.DerivePath(fmt.Sprintf("m/44'/%d'", coinType))
	if err != nil {
		return nil, fmt.Errorf("failed to derive root path: %w", err)
	}

	return &HDWallet{
		root:      root,
		mnemonic:  mnemonic,
		coinType:  coinType,
		addresses: make(map[string]*AddressInfo),
		nextIndex: make(map[accountKey]uint32),
	}, nil
}

// NewHDWalletFromPhrase is a convenience constructor.
func NewHDWalletFromPhrase(phrase string, password string, coinType uint32) (*HDWallet, error) {
	m, err := NewMnemonicFromPhrase(phrase, password)
	if err != nil {
		return nil, err
	}
	return NewHDWallet(m, coinType)
}

// NewRandomHDWallet generates a new random wallet.
func NewRandomHDWallet(coinType uint32) (*HDWallet, error) {
	m, err := GenerateMnemonic("")
	if err != nil {
		return nil, err
	}
	return NewHDWallet(m, coinType)
}

// DeriveAddress derives the next address for a given account and target zone.
// It iterates child indices until an address matching the target zone AND
// correct ledger scope is found.
func (w *HDWallet) DeriveAddress(account uint32, zone common.Location) (*AddressInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	key := accountKey{account: account, change: false}
	startIndex := w.nextIndex[key]

	// Derive the account node: m/44'/<coinType>'/<account>'/0
	accountNode, err := w.root.DerivePath(fmt.Sprintf("%d'/0", account))
	if err != nil {
		return nil, fmt.Errorf("failed to derive account node: %w", err)
	}

	for i := uint32(0); i < MaxDerivationAttempts; i++ {
		index := startIndex + i
		child, err := accountNode.DeriveChild(index)
		if err != nil {
			continue // skip invalid derivations
		}

		addrBytes, err := child.AddressBytes()
		if err != nil {
			continue
		}

		if IsValidAddressForZone(w.coinType, addrBytes, zone) {
			addrHex := fmt.Sprintf("0x%x", addrBytes)
			pubBytes, err := child.PublicKeyBytes()
			if err != nil {
				continue
			}
			pubHex := fmt.Sprintf("0x%x", pubBytes)

			info := &AddressInfo{
				PubKey:  pubHex,
				Address: addrHex,
				Account: account,
				Index:   index,
				Zone:    zone,
				IsQi:    w.coinType == CoinTypeQi,
			}
			w.addresses[addrHex] = info
			w.nextIndex[key] = index + 1
			return info, nil
		}
	}

	return nil, fmt.Errorf("no valid address found after %d attempts for zone %v", MaxDerivationAttempts, zone)
}

// DeriveAddressAtIndex derives a specific address without zone filtering.
func (w *HDWallet) DeriveAddressAtIndex(account uint32, change bool, index uint32) (*AddressInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	changeBit := uint32(0)
	if change {
		changeBit = 1
	}

	node, err := w.root.DerivePath(fmt.Sprintf("%d'/%d/%d", account, changeBit, index))
	if err != nil {
		return nil, err
	}

	addrBytes, err := node.AddressBytes()
	if err != nil {
		return nil, err
	}

	loc := LocationFromAddress(addrBytes)
	addrHex := fmt.Sprintf("0x%x", addrBytes)
	pubBytes, err := node.PublicKeyBytes()
	if err != nil {
		return nil, err
	}
	pubHex := fmt.Sprintf("0x%x", pubBytes)

	info := &AddressInfo{
		PubKey:  pubHex,
		Address: addrHex,
		Account: account,
		Index:   index,
		Zone:    loc,
		IsQi:    IsQiAddress(addrBytes),
	}
	w.addresses[addrHex] = info
	return info, nil
}

// GetPrivateKeyForAddress returns the ECDSA private key for a previously derived address.
func (w *HDWallet) GetPrivateKeyForAddress(address string) (*ecdsa.PrivateKey, error) {
	w.mu.RLock()
	info, ok := w.addresses[address]
	w.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("address %s not found in wallet", address)
	}

	changeBit := uint32(0)
	node, err := w.root.DerivePath(fmt.Sprintf("%d'/%d/%d", info.Account, changeBit, info.Index))
	if err != nil {
		return nil, err
	}

	return node.PrivateKey()
}

// GetAddressInfo returns info for a known address.
func (w *HDWallet) GetAddressInfo(address string) (*AddressInfo, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	info, ok := w.addresses[address]
	if !ok {
		return nil, fmt.Errorf("address %s not found", address)
	}
	return info, nil
}

// GetAddressesForZone returns all derived addresses matching a zone.
func (w *HDWallet) GetAddressesForZone(zone common.Location) []*AddressInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var result []*AddressInfo
	for _, info := range w.addresses {
		if len(info.Zone) >= 2 && len(zone) >= 2 &&
			info.Zone[0] == zone[0] && info.Zone[1] == zone[1] {
			result = append(result, info)
		}
	}
	return result
}

// Phrase returns the mnemonic phrase.
func (w *HDWallet) Phrase() string {
	return w.mnemonic.Phrase
}

// CoinType returns the wallet's coin type.
func (w *HDWallet) CoinType() uint32 {
	return w.coinType
}

// SignHash signs a 32-byte hash with the private key for the given address.
func (w *HDWallet) SignHash(address string, hash []byte) ([]byte, error) {
	privKey, err := w.GetPrivateKeyForAddress(address)
	if err != nil {
		return nil, err
	}
	return crypto.Sign(hash, privKey)
}

// Addresses returns all derived addresses.
func (w *HDWallet) Addresses() []*AddressInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]*AddressInfo, 0, len(w.addresses))
	for _, info := range w.addresses {
		result = append(result, info)
	}
	return result
}
