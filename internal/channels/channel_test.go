package channels_test

import (
	"testing"

	"github.com/basket/go-claw/internal/channels"
)

// Compile-time interface check: TelegramChannel must implement Channel.
var _ channels.Channel = (*channels.TelegramChannel)(nil)

func TestTelegramChannel_Name(t *testing.T) {
	// NewTelegramChannel requires non-nil router and store for real use, but
	// the Name() method only returns a constant and does not touch any
	// dependencies, so we can construct a minimal instance with nil deps.
	ch := channels.NewTelegramChannel("fake-token", nil, nil, nil, nil)
	if got := ch.Name(); got != "telegram" {
		t.Fatalf("TelegramChannel.Name() = %q, want %q", got, "telegram")
	}
}

func TestTelegramChannel_AllowlistEmpty(t *testing.T) {
	// Constructing with an empty allowlist should not panic.
	ch := channels.NewTelegramChannel("fake-token", []int64{}, nil, nil, nil)
	if ch == nil {
		t.Fatal("expected non-nil TelegramChannel with empty allowlist")
	}
}

func TestTelegramChannel_AllowlistPopulated(t *testing.T) {
	// Constructing with specific allowed IDs should not panic.
	ids := []int64{123, 456, 789}
	ch := channels.NewTelegramChannel("fake-token", ids, nil, nil, nil)
	if ch == nil {
		t.Fatal("expected non-nil TelegramChannel with populated allowlist")
	}
	if got := ch.Name(); got != "telegram" {
		t.Fatalf("TelegramChannel.Name() = %q, want %q", got, "telegram")
	}
}
