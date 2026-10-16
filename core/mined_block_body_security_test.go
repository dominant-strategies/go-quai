package core

import (
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

func newMinedBodyTestWorkObject() *types.WorkObject {
	bodyHeader := types.EmptyHeader()
	wh := newTestWorkObjectHeader(1)
	wh.SetHeaderHash(bodyHeader.Hash())
	body := types.EmptyWorkObjectBody()
	body.SetHeader(bodyHeader)
	return types.NewWorkObject(wh, body, nil)
}

// H8: when the bounded pending body cache has evicted a body, a mined object
// that carries its own bound body must still build a block.
func TestMinedBlockBodyFromWorkObjectUsesBoundBody(t *testing.T) {
	wo := newMinedBodyTestWorkObject()
	body := minedBlockBodyFromWorkObject(wo)
	if body == nil {
		t.Fatal("bound body rejected")
	}
	if body == wo || body.Body() == wo.Body() {
		t.Fatal("the fallback body must be a copy, the stratum server shares the original")
	}
	if body.Header().Hash() != wo.HeaderHash() {
		t.Fatal("fallback body is not the mined body")
	}
}

// The fallback must not accept a body that the work does not commit to.
func TestMinedBlockBodyFromWorkObjectRejectsUnboundBody(t *testing.T) {
	if minedBlockBodyFromWorkObject(nil) != nil {
		t.Fatal("nil work object accepted")
	}

	noBody := types.NewWorkObject(newTestWorkObjectHeader(1), nil, nil)
	if minedBlockBodyFromWorkObject(noBody) != nil {
		t.Fatal("work object without a body accepted")
	}

	otherHeader := newMinedBodyTestWorkObject()
	forged := types.EmptyHeader()
	forged.SetGasLimit(12345)
	otherHeader.Body().SetHeader(forged)
	if minedBlockBodyFromWorkObject(otherHeader) != nil {
		t.Fatal("body header that does not match the header hash accepted")
	}

	extraTx := newMinedBodyTestWorkObject()
	extraTx.Body().SetTransactions(types.Transactions{types.NewTx(&types.QuaiTx{ChainID: big.NewInt(1), Nonce: 1})})
	if minedBlockBodyFromWorkObject(extraTx) != nil {
		t.Fatal("transactions that do not match the tx root accepted")
	}

	extraEtx := newMinedBodyTestWorkObject()
	extraEtx.Body().SetOutboundEtxs(types.Transactions{types.NewTx(&types.QuaiTx{ChainID: big.NewInt(1), Nonce: 1})})
	if minedBlockBodyFromWorkObject(extraEtx) != nil {
		t.Fatal("outbound etxs that do not match the etx root accepted")
	}

	extraUncle := newMinedBodyTestWorkObject()
	extraUncle.Body().SetUncles([]*types.WorkObjectHeader{newTestWorkObjectHeader(1)})
	if minedBlockBodyFromWorkObject(extraUncle) != nil {
		t.Fatal("uncles that do not match the uncle root accepted")
	}

	regionBody := newMinedBodyTestWorkObject()
	regionBody.Body().SetManifest(types.BlockManifest{common.Hash{1}})
	if minedBlockBodyFromWorkObject(regionBody) != nil {
		t.Fatal("subordinate manifest accepted in a zone body")
	}

	primeBody := newMinedBodyTestWorkObject()
	primeBody.Body().SetInterlinkHashes(common.Hashes{common.Hash{1}})
	if minedBlockBodyFromWorkObject(primeBody) != nil {
		t.Fatal("interlink hashes accepted in a zone body")
	}
}
