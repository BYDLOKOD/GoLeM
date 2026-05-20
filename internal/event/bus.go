package event

import (
	"sync"
	"time"
)

// defaultChanBufSize is the buffer capacity for subscriber channels.
const defaultChanBufSize = 64

// Bus distributes events to subscribers via typed channels.
// A nil *Bus is safe -- Publish and Subscribe are no-ops.
type Bus struct {
	mu          sync.RWMutex
	subs        []*subscriber
	chanBufSize int
	closed      bool
}

// NewBus creates a new event bus. The returned bus is ready to use immediately.
func NewBus() *Bus {
	return &Bus{
		chanBufSize: defaultChanBufSize,
	}
}

// Subscribe returns a channel that receives events matching the given filter.
// An empty filter receives all events. The channel has a bounded buffer;
// slow subscribers that don't drain the channel will have events dropped.
//
// A nil *Bus returns nil.
func (b *Bus) Subscribe(filter ...EventType) <-chan Event {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		// Return a closed channel so that ranging over it exits immediately.
		ch := make(chan Event)
		close(ch)
		return ch
	}

	ch := make(chan Event, b.chanBufSize)

	s := &subscriber{ch: ch}
	if len(filter) > 0 {
		s.filter = make(map[EventType]bool, len(filter))
		for _, ft := range filter {
			s.filter[ft] = true
		}
	}

	b.subs = append(b.subs, s)
	return ch
}

// Publish sends an event to every matching subscriber.
// Slow subscribers whose channel buffer is full will have the event silently
// dropped (non-blocking send) to avoid back-pressure on producers.
//
// A nil *Bus is a no-op.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}

	// Set timestamp if not already set by the producer.
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, s := range b.subs {
		if s.matches(e) {
			select {
			case s.ch <- e:
				// Sent successfully.
			default:
				// Channel full -- drop event for this subscriber.
			}
		}
	}
}

// Close drains and closes all subscriber channels. After Close, Publish is a
// no-op and Subscribe returns a closed channel.
// Close is idempotent: calling it multiple times is safe.
//
// A nil *Bus is a no-op.
func (b *Bus) Close() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true

	for _, s := range b.subs {
		close(s.ch)
	}
	b.subs = nil
}
