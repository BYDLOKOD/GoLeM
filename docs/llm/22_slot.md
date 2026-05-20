---
id: slot
kind: spec
touches: internal/slot/
---

# Slot - Concurrency Control and Unix Socket Wakeups

See also: [21_job_lifecycle.md](21_job_lifecycle.md) · [10_cli.md](10_cli.md).

## Purpose

`SlotManager` tracks how many subagent jobs are running concurrently. When
`maxParallel == 0` (unlimited) the slot is claimed immediately. When
`maxParallel > 0` callers block in `WaitForSlot` until the running count
drops below the limit.

In practice GoLeM currently always passes `0` (unlimited) to
`NewSlotManager`. Rate-limiting is delegated entirely to the proxy layer
(per-model semaphores). The slot manager still runs to maintain the counter
file for reconciliation and to publish `SlotAcquired`/`SlotReleased` events.

## Files on disk

Both files live in `subagentsDir` (default `~/.claude/subagents/`):

| File | Purpose |
|------|---------|
| `.running_count` | Integer count of running jobs (atomic write on every claim/release). |
| `.counter.lock` | Exclusive flock target for counter mutations. |
| `.slot.sock` | Unix domain socket for instant wakeup notification. |

## Counter invariants

1. Counter is never negative (clamped at 0 in `ReleaseSlot`).
2. `Init()` resets to 0 if the file contains non-integer content.
3. All counter reads and writes happen inside `withLock`, which holds an
   exclusive flock on `.counter.lock`.

## Locking strategy

`withLock(fn)` tries `syscall.Flock(LOCK_EX)` first. If flock is unavailable
(forced via `LOCK_FALLBACK=true` env var), falls back to mkdir-based locking:

- Acquires lock by `os.Mkdir(lockfile + ".d")`.
- Stale locks (older than `StaleLockSeconds = 60` s) are removed and
  retried.
- Releases by `os.Remove(lockfile + ".d")`.

## WaitForSlot

When `maxParallel == 0`: calls `ClaimSlot()` immediately, publishes
`SlotAcquired`, returns.

When `maxParallel > 0`: loops:
1. Under lock, checks counter < maxParallel. If yes, increments and returns.
2. If no slot: waits via `SlotNotifier.Wait(ctx)` with a 30 s timeout,
   then retries.

The 30 s timeout is currently hardcoded (noted as TODO in `slot.go:317`).

## SlotNotifier (Unix socket wakeup)

`SlotNotifier` eliminates the original 2-second polling loop with instant
wakeups via a Unix domain socket at `.slot.sock`.

`Start()`:
- Removes stale socket file if present.
- Calls `net.ListenUnix("unix", addr)`.
- Launches `acceptLoop` goroutine to accept connections.

`Notify()`:
- Writes byte `0x01` to every connected client.
- Closes each connection after writing (single-use connections).
- No-op if not started.

`Wait(ctx)`:
- Connects to the socket.
- Blocks reading one byte.
- Returns on notification, context cancellation, or connection close.
- Falls back to `time.Sleep(PollInterval)` if the socket is unavailable.

`Stop()`:
- Closes all client connections and the listener.
- Removes the socket file.
- Idempotent.

`ReleaseSlot` calls `sm.notifier.Notify()` after decrementing the counter.

## Event integration

`SlotManager.SetBus(bus)` wires an `*event.Bus`.

- `WaitForSlot` publishes `event.SlotAcquired` with `current_count` and
  `max_parallel` in the data map.
- `ReleaseSlot` publishes `event.SlotReleased` with the same fields.

Both are no-ops when `bus` is nil.

## Process liveness

`IsProcessAlive(pid)` sends `signal 0` to the process. Returns true if the
process exists and is not in zombie state (`/proc/<pid>/stat` state field
`Z`). EPERM (process exists, no permission to signal) is treated as alive.

`TerminateProcessGroup(pid)` sends SIGTERM to `-pid` (process group), waits
1 s, then sends SIGKILL.
