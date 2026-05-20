---
id: handoff
kind: guide
---

# Handoff - GoLeM v2.0.0

See also: [00_index.md](00_index.md).

## Current state

Branch: `main`. Last merge: PR #1 (Arsolitt/main), commit `a9dbccd`.

All 17 test packages pass. No test failures. Full run takes ~60 s because
`internal/claude`, `internal/cmd`, and `internal/mcp/tools` actually invoke
the `claude` binary where not guarded by `-short`.

```
go test ./...   # all 17 packages pass
```

Binary version constant: `version = "2.0.0"` (`cmd/glm/main.go:31`).

## What exists

The merge added six new packages on top of the pre-merge baseline:

| Package | Purpose |
|---------|---------|
| `internal/event` | Pub/sub event bus |
| `internal/artifact` | Typed artifact persistence (text/JSON/file_ref) |
| `internal/slot/notify.go` | Unix socket wakeup for slot waiters |
| `internal/retry` | Exponential backoff with jitter |
| `internal/router` | Prompt complexity estimation (light/medium/heavy) |
| `internal/prompt` | Constraint expansion + system prompt assembly |
| `internal/dag` | DAG pipeline with scheduler, gate steps, retry |
| `internal/mcp` | JSON-RPC 2.0 MCP server + stdio transport |
| `internal/mcp/tools` | Eight MCP tool handlers |
| `internal/proxy` | Per-model registry + retry (extended from pre-merge) |

Key behavioral change: global `api_rps` / `max_parallel` config key is
silently ignored (backward compat). Per-model concurrency is now configured
via `[models]` TOML section.

## What does NOT exist

- Event bus is wired into `internal/claude` and `internal/job`, but is NOT
  connected to the MCP server (there is a `// TODO` at `main.go:1182`).
  MCP progress notifications (`glm_start` progress events) are not emitted.
- No HTTP-based API - only CLI and MCP (stdio).
- No Windows support (flock, Unix sockets, `/proc` references).
- No TOML format other than hand-rolled parser - no TOML arrays.
- Provider multi-provider support exists in `internal/config/provider.go` but
  is not exercised by any current command (all commands use `config.Load`
  which reads only the Z.AI defaults path).

## Read order for a new session

1. This file.
2. [10_cli.md](10_cli.md) - command dispatch, flags, exit codes.
3. [21_job_lifecycle.md](21_job_lifecycle.md) - job FSM (most referenced).
4. [23_proxy.md](23_proxy.md) - if touching proxy or rate-limiting.
5. [40_dag.md](40_dag.md) - if touching pipeline execution.
6. [30_mcp.md](30_mcp.md) + [31_mcp_tools.md](31_mcp_tools.md) - if touching MCP.

## Smoke test

```bash
cd /home/veschin/work/GoLeM
go build -o /tmp/glm ./cmd/glm/     # should produce a binary, no errors
go test ./... -short                 # all 17 packages pass, no real claude calls
go vet ./...                         # no issues
bash docs/llm/validate.sh           # OK: N links valid
```

Expected output for `go test ./... -short`:

```
ok  github.com/veschin/GoLeM/internal/artifact
ok  github.com/veschin/GoLeM/internal/claude
ok  github.com/veschin/GoLeM/internal/cmd
ok  github.com/veschin/GoLeM/internal/config
ok  github.com/veschin/GoLeM/internal/dag
ok  github.com/veschin/GoLeM/internal/event
ok  github.com/veschin/GoLeM/internal/exitcode
ok  github.com/veschin/GoLeM/internal/job
ok  github.com/veschin/GoLeM/internal/log
ok  github.com/veschin/GoLeM/internal/mcp
ok  github.com/veschin/GoLeM/internal/mcp/tools
ok  github.com/veschin/GoLeM/internal/prompt
ok  github.com/veschin/GoLeM/internal/proxy
ok  github.com/veschin/GoLeM/internal/retry
ok  github.com/veschin/GoLeM/internal/router
ok  github.com/veschin/GoLeM/internal/slot
ok  github.com/veschin/GoLeM/internal/validation
```

## Next options

**Path A (~1h): wire event bus into MCP progress notifications.**
The `// TODO` at `cmd/glm/main.go:1182` points to this gap. Subscribe to
`event.JobRunning`/`event.JobDone` in `cmdMCP` and emit JSON-RPC
notifications via `transport.WriteNotification`. Needs a new notification
method name agreed with the MCP client.

**Path B (~2h): add `update` command test coverage.**
`internal/cmd/` has no test for `UpdateCmd`. The command shells out to `git`
and `go build`; tests would need a fake binary injection pattern like
`DoctorCmd` uses.

**Path C (~30m): extend `[providers.*]` support to CLI commands.**
`LoadProvider` exists but is unused at the CLI level. `cmdSession` and
`cmdRun` both call `config.Load`, which ignores `[providers.*]`. Wiring it
would let users switch between multiple API backends via config.

## Agent error to log

None this session (documentation bootstrap, no code changed).
