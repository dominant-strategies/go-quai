package randomx_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/spruce-solutions/go-quai/consensus/randomx"
)

var testPairs = [][][]byte{
	// randomX
	{
		[]byte("test key 000"),
		[]byte("This is a test"),
		[]byte("c90f60ba2ebed5c7404e035853d90c3f8416b303125ca2e05dee529bb35df627"),
	},
}

func TestComputeHash(t *testing.T) {
	var tp = testPairs[0]

	t.Log("Create new RandomX hasher")
	config := randomx.Config{
		NotifyFull: true,
	}
	rx, err := randomx.New(config, tp[0], nil, false, randomx.FlagDefault)
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
	hash := rx.Hash(tp[1])
	if !bytes.Equal(hash, hashCorrect) {
		t.Logf("answer is incorrect: %x, %x", hash, hashCorrect)
		t.Fail()
	}
}
