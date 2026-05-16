package event

import (
	"sync"
	"testing"
	"time"
)

func TestSubscribeReceivesPublishedEvents(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch := bus.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})

	select {
	case e := <-ch:
		if e.Type != JobQueued {
			t.Errorf("expected JobQueued, got %d", e.Type)
		}
		if e.JobID != "job-1" {
			t.Errorf("expected job-1, got %s", e.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribeWithFilter(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	// Subscribe to only JobDone events.
	ch := bus.Subscribe(JobDone)

	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})
	bus.Publish(Event{Type: JobDone, JobID: "job-2"})
	bus.Publish(Event{Type: JobFailed, JobID: "job-3"})

	select {
	case e := <-ch:
		if e.Type != JobDone {
			t.Errorf("expected JobDone, got %d", e.Type)
		}
		if e.JobID != "job-2" {
			t.Errorf("expected job-2, got %s", e.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered event")
	}

	// The channel should be empty -- no more JobDone events were published.
	select {
	case e := <-ch:
		t.Fatalf("unexpected event received: type=%d jobID=%s", e.Type, e.JobID)
	default:
		// Expected: no more events.
	}
}

func TestSubscribeWithMultipleFilterTypes(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch := bus.Subscribe(JobDone, JobFailed)

	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})
	bus.Publish(Event{Type: JobDone, JobID: "job-2"})
	bus.Publish(Event{Type: JobFailed, JobID: "job-3"})

	received := collectEvents(t, ch, 2)
	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
	types := map[EventType]bool{}
	for _, e := range received {
		types[e.Type] = true
	}
	if !types[JobDone] || !types[JobFailed] {
		t.Errorf("expected JobDone and JobFailed, got %v", types)
	}
}

func TestPublishToMultipleSubscribers(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()

	bus.Publish(Event{Type: JobRunning, JobID: "job-1"})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.JobID != "job-1" {
				t.Errorf("subscriber %d: expected job-1, got %s", i, e.JobID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestSlowSubscriberDropped(t *testing.T) {
	bus := NewBus()
	// Use a very small buffer to make the test deterministic.
	bus.chanBufSize = 1
	defer bus.Close()

	ch := bus.Subscribe()

	// Fill the channel buffer. The subscriber is "slow" because it doesn't drain.
	bus.Publish(Event{Type: JobQueued, JobID: "fill-1"}) // occupies buffer

	// This publish should be dropped because the buffer is full.
	bus.Publish(Event{Type: JobQueued, JobID: "dropped"})

	// Only one event should be in the channel.
	select {
	case e := <-ch:
		if e.JobID != "fill-1" {
			t.Errorf("expected fill-1, got %s", e.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event")
	}

	// Second event should not be in the channel.
	select {
	case e := <-ch:
		t.Fatalf("unexpected event (should have been dropped): %v", e)
	default:
		// Expected: dropped.
	}
}

func TestCloseClosesAllChannels(t *testing.T) {
	bus := NewBus()

	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()

	bus.Close()

	// Both channels should be closed after Close.
	for i, ch := range []<-chan Event{ch1, ch2} {
		_, ok := <-ch
		if ok {
			t.Errorf("subscriber %d: channel should be closed", i)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	bus := NewBus()

	bus.Subscribe()

	// Calling Close multiple times must not panic.
	bus.Close()
	bus.Close()
	bus.Close()
}

func TestPublishAfterCloseIsNoOp(t *testing.T) {
	bus := NewBus()
	bus.Subscribe()
	bus.Close()

	// Publish after close must not panic and must not send to closed channels.
	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})
}

func TestNilBusSafety(t *testing.T) {
	var bus *Bus // nil

	// Subscribe on nil bus must return nil channel (not panic).
	ch := bus.Subscribe()
	if ch != nil {
		t.Fatal("expected nil channel from nil Bus.Subscribe")
	}

	// Publish on nil bus must not panic.
	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})

	// Close on nil bus must not panic.
	bus.Close()
}

func TestEventTimestamp(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch := bus.Subscribe()

	before := time.Now()
	bus.Publish(Event{Type: JobQueued, JobID: "job-1"})

	select {
	case e := <-ch:
		if e.Timestamp.Before(before) {
			t.Error("event timestamp is before publish time")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestConcurrentPublish(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch := bus.Subscribe()

	const goroutines = 10
	const eventsPer = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range eventsPer {
				bus.Publish(Event{Type: JobProgress, JobID: "job-1", Data: id*eventsPer + j})
			}
		}(i)
	}
	wg.Wait()

	// Give the channel time to drain.
	time.Sleep(100 * time.Millisecond)

	// Drain remaining events.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:

	if count == 0 {
		t.Fatal("no events received from concurrent publishes")
	}
	// We can't assert exact count because some may be dropped,
	// but we should receive most of them.
	t.Logf("received %d of %d events", count, goroutines*eventsPer)
}

func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	bus := NewBus()
	bus.Close()

	ch := bus.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe after Close should return a non-nil closed channel")
	}

	// A closed channel should return the zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out -- channel should be closed and readable")
	}
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{JobQueued, "JobQueued"},
		{JobRunning, "JobRunning"},
		{JobProgress, "JobProgress"},
		{ToolUse, "ToolUse"},
		{JobDone, "JobDone"},
		{JobFailed, "JobFailed"},
		{JobTimeout, "JobTimeout"},
		{JobKilled, "JobKilled"},
		{SlotAcquired, "SlotAcquired"},
		{SlotReleased, "SlotReleased"},
		{EventType(999), "Unknown"},
	}

	for _, tt := range tests {
		got := tt.et.String()
		if got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.et, got, tt.want)
		}
	}
}

// collectEvents reads up to n events from ch with a timeout.
func collectEvents(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	var events []Event
	timeout := time.After(time.Second)
	for len(events) < n {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, e)
		case <-timeout:
			return events
		}
	}
	return events
}
