package quai

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/trie"
	"github.com/stretchr/testify/require"
)

type stubTrieBackend struct {
	quaiapi.Backend
	processingState bool
	nodes           map[common.Hash][]byte
	calls           int
}

func (s *stubTrieBackend) ProcessingState() bool {
	return s.processingState
}

func (s *stubTrieBackend) TrieNode(hash common.Hash) ([]byte, error) {
	s.calls++
	blob, ok := s.nodes[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return blob, nil
}

func TestQuaiBackendGetTrieNode(t *testing.T) {
	qbe, err := NewQuaiBackend()
	require.NoError(t, err)

	hashA := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	hashMissing := common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	blobA := []byte{0x01, 0x02, 0x03}

	stub := &stubTrieBackend{
		processingState: true,
		nodes:           map[common.Hash][]byte{hashA: blobA},
	}
	var apiBackend quaiapi.Backend = stub
	zoneLoc := common.Location{0, 0}
	qbe.SetApiBackend(&apiBackend, zoneLoc)

	// Nil receiver, non-zone locations, and out-of-bounds locations return nil safely
	var nilBackend *QuaiBackend
	require.Nil(t, nilBackend.GetTrieNode(hashA, zoneLoc))
	require.Nil(t, qbe.GetTrieNode(hashA, common.Location{}))
	require.Nil(t, qbe.GetTrieNode(hashA, common.Location{0}))
	require.Nil(t, qbe.GetTrieNode(hashA, common.Location{byte(common.MaxRegions + 1), 0}))
	require.Nil(t, qbe.GetTrieNode(hashA, common.Location{0, byte(common.MaxZones + 1)}))
	require.Nil(t, qbe.GetTrieNode(hashA, common.Location{0, 1}))

	// Valid hit and miss
	resp := qbe.GetTrieNode(hashA, zoneLoc)
	require.NotNil(t, resp)
	require.Equal(t, blobA, resp.NodeData)
	require.Nil(t, qbe.GetTrieNode(hashMissing, zoneLoc))

	// Non-state-processing backend refuses lookup
	stub.processingState = false
	require.Nil(t, qbe.GetTrieNode(hashA, zoneLoc))
}

func TestQuaiBackendGetTrieNodesBatchLimitsAndDedup(t *testing.T) {
	qbe, err := NewQuaiBackend()
	require.NoError(t, err)

	zoneLoc := common.Location{0, 0}
	nodes := make(map[common.Hash][]byte)
	var allHashes []common.Hash
	for i := 0; i < trie.MaxTrieNodeBatchSize+10; i++ {
		h := common.BigToHash(common.Big1)
		h[0] = byte(i >> 8)
		h[1] = byte(i)
		nodes[h] = []byte{byte(i), 0xaa, 0xbb}
		allHashes = append(allHashes, h)
	}

	stub := &stubTrieBackend{
		processingState: true,
		nodes:           nodes,
	}
	var apiBackend quaiapi.Backend = stub
	qbe.SetApiBackend(&apiBackend, zoneLoc)

	// Deduplication prevents amplification
	dupResp := qbe.GetTrieNodes([]common.Hash{allHashes[0], allHashes[0], allHashes[0]}, zoneLoc)
	require.NotNil(t, dupResp)
	require.Len(t, dupResp.Nodes, 1)
	require.Equal(t, 1, stub.calls)

	// Batch count cap
	stub.calls = 0
	cappedResp := qbe.GetTrieNodes(allHashes, zoneLoc)
	require.NotNil(t, cappedResp)
	require.Len(t, cappedResp.Nodes, trie.MaxTrieNodeBatchSize)
	require.Equal(t, trie.MaxTrieNodeBatchSize, stub.calls)

	// Byte cap
	bigChunk := bytes.Repeat([]byte{0x42}, 700*1024)
	bigNodes := map[common.Hash][]byte{
		allHashes[0]: bigChunk,
		allHashes[1]: bigChunk,
		allHashes[2]: bigChunk,
		allHashes[3]: bigChunk,
	}
	stub.nodes = bigNodes
	byteCapped := qbe.GetTrieNodes(allHashes[:4], zoneLoc)
	require.NotNil(t, byteCapped)
	// 2 * 700 KiB = 1400 KiB <= 2 MiB; adding a 3rd would be 2100 KiB > 2 MiB (2048 KiB)
	require.Len(t, byteCapped.Nodes, 2)

	// Empty / non-processing / missing
	require.Nil(t, qbe.GetTrieNodes(nil, zoneLoc))
	require.Nil(t, qbe.GetTrieNodes([]common.Hash{common.HexToHash("0xdead")}, zoneLoc))
	stub.processingState = false
	require.Nil(t, qbe.GetTrieNodes(allHashes[:2], zoneLoc))
}
