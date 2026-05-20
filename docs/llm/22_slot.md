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

## Job types

The slot package defines its own `JobStatus` type and `Job` struct
(`slot.go:31-47`), separate from `job.Job` in `internal/job/`. The slot-level
`Job` holds reconciliation metadata (`JobID`, `Status`, `PID`, `HasPID`,
`Stderr`) and is consumed by `SlotManager.Reconcile`. The `job.Job` struct
holds lifecycle and filesystem state (`ID`, `ProjectID`, `Dir`, `Bus`).

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

The 30 s timeout is currently hardcoded (noted as TODO in `slot.go:316`).

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
`Z`). EPERM also triggers a zombie check via `/proc/<pid>/stat` - a zombie
with EPERM returns false.

`TerminateProcessGroup(pid)` sends SIGTERM to `-pid` (process group), waits
1 s, then sends SIGKILL.

## SlotManager.Reconcile

`SlotManager.Reconcile(jobs []*Job) error` accepts a slice of slot-level
`Job` values, counts those that are running with a live PID, marks dead ones
as failed (appending a death message to their `Stderr` field), and resets the
counter file to the alive count under lock (`slot.go:328`). This is the
slot-package counterpart to `job.Reconcile`.

## Facade methods

- `StartNotifier() error` delegates to `sm.notifier.Start()` (`slot.go:75`).
- `StopNotifier()` delegates to `sm.notifier.Stop()` (`slot.go:80`).

These let callers manage the Unix socket notifier through the `SlotManager`
without holding a direct reference to `SlotNotifier`.

## Path helpers

- `CounterPath() string` returns `filepath.Join(sm.dir, CounterFile)` (`slot.go:85`).
- `LockPath() string` returns `filepath.Join(sm.dir, LockFile)` (`slot.go:90`).

## Zombie detection via /proc

`isZombieViaProc(pid int) bool` reads `/proc/<pid>/stat`, parses the state
field (the character after the closing `)` of the comm field), and returns
`true` when the state is `Z` (`slot.go:354`). Returns `false` when `/proc` is
not available. This is used by `IsProcessAlive` to distinguish zombie
processes from live ones.

## Atomic write strategy

The slot package uses `os.CreateTemp(dir, ".counter-tmp-*")` with a random
suffix for atomic counter writes (`slot.go:146-169`), then renames to the
target. This differs from `job.AtomicWrite` which uses a pid-based temporary
name (`path + ".tmp." + pid`). The random suffix avoids collisions when
multiple processes write to the same counter concurrently.
