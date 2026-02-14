package bus

import (
	"sync"
	"testing"
	"time"
)

func TestBus_PublishSubscribe(t *testing.T) {
	b := New()
	sub := b.Subscribe("test")
	defer b.Unsubscribe(sub)

	b.Publish("test.event", "hello")

	select {
	case event := <-sub.Ch():
		if event.Topic != "test.event" {
			t.Fatalf("topic = %q, want %q", event.Topic, "test.event")
		}
		if event.Payload != "hello" {
			t.Fatalf("payload = %v, want %q", event.Payload, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestBus_PrefixMatching(t *testing.T) {
	b := New()

	// Subscribe to "task." prefix.
	taskSub := b.Subscribe("task.")
	defer b.Unsubscribe(taskSub)

	// Subscribe to all events.
	allSub := b.Subscribe("")
	defer b.Unsubscribe(allSub)

	b.Publish("task.created", "new task")
	b.Publish("system.status", "ok")

	// taskSub should receive task.created but not system.status.
	select {
	case event := <-taskSub.Ch():
		if event.Topic != "task.created" {
			t.Fatalf("topic = %q, want task.created", event.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for task event")
	}

	// taskSub should not have system.status.
	select {
	case event := <-taskSub.Ch():
		t.Fatalf("unexpected event on taskSub: %v", event)
	case <-time.After(50 * time.Millisecond):
		// Expected: no more events.
	}

	// allSub should receive both.
	received := 0
	for i := 0; i < 2; i++ {
		select {
		case <-allSub.Ch():
			received++
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for all event")
		}
	}
	if received != 2 {
		t.Fatalf("allSub received %d events, want 2", received)
	}
}

func TestBus_NonBlocking(t *testing.T) {
	b := New()
	sub := b.Subscribe("test")
	defer b.Unsubscribe(sub)

	// Fill the buffer.
	for i := 0; i < defaultBufferSize+10; i++ {
		b.Publish("test.event", i)
	}

	// Should not deadlock. Drain what we can.
	count := 0
	for {
		select {
		case <-sub.Ch():
			count++
		default:
			goto done
		}
	}
done:
	if count != defaultBufferSize {
		t.Fatalf("received %d events, expected %d (buffer size)", count, defaultBufferSize)
	}
}

func TestBus_Unsubscribe(t *testing.T) {
	b := New()
	sub := b.Subscribe("test")

	if b.SubscriberCount() != 1 {
		t.Fatalf("count = %d, want 1", b.SubscriberCount())
	}

	b.Unsubscribe(sub)

	if b.SubscriberCount() != 0 {
		t.Fatalf("count = %d, want 0", b.SubscriberCount())
	}

	// Channel should be closed.
	_, ok := <-sub.Ch()
	if ok {
		t.Fatal("expected closed channel")
	}
}

func TestBus_MultipleSubscribers(t *testing.T) {
	b := New()
	sub1 := b.Subscribe("test")
	sub2 := b.Subscribe("test")
	defer b.Unsubscribe(sub1)
	defer b.Unsubscribe(sub2)

	b.Publish("test.event", "shared")

	for _, sub := range []*Subscription{sub1, sub2} {
		select {
		case event := <-sub.Ch():
			if event.Payload != "shared" {
				t.Fatalf("payload = %v, want shared", event.Payload)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}

func TestBus_ConcurrentPublish(t *testing.T) {
	b := New()
	sub := b.Subscribe("")
	defer b.Unsubscribe(sub)

	const goroutines = 10
	const perGoroutine = 5
	total := goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				b.Publish("concurrent", id*100+i)
			}
		}(g)
	}
	wg.Wait()

	received := 0
	for {
		select {
		case <-sub.Ch():
			received++
		default:
			goto done2
		}
	}
done2:
	if received != total {
		t.Fatalf("received %d events, want %d", received, total)
	}
}
