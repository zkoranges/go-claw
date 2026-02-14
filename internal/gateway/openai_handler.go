package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/shared"
	"github.com/basket/go-claw/internal/tokenutil"
	"github.com/google/uuid"
)

func (s *Server) handleOpenAIChatCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.openAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
		return
	}
	if !s.authorize(r) {
		s.openAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.openAIError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON")
		return
	}

	// 1. Route to agent by model prefix (needed before session ID generation).
	agentID := "default"
	if strings.HasPrefix(req.Model, "agent:") {
		agentID = strings.TrimPrefix(req.Model, "agent:")
	}

	// 2. Determine Session ID
	// OpenAI API is stateless (history passed in request). GoClaw is stateful.
	// We use the "user" field (if present) as a deterministic Session ID to allow persistence.
	// Include agentID in the namespace so each agent gets its own session.
	var sessionID string
	if req.User != "" {
		sessionID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("goclaw:user:"+req.User+":agent:"+agentID)).String()
	} else {
		sessionID = uuid.NewString()
	}

	// 3. Extract Prompt
	if len(req.Messages) == 0 {
		s.openAIError(w, http.StatusBadRequest, "invalid_request_error", "Messages list is empty")
		return
	}
	lastMsg := req.Messages[len(req.Messages)-1]
	if lastMsg.Role != "user" {
		s.openAIError(w, http.StatusBadRequest, "invalid_request_error", "Last message must be from user")
		return
	}
	prompt := lastMsg.Content

	// 4. Seed prior messages into session history so the Brain sees full context.
	// The OpenAI API is stateless (client sends full history each request), but
	// GoClaw's Brain loads history from the DB. We reconcile by ensuring all
	// prior messages exist in the session before dispatching the new prompt.
	if err := s.cfg.Store.EnsureSession(r.Context(), sessionID); err != nil {
		s.openAIError(w, http.StatusInternalServerError, "internal_error", "session init: "+err.Error())
		return
	}
	for _, msg := range req.Messages[:len(req.Messages)-1] {
		role := strings.ToLower(msg.Role)
		if role == "system" || role == "user" || role == "assistant" || role == "tool" {
			_ = s.cfg.Store.AddHistory(r.Context(), sessionID, agentID, role, msg.Content, tokenutil.EstimateTokens(msg.Content))
		}
	}

	// 5. Create Task
	traceID := shared.NewTraceID()
	ctx := shared.WithTraceID(r.Context(), traceID)

	// Stream vs Non-Stream
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		_, err := s.cfg.Registry.StreamChatTask(ctx, agentID, sessionID, prompt, func(chunk string) error {
			resp := ChatCompletionResponse{
				ID:      "chatcmpl-" + traceID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   req.Model,
				Choices: []ChatCompletionChoice{
					{
						Index: 0,
						Message: ChatCompletionMessage{
							Role:    "assistant",
							Content: chunk,
						},
						FinishReason: "", // null for chunks
					},
				},
			}
			b, _ := json.Marshal(resp)
			fmt.Fprintf(w, "data: %s\n\n", string(b))
			w.(http.Flusher).Flush()
			return nil
		})

		if err != nil {
			// If error occurs during stream start, we might have already sent headers.
			// Ideally we send an error chunk?
			slog.Error("openai stream error", "error", err)
		}

		// Send [DONE]
		fmt.Fprintf(w, "data: [DONE]\n\n")
		return
	}

	// Non-streaming
	// We need to wait for the task to complete.
	// engine.CreateChatTask creates it async.
	// We need a synchronous wait helper or use the engine directly?
	// The Engine struct has `CreateChatTask` which returns taskID.
	// It does NOT wait.
	// To support sync API, we need to poll the store for the result.

	taskID, err := s.cfg.Registry.CreateChatTask(ctx, agentID, sessionID, prompt)
	if err != nil {
		s.openAIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Poll for completion (with timeout)
	timeout := 60 * time.Second // Default timeout for sync requests
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case <-timeoutTimer.C:
			s.openAIError(w, http.StatusGatewayTimeout, "timeout", "Task timed out")
			return
		case <-ticker.C:
			task, err := s.cfg.Store.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			if task.Status == "SUCCEEDED" {
				// Parse result
				var resPayload struct {
					Reply string `json:"reply"`
				}
				reply := ""
				if json.Unmarshal([]byte(task.Result), &resPayload) == nil {
					reply = resPayload.Reply
				} else {
					reply = task.Result // Fallback
				}

				resp := ChatCompletionResponse{
					ID:      "chatcmpl-" + taskID,
					Object:  "chat.completion",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []ChatCompletionChoice{
						{
							Index: 0,
							Message: ChatCompletionMessage{
								Role:    "assistant",
								Content: reply,
							},
							FinishReason: "stop",
						},
					},
					Usage: Usage{
						TotalTokens: tokenutil.EstimateTokens(prompt) + tokenutil.EstimateTokens(reply),
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			if task.Status == "FAILED" || task.Status == "DEAD_LETTER" || task.Status == "CANCELED" {
				s.openAIError(w, http.StatusInternalServerError, "task_failed", fmt.Sprintf("Task failed: %s", task.Error))
				return
			}
		}
	}
}

func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.openAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
		return
	}
	if !s.authorize(r) {
		s.openAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
		return
	}

	models := []Model{
		{ID: "goclaw-v1", Object: "model", Created: 1677610602, OwnedBy: "goclaw"},
	}
	for _, a := range s.cfg.Registry.ListAgents() {
		models = append(models, Model{
			ID:      "agent:" + a.AgentID,
			Object:  "model",
			Created: 1677610602,
			OwnedBy: "goclaw",
		})
	}
	resp := ModelListResponse{
		Object: "list",
		Data:   models,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) openAIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	errResp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    code,
			"code":    code,
		},
	}
	_ = json.NewEncoder(w).Encode(errResp)
}
