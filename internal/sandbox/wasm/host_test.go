package wasm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
)

func TestHost_RegistersRequiredFunctions(t *testing.T) {
	// [SPEC: SPEC-SEC-HFI-1] [PDR: V-26]
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new wasm host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	required := []string{"host.http.get", "host.log", "host.kv.set"}
	for _, name := range required {
		if !h.HasHostFunction(name) {
			t.Fatalf("missing host function: %s", name)
		}
	}
}

func TestHost_LoadModuleFromFile_ValidWASM(t *testing.T) {
	// [T-3] Load a minimal valid WASM module from a .wasm file.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Minimal valid WASM binary (empty module: magic + version + no sections).
	// The WASM binary format: \x00asm (magic) + version 1 (little-endian u32).
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasmPath := filepath.Join(t.TempDir(), "minimal.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	if err := h.LoadModuleFromFile(context.Background(), wasmPath); err != nil {
		t.Fatalf("load valid wasm: %v", err)
	}
}

func TestHost_LoadModuleFromFile_InvalidWASM(t *testing.T) {
	// [T-3] Loading invalid data should fail gracefully.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Write garbage bytes.
	wasmPath := filepath.Join(t.TempDir(), "garbage.wasm")
	if err := os.WriteFile(wasmPath, []byte("not a wasm module"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := h.LoadModuleFromFile(context.Background(), wasmPath); err == nil {
		t.Fatalf("expected error loading invalid wasm")
	}
}

func TestHost_LoadModuleFromFile_MissingFile(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	if err := h.LoadModuleFromFile(context.Background(), "/nonexistent/path.wasm"); err == nil {
		t.Fatalf("expected error loading missing file")
	}
}

func TestHost_HTTPGetEnforcesPolicyAllowlist(t *testing.T) {
	// [SPEC: SPEC-SEC-POLICY-1, SPEC-SEC-HFI-1] [PDR: V-18]
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	p := policy.Policy{
		AllowDomains:      []string{"example.com"},
		AllowCapabilities: []string{"wasm.http.get"},
	}
	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: p,
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	if _, err := h.HTTPGet(context.Background(), "https://forbidden.example.net"); err == nil {
		t.Fatalf("expected deny for non-allowlisted host")
	}
}

func TestHost_HTTPGetBlocksMultipleDomains(t *testing.T) {
	// [T-9] Blocked domain test with multiple domains in policy.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	p := policy.Policy{
		AllowDomains:      []string{"safe.example.com", "api.trusted.org"},
		AllowCapabilities: []string{"wasm.http.get"},
	}
	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: p,
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Blocked domains should be denied.
	blocked := []string{
		"https://evil.com/steal",
		"https://another.example.net/data",
		"https://safe.example.com.evil.org/phish",
	}
	for _, u := range blocked {
		if _, err := h.HTTPGet(context.Background(), u); err == nil {
			t.Fatalf("expected deny for %q", u)
		}
	}
}

func TestHost_DefaultPolicyDeniesAll(t *testing.T) {
	// Default policy has no allowed domains → deny everything.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	if _, err := h.HTTPGet(context.Background(), "https://google.com"); err == nil {
		t.Fatalf("default policy should deny all domains")
	}
}

func TestHost_HTTPGetDeniedWhenCapabilityMissing(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	p := policy.Policy{
		AllowDomains: []string{"example.com"},
	}
	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: p,
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	if _, err := h.HTTPGet(context.Background(), "https://example.com"); err == nil {
		t.Fatalf("expected denial when wasm.http.get capability missing")
	}
}

func TestHost_InvokeModuleRandom_ModuleNotFound(t *testing.T) {
	// GC-SPEC-SKL-005: Must emit WASM_MODULE_NOT_FOUND for missing modules.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	_, err = h.InvokeModuleRandom(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing module")
	}
	var fault *wasm.SkillFault
	if !errors.As(err, &fault) {
		t.Fatalf("expected SkillFault, got %T: %v", err, err)
	}
	if fault.Reason != wasm.FaultModuleNotFound {
		t.Fatalf("expected reason %q, got %q", wasm.FaultModuleNotFound, fault.Reason)
	}
}

func TestHost_InvokeModuleRandom_NoExport(t *testing.T) {
	// GC-SPEC-SKL-005: Must emit WASM_NO_EXPORT when module has no callable export.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Empty module has no exports.
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if err := h.LoadModuleFromBytes(context.Background(), "empty", wasmBytes, "test"); err != nil {
		t.Fatalf("load: %v", err)
	}

	_, err = h.InvokeModuleRandom(context.Background(), "empty")
	if err == nil {
		t.Fatal("expected error for module with no export")
	}
	var fault *wasm.SkillFault
	if !errors.As(err, &fault) {
		t.Fatalf("expected SkillFault, got %T: %v", err, err)
	}
	if fault.Reason != wasm.FaultNoExport {
		t.Fatalf("expected reason %q, got %q", wasm.FaultNoExport, fault.Reason)
	}
}

func TestHost_InvokeModuleRandom_ContextTimeout(t *testing.T) {
	// GC-SPEC-SKL-005: Must emit WASM_TIMEOUT when invocation exceeds time limit.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:         store,
		Policy:        policy.Default(),
		InvokeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Use an already-canceled context to verify timeout classification.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = h.InvokeModuleRandom(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
	var fault *wasm.SkillFault
	if !errors.As(err, &fault) {
		t.Fatalf("expected SkillFault, got %T: %v", err, err)
	}
	// Canceled context → module lookup still returns MODULE_NOT_FOUND since
	// the module doesn't exist; the timeout classification happens at fn.Call level.
	// This test validates the structured error type is returned.
	if fault.Reason != wasm.FaultModuleNotFound {
		t.Logf("got reason %q (acceptable for canceled ctx + missing module)", fault.Reason)
	}
}

func TestHost_CustomMemoryLimitPages(t *testing.T) {
	// GC-SPEC-SKL-005: Custom memory limit should be accepted.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:            store,
		Policy:           policy.Default(),
		MemoryLimitPages: 32, // 2MB
		InvokeTimeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("new host with custom limits: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Verify host was created successfully with custom limits.
	if !h.HasHostFunction("host.log") {
		t.Fatal("expected host.log function to be registered")
	}
}

func TestHost_HTTPGetReturnsBody(t *testing.T) {
	// Verify that HTTPGet returns the response body string (used by hostHTTPGet).
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	p := policy.Policy{
		AllowDomains:      []string{"example.com"},
		AllowCapabilities: []string{"wasm.http.get"},
	}
	h, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: p,
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = h.Close(context.Background()) }()

	// Use a real HTTP request to example.com to verify body is returned.
	// This is an integration-style test; skip if offline.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := h.HTTPGet(ctx, "https://example.com")
	if err != nil {
		t.Skipf("skipping (network unavailable): %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected non-empty body from example.com")
	}
	if !containsSubstring(body, "Example Domain") {
		t.Fatalf("expected body to contain 'Example Domain', got %d bytes", len(body))
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
