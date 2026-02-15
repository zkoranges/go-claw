package bus

import (
	"strings"
	"sync"
)

const defaultBufferSize = 100

// Event is a message published on the bus.
type Event struct {
	Topic   string
	Payload interface{}
}

// Task event topics.
const (
	TopicTaskStateChanged  = "task.state_changed"
	TopicTaskMetrics       = "task.metrics"
	TopicTaskTokens        = "task.tokens"
	TopicTaskCompleted     = "task.completed"
	TopicTaskFailed        = "task.failed"
	TopicTaskRetrying      = "task.retrying"
)

// TaskStateChangedEvent is published when a task's state changes.
type TaskStateChangedEvent struct {
	TaskID    string // Task ID
	SessionID string // Session ID
	OldStatus string // Previous status (e.g. QUEUED)
	NewStatus string // New status (e.g. RUNNING)
}

// TaskMetricsEvent is published when task metrics are recorded.
type TaskMetricsEvent struct {
	TaskID             string  // Task ID
	InputTokens        int     // Input tokens used
	OutputTokens       int     // Output tokens used
	TotalTokens        int     // Total tokens used
	EstimatedCostUSD   float64 // Estimated cost in USD
}

// TaskTokensEvent is published when task token counts are updated.
type TaskTokensEvent struct {
	TaskID           string // Task ID
	PromptTokens     int    // Prompt tokens
	CompletionTokens int    // Completion tokens
}

// Subscription represents an active subscription.
type Subscription struct {
	id     int
	prefix string
	ch     chan Event
}

// Ch returns the channel to receive events on.
func (s *Subscription) Ch() <-chan Event {
	return s.ch
}

// Bus is a simple in-process pub/sub message bus with topic prefix matching.
type Bus struct {
	mu     sync.RWMutex
	subs   map[int]*Subscription
	nextID int
}

// New creates a new Bus.
func New() *Bus {
	return &Bus{
		subs: make(map[int]*Subscription),
	}
}

// Subscribe creates a subscription for events matching the given topic prefix.
// An empty prefix matches all topics.
// The returned channel has a buffer of 100 events; slow consumers will miss events
// (non-blocking send).
func (b *Bus) Subscribe(topicPrefix string) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	sub := &Subscription{
		id:     b.nextID,
		prefix: topicPrefix,
		ch:     make(chan Event, defaultBufferSize),
	}
	b.subs[sub.id] = sub
	return sub
}

// Unsubscribe removes a subscription and closes its channel.
func (b *Bus) Unsubscribe(sub *Subscription) {
	if sub == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subs[sub.id]; ok {
		delete(b.subs, sub.id)
		close(sub.ch)
	}
}

// Publish sends an event to all matching subscribers.
// Delivery is non-blocking: if a subscriber's buffer is full, the event is dropped.
func (b *Bus) Publish(topic string, payload interface{}) {
	event := Event{
		Topic:   topic,
		Payload: payload,
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subs {
		if sub.prefix == "" || strings.HasPrefix(topic, sub.prefix) {
			// Non-blocking send.
			select {
			case sub.ch <- event:
			default:
				// Buffer full, drop event for this subscriber.
			}
		}
	}
}

// SubscriberCount returns the number of active subscriptions.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
