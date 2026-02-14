package main

import (
	"encoding/json"
	"testing"
)

func TestFrameID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
		ok   bool
	}{
		{name: "number", raw: "1005", want: 1005, ok: true},
		{name: "string unsupported", raw: `"1005"`, want: 0, ok: false},
		{name: "invalid", raw: "{", want: 0, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := frameID(json.RawMessage(tt.raw))
			if ok != tt.ok {
				t.Fatalf("ok mismatch: got=%v want=%v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("id mismatch: got=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestExtractField(t *testing.T) {
	raw := json.RawMessage(`{"approval_id":"abc","status":"DENIED"}`)
	id, err := extractField(raw, "approval_id")
	if err != nil {
		t.Fatalf("extractField error: %v", err)
	}
	if id != "abc" {
		t.Fatalf("expected abc, got %q", id)
	}
	if _, err := extractField(raw, "missing"); err == nil {
		t.Fatal("expected error for missing field")
	}
}
