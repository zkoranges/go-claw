package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/engine"
	"github.com/google/uuid"
)

// mockStreamBrain implements engine.Brain for streaming tests.
type mockStreamBrain struct {
	chunks []string
	err    error
}

func (m *mockStreamBrain) Respond(_ context.Context, _, _ string) (string, error) {
	return `{"reply":"ok"}`, nil
}

func (m *mockStreamBrain) Stream(_ context.Context, _, _ string, onChunk func(string) error) error {
	if m.err != nil {
		return m.err
	}
	for _, chunk := range m.chunks {
		if err := onChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

func TestStreamChatTask_PublishesBusEvents(t *testing.T) {
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	brain := &mockStreamBrain{chunks: []string{"Hello", " ", "world"}}
	proc := engine.EchoProcessor{Brain: brain}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 50 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
		Bus:          b,
	})
	ctx := context.Background()
	eng.Start(ctx)

	// Subscribe to all stream events before streaming.
	sub := b.Subscribe("stream.")
	defer b.Unsubscribe(sub)

	_, err := eng.StreamChatTask(ctx, sessionID, "test message", func(content string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatTask: %v", err)
	}

	// Collect events.
	var events []bus.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-sub.Ch():
			if !ok {
				goto done
			}
			events = append(events, evt)
			// After receiving stream.done, stop collecting.
			if evt.Topic == bus.TopicStreamDone {
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:

	// Should have 3 token events + 1 done event = 4 total.
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}

	// Verify token events.
	for i, expectedToken := range []string{"Hello", " ", "world"} {
		if events[i].Topic != bus.TopicStreamToken {
			t.Errorf("event[%d] topic = %q, want %q", i, events[i].Topic, bus.TopicStreamToken)
		}
		tokenEvt, ok := events[i].Payload.(bus.StreamTokenEvent)
		if !ok {
			t.Fatalf("event[%d] payload type = %T, want StreamTokenEvent", i, events[i].Payload)
		}
		if tokenEvt.Token != expectedToken {
			t.Errorf("event[%d] token = %q, want %q", i, tokenEvt.Token, expectedToken)
		}
	}

	// Verify done event.
	if events[3].Topic != bus.TopicStreamDone {
		t.Errorf("event[3] topic = %q, want %q", events[3].Topic, bus.TopicStreamDone)
	}
}

func TestStreamChatTask_CallsOnChunk(t *testing.T) {
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	brain := &mockStreamBrain{chunks: []string{"alpha", "beta", "gamma"}}
	proc := engine.EchoProcessor{Brain: brain}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 50 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
		Bus:          b,
	})
	ctx := context.Background()
	eng.Start(ctx)

	var mu sync.Mutex
	var received []string

	_, err := eng.StreamChatTask(ctx, sessionID, "test", func(content string) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, content)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatTask: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("onChunk called %d times, want 3", len(received))
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if received[i] != want {
			t.Errorf("chunk[%d] = %q, want %q", i, received[i], want)
		}
	}
}

func TestStreamChatTask_StreamDoneEvent(t *testing.T) {
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	brain := &mockStreamBrain{chunks: []string{"done-test"}}
	proc := engine.EchoProcessor{Brain: brain}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 50 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
		Bus:          b,
	})
	ctx := context.Background()
	eng.Start(ctx)

	// Subscribe specifically to stream.done.
	sub := b.Subscribe(bus.TopicStreamDone)
	defer b.Unsubscribe(sub)

	taskID, err := eng.StreamChatTask(ctx, sessionID, "test", func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamChatTask: %v", err)
	}

	select {
	case evt := <-sub.Ch():
		doneEvt, ok := evt.Payload.(bus.StreamDoneEvent)
		if !ok {
			t.Fatalf("payload type = %T, want StreamDoneEvent", evt.Payload)
		}
		if doneEvt.TaskID != taskID {
			t.Errorf("TaskID = %q, want %q", doneEvt.TaskID, taskID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stream.done event")
	}
}

func TestStreamChatTask_QueueDepthEnforced(t *testing.T) {
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	brain := &mockStreamBrain{chunks: []string{"x"}}
	proc := engine.EchoProcessor{Brain: brain}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		TaskTimeout:   5 * time.Second,
		MaxQueueDepth: 1,
		Bus:           b,
	})
	ctx := context.Background()
	eng.Start(ctx)

	// First task should succeed (depth = 0).
	_, err := eng.StreamChatTask(ctx, sessionID, "first", func(string) error { return nil })
	if err != nil {
		t.Fatalf("first StreamChatTask: %v", err)
	}

	// Create a pending task to fill the queue.
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	if _, err := store.CreateTask(ctx, sessionID, `{"content":"filler"}`); err != nil {
		t.Fatalf("create filler task: %v", err)
	}

	// Second stream attempt should fail with backpressure.
	_, err = eng.StreamChatTask(ctx, sessionID, "second", func(string) error { return nil })
	if err == nil {
		t.Fatal("expected ErrQueueSaturated, got nil")
	}
	if err != engine.ErrQueueSaturated {
		t.Fatalf("expected ErrQueueSaturated, got %v", err)
	}
}

func TestStreamChatTask_NonStreamingFallback(t *testing.T) {
	// Verify that the non-streaming CreateChatTask path still works alongside streaming.
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	proc := &countingProcessor{sleep: 10 * time.Millisecond}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  2,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
		Bus:          b,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)

	// Create a non-streaming task.
	taskID, err := eng.CreateChatTask(ctx, sessionID, "non-streaming test")
	if err != nil {
		t.Fatalf("CreateChatTask: %v", err)
	}

	// Wait for the non-streaming task to complete.
	task := waitForTaskStatus(t, store, taskID, "SUCCEEDED", 3*time.Second)
	if task == nil {
		t.Fatal("non-streaming task did not complete")
	}

	// Subscribe to stream events to verify no streaming events were published.
	sub := b.Subscribe("stream.")
	defer b.Unsubscribe(sub)

	// Give a brief window to check that no stream events arrive.
	select {
	case evt := <-sub.Ch():
		t.Fatalf("unexpected stream event for non-streaming task: %+v", evt)
	case <-time.After(100 * time.Millisecond):
		// Expected: no stream events.
	}
}

func TestStreamChatTask_StreamFailureNoDoneEvent(t *testing.T) {
	store := openStoreForEngineTest(t)
	b := bus.New()
	sessionID := uuid.NewString()

	brain := &mockStreamBrain{err: fmt.Errorf("LLM unavailable")}
	proc := engine.EchoProcessor{Brain: brain}

	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 50 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
		Bus:          b,
	})
	ctx := context.Background()
	eng.Start(ctx)

	// Subscribe to stream.done to verify it is NOT published on failure.
	sub := b.Subscribe(bus.TopicStreamDone)
	defer b.Unsubscribe(sub)

	_, err := eng.StreamChatTask(ctx, sessionID, "will fail", func(string) error { return nil })
	// StreamChatTask records failure internally, returns taskID + nil.
	if err != nil {
		t.Fatalf("StreamChatTask returned unexpected error: %v", err)
	}

	// Verify no done event was published.
	select {
	case evt := <-sub.Ch():
		t.Fatalf("unexpected stream.done event on failure: %+v", evt)
	case <-time.After(200 * time.Millisecond):
		// Expected: no done event on failure.
	}
}
