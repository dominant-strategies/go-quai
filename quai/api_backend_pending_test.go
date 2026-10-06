package quai

import (
	"context"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/rpc"
)

func TestStateAndHeaderByNumberRejectsPendingState(t *testing.T) {
	backend := new(QuaiAPIBackend)
	state, header, err := backend.StateAndHeaderByNumber(context.Background(), rpc.PendingBlockNumber)
	if state != nil || header != nil || err == nil || !strings.Contains(err.Error(), "pending state is unavailable") {
		t.Fatalf("pending state: state=%v header=%v err=%v", state, header, err)
	}
}

func TestAccountRangeRejectsPendingState(t *testing.T) {
	api := new(PublicDebugAPI)
	_, err := api.AccountRange(rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber), nil, 1, true, true, false)
	if err == nil || !strings.Contains(err.Error(), "pending state is unavailable") {
		t.Fatalf("pending account range: %v", err)
	}
}
