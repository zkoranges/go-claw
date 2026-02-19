# Contributing to GoClaw

## Prerequisites

- **Go 1.24+** (`go version`)
- **SQLite** (bundled via `modernc.org/sqlite`, no external install needed)
- **just** command runner (`brew install just` or see [casey/just](https://github.com/casey/just))
- **Optional**: [Ollama](https://ollama.ai) for local model testing
- **Optional**: `golangci-lint` for `just lint`

## Quick Start

```bash
git clone https://github.com/basket/go-claw.git
cd go-claw
just check   # build + vet + test (the standard pre-commit command)
```

## Development Workflow

### Before Every Commit

```bash
just check
```

This runs `go build`, `go vet`, and `go test ./... -count=1`. All three must pass.

### Running a Single Package

```bash
go test ./internal/persistence/ -count=1 -timeout 120s
```

### Running Benchmarks

```bash
just bench
# or: go test ./internal/persistence/ -bench=. -run='^$'
```

### Test Coverage

```bash
just coverage
```

### Available Just Targets

| Command | Description |
|---------|-------------|
| `just build` | Build the binary to `/tmp/goclaw` |
| `just test` | Run all tests |
| `just vet` | Run `go vet` |
| `just check` | Build + vet + test |
| `just lint` | Run `golangci-lint` (falls back to `go vet`) |
| `just coverage` | Generate test coverage report |
| `just bench` | Run persistence benchmarks |
| `just fmt` | Format all Go files |
| `just run` | Build and start interactive TUI |
| `just run-headless` | Build and start headless daemon |

## Code Style

- **Formatting**: `gofmt` (enforced by `just fmt-check`)
- **Logging**: Standard library `log/slog` for structured logging
- **IDs**: `github.com/google/uuid` for all identifiers
- **Config**: `gopkg.in/yaml.v3` for YAML config
- **Errors**: Wrap with context: `fmt.Errorf("operation: %w", err)`
- **Traceability**: Reference spec requirements: `// GC-SPEC-XXX-NNN: description`
- **Status names**: Use canonical constants: `TaskStatusQueued`, `TaskStatusSucceeded`

## Testing Patterns

### Zero API Credits

All tests run offline. No real API calls, no budget consumption.

Brain tests set `GEMINI_API_KEY=""` to force `llmOn=false` (fallback mode).

### Table-Driven Tests

```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "hello", "HELLO", false},
        {"empty input", "", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := MyFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
            }
            if got != tt.want {
                t.Errorf("got %q, want %q", got, tt.want)
            }
        })
    }
}
```

### Session IDs Must Be UUIDs

```go
sessionID := uuid.NewString() // correct
sessionID := "test-session"   // will fail
```

### Test Flags

Always use `-count=1` to disable test caching.

## How to Add Things

### Adding a Tool

1. Create `internal/tools/my_tool.go`
2. Implement the tool function matching the existing pattern in `tools.go`
3. Register in `RegisterBuiltinTools()` in `tools.go`
4. Add the capability name to `knownCapabilities` in `internal/policy/policy.go`
5. Add tests in `internal/tools/my_tool_test.go`

### Adding a TUI Command

1. Add a case to `handleCommand()` in `internal/tui/chat.go`
2. Add help text to the `/help` case
3. Add tests in `internal/tui/chat_test.go`

### Adding an Agent via Config

Add to `~/.goclaw/config.yaml`:

```yaml
agents:
  - agent_id: my-agent
    provider: "google"
    model: "gemini-2.5-flash"
    soul: "You are a helpful assistant."
    capabilities:
      - tools.read_file
```

Agents hot-reload when `config.yaml` changes.

## Architecture Overview

See `CLAUDE.md` for detailed architecture documentation.

Key directories:
- `cmd/goclaw/` — Entry point and CLI subcommands
- `internal/persistence/` — SQLite store, schema migrations, task queue
- `internal/engine/` — Task execution engine, LLM brain
- `internal/gateway/` — HTTP/WebSocket server, REST API
- `internal/tui/` — Bubbletea terminal UI
- `internal/policy/` — Default-deny policy engine
- `internal/tools/` — Built-in tool registry
- `internal/agent/` — Multi-agent registry
