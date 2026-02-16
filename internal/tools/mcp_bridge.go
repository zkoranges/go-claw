package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/mcp"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// RegisterMCPTools discovers MCP tools for a specific agent and registers them.
// Calls Manager.DiscoverTools to enumerate tools allowed by policy.
// Tool invocations route through Manager.InvokeTool for per-agent policy enforcement and timeouts.
func RegisterMCPTools(g *genkit.Genkit, agentID string, manager *mcp.Manager) []ai.ToolRef {
	ctx := context.Background()

	tools, err := manager.DiscoverTools(ctx, agentID)
	if err != nil {
		slog.Warn("mcp tool discovery failed", "agent", agentID, "error", err)
		// Non-fatal: agent works without MCP tools
		return nil
	}

	var refs []ai.ToolRef

	for _, tool := range tools {
		toolName := fmt.Sprintf("mcp_%s_%s", tool.ServerName, tool.Name)
		serverName := tool.ServerName
		mcpToolName := tool.Name
		agentIDCapture := agentID // Capture for closure

		// Parse input schema to map
		var schema map[string]any
		if len(tool.InputSchema) > 0 {
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				slog.Warn("failed to parse mcp input schema", "tool", toolName, "error", err)
				schema = map[string]any{"type": "object"}
			}
		} else {
			schema = map[string]any{"type": "object"}
		}

		// Append schema to description so LLM sees it since we can't easily patch Genkit schema
		schemaJSON, _ := json.MarshalIndent(schema, "", "  ")
		description := fmt.Sprintf("%s\n\nInput Schema:\n%s", tool.Description, string(schemaJSON))

		// Define generic tool
		// Genkit generates schema from Input type. map[string]any -> object with no properties?
		t := genkit.DefineTool(g, toolName, description,
			func(ctx *ai.ToolContext, input map[string]any) (any, error) {
				// Audit record handled here since this is the execution entry point

				argsJSON, err := json.Marshal(input)
				if err != nil {
					audit.Record("run", "tools.mcp", "failure", agentIDCapture, fmt.Sprintf("%s:%s", serverName, mcpToolName))
					return nil, fmt.Errorf("mcp tool %s/%s: marshal args: %w", serverName, mcpToolName, err)
				}

				res, err := manager.InvokeTool(ctx.Context, agentIDCapture, serverName, mcpToolName, argsJSON)
				status := "success"
				if err != nil {
					status = "failure"
				}
				audit.Record("run", "tools.mcp", status, agentIDCapture, fmt.Sprintf("%s:%s", serverName, mcpToolName))

				if err != nil {
					return nil, fmt.Errorf("mcp tool %s/%s: %w", serverName, mcpToolName, err)
				}

				var resObj any
				if err := json.Unmarshal(res, &resObj); err != nil {
					return string(res), nil
				}
				return resObj, nil
			},
		)

		refs = append(refs, t)
	}

	slog.Info("registered mcp tools", "agent", agentID, "count", len(refs))
	return refs
}
