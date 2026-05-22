---
id: handoff
kind: guide
---

# Handoff - correctness fix session shipped as v1.5.1

See also: [00_index.md](00_index.md) · [23_proxy.md](23_proxy.md) · [11_config.md](11_config.md).

## Current state

Branch: `main`. Version `version = "1.5.1"` (`cmd/glm/main.go:32`), tagged and
released.

This session shipped **nine correctness fixes**, each TDD (failing test first,
then fix), verified together:

```
go build ./...        # exit 0
go vet ./...           # clean
go test ./...          # all 18 packages ok
go test -race ./...    # exit 0, no data races
```

Two critic rounds (opus on the first five fixes, sonnet on the last three)
reviewed the full diff against executing code: all correct, no regressions.

| Fix | File | What was wrong |
|-----|------|----------------|
| 1 | `internal/cmd/json.go` | `status --json` reported a dead "running" job as running forever (reconcile gated on a `Contains(jobID,"dead")` test marker). Now checks PID liveness. |
| 2 | `internal/cmd/clean.go` | `clean` only walked the flat layout, so project-scoped jobs (the production layout) were never removed. Now walks both and prunes emptied project dirs. |
| 3 | `internal/job/reconcile.go` | One malformed job aborted the whole reconciliation sweep. Now logs per-job and continues. |
| 4 | `internal/dag/scheduler.go` + `executor.go` | Goroutine leak on context cancellation (completion buffer undersized); a ruleless gate silently passed everything. Both fixed. |
| 5 | `internal/proxy/lifecycle.go` | `proxy_port` ignored (`--port 0` hard-coded) - the stale-port `ConnectionRefused` root cause. Now honored end-to-end. |
| 6 | `internal/cmd/doctor.go` | `doctor` reported a healthy API as unreachable (only 200 = OK). Any HTTP response now counts as reachable. |
| 7 | `internal/config/config.go` | TOML quote stripping corrupted values with interior quotes. Now strips only a matched pair. |
| 8 | `internal/cmd/execute.go` | Final status written non-atomically (partial-read race for `status --json`). Now atomic. |

(Fix 4 bundles the two `internal/dag` fixes; fix 8 is the atomic-write
hardening.) All docs were refreshed to match: the specs above, the new
`90_lessons/01_claude_output_array.md`, and this session's
`devlog/02_devlog_fix_session.md`. The owner's in-flight edits to
`10_cli`/`11_config`/`20_claude_execution` were integrated.

## Main finding: proxy stale-port bug (root cause now fixed - see fix 6)

> Resolved this session: `proxy_port` is now honored, so a fixed port survives
> daemon restarts and a long-lived MCP session's cached URL stays valid. The
> diagnosis below is retained for context; the only remaining gap is the
> *default* `proxy_port = 0` case, addressed by Path A under "Next".

Symptom: a `glm_run` (or any MCP-driven job) fails immediately with
`API Error: Unable to connect to API (ConnectionRefused)`, and no job
directory is created. The message text is **not** in the GoLeM source - it is
printed by the `claude` CLI when it cannot reach `ANTHROPIC_BASE_URL`.

Root cause is three facts in the code combining:

1. `ensureProxy` runs **once at `glm mcp` startup** (`cmd/glm/main.go:1273`)
   and freezes the port into process memory:
   `cfg.ZaiBaseURL = fmt.Sprintf("http://localhost:%d", proxyPort)`
   (`main.go:1263`). The job path never re-resolves it - the only
   `proxy.EnsureRunning` caller in the whole tree is `main.go:1258`. Every job
   in that MCP session reuses the frozen URL (`internal/cmd/execute.go:221`,
   `internal/dag/executor.go:112` read `cfg.ZaiBaseURL`).
2. The proxy daemon always starts with `--port 0` (hard-coded at
   `internal/proxy/lifecycle.go:68`, ignoring `cfg.ProxyPort`), so it binds a
   **new random port on every start**.
3. The daemon shuts itself down after idle timeout (`--idle-timeout 600`).

Together: a long-lived `glm mcp` process holds the port of a proxy that has
since died on idle timeout; the replacement daemon binds a different port the
MCP process never learns. The first job after an idle gap hits the dead port
and gets `ConnectionRefused`.

The dead port for the failing job is **inferred from process/port timing**, not
observed - no job directory was created, so the dialed address was not captured.
The timing proof (MCP PID start vs. proxy port-lifetime windows) is in
[devlog/01_devlog_proxy_stale_port.md](devlog/01_devlog_proxy_stale_port.md).

Secondary bug surfaced: `proxy_port` in `glm.toml` has **no effect** -
`EnsureRunning` hard-codes `--port 0` (`lifecycle.go:68`); `cmdProxy` reads
`cfg.ProxyPort` (`main.go:1138`) only as a default that `--port 0` overrides.

## Next

- **Path B - honor `proxy_port` - DONE this session (fix 6).** `EnsureRunning`
  takes a `port` arg, `ensureProxy` passes `cfg.ProxyPort`, and the new
  `proxyDaemonArgs` helper builds the launch args. A fixed `proxy_port` now
  survives daemon restarts.
- **Path A - re-resolve proxy per job (~1-2h, still open).** For the default
  `proxy_port = 0` the URL is still resolved once at MCP startup and never
  re-resolved, so a daemon that dies mid-session strands the cached URL. Move
  the liveness check onto the job path: in the job handler (`internal/mcp/tools`
  run/start/chain -> `internal/cmd/execute.go`) call `proxy.IsRunning`/
  `EnsureRunning` and rebuild `ZaiBaseURL` before launching `claude`. Add a
  "daemon died between two jobs" test. With a fixed `proxy_port` this is now
  belt-and-suspenders rather than the only line of defense.

Fast deterministic repro before fixing: start `glm mcp`, then kill the proxy
daemon (`glm proxy stop` or `kill $(cat ~/.config/GoLeM/proxy.pid)`), then send
a job to that MCP session -> `ConnectionRefused`.

## What does NOT exist yet

- Path A (per-job proxy re-resolution) - see "Next".
- `internal/event` is still not bridged into the MCP server; `glm_start` emits
  no JSON-RPC progress notifications (`// TODO` at `cmd/glm/main.go:1277`).
- `internal/config/provider.go` `LoadProvider` (line 76) is defined but called
  by no command - multi-backend switching is unwired.

## Read order for a new session

1. This file.
2. [23_proxy.md](23_proxy.md) - proxy lifecycle; the center of the bug.
3. [11_config.md](11_config.md) - `proxy_port`, base URL, env overrides.
4. [20_claude_execution.md](20_claude_execution.md) - how `ANTHROPIC_BASE_URL`
   reaches the `claude` subprocess.
5. [10_cli.md](10_cli.md) - dispatch, `ensureProxy`/`cmdMCP`.
6. [31_mcp_tools.md](31_mcp_tools.md) - job handlers, if editing the job path.

## Smoke test (local)

```bash
cd /home/veschin/work/GoLeM
go build -o /tmp/glm ./cmd/glm/        # exit 0, no errors
go vet ./...                           # clean
go test ./... -short                   # every package prints ok (18 total)
bash docs/llm/validate.sh              # OK: all links valid (exit 0)
```

## Smoke test (Docker, real Z.AI key)

```bash
docker build -f test/Dockerfile.smoke -t golem-smoke:test .
docker run --rm \
  -v "$HOME/.config/GoLeM/zai_api_key:/home/testuser/.config/GoLeM/zai_api_key:ro" \
  golem-smoke:test
```

Builds glm from source, installs the Anthropic `claude` CLI, runs `glm _install`
non-interactively, exercises `glm run`/`glm chain` through the proxy against
Z.AI, drives the MCP server over stdio, and runs `glm _uninstall`.

## Agent error to log

None this session.
