package hdwallet

import (
	"encoding/json"
	"fmt"
)

// SerializedWallet is the JSON-serializable state of the wallet.
type SerializedWallet struct {
	Version   int            `json:"version"`
	Phrase    string         `json:"phrase"`
	Password  string         `json:"password,omitempty"`
	CoinType  uint32         `json:"coinType"`
	Addresses []*AddressInfo `json:"addresses"`
}

// Serialize returns the wallet state as JSON bytes.
func (w *HDWallet) Serialize() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	addrs := make([]*AddressInfo, 0, len(w.addresses))
	for _, info := range w.addresses {
		addrs = append(addrs, info)
	}

	sw := &SerializedWallet{
		Version:   1,
		Phrase:    w.mnemonic.Phrase,
		Password:  w.mnemonic.Password,
		CoinType:  w.coinType,
		Addresses: addrs,
	}

	return json.Marshal(sw)
}

// DeserializeWallet restores a wallet from JSON bytes.
func DeserializeWallet(data []byte) (*HDWallet, error) {
	var sw SerializedWallet
	if err := json.Unmarshal(data, &sw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal wallet: %w", err)
	}

	if sw.Version != 1 {
		return nil, fmt.Errorf("unsupported wallet version %d", sw.Version)
	}

	w, err := NewHDWalletFromPhrase(sw.Phrase, sw.Password, sw.CoinType)
	if err != nil {
		return nil, err
	}

	// Restore address records
	for _, info := range sw.Addresses {
		w.addresses[info.Address] = info
		// Update nextIndex to be past the highest known index for each account
		key := accountKey{account: info.Account, change: false}
		if info.Index+1 > w.nextIndex[key] {
			w.nextIndex[key] = info.Index + 1
		}
	}

	return w, nil
}
