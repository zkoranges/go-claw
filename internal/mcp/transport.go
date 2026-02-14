package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Transport defines the communication layer for MCP.
type Transport interface {
	Send(ctx context.Context, msg json.RawMessage) error
	Receive(ctx context.Context) (json.RawMessage, error)
	Close() error
}

// StdioTransport implements MCP transport over stdio.
type StdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  io.ReadCloser
	mu      sync.Mutex
	running bool
}

// NewStdioTransport starts a subprocess and connects to its stdio.
func NewStdioTransport(command string, args []string, env map[string]string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	// Merge environment
	cmd.Env = os.Environ()
	for k, v := range env {
		// Expand environment variables in values
		expanded := os.ExpandEnv(v)
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, expanded))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command %q: %w", command, err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		stderr:  stderr,
		running: true,
	}

	// Log stderr in background
	go func() {
		scanner := bufio.NewScanner(t.stderr)
		for scanner.Scan() {
			slog.Debug("mcp stderr", "server", command, "msg", scanner.Text())
		}
	}()

	return t, nil
}

// Send sends a JSON-RPC message.
func (t *StdioTransport) Send(ctx context.Context, msg json.RawMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return fmt.Errorf("transport closed")
	}

	// JSON-RPC over stdio is typically newline delimited
	msgWithNewline := append(msg, '\n')
	if _, err := t.stdin.Write(msgWithNewline); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

// Receive blocks until a message is received or context is cancelled.
// Note: This simple implementation assumes one-line-per-message (JSON-RPC).
func (t *StdioTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	// We can't easily select on bufio.Reader and Context, so we use a goroutine if strict cancellation is needed.
	// For simplicity in this v0.1, we rely on ReadString responding to process termination.

	// Use a channel to support context cancellation
	type result struct {
		msg []byte
		err error
	}
	ch := make(chan result, 1)

	go func() {
		line, err := t.stdout.ReadBytes('\n')
		if err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{line, nil}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return json.RawMessage(res.msg), nil
	}
}

// Close kills the subprocess.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return nil
	}
	t.running = false

	_ = t.stdin.Close()
	t.stdout.Reset(nil) // Detach

	if t.cmd.Process != nil {
		return t.cmd.Process.Kill()
	}
	return nil
}

// ReconnectableTransport wraps a StdioTransport with automatic reconnection.
type ReconnectableTransport struct {
	command string
	args    []string
	env     map[string]string

	mu        sync.Mutex
	transport *StdioTransport
	maxRetry  int
}

// NewReconnectableTransport creates a transport that auto-reconnects on failure.
func NewReconnectableTransport(command string, args []string, env map[string]string) (*ReconnectableTransport, error) {
	transport, err := NewStdioTransport(command, args, env)
	if err != nil {
		return nil, err
	}
	return &ReconnectableTransport{
		command:   command,
		args:      args,
		env:       env,
		transport: transport,
		maxRetry:  3,
	}, nil
}

// Send sends a message, attempting reconnection on failure.
func (r *ReconnectableTransport) Send(ctx context.Context, msg json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	err := r.transport.Send(ctx, msg)
	if err == nil {
		return nil
	}

	// Attempt reconnect with exponential backoff.
	backoff := time.Second
	for attempt := 0; attempt < r.maxRetry; attempt++ {
		slog.Info("mcp: reconnecting", "command", r.command, "attempt", attempt+1, "backoff", backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		_ = r.transport.Close()
		newTransport, newErr := NewStdioTransport(r.command, r.args, r.env)
		if newErr != nil {
			backoff *= 2
			continue
		}
		r.transport = newTransport

		if sendErr := r.transport.Send(ctx, msg); sendErr == nil {
			slog.Info("mcp: reconnected successfully", "command", r.command)
			return nil
		}
		backoff *= 2
	}

	return fmt.Errorf("mcp: reconnect failed after %d attempts: %w", r.maxRetry, err)
}

// Receive delegates to the underlying transport.
func (r *ReconnectableTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	r.mu.Lock()
	t := r.transport
	r.mu.Unlock()
	return t.Receive(ctx)
}

// Close closes the underlying transport.
func (r *ReconnectableTransport) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transport.Close()
}
