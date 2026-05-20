// Package event provides a lightweight publish/subscribe event bus for GoLeM
// subsystems. Producers emit typed events; consumers subscribe to the subset
// they care about. A nil *Bus is safe to use -- all methods are no-ops.
package event

import "time"

// EventType enumerates lifecycle events produced by GoLeM subsystems.
type EventType int

const (
	JobQueued    EventType = iota // job entered the queue
	JobRunning                    // subagent started executing
	JobProgress                   // progress update (e.g. tool call completed)
	ToolUse                       // a tool was invoked by the subagent
	JobDone                       // subagent finished successfully
	JobFailed                     // subagent exited with error
	JobTimeout                    // subagent exceeded its time limit
	JobKilled                     // subagent was killed by the user
	SlotAcquired                  // a concurrency slot was claimed
	SlotReleased                  // a concurrency slot was released
)

// String returns a human-readable name for the event type.
func (et EventType) String() string {
	switch et {
	case JobQueued:
		return "JobQueued"
	case JobRunning:
		return "JobRunning"
	case JobProgress:
		return "JobProgress"
	case ToolUse:
		return "ToolUse"
	case JobDone:
		return "JobDone"
	case JobFailed:
		return "JobFailed"
	case JobTimeout:
		return "JobTimeout"
	case JobKilled:
		return "JobKilled"
	case SlotAcquired:
		return "SlotAcquired"
	case SlotReleased:
		return "SlotReleased"
	default:
		return "Unknown"
	}
}

// Event is the unit of information flowing through the bus.
type Event struct {
	Type      EventType
	JobID     string
	Timestamp time.Time
	Data      any // payload varies by EventType
}

// subscriber represents a subscriber with an optional type filter.
type subscriber struct {
	ch     chan Event
	filter map[EventType]bool // nil means "all events"
}

// matches reports whether e matches the subscriber's filter.
func (s *subscriber) matches(e Event) bool {
	if s.filter == nil {
		return true
	}
	return s.filter[e.Type]
}
