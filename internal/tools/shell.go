package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const (
	defaultShellTimeout = 30 * time.Second
	maxShellTimeout     = 120 * time.Second
	maxShellOutput      = 8 * 1024 // 8KB
)

// Executor defines the interface for running shell commands.
type Executor interface {
	Exec(ctx context.Context, cmd, workDir string) (stdout, stderr string, exitCode int, err error)
}

// HostExecutor runs commands locally.
type HostExecutor struct{}

func (h *HostExecutor) Exec(ctx context.Context, cmd, workDir string) (stdout, stderr string, exitCode int, err error) {
	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd)
	if workDir != "" {
		execCmd.Dir = workDir
	}

	var outBuf, errBuf bytes.Buffer
	execCmd.Stdout = &outBuf
	execCmd.Stderr = &errBuf

	runErr := execCmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Other errors (e.g. not found, killed)
			exitCode = -1
			err = runErr
		}
	} else {
		exitCode = 0
	}
	return outBuf.String(), errBuf.String(), exitCode, err
}

// denyList contains commands that should never be executed.
var denyList = map[string]struct{}{
	"rm":       {},
	"rmdir":    {},
	"mkfs":     {},
	"dd":       {},
	"shutdown": {},
	"reboot":   {},
	"halt":     {},
	"poweroff": {},
	"kill":     {},
	"killall":  {},
	"pkill":    {},
	"sudo":     {},
	"su":       {},
	"chmod":    {},
	"chown":    {},
}

// ShellInput is the input for the exec tool.
type ShellInput struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// ShellOutput is the output for the exec tool.
type ShellOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func registerShell(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	// Decide executor based on config (TODO: pass config to registerShell or Registry)
	// For now we assume host executor unless sandbox is explicitly requested.
	// Since Registry struct doesn't have the full config, we might need to update Registry or passed args.
	// However, we can default to HostExecutor for now and let the caller swap it if needed?
	// Better: Update Registry to hold ShellConfig? Or just check if we can access it.
	// Registry has APIKeys but not full config.
	// Let's use HostExecutor by default here, and we will update `tools.go` to pass the config or executor.
	var executor Executor = &HostExecutor{}

	// If the registry has a way to provide the sandbox, we'd use it.
	// Let's assume we can upgrade this later. For P2-2, we need to enable it.
	// I'll update tools.go to pass the Executor.

	return genkit.DefineTool(g, "exec",
		"Execute a shell command and return its output. Commands on the deny list (rm, sudo, kill, etc.) are blocked. Output is truncated to 8KB and secrets are redacted.",
		func(ctx *ai.ToolContext, input ShellInput) (ShellOutput, error) {
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.exec") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.exec", "missing_capability", pv, "exec")
				return ShellOutput{}, fmt.Errorf("policy denied capability %q", "tools.exec")
			}
			audit.Record("allow", "tools.exec", "capability_granted", policyVersion(reg.Policy), input.Command)

			// Parse command to check deny list.
			parts := strings.Fields(strings.TrimSpace(input.Command))
			if len(parts) == 0 {
				return ShellOutput{}, fmt.Errorf("empty command")
			}
			// Block injection vectors.
			for _, op := range []string{";", "$(", "`"} {
				if strings.Contains(input.Command, op) {
					return ShellOutput{}, fmt.Errorf("command contains disallowed operator %q", op)
				}
			}
			// Allow pipes and logical operators, but validate each segment against deny-list.
			segments := splitCommandSegments(input.Command)
			for _, seg := range segments {
				segParts := strings.Fields(strings.TrimSpace(seg))
				for _, tok := range segParts {
					if _, blocked := denyList[tok]; blocked {
						return ShellOutput{}, fmt.Errorf("command %q is on the deny list", tok)
					}
				}
			}

			// Determine timeout.
			timeout := defaultShellTimeout
			if input.TimeoutSec > 0 {
				timeout = time.Duration(input.TimeoutSec) * time.Second
				if timeout > maxShellTimeout {
					timeout = maxShellTimeout
				}
			}

			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// Use executor
			// We need to access the executor. Since this closure captures variables,
			// if we change registerShell signature, we can pass it.
			// But wait, I can't change registerShell signature without changing tools.go.
			// I should pause and update tools.go first or assume I will.
			// I'll assume reg has an Executor field.

			// For now, I'll use the local var 'executor' defined above (HostExecutor).
			// To make it dynamic, I need to inject it.
			// I will add Executor field to Registry struct in the next step.

			exec := executor
			if reg.ShellExecutor != nil {
				exec = reg.ShellExecutor
			}

			stdout, stderr, exitCode, err := exec.Exec(execCtx, input.Command, input.WorkingDir)

			if err != nil && exitCode == 0 {
				// System error (not command failure)
				if execCtx.Err() == context.DeadlineExceeded {
					return ShellOutput{
						Stderr:   "command timed out",
						ExitCode: -1,
					}, nil
				}
				return ShellOutput{}, fmt.Errorf("exec: %w", err)
			}

			outStr := truncateOutput(stdout, maxShellOutput)
			errStr := truncateOutput(stderr, maxShellOutput)

			// Redact secrets from output.
			outStr = shared.Redact(outStr)
			errStr = shared.Redact(errStr)

			return ShellOutput{
				Stdout:   outStr,
				Stderr:   errStr,
				ExitCode: exitCode,
			}, nil
		},
	)
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
}

// splitCommandSegments splits a command at pipe and logical operators,
// returning the individual command segments for deny-list checking.
func splitCommandSegments(cmd string) []string {
	var segments []string
	current := cmd
	for current != "" {
		// Find the first operator.
		minIdx := len(current)
		matchLen := 0
		for _, op := range []string{"||", "&&", "|"} {
			if idx := strings.Index(current, op); idx >= 0 && idx < minIdx {
				minIdx = idx
				matchLen = len(op)
			}
		}
		if matchLen > 0 {
			seg := strings.TrimSpace(current[:minIdx])
			if seg != "" {
				segments = append(segments, seg)
			}
			current = current[minIdx+matchLen:]
		} else {
			// No more operators.
			seg := strings.TrimSpace(current)
			if seg != "" {
				segments = append(segments, seg)
			}
			break
		}
	}
	return segments
}
