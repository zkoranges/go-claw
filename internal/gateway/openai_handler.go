package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/bus"
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

	// Tools field is accepted but ignored — tools run autonomously via Genkit.

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

	// 5. Build context with trace ID and sampling config.
	traceID := shared.NewTraceID()
	ctx := shared.WithTraceID(r.Context(), traceID)

	// Pass sampling parameters through to the Brain via context.
	if req.Temperature != nil || req.TopP != nil || req.TopK != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		sc := &shared.SamplingConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			TopK:            req.TopK,
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.Stop,
		}
		ctx = shared.WithSamplingConfig(ctx, sc)
	}

	promptTokens := tokenutil.EstimateTokens(prompt)

	// Stream vs Non-Stream
	if req.Stream {
		s.handleOpenAIStream(w, ctx, req, agentID, sessionID, prompt, traceID, promptTokens)
		return
	}

	// Non-streaming path
	s.handleOpenAINonStream(w, ctx, req, agentID, sessionID, prompt, promptTokens)
}

// handleOpenAIStream handles the SSE streaming path for chat completions.
func (s *Server) handleOpenAIStream(w http.ResponseWriter, ctx context.Context, req ChatCompletionRequest, agentID, sessionID, prompt, traceID string, promptTokens int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.openAIError(w, http.StatusInternalServerError, "internal_error", "streaming not supported")
		return
	}

	var mu sync.Mutex
	completionTokens := 0

	writeSSE := func(resp ChatCompletionResponse) {
		b, _ := json.Marshal(resp)
		mu.Lock()
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
		mu.Unlock()
	}

	// Subscribe to tool-call events on the bus for real-time tool visibility.
	var toolSub *bus.Subscription
	var toolDone chan struct{}
	if s.cfg.Bus != nil {
		toolSub = s.cfg.Bus.Subscribe(bus.TopicStreamToolCall)
		toolDone = make(chan struct{})
		go func() {
			defer close(toolDone)
			toolCallIdx := 0
			for {
				select {
				case evt, ok := <-toolSub.Ch():
					if !ok {
						return
					}
					toolEvt, ok := evt.Payload.(bus.StreamToolCallEvent)
					if !ok {
						continue
					}
					// Filter: only show tool calls for this request's agent.
					if toolEvt.AgentID != "" && toolEvt.AgentID != agentID {
						continue
					}
					idx := toolCallIdx
					toolCallIdx++
					writeSSE(ChatCompletionResponse{
						ID:      "chatcmpl-" + traceID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   req.Model,
						Choices: []ChatCompletionChoice{
							{
								Index: 0,
								Delta: &ChatCompletionMessage{
									Role: "assistant",
									ToolCalls: []ToolCall{
										{
											Index: idx,
											ID:    "call_" + toolEvt.ToolName,
											Type:  "function",
											Function: ToolFunction{
												Name: toolEvt.ToolName,
											},
										},
									},
								},
							},
						},
					})
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	_, err := s.cfg.Registry.StreamChatTask(ctx, agentID, sessionID, prompt, func(chunk string) error {
		tokens := tokenutil.EstimateTokens(chunk)
		mu.Lock()
		completionTokens += tokens
		mu.Unlock()

		writeSSE(ChatCompletionResponse{
			ID:      "chatcmpl-" + traceID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Delta: &ChatCompletionMessage{
						Role:    "assistant",
						Content: chunk,
					},
				},
			},
		})
		return nil
	})

	// Unsubscribe from tool events and wait for goroutine to finish.
	if toolSub != nil {
		s.cfg.Bus.Unsubscribe(toolSub)
		<-toolDone
	}

	if err != nil {
		slog.Error("openai stream error", "error", err)
	}

	// Send final chunk with finish_reason and usage.
	writeSSE(ChatCompletionResponse{
		ID:      "chatcmpl-" + traceID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Delta:        &ChatCompletionMessage{},
				FinishReason: strPtr("stop"),
			},
		},
		Usage: &Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	})

	// Send [DONE]
	mu.Lock()
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	mu.Unlock()
}

// handleOpenAINonStream handles the synchronous (polling) path for chat completions.
func (s *Server) handleOpenAINonStream(w http.ResponseWriter, ctx context.Context, req ChatCompletionRequest, agentID, sessionID, prompt string, promptTokens int) {
	taskID, err := s.cfg.Registry.CreateChatTask(ctx, agentID, sessionID, prompt)
	if err != nil {
		s.openAIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Poll for completion — no artificial timeout.
	// The request context (ctx) cancels when the client disconnects;
	// the engine's task_timeout_seconds protects against runaway tasks.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.openAIError(w, http.StatusGatewayTimeout, "client_disconnected", "Client closed connection")
			return
		case <-ticker.C:
			task, err := s.cfg.Store.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			if task.Status == "SUCCEEDED" {
				var resPayload struct {
					Reply string `json:"reply"`
				}
				reply := ""
				if json.Unmarshal([]byte(task.Result), &resPayload) == nil {
					reply = resPayload.Reply
				} else {
					reply = task.Result
				}

				completionTokens := tokenutil.EstimateTokens(reply)
				resp := ChatCompletionResponse{
					ID:      "chatcmpl-" + taskID,
					Object:  "chat.completion",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []ChatCompletionChoice{
						{
							Index: 0,
							Message: &ChatCompletionMessage{
								Role:    "assistant",
								Content: reply,
							},
							FinishReason: strPtr("stop"),
						},
					},
					Usage: &Usage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(resp); err != nil {
					slog.Warn("openai: failed to write response", "error", err)
				}
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
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("openai: failed to write models response", "error", err)
	}
}

func (s *Server) openAIError(w http.ResponseWriter, status int, code, message string) {
	// Derive the error type from HTTP status per OpenAI spec.
	errType := "server_error"
	switch {
	case status == http.StatusBadRequest, status == http.StatusMethodNotAllowed:
		errType = "invalid_request_error"
	case status == http.StatusUnauthorized:
		errType = "authentication_error"
	case status == http.StatusForbidden:
		errType = "permission_error"
	case status == http.StatusNotFound:
		errType = "not_found_error"
	case status == http.StatusTooManyRequests:
		errType = "rate_limit_error"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	errResp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    code,
		},
	}
	if err := json.NewEncoder(w).Encode(errResp); err != nil {
		slog.Warn("openai: failed to write error response", "error", err)
	}
}

func strPtr(s string) *string { return &s }
