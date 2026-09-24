package pubsubManager

import (
	"context"
	"testing"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestGossipValidatorRejectsInsteadOfCrashingOnPanic(t *testing.T) {
	validator := (&PubsubManager{}).ValidatorFunc()
	if result := validator(context.Background(), peer.ID("malformed-peer"), nil); result != pubsub.ValidationReject {
		t.Fatalf("validation panic returned %v, want reject", result)
	}
}
