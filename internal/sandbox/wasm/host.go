package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"
)

// GC-SPEC-SKL-005: Deterministic fault reason codes for skill invocations.
const (
	FaultModuleNotFound = "WASM_MODULE_NOT_FOUND"
	FaultTimeout        = "WASM_TIMEOUT"
	FaultMemoryExceeded = "WASM_MEMORY_EXCEEDED"
	FaultNoExport       = "WASM_NO_EXPORT"
	FaultExecError      = "WASM_FAULT"
	FaultQuarantined    = "WASM_QUARANTINED" // GC-SPEC-SKL-007
)

// SkillFault is a structured error emitted by skill invocations (GC-SPEC-SKL-005).
type SkillFault struct {
	Reason string // one of the Fault* constants
	Module string
	Detail string
}

func (e *SkillFault) Error() string {
	return fmt.Sprintf("%s: module=%s: %s", e.Reason, e.Module, e.Detail)
}

// DefaultMemoryLimitPages is 160 pages = 10MB (each WASM page = 64KB).
const DefaultMemoryLimitPages = 160

// DefaultAggregateMemoryLimitPages is 640 pages = 40MB total across all modules.
const DefaultAggregateMemoryLimitPages uint32 = 640

// FaultMemoryExhausted is returned when aggregate WASM memory is exhausted.
const FaultMemoryExhausted = "WASM_HOST_MEMORY_EXHAUSTED"

// DefaultInvokeTimeout is the wall-clock limit for a single skill invocation.
const DefaultInvokeTimeout = 30 * time.Second

type Config struct {
	Store  *persistence.Store
	Policy policy.Checker
	Logger *slog.Logger

	// GC-SPEC-SKL-005: Resource limits for WASM invocations.
	// MemoryLimitPages caps memory per module (1 page = 64KB). 0 uses DefaultMemoryLimitPages.
	MemoryLimitPages uint32
	// AggregateMemoryLimitPages caps total memory across all loaded modules. 0 uses DefaultAggregateMemoryLimitPages.
	AggregateMemoryLimitPages uint32
	// InvokeTimeout caps wall-clock time per invocation. 0 uses DefaultInvokeTimeout.
	InvokeTimeout time.Duration
}

type Host struct {
	store  *persistence.Store
	policy policy.Checker
	logger *slog.Logger

	runtime       wazero.Runtime
	invokeTimeout time.Duration

	hostFunctions map[string]struct{}

	modulesMu            sync.Mutex
	modules              map[string]api.Module
	moduleMemoryPages    map[string]uint32
	aggregateMemoryLimit uint32
}

func NewHost(ctx context.Context, cfg Config) (*Host, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Policy == nil {
		cfg.Policy = policy.Default()
	}

	memPages := cfg.MemoryLimitPages
	if memPages == 0 {
		memPages = DefaultMemoryLimitPages
	}
	aggLimit := cfg.AggregateMemoryLimitPages
	if aggLimit == 0 {
		aggLimit = DefaultAggregateMemoryLimitPages
	}
	invokeTimeout := cfg.InvokeTimeout
	if invokeTimeout == 0 {
		invokeTimeout = DefaultInvokeTimeout
	}

	// GC-SPEC-SKL-005: Configure runtime with memory limits and context-driven termination.
	runtimeCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memPages).
		WithCloseOnContextDone(true)

	h := &Host{
		store:                cfg.Store,
		policy:               cfg.Policy,
		logger:               cfg.Logger,
		runtime:              wazero.NewRuntimeWithConfig(ctx, runtimeCfg),
		invokeTimeout:        invokeTimeout,
		hostFunctions:        map[string]struct{}{},
		modules:              map[string]api.Module{},
		moduleMemoryPages:    map[string]uint32{},
		aggregateMemoryLimit: aggLimit,
	}

	builder := h.runtime.NewHostModuleBuilder("host")
	builder.NewFunctionBuilder().WithFunc(h.hostHTTPGet).Export("host.http.get")
	builder.NewFunctionBuilder().WithFunc(h.hostLog).Export("host.log")
	builder.NewFunctionBuilder().WithFunc(h.hostKVSet).Export("host.kv.set")

	h.hostFunctions["host.http.get"] = struct{}{}
	h.hostFunctions["host.log"] = struct{}{}
	h.hostFunctions["host.kv.set"] = struct{}{}

	if _, err := builder.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("instantiate host module: %w", err)
	}
	return h, nil
}

func (h *Host) HasHostFunction(name string) bool {
	_, ok := h.hostFunctions[name]
	return ok
}

func (h *Host) Close(ctx context.Context) error {
	h.modulesMu.Lock()
	for name, module := range h.modules {
		_ = module.Close(ctx)
		delete(h.modules, name)
		delete(h.moduleMemoryPages, name)
	}
	h.modulesMu.Unlock()
	return h.runtime.Close(ctx)
}

func (h *Host) HasModule(name string) bool {
	h.modulesMu.Lock()
	defer h.modulesMu.Unlock()
	_, ok := h.modules[name]
	return ok
}

// MemoryStats returns aggregate memory pages, per-module breakdown, and the configured limit.
func (h *Host) MemoryStats() (aggregatePages uint32, perModule map[string]uint32, limit uint32) {
	h.modulesMu.Lock()
	defer h.modulesMu.Unlock()
	perModule = make(map[string]uint32, len(h.moduleMemoryPages))
	for name, pages := range h.moduleMemoryPages {
		aggregatePages += pages
		perModule[name] = pages
	}
	limit = h.aggregateMemoryLimit
	return
}

func (h *Host) InvokeModuleRandom(ctx context.Context, moduleName string) (int32, error) {
	// GC-SPEC-SKL-007: Check quarantine before invocation.
	if h.store != nil {
		if quarantined, err := h.store.IsSkillQuarantined(ctx, moduleName); err == nil && quarantined {
			h.logger.Warn("skill quarantined, invocation denied", "module", moduleName)
			return 0, &SkillFault{Reason: FaultQuarantined, Module: moduleName, Detail: "skill quarantined due to repeated faults"}
		}
	}

	h.modulesMu.Lock()
	module, ok := h.modules[moduleName]
	h.modulesMu.Unlock()
	if !ok {
		return 0, &SkillFault{Reason: FaultModuleNotFound, Module: moduleName, Detail: "module not loaded"}
	}

	// GC-SPEC-SKL-005: Enforce per-invocation time limit.
	invokeCtx, cancel := context.WithTimeout(ctx, h.invokeTimeout)
	defer cancel()

	exports := []string{"random", "Random", "run", "main"}
	for _, fnName := range exports {
		fn := module.ExportedFunction(fnName)
		if fn == nil {
			continue
		}
		results, err := fn.Call(invokeCtx)
		if err != nil {
			if fault := classifyFault(moduleName, err); fault != nil {
				h.logger.Warn("skill invocation fault", "module", moduleName, "fn", fnName, "reason", fault.Reason)
				// GC-SPEC-SKL-007: Record fault and possibly quarantine.
				h.recordSkillFault(ctx, moduleName)
				return 0, fault
			}
			continue
		}
		if len(results) == 0 {
			return 0, nil
		}
		return int32(results[0]), nil
	}
	return 0, &SkillFault{Reason: FaultNoExport, Module: moduleName, Detail: "no callable random export found"}
}

// recordSkillFault increments the fault counter and logs quarantine events (GC-SPEC-SKL-007).
func (h *Host) recordSkillFault(ctx context.Context, moduleName string) {
	if h.store == nil {
		return
	}
	quarantined, err := h.store.IncrementSkillFault(ctx, moduleName, 0)
	if err != nil {
		h.logger.Error("failed to record skill fault", "module", moduleName, "error", err)
		return
	}
	if quarantined {
		h.logger.Warn("skill auto-quarantined due to repeated faults", "module", moduleName)
		audit.Record("quarantine", "skill.invoke", "fault_threshold_exceeded", "", moduleName)
	}
}

// classifyFault maps a WASM execution error to a deterministic SkillFault (GC-SPEC-SKL-005).
func classifyFault(moduleName string, err error) *SkillFault {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &SkillFault{Reason: FaultTimeout, Module: moduleName, Detail: err.Error()}
	}
	if errors.Is(err, context.Canceled) {
		return &SkillFault{Reason: FaultTimeout, Module: moduleName, Detail: "canceled"}
	}
	// wazero raises sys.ExitError on context-driven termination.
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return &SkillFault{Reason: FaultTimeout, Module: moduleName, Detail: err.Error()}
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "memory") {
		return &SkillFault{Reason: FaultMemoryExceeded, Module: moduleName, Detail: errMsg}
	}
	return &SkillFault{Reason: FaultExecError, Module: moduleName, Detail: errMsg}
}

func (h *Host) HTTPGet(ctx context.Context, rawURL string) (string, error) {
	if h.policy == nil || !h.policy.AllowCapability("wasm.http.get") {
		pv := ""
		if h.policy != nil {
			pv = h.policy.PolicyVersion()
		}
		audit.Record("deny", "wasm.http.get", "missing_capability", pv, rawURL)
		return "", fmt.Errorf("policy denied capability %q", "wasm.http.get")
	}
	audit.Record("allow", "wasm.http.get", "capability_granted", h.policy.PolicyVersion(), rawURL)
	if !h.policy.AllowHTTPURL(rawURL) {
		audit.Record("deny", "wasm.http.get", "url_denied", h.policy.PolicyVersion(), rawURL)
		return "", fmt.Errorf("policy denied host.http.get for url %q", rawURL)
	}
	audit.Record("allow", "wasm.http.get", "url_allowed", h.policy.PolicyVersion(), rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (h *Host) LoadModuleFromFile(ctx context.Context, srcPath string) error {
	wasmBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read wasm module: %w", err)
	}
	name := moduleNameFromPath(srcPath)
	return h.LoadModuleFromBytes(ctx, name, wasmBytes, srcPath)
}

func (h *Host) LoadModuleFromBytes(ctx context.Context, name string, wasmBytes []byte, source string) error {
	compiled, err := h.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("compile wasm module %s: %w", name, err)
	}

	// Pre-check: estimate memory from compiled module's memory section.
	// Min() returns the initial page count declared in the module.
	var estimatedPages uint32
	for _, def := range compiled.ImportedMemories() {
		estimatedPages += def.Min()
	}
	for _, def := range compiled.ExportedMemories() {
		estimatedPages += def.Min()
	}
	// Each module uses at least 1 page for tracking purposes.
	if estimatedPages == 0 {
		estimatedPages = 1
	}

	h.modulesMu.Lock()
	// Calculate current aggregate, excluding the module being replaced.
	var currentAggregate uint32
	for n, pages := range h.moduleMemoryPages {
		if n != name {
			currentAggregate += pages
		}
	}
	if currentAggregate+estimatedPages > h.aggregateMemoryLimit {
		h.modulesMu.Unlock()
		return &SkillFault{
			Reason: FaultMemoryExhausted,
			Module: name,
			Detail: fmt.Sprintf("WASM Host Memory Exhausted: aggregate=%d pages, new=%d pages, limit=%d pages",
				currentAggregate, estimatedPages, h.aggregateMemoryLimit),
		}
	}
	// Close existing module before instantiating replacement (wazero tracks names).
	if old, ok := h.modules[name]; ok {
		_ = old.Close(ctx)
		delete(h.modules, name)
		delete(h.moduleMemoryPages, name)
	}
	h.modulesMu.Unlock()

	module, err := h.runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(name))
	if err != nil {
		return fmt.Errorf("instantiate wasm module %s: %w", name, err)
	}

	// Query actual memory pages after instantiation.
	// Use Grow(0) which safely returns current pages without overflow risk.
	actualPages := estimatedPages
	func() {
		defer func() { recover() }() // guard against nil memory interface
		if mem := module.Memory(); mem != nil {
			if pages, ok := mem.Grow(0); ok {
				actualPages = pages
			}
		}
	}()
	if actualPages == 0 {
		actualPages = 1
	}

	h.modulesMu.Lock()
	defer h.modulesMu.Unlock()
	h.modules[name] = module
	h.moduleMemoryPages[name] = actualPages

	// Recalculate aggregate for logging.
	var aggregate uint32
	for _, pages := range h.moduleMemoryPages {
		aggregate += pages
	}
	h.logger.Info("wasm module loaded", "module", name, "path", source,
		"memory_pages", actualPages, "aggregate_pages", aggregate, "limit_pages", h.aggregateMemoryLimit)
	return nil
}

func moduleNameFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// readWASMString reads a string from WASM linear memory at the given pointer and length.
func readWASMString(module api.Module, ptr, length uint32) (string, bool) {
	data, ok := module.Memory().Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(data), true
}

func (h *Host) hostHTTPGet(ctx context.Context, module api.Module, ptr uint32, length uint32) uint32 {
	rawURL, ok := readWASMString(module, ptr, length)
	if !ok {
		h.logger.Error("host.http.get: failed to read URL from wasm memory", "ptr", ptr, "len", length)
		return 0
	}

	body, err := h.HTTPGet(ctx, rawURL)
	if err != nil {
		h.logger.Error("host.http.get failed", "url", rawURL, "error", err)
		return 0
	}

	bodyBytes := []byte(body)
	bodyLen := uint32(len(bodyBytes))

	// Try to write body to guest memory via exported alloc function.
	allocFn := module.ExportedFunction("alloc")
	if allocFn != nil {
		results, err := allocFn.Call(ctx, uint64(bodyLen))
		if err == nil && len(results) > 0 {
			destPtr := uint32(results[0])
			if module.Memory().Write(destPtr, bodyBytes) {
				h.logger.Info("host.http.get: body written to guest memory", "url", rawURL, "body_len", bodyLen, "ptr", destPtr)
				return destPtr
			}
		}
		h.logger.Warn("host.http.get: alloc/write failed, falling back to KV store", "url", rawURL)
	}

	// Fallback: store body in KV store if guest doesn't export alloc.
	if h.store != nil {
		kvKey := fmt.Sprintf("http_response:%s:%d", rawURL, time.Now().UnixNano())
		if err := h.store.KVSet(ctx, kvKey, body); err != nil {
			h.logger.Error("host.http.get: KV store fallback failed", "url", rawURL, "error", err)
			return 0
		}
		h.logger.Info("host.http.get: body stored in KV", "url", rawURL, "key", kvKey, "body_len", bodyLen)
	}

	return bodyLen
}

func (h *Host) hostLog(ctx context.Context, module api.Module, levelPtr uint32, levelLen uint32, msgPtr uint32, msgLen uint32) {
	level, ok := readWASMString(module, levelPtr, levelLen)
	if !ok {
		level = "info"
	}
	msg, ok := readWASMString(module, msgPtr, msgLen)
	if !ok {
		h.logger.Warn("host.log: failed to read message from wasm memory")
		return
	}

	switch strings.ToLower(level) {
	case "error":
		h.logger.Error("wasm guest log", "msg", msg)
	case "warn":
		h.logger.Warn("wasm guest log", "msg", msg)
	case "debug":
		h.logger.Debug("wasm guest log", "msg", msg)
	default:
		h.logger.Info("wasm guest log", "msg", msg)
	}
}

func (h *Host) hostKVSet(ctx context.Context, module api.Module, keyPtr uint32, keyLen uint32, valPtr uint32, valLen uint32) uint32 {
	if h.policy == nil || !h.policy.AllowCapability("wasm.kv.set") {
		pv := ""
		if h.policy != nil {
			pv = h.policy.PolicyVersion()
		}
		audit.Record("deny", "wasm.kv.set", "missing_capability", pv, "")
		h.logger.Error("host.kv.set denied", "reason", "missing capability", "capability", "wasm.kv.set")
		return 0
	}
	audit.Record("allow", "wasm.kv.set", "capability_granted", h.policy.PolicyVersion(), "")
	key, ok := readWASMString(module, keyPtr, keyLen)
	if !ok {
		h.logger.Error("host.kv.set: failed to read key from wasm memory")
		return 0
	}
	val, ok := readWASMString(module, valPtr, valLen)
	if !ok {
		h.logger.Error("host.kv.set: failed to read value from wasm memory")
		return 0
	}

	if err := h.store.KVSet(ctx, key, val); err != nil {
		h.logger.Error("host.kv.set failed", "key", key, "error", err)
		return 0
	}
	h.logger.Info("host.kv.set completed", "key", key)
	return 1
}
