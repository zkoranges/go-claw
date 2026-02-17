package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// detectOllamaTools queries Ollama's /api/show endpoint to check if a model
// supports tool/function calling. Returns false on any error (safe default).
// baseURL should be the OpenAI-compat URL ending in /v1.
// model may have an "ollama/" prefix which is stripped for the API call.
func detectOllamaTools(baseURL, model string) bool {
	// Strip /v1 suffix to get native Ollama API URL.
	ollamaURL := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")

	// Strip ollama/ prefix — Ollama expects bare model names.
	model = strings.TrimPrefix(model, "ollama/")

	client := &http.Client{Timeout: 3 * time.Second}
	body := fmt.Sprintf(`{"model":%q}`, model)
	resp, err := client.Post(ollamaURL+"/api/show", "application/json", strings.NewReader(body))
	if err != nil {
		slog.Debug("ollama tool detection failed (connection)", "error", err, "model", model)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("ollama tool detection failed (status)", "status", resp.StatusCode, "model", model)
		return false
	}

	var result struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Debug("ollama tool detection failed (decode)", "error", err, "model", model)
		return false
	}

	if slices.Contains(result.Capabilities, "tools") {
		slog.Info("ollama model supports tools", "model", model)
		return true
	}
	slog.Info("ollama model does not support tools", "model", model, "capabilities", result.Capabilities)
	return false
}
