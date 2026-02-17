package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/basket/go-claw/internal/bus"
)

// streamSSEEvent represents a single SSE event sent to the client.
type streamSSEEvent struct {
	Type     string `json:"type"`
	Token    string `json:"token,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

// handleTaskStream implements GET /api/v1/task/stream?task_id=XXX.
// It subscribes to bus events filtered by task_id and returns an SSE stream
// of streaming tokens and completion signals.
func (s *Server) handleTaskStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		http.Error(w, "task_id query parameter is required", http.StatusBadRequest)
		return
	}

	if s.cfg.Bus == nil {
		http.Error(w, "streaming not available: event bus not configured", http.StatusServiceUnavailable)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to all stream events.
	sub := s.cfg.Bus.Subscribe("stream.")
	defer s.cfg.Bus.Unsubscribe(sub)

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected.
			slog.Debug("sse: client disconnected", "task_id", taskID)
			return

		case event, ok := <-sub.Ch():
			if !ok {
				// Subscription closed.
				return
			}

			// Filter events by task_id.
			var sseEvent *streamSSEEvent

			switch payload := event.Payload.(type) {
			case bus.StreamTokenEvent:
				if payload.TaskID != taskID {
					continue
				}
				sseEvent = &streamSSEEvent{
					Type:  "token",
					Token: payload.Token,
				}

			case bus.StreamDoneEvent:
				if payload.TaskID != taskID {
					continue
				}
				sseEvent = &streamSSEEvent{
					Type: "done",
				}

			case bus.StreamToolCallEvent:
				if payload.TaskID != taskID {
					continue
				}
				sseEvent = &streamSSEEvent{
					Type:     "tool_call",
					ToolName: payload.ToolName,
				}

			default:
				continue
			}

			data, err := json.Marshal(sseEvent)
			if err != nil {
				slog.Error("sse: marshal event", "error", err)
				continue
			}

			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				slog.Debug("sse: write failed (client disconnected?)", "task_id", taskID, "error", err)
				return
			}
			flusher.Flush()

			// If this was a done event, close the stream.
			if sseEvent.Type == "done" {
				return
			}
		}
	}
}
