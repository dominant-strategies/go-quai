package blake3_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus/blake3"
	"github.com/spruce-solutions/go-quai/core/types"
)

var testPairs = [][][]byte{
	{
		[]byte("This is a test"),
		[]byte("c90f60ba2ebed5c7404e035853d90c3f8416b303125ca2e05dee529bb35df627"),
	},
}

func TestComputeHash(t *testing.T) {
	var tp = testPairs[0]

	t.Log("Create new Blake3 hasher")
	config := blake3.Config{
		NotifyFull: true,
	}
	blake3, err := blake3.New(config, nil, false)
	if nil != err {
		t.Log(err)
		t.Fail()
	}

	var hashCorrect = make([]byte, hex.DecodedLen(len(tp[2])))
	_, err = hex.Decode(hashCorrect, tp[2])
	if err != nil {
		t.Log(err)
		t.Fail()
	}

	t.Log("Compute hash and check result")
	header := types.Header{ParentHash: []common.Hash{common.BytesToHash(tp[1])}}
	hash := blake3.SealHash(&header)
	if !bytes.Equal(hash.Bytes(), hashCorrect) {
		t.Logf("answer is incorrect: %x, %x", hash, hashCorrect)
		t.Fail()
	}
}
