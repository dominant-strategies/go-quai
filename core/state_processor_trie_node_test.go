package core

import (
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/rawdb"
	"github.com/dominant-strategies/go-quai/core/state"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/stretchr/testify/require"
)

func TestStateProcessorTrieNodeLookup(t *testing.T) {
	stateDB := rawdb.NewMemoryDatabase(log.Global)
	etxDB := rawdb.NewMemoryDatabase(log.Global)

	sp := &StateProcessor{
		stateCache: state.NewDatabase(stateDB),
		etxCache:   state.NewDatabase(etxDB),
	}

	// 1. EVM state trie node
	evmBlob := []byte("evm-trie-node-blob")
	evmHash := crypto.Keccak256Hash(evmBlob)
	rawdb.WriteTrieNode(stateDB, evmHash, evmBlob)

	gotEvm, err := sp.TrieNode(evmHash)
	require.NoError(t, err)
	require.Equal(t, evmBlob, gotEvm)

	// Defensive copy check
	gotEvm[0] ^= 0xff
	gotEvmAgain, err := sp.TrieNode(evmHash)
	require.NoError(t, err)
	require.Equal(t, evmBlob, gotEvmAgain)

	// 2. ETX set trie node (only in etxCache)
	etxBlob := []byte("etx-trie-node-blob")
	etxHash := crypto.Keccak256Hash(etxBlob)
	rawdb.WriteTrieNode(etxDB, etxHash, etxBlob)

	gotEtx, err := sp.TrieNode(etxHash)
	require.NoError(t, err)
	require.Equal(t, etxBlob, gotEtx)

	// 3. Contract bytecode (prefixed code scheme)
	codeBlob := []byte{0x60, 0x80, 0x60, 0x40, 0x52}
	codeHash := crypto.Keccak256Hash(codeBlob)
	rawdb.WriteCode(stateDB, codeHash, codeBlob)

	gotCode, err := sp.TrieNode(codeHash)
	require.NoError(t, err)
	require.Equal(t, codeBlob, gotCode)

	// 4. Missing hash returns error
	missingHash := common.HexToHash("0x1234")
	gotMissing, err := sp.TrieNode(missingHash)
	require.Error(t, err)
	require.Nil(t, gotMissing)

	// 5. Nil receiver safety
	var nilSP *StateProcessor
	_, err = nilSP.TrieNode(evmHash)
	require.Error(t, err)
}
