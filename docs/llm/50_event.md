---
id: event
kind: spec
touches: internal/event/
---

# Event Bus - Pub/Sub for Job Lifecycle Events

See also: [21_job_lifecycle.md](21_job_lifecycle.md) · [22_slot.md](22_slot.md).

## Design

`internal/event` provides a lightweight publish/subscribe bus. A nil `*Bus`
is safe to use throughout - all methods (`Publish`, `Subscribe`) are no-ops
on a nil receiver. This means callers do not need to guard against an unset
bus.

## Event types

```go
// internal/event/event.go
const (
    JobQueued    // job entered the queue
    JobRunning   // subagent subprocess started
    JobProgress  // progress update (reserved; not currently emitted)
    ToolUse      // a tool was invoked (reserved; not currently emitted)
    JobDone      // subagent finished successfully
    JobFailed    // subagent exited with error
    JobTimeout   // subagent exceeded its time limit
    JobKilled    // subagent was killed by the user
    SlotAcquired // a concurrency slot was claimed
    SlotReleased // a concurrency slot was released
)
```

`JobProgress`, `ToolUse`, and `JobKilled` are defined but not currently emitted
by any producer.

## Event struct

```go
type Event struct {
    Type      EventType
    JobID     string
    Timestamp time.Time
    Data      any // payload varies by EventType
}
```

`Data` payloads by type:

| EventType | Data keys |
|-----------|-----------|
| `JobQueued` | `project_id`, `job_id` |
| `JobRunning` | `model`, `workdir`, `project_id` |
| `JobDone` | `exit_code`, `project_id` |
| `JobFailed` | `exit_code`, `stderr` (truncated to 500 chars), `project_id` |
| `JobTimeout` | `project_id` |
| `SlotAcquired` | `current_count`, `max_parallel` |
| `SlotReleased` | `current_count`, `max_parallel` |

## Bus API

`NewBus() *Bus` - creates a ready bus with a subscriber channel buffer of 64
events (`defaultChanBufSize`).

`Bus.Subscribe(filter ...EventType) <-chan Event` - returns a buffered
channel. An empty filter receives all event types. Slow subscribers whose
channel is full have events silently dropped (non-blocking send).

`Bus.Publish(e Event)` - delivers to all matching subscribers. Sets
`e.Timestamp` if zero. No-op on closed or nil bus.

`Bus.Close()` - closes all subscriber channels and sets `closed = true`.
Subsequent `Publish` calls are no-ops. Idempotent. Ranging over a closed
subscriber channel exits immediately.

## Helper

`event.Publish(bus *Bus, e Event)` - nil-safe top-level function used by all
producers. Equivalent to `bus.Publish(e)` with a nil guard.

## Current wiring

| Producer | Events emitted |
|----------|---------------|
| `claude.Execute` | `JobRunning`, `JobDone`, `JobFailed`, `JobTimeout` |
| `job.Job.EmitQueued` | `JobQueued` |
| `slot.SlotManager.WaitForSlot` | `SlotAcquired` |
| `slot.SlotManager.ReleaseSlot` | `SlotReleased` |

No consumers are wired in the current CLI path. The MCP server has a
`// TODO` to connect the bus for progress notifications (`cmd/glm/main.go:1181`).
