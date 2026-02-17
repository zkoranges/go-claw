package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/gateway"
	"github.com/google/uuid"
)

// streamSSEEvent mirrors the SSE event structure from stream.go for test decoding.
type streamSSEEvent struct {
	Type     string `json:"type"`
	Token    string `json:"token,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

func TestStreamSSE_ContentType(t *testing.T) {
	b := bus.New()
	ts, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.Bus = b
	})

	taskID := uuid.NewString()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/task/stream?task_id="+taskID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	// Publish a done event immediately so the handler returns.
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish(bus.TopicStreamDone, bus.StreamDoneEvent{
			TaskID:  taskID,
			AgentID: "default",
		})
	}()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamSSE_MissingTaskID(t *testing.T) {
	b := bus.New()
	ts, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.Bus = b
	})

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/task/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStreamSSE_MethodNotAllowed(t *testing.T) {
	b := bus.New()
	ts, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.Bus = b
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/task/stream?task_id="+uuid.NewString(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestStreamSSE_StreamsTokens(t *testing.T) {
	b := bus.New()
	ts, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.Bus = b
	})

	taskID := uuid.NewString()

	// Publish token and done events in background after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish(bus.TopicStreamToken, bus.StreamTokenEvent{
			TaskID:  taskID,
			AgentID: "default",
			Token:   "Hello",
		})
		time.Sleep(10 * time.Millisecond)
		b.Publish(bus.TopicStreamToken, bus.StreamTokenEvent{
			TaskID:  taskID,
			AgentID: "default",
			Token:   " world",
		})
		time.Sleep(10 * time.Millisecond)
		b.Publish(bus.TopicStreamDone, bus.StreamDoneEvent{
			TaskID:  taskID,
			AgentID: "default",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/task/stream?task_id="+taskID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Read SSE events.
	scanner := bufio.NewScanner(resp.Body)
	var events []streamSSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var evt streamSSEEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			t.Fatalf("unmarshal SSE event: %v", err)
		}
		events = append(events, evt)
	}

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %+v", len(events), events)
	}

	// First two should be tokens.
	if events[0].Type != "token" || events[0].Token != "Hello" {
		t.Errorf("event[0] = %+v, want token=Hello", events[0])
	}
	if events[1].Type != "token" || events[1].Token != " world" {
		t.Errorf("event[1] = %+v, want token=' world'", events[1])
	}
	// Last should be done.
	if events[len(events)-1].Type != "done" {
		t.Errorf("last event type = %q, want done", events[len(events)-1].Type)
	}
}

func TestStreamSSE_ClientDisconnect(t *testing.T) {
	b := bus.New()
	ts, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.Bus = b
	})

	taskID := uuid.NewString()

	// Create a request with a short-lived context to simulate client disconnect.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/task/stream?task_id="+taskID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context canceled during dial is expected.
		return
	}
	defer resp.Body.Close()

	// The server should handle the disconnect gracefully.
	// The response should have started with 200 (SSE headers sent).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Wait for the context to be canceled.
	<-ctx.Done()

	// Verify the bus subscriber was cleaned up.
	// Give a small grace period for cleanup.
	time.Sleep(100 * time.Millisecond)

	// The handler should have returned and unsubscribed.
	// Publishing should not panic.
	b.Publish(bus.TopicStreamToken, bus.StreamTokenEvent{
		TaskID:  taskID,
		AgentID: "default",
		Token:   "after disconnect",
	})
}

func TestStreamSSE_FiltersByTaskID(t *testing.T) {
	b := bus.New()

	// Use httptest directly to avoid needing a full gateway server.
	srv := gateway.New(gateway.Config{
		Store:     openStoreForGatewayTest(t),
		Registry:  makeTestRegistry(openStoreForGatewayTest(t), nil),
		AuthToken: gatewayTestAuthToken,
		Bus:       b,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	taskID := uuid.NewString()
	otherTaskID := uuid.NewString()

	// Publish events for two different task IDs, then done for our task.
	go func() {
		time.Sleep(50 * time.Millisecond)
		// Event for a different task - should be filtered out.
		b.Publish(bus.TopicStreamToken, bus.StreamTokenEvent{
			TaskID:  otherTaskID,
			AgentID: "default",
			Token:   "wrong task",
		})
		time.Sleep(10 * time.Millisecond)
		// Event for our task - should be included.
		b.Publish(bus.TopicStreamToken, bus.StreamTokenEvent{
			TaskID:  taskID,
			AgentID: "default",
			Token:   "right task",
		})
		time.Sleep(10 * time.Millisecond)
		b.Publish(bus.TopicStreamDone, bus.StreamDoneEvent{
			TaskID:  taskID,
			AgentID: "default",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/task/stream?task_id="+taskID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var events []streamSSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var evt streamSSEEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		events = append(events, evt)
	}

	// Should have exactly 2 events: the "right task" token and the "done".
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "token" || events[0].Token != "right task" {
		t.Errorf("event[0] = %+v, want token='right task'", events[0])
	}
	if events[1].Type != "done" {
		t.Errorf("event[1] type = %q, want done", events[1].Type)
	}
}
