package quaiclient

import (
	"context"
	"testing"
	"time"

	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
)

func TestSubscribePendingHeader(t *testing.T) {
	logger := log.NewLogger("", "info", 0)
	client, err := Dial("ws://localhost:8200", logger)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test without powId (defaults to Progpow)
	t.Run("WithoutPowId", func(t *testing.T) {
		ch := make(chan []byte, 10)
		sub, err := client.SubscribePendingHeader(ctx, ch)
		if err != nil {
			t.Fatalf("Failed to subscribe without powId: %v", err)
		}
		defer sub.Unsubscribe()
		t.Log("Successfully subscribed without powId (defaults to Progpow)")

		// Wait for a header
		select {
		case header := <-ch:
			t.Logf("Received header without powId: %d bytes", len(header))
		case err := <-sub.Err():
			t.Fatalf("Subscription error: %v", err)
		case <-ctx.Done():
			t.Fatal("Timeout waiting for header")
		}
	})

	// Test with Kawpow
	t.Run("WithKawpow", func(t *testing.T) {
		ch := make(chan []byte, 10)
		sub, err := client.SubscribePendingHeader(ctx, ch, types.Kawpow)
		if err != nil {
			t.Fatalf("Failed to subscribe with Kawpow: %v", err)
		}
		defer sub.Unsubscribe()
		t.Log("Successfully subscribed with Kawpow")

		// Wait for a header
		select {
		case header := <-ch:
			t.Logf("Received Kawpow header: %d bytes", len(header))
		case err := <-sub.Err():
			t.Fatalf("Subscription error: %v", err)
		case <-ctx.Done():
			t.Fatal("Timeout waiting for header")
		}
	})

	// Test with Progpow explicitly
	t.Run("WithProgpow", func(t *testing.T) {
		ch := make(chan []byte, 10)
		sub, err := client.SubscribePendingHeader(ctx, ch, types.Progpow)
		if err != nil {
			t.Fatalf("Failed to subscribe with Progpow: %v", err)
		}
		defer sub.Unsubscribe()
		t.Log("Successfully subscribed with Progpow")

		// Wait for a header
		select {
		case header := <-ch:
			t.Logf("Received Progpow header: %d bytes", len(header))
		case err := <-sub.Err():
			t.Fatalf("Subscription error: %v", err)
		case <-ctx.Done():
			t.Fatal("Timeout waiting for header")
		}
	})
}

func TestGetPendingHeader(t *testing.T) {
	logger := log.NewLogger("", "info", 0)
	client, err := Dial("ws://localhost:8200", logger)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test without powId (defaults to Progpow)
	t.Run("WithoutPowId", func(t *testing.T) {
		header, err := client.GetPendingHeader(ctx)
		if err != nil {
			t.Fatalf("Failed to get pending header without powId: %v", err)
		}
		if header == nil {
			t.Fatal("Received nil header")
		}
		t.Logf("Received header without powId: hash=%s number=%d", header.Hash().Hex(), header.NumberU64(2))
	})

	// Test with Kawpow
	t.Run("WithKawpow", func(t *testing.T) {
		header, err := client.GetPendingHeader(ctx, types.Kawpow)
		if err != nil {
			t.Fatalf("Failed to get pending header with Kawpow: %v", err)
		}
		if header == nil {
			t.Fatal("Received nil header")
		}
		t.Logf("Received Kawpow header: hash=%s number=%d", header.Hash().Hex(), header.NumberU64(2))
		if header.AuxPow() == nil {
			t.Log("Warning: AuxPow is nil for Kawpow header")
		} else {
			t.Log("AuxPow present in Kawpow header")
		}
	})

	// Test with Progpow explicitly
	t.Run("WithProgpow", func(t *testing.T) {
		header, err := client.GetPendingHeader(ctx, types.Progpow)
		if err != nil {
			t.Fatalf("Failed to get pending header with Progpow: %v", err)
		}
		if header == nil {
			t.Fatal("Received nil header")
		}
		t.Logf("Received Progpow header: hash=%s number=%d", header.Hash().Hex(), header.NumberU64(2))
	})
}
