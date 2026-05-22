---
id: handoff
kind: guide
---

# Handoff - proxy stale-port bug diagnosed (v1.5.0)

See also: [00_index.md](00_index.md) · [23_proxy.md](23_proxy.md) · [11_config.md](11_config.md).

## Current state

Branch: `main`. Version constant `version = "1.5.0"` (`cmd/glm/main.go:32`),
matching the latest tag `v1.5.0` (golem shipped as a Claude Code skill).

Baseline verified this session:

```
go build -o /tmp/glm ./cmd/glm/   # exit 0
go vet ./...                       # clean
go test ./... -short              # every package ok, 18 total
```

Three uncommitted doc edits were already present at session start and were not
touched: `docs/llm/10_cli.md`, `11_config.md`, `20_claude_execution.md`.

This session did **no code changes** - it diagnosed a runtime bug. The
diagnosis below is the main artifact; the fix is not written yet.

## Main finding: proxy stale-port bug

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

## Next (pick one; not a queue)

- **Path A - re-resolve proxy per job (~1-2h, recommended).** Move the
  liveness check off MCP startup and onto the job path: in the job handler
  (`internal/mcp/tools` run/start/chain -> `internal/cmd/execute.go`) call
  `proxy.IsRunning` (or `EnsureRunning`) and rebuild `ZaiBaseURL` before
  launching `claude`. Add a test for "daemon died between two jobs". This fixes
  the root cause, not just the symptom.
- **Path B - honor `proxy_port` (~30m, partial).** Pass `cfg.ProxyPort` into
  the daemon launch at `lifecycle.go:68` instead of hard-coding `0`. A stable
  port survives daemon restarts so the frozen URL stays valid. Does not fix the
  cold-start gap; best as defense-in-depth on top of A.
- Both A+B together is the durable answer.

Fast deterministic repro before fixing: start `glm mcp`, then kill the proxy
daemon (`glm proxy stop` or `kill $(cat ~/.config/GoLeM/proxy.pid)`), then send
a job to that MCP session -> `ConnectionRefused`.

## What does NOT exist yet

- The fix above - only the diagnosis exists.
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
