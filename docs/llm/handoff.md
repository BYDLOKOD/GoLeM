---
id: handoff
kind: guide
---

# Handoff - oneOf schema fix shipped as v1.5.2

See also: [00_index.md](00_index.md) · [23_proxy.md](23_proxy.md) · [11_config.md](11_config.md) · [31_mcp_tools.md](31_mcp_tools.md) · [90_lessons/02_mcp_oneof_top_level.md](90_lessons/02_mcp_oneof_top_level.md) · [devlog/01_devlog_proxy_stale_port.md](devlog/01_devlog_proxy_stale_port.md) · [devlog/03_devlog_oneof_fix_session.md](devlog/03_devlog_oneof_fix_session.md).

## Current state

Branch: `main`. Version `version = "1.5.2"` (`cmd/glm/main.go:32`), tagged
and released.

This session closed one user-visible defect:

```
go build ./...       # exit 0
go vet ./...          # clean
go test ./...         # all packages ok
```

| Change | File | What it does |
|--------|------|----------------|
| Schema fix | `internal/mcp/tools/chain.go` | Drops top-level `oneOf` from `glm_chain.input_schema`. The Anthropic Messages API rejects `oneOf`/`allOf`/`anyOf` at the top level of a tool schema with HTTP 400 - which was crashing every Claude Code session that registered the MCP server at first model call. Both `prompts` and `steps` are now plain optional properties; the handler enforces "at least one is populated" at runtime. |
| Test rewrite | `internal/mcp/tools/tools_test.go` | `TestChainDefinition_AcceptsEitherPromptsOrSteps` now asserts the *absence* of `oneOf`/`allOf`/`anyOf` and of top-level `required`, instead of asserting their presence. |
| New test | `internal/mcp/tools/tools_test.go` | `TestChainHandler_BothFieldsPresent_StepsTakePrecedence` locks the runtime precedence rule: when an input carries both `prompts` and `steps`, `steps` wins. |
| Release | `cmd/glm/main.go` | `version = "1.5.2"`. |

The fix was verified end-to-end through a running Claude Code session
after `/mcp` reconnected the long-lived `glm mcp` subprocess onto the
rebuilt binary. See
[devlog/03_devlog_oneof_fix_session.md](devlog/03_devlog_oneof_fix_session.md)
for the verification trail and the adjacent proxy-misconfig finding.

## Background: proxy stale-port bug (Path B fixed in v1.5.1, Path A still open)

> Carried over for the session that picks up Path A. Path B (honor
> `proxy_port` end-to-end) shipped in v1.5.1 - a fixed port now survives
> daemon restarts and a long-lived MCP session's cached URL stays valid.
> What is described below is the **default** `proxy_port = 0` case that
> Path A still needs to close.

Symptom: a `glm_run` (or any MCP-driven job) fails immediately with
`API Error: Unable to connect to API (ConnectionRefused)`, and no job
directory is created. The message text is **not** in the GoLeM source - it
is printed by the `claude` CLI when it cannot reach `ANTHROPIC_BASE_URL`.

Root cause is three facts in the code combining:

1. `ensureProxy` runs **once at `glm mcp` startup**
   (`cmd/glm/main.go:1273`, body at `main.go:1246-1264`) and freezes the
   port into process memory:
   `cfg.ZaiBaseURL = fmt.Sprintf("http://localhost:%d", proxyPort)`
   (`main.go:1263`). The job path never re-resolves it - the only
   `proxy.EnsureRunning` caller in the whole tree is `main.go:1258`. Every
   job in that MCP session reuses the frozen URL
   (`internal/cmd/execute.go:223`, `internal/dag/executor.go:112` read
   `cfg.ZaiBaseURL`).
2. With the default `proxy_port = 0` the daemon binds a fresh OS-assigned
   port each time it (re)starts. (For a non-zero `proxy_port` this is
   moot - Path B threaded `cfg.ProxyPort` through `EnsureRunning` so the
   daemon rebinds the same port.)
3. The daemon shuts itself down after `ProxyIdleTimeout` seconds (default
   600).

Together, in the default-port case: a long-lived `glm mcp` process holds
the port of a proxy that has since died on idle timeout; the replacement
daemon binds a different port the MCP process never learns. The first job
after an idle gap hits the dead port and gets `ConnectionRefused`.

The dead port for the failing job is **inferred from process/port timing**,
not observed - no job directory was created, so the dialed address was not
captured. The timing proof (MCP PID start vs. proxy port-lifetime windows)
is in
[devlog/01_devlog_proxy_stale_port.md](devlog/01_devlog_proxy_stale_port.md).

## Next

- **Path A - re-resolve proxy per job (~1-2h, still open).** For the default
  `proxy_port = 0` case the proxy URL is resolved once at MCP startup and
  never re-resolved, so a daemon that dies mid-session strands the cached
  URL. Move the liveness check onto the job path: in the job handler
  (`internal/mcp/tools` run/start/chain -> `internal/cmd/execute.go`) call
  `proxy.IsRunning`/`EnsureRunning` and rebuild `ZaiBaseURL` before
  launching `claude`. Add a "daemon died between two jobs" test. With a
  fixed `proxy_port` (Path B, shipped in v1.5.1) this is now
  belt-and-suspenders rather than the only line of defense. Fast
  deterministic repro before fixing: start `glm mcp`, then kill the proxy
  daemon (`kill $(cat ~/.config/GoLeM/proxy.pid)`), then send a job to that
  MCP session - `ConnectionRefused`.

- **End-to-end MCP test in CI.** `internal/e2e/` exists under
  `go:build e2e` but is not regularly run. Every MCP tool schema change
  rides "ship and pray" until that suite gates merges. The `oneOf` defect
  was caught by a real session; it should be caught by a test.

## What does NOT exist yet

- Path A (per-job proxy re-resolution) - see "Background" above and "Next".
- `internal/event` is still not bridged into the MCP server; `glm_start`
  emits no JSON-RPC progress notifications (`// TODO` near `cmdMCP` at
  `cmd/glm/main.go:1266`).
- `internal/config/provider.go` `LoadProvider` (line 76) is defined but
  called by no command - multi-backend switching is unwired.
- **No deprecation warning for `api_rps` / `max_parallel`.** Both keys are
  silently accepted and silently ignored
  (`internal/config/config.go:261`). An owner who carried `api_rps = 10`
  forward from an older config thinks they have ten concurrent slots; the
  proxy actually falls back to a global semaphore of 1. The fix is either
  a `glm doctor` warning on parse, or removing the parse-tolerance and
  failing loudly. The user-side workaround is the `[models]` section in
  [11_config.md](11_config.md).
- **No `glm_help` / self-description MCP tool.** When a golem subagent
  starts, it has no way to discover what GoLeM features are available, what
  constraints do, how chain/pipeline differ, or what the proxy does. A
  `glm_help` tool that returns a structured description of all tools, their
  parameters, constraint vocabulary, routing rules, and gotchas would let
  the orchestrating agent brief the golem on first use instead of guessing.
- **`doctor` reports `zai_reachable FAIL` when Z.AI rejects the unauth
  HEAD with EOF.** The v1.5.1 fix taught `doctor` to treat **any HTTP
  response** as reachable, but Z.AI closes the connection without a
  response on an unauthenticated HEAD - so `Head https://api.z.ai/api/anthropic`
  returns `EOF`, doctor reports `FAIL`, and the user sees a red status even
  while the proxy is happily round-tripping authenticated POSTs to the same
  endpoint (visible as `proxy: active=N total=M` two lines below in the
  same `doctor` output). The heuristic needs to also treat "TCP connected
  but server closed before HTTP response" as reachable, or to send a
  minimally-authed request that elicits a normal response.

## Read order for a new session

1. This file.
2. [90_lessons/02_mcp_oneof_top_level.md](90_lessons/02_mcp_oneof_top_level.md) -
   the schema-disjunction gotcha; read before touching any tool schema.
3. [31_mcp_tools.md](31_mcp_tools.md) - tool handler shapes; touched by the
   fix.
4. [23_proxy.md](23_proxy.md) - proxy lifecycle; needed for Path A.
5. [11_config.md](11_config.md) - TOML keys, `[models]` section, env
   overrides; touched by the proxy-misconfig finding.
6. [20_claude_execution.md](20_claude_execution.md) - how
   `ANTHROPIC_BASE_URL` reaches the `claude` subprocess.
7. [10_cli.md](10_cli.md) - dispatch, `ensureProxy`/`cmdMCP`.

## Smoke test (local)

```bash
cd /home/veschin/work/GoLeM
go build -o /tmp/glm ./cmd/glm/        # exit 0, no errors
go vet ./...                           # clean
go test ./... -short                   # every package prints ok
bash docs/llm/validate.sh              # OK: all links valid (exit 0)
```

## Smoke test (Docker, real Z.AI key)

```bash
docker build -f test/Dockerfile.smoke -t golem-smoke:test .
docker run --rm \
  -v "$HOME/.config/GoLeM/zai_api_key:/home/testuser/.config/GoLeM/zai_api_key:ro" \
  golem-smoke:test
```

Builds glm from source, installs the Anthropic `claude` CLI, runs
`glm _install` non-interactively, exercises `glm run`/`glm chain` through
the proxy against Z.AI, drives the MCP server over stdio, and runs
`glm _uninstall`.

## Agent error log

| Version | Error | Status |
|---------|-------|--------|
| 1.5.2 | `glm_chain` schema's top-level `oneOf` killed Claude Code sessions with `tools.N.custom.input_schema: input_schema does not support oneOf, allOf, or anyOf at the top level`. | Fixed; see [90_lessons/02_mcp_oneof_top_level.md](90_lessons/02_mcp_oneof_top_level.md). |
| 1.4.0 | `claude --output-format json` flipped from object to array; parser silently produced empty `stdout.txt`. | Fixed; see [90_lessons/01_claude_output_array.md](90_lessons/01_claude_output_array.md). |
