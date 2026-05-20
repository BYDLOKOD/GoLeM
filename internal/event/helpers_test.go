package event

import (
	"testing"
	"time"
)

func TestPublishHelper_NilBus(t *testing.T) {
	// Must not panic when bus is nil.
	Publish(nil, Event{
		Type:      JobQueued,
		Timestamp: time.Now(),
	})
}

func TestPublishHelper_ActiveBus(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ch := bus.Subscribe(JobQueued)
	Publish(bus, Event{
		Type:      JobQueued,
		JobID:     "test-job",
		Timestamp: time.Now(),
		Data:      map[string]any{"key": "val"},
	})

	select {
	case e := <-ch:
		if e.Type != JobQueued {
			t.Errorf("type = %v, want %v", e.Type, JobQueued)
		}
		if e.JobID != "test-job" {
			t.Errorf("job_id = %v, want test-job", e.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}
