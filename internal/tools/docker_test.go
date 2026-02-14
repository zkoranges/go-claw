package tools

import (
	"testing"
)

// Mock test to avoid needing actual Docker daemon in CI
func TestDockerSandbox_Config(t *testing.T) {
	sandbox, err := NewDockerSandbox("alpine", 128, "none", "/tmp/ws")
	if err != nil {
		// If docker not available, we expect error, but we want to verify config logic
		// Just skip if client creation fails due to no daemon
		t.Skip("docker client init failed (expected in CI without docker):", err)
	}
	defer sandbox.Close()

	if sandbox.image != "alpine" {
		t.Errorf("expected alpine, got %s", sandbox.image)
	}
	if sandbox.memoryMB != 128*1024*1024 {
		t.Errorf("expected 128MB, got %d bytes", sandbox.memoryMB)
	}
}

// Ensure interface compliance (if we had an interface)
// For now just basic struct test.
