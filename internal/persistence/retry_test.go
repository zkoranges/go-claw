package persistence

import (
	"context"
	"fmt"
	"testing"
)

// GC-SPEC-PER-002: Tests for retryOnBusy and isSQLiteBusy.

func TestIsSQLiteBusy(t *testing.T) {
	tests := []struct {
		err    error
		expect bool
	}{
		{nil, false},
		{fmt.Errorf("some other error"), false},
		{fmt.Errorf("database is locked"), true},
		{fmt.Errorf("database table is locked"), true},
		{fmt.Errorf("SQLITE_BUSY (5)"), true},
		{fmt.Errorf("SQLITE_LOCKED (6)"), true},
		{fmt.Errorf("wrapped: database is locked"), true},
	}
	for _, tt := range tests {
		got := isSQLiteBusy(tt.err)
		if got != tt.expect {
			t.Errorf("isSQLiteBusy(%v) = %v, want %v", tt.err, got, tt.expect)
		}
	}
}

func TestRetryOnBusy_NoError(t *testing.T) {
	calls := 0
	err := retryOnBusy(context.Background(), 3, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnBusy_NonBusyError(t *testing.T) {
	calls := 0
	err := retryOnBusy(context.Background(), 3, func() error {
		calls++
		return fmt.Errorf("not a busy error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on non-busy), got %d", calls)
	}
}

func TestRetryOnBusy_BusyThenSuccess(t *testing.T) {
	calls := 0
	err := retryOnBusy(context.Background(), 3, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnBusy_ExhaustedRetries(t *testing.T) {
	calls := 0
	err := retryOnBusy(context.Background(), 2, func() error {
		calls++
		return fmt.Errorf("database is locked")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// maxRetries=2 means attempts 0,1,2 = 3 total calls.
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnBusy_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryOnBusy(ctx, 5, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return fmt.Errorf("database is locked")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
