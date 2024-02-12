package types

import (
	"testing"
)

func TestPendingEtxsValidity(t *testing.T) {
	pendingEtxs := &PendingEtxs{EmptyHeader(), []Transactions{Transactions{}, Transactions{}, Transactions{}}}

	t.Log("Len of pendingEtxs", len(pendingEtxs.Etxs))

	pendingEtxs.IsValid(nil, 2)
}
