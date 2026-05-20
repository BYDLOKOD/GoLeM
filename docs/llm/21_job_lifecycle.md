---
id: job-lifecycle
kind: spec
touches: internal/job/
---

# Job Lifecycle - Directory Layout, Status FSM, Atomic Writes

See also: [20_claude_execution.md](20_claude_execution.md) · [22_slot.md](22_slot.md) · [10_cli.md](10_cli.md).

## Directory layout

Jobs live under `~/.claude/subagents/<project-id>/<job-id>/`.

```
~/.claude/subagents/
  <project-id>/
    <job-id>/
      status              # current status string (atomic write)
      status.lock         # flock file for status transitions
      prompt.txt          # the prompt sent to claude
      workdir.txt         # working directory used
      permission_mode.txt # permission mode used
      model.txt           # "opus=X sonnet=Y haiku=Z"
      started_at.txt      # RFC3339 UTC timestamp
      finished_at.txt     # RFC3339 UTC timestamp
      pid.txt             # PID of the glm process that owns the job
      raw.json            # raw stdout from claude --output-format json
      stdout.txt          # parsed .result from raw.json
      stderr.txt          # raw stderr from claude subprocess
      changelog.txt       # file-change summary from tool_use events
      exit_code.txt       # decimal exit code (absent when 0)
      created_at.txt      # RFC3339 UTC timestamp (for stale queue detection)
```

The `created_at.txt` file is read by `IsStaleQueued` (reconciliation) to
detect jobs stuck in `queued` state for more than 5 minutes. It is written
externally (not by the `internal/job` package itself).

## Project ID

`job.ResolveProjectID(absPath) string` produces `{basename}-{crc32}` where
`crc32` is the CRC32 IEEE checksum of the full absolute path as a decimal
integer. This groups all jobs for the same directory without collisions across
differently named projects at different paths.

Example: `/home/user/myproject` -> `myproject-3842719234`.

## Job ID

`job.GenerateJobID() string` produces `job-YYYYMMDD-HHMMSS-XXXXXXXX` where
`XXXXXXXX` is 4 random bytes from `crypto/rand` encoded as lowercase hex.

`ValidateJobID(id)` accepts only lowercase alphanumeric, `-`, and `_`. Empty
IDs and IDs containing path separators or dots return `err:validation`.

## Status FSM

States and allowed transitions:

```
queued -> running -> done
                 -> failed
                 -> timeout
                 -> killed
                 -> permission_error
```

Terminal states (`done`, `failed`, `timeout`, `killed`, `permission_error`)
have no outgoing transitions. Attempting an invalid transition returns an
error from `StatusTransition`.

`ReadStatus(dir)` returns `StatusFailed` for any missing or unrecognized
status file.

## Atomic writes

`AtomicWrite(path, data)` writes data to `path.tmp.<pid>` then calls
`os.Rename` to the target. Readers never observe a partial write. Used for
the `status` file and the slot counter.

`StatusTransition(newStatus)` protects the read-check-write sequence with an
exclusive `syscall.Flock` on `status.lock` to prevent TOCTOU races.

`SetStatus(newStatus)` calls `AtomicWrite` directly without locking (used
for the initial `queued` write and trusted internal callers).

## Reconciliation

`job.Reconcile(subagentsDir, now)` is called once at process startup.

It scans all job directories (both flat `subagentsDir/<job-id>` layout and
project-scoped `subagentsDir/<project-id>/<job-id>` layout), and for each:

- `running` + dead PID -> status set to `failed`, stderr appended
  `[GoLeM] Process died unexpectedly (PID N)` + `__stale_recovered__` marker.
- `queued` + older than 5 minutes (`staleQueueThreshold`) -> status set to
  `failed`, stderr appended `[GoLeM] Job stuck in queue for over 5 minutes`
  + marker.
- `running` + live PID -> counted as alive.

After scanning, writes the alive count to the `.running_count` slot counter
file. The whole operation is protected by an exclusive flock on
`subagentsDir/.reconcile.lock`.

`CleanStale(subagentsDir)` removes all job directories whose `stderr.txt`
contains the `__stale_recovered__` marker. Manually killed or otherwise
failed jobs are left intact.

## FindJobDir search order

`FindJobDir(subagentsRoot, currentProjectID, jobID)` searches in order:

1. `subagentsRoot/<currentProjectID>/<jobID>` (current project scope).
2. `subagentsRoot/<jobID>` (legacy flat layout).
3. `subagentsRoot/*/<jobID>` (any other project directory).

Returns `ErrNotFound` (`err:not_found`) if not found. Validates `jobID`
first via `ValidateJobID`.

## Event integration

`Job.SetBus(bus)` wires an `*event.Bus`. `Job.EmitQueued()` publishes
`event.JobQueued` with `project_id` and `job_id` in the data map.
`claude.Execute` publishes `JobRunning`, `JobDone`, `JobFailed`,
`JobTimeout`. If bus is nil, all publishes are no-ops.
