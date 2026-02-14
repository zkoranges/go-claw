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

// RegisterMCPTools discovers MCP tools and registers them.
func RegisterMCPTools(g *genkit.Genkit, reg *Registry, manager *mcp.Manager) []ai.ToolRef {
	mcpTools, err := manager.AllTools(context.Background())
	if err != nil {
		slog.Warn("failed to list mcp tools", "error", err)
		return nil
	}

	var refs []ai.ToolRef

	for server, tools := range mcpTools {
		for _, tool := range tools {
			toolName := fmt.Sprintf("mcp_%s_%s", server, tool.Name)
			serverName := server
			mcpToolName := tool.Name

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
						audit.Record("run", "tools.mcp", "failure", "", fmt.Sprintf("%s:%s", serverName, mcpToolName))
						return nil, fmt.Errorf("marshal args: %w", err)
					}

					res, err := manager.CallTool(ctx, serverName, mcpToolName, argsJSON)
					status := "success"
					if err != nil {
						status = "failure"
					}
					audit.Record("run", "tools.mcp", status, "", fmt.Sprintf("%s:%s", serverName, mcpToolName))

					if err != nil {
						return nil, err
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
	}

	slog.Info("registered mcp tools", "count", len(refs))
	return refs
}
