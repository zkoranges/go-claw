package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/memory"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// MemoryReadInput is the input for the memory_read tool.
type MemoryReadInput struct {
	Path string `json:"path"`
}

// MemoryReadOutput is the output for the memory_read tool.
type MemoryReadOutput struct {
	Content string `json:"content"`
}

// MemoryWriteInput is the input for the memory_write tool.
type MemoryWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append,omitempty"`
}

// MemoryWriteOutput is the output for the memory_write tool.
type MemoryWriteOutput struct {
	Written bool   `json:"written"`
	Path    string `json:"path"`
}

// MemorySearchInput is the input for the memory_search tool.
type MemorySearchInput struct {
	Query string `json:"query"`
}

// MemorySearchOutput is the output for the memory_search tool.
type MemorySearchOutput struct {
	Hits []memory.SearchHit `json:"hits"`
}

// workspaceRoot returns the workspace directory under GOCLAW_HOME.
func workspaceRoot() string {
	home := os.Getenv("GOCLAW_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".goclaw")
	}
	return filepath.Join(home, "workspace")
}

// registerMemoryTools registers the memory_read, memory_write, and
// memory_search tools with the Genkit instance.
func registerMemoryTools(g *genkit.Genkit, reg *Registry) []ai.ToolRef {
	readTool := genkit.DefineTool(g, "memory_read",
		"Read a file from the agent memory workspace. Path is relative to the workspace root.",
		func(ctx *ai.ToolContext, input MemoryReadInput) (MemoryReadOutput, error) {
			reg.publishToolCall(ctx, "memory_read")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.memory_read") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.memory_read", "missing_capability", pv, "memory_read")
				return MemoryReadOutput{}, fmt.Errorf("policy denied capability %q", "tools.memory_read")
			}
			audit.Record("allow", "tools.memory_read", "capability_granted", policyVersion(reg.Policy), input.Path)

			ws, err := memory.NewWorkspace(workspaceRoot())
			if err != nil {
				return MemoryReadOutput{}, fmt.Errorf("memory workspace: %w", err)
			}
			content, err := ws.Read(input.Path)
			if err != nil {
				return MemoryReadOutput{}, err
			}
			return MemoryReadOutput{Content: content}, nil
		},
	)

	writeTool := genkit.DefineTool(g, "memory_write",
		"Write or append content to a file in the agent memory workspace. Path is relative to the workspace root. Set append=true to append instead of overwrite.",
		func(ctx *ai.ToolContext, input MemoryWriteInput) (MemoryWriteOutput, error) {
			reg.publishToolCall(ctx, "memory_write")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.memory_write") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.memory_write", "missing_capability", pv, "memory_write")
				return MemoryWriteOutput{}, fmt.Errorf("policy denied capability %q", "tools.memory_write")
			}
			audit.Record("allow", "tools.memory_write", "capability_granted", policyVersion(reg.Policy), input.Path)

			ws, err := memory.NewWorkspace(workspaceRoot())
			if err != nil {
				return MemoryWriteOutput{}, fmt.Errorf("memory workspace: %w", err)
			}

			if input.Append {
				err = ws.Append(input.Path, input.Content)
			} else {
				err = ws.Write(input.Path, input.Content)
			}
			if err != nil {
				return MemoryWriteOutput{}, err
			}
			return MemoryWriteOutput{Written: true, Path: input.Path}, nil
		},
	)

	searchTool := genkit.DefineTool(g, "memory_search",
		"Search the agent memory workspace for files containing the query string. Returns matching lines with file paths and line numbers.",
		func(ctx *ai.ToolContext, input MemorySearchInput) (MemorySearchOutput, error) {
			reg.publishToolCall(ctx, "memory_search")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.memory_read") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.memory_read", "missing_capability", pv, "memory_search")
				return MemorySearchOutput{}, fmt.Errorf("policy denied capability %q", "tools.memory_read")
			}
			audit.Record("allow", "tools.memory_read", "capability_granted", policyVersion(reg.Policy), input.Query)

			ws, err := memory.NewWorkspace(workspaceRoot())
			if err != nil {
				return MemorySearchOutput{}, fmt.Errorf("memory workspace: %w", err)
			}
			hits, err := ws.Search(input.Query)
			if err != nil {
				return MemorySearchOutput{}, err
			}
			if hits == nil {
				hits = []memory.SearchHit{}
			}
			return MemorySearchOutput{Hits: hits}, nil
		},
	)

	return []ai.ToolRef{readTool, writeTool, searchTool}
}
