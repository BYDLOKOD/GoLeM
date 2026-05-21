---
id: handoff
kind: guide
---

# Handoff - GoLeM v2.0.0 release

See also: [00_index.md](00_index.md).

## Current state

Branch: `main`. Latest tag: `v2.0.0` (release prep round).

All 17 packages pass `go test ./... -short`, `go test -race ./...`, and a
Docker-based end-to-end smoke test (`test/Dockerfile.smoke` +
`test/docker-smoke.sh`) against a real Z.AI account.

```
go test ./...                              # full suite
go test ./... -short                       # excludes real-claude tests
go test -race ./...                        # race detector
bash test/docker-smoke.sh                  # in-container smoke (needs Docker)
```

Binary version constant: `version = "2.0.0"` (`cmd/glm/main.go`).

## What was fixed in the release prep

These bugs were caught during the v2.0.0 smoke pass and are now closed:

- `glm help` previously printed to stderr. It now prints to stdout when called
  as a command (`help`, `--help`, `-h`); stderr is used only when usage is
  printed alongside an error.
- `glm run/start/chain` accepted only `-d`/`-t`. The long forms `--dir` and
  `--timeout` now work too and are reflected in completions.
- The chain prompt extractor used a hard-coded flag list that did not include
  the new long-form flags, which made `--dir VAL --timeout VAL` leak into the
  prompt list. Fixed.
- `glm _uninstall` removed the entire config directory unconditionally, which
  silently wiped the API key when the user had declined the "Remove
  credentials?" prompt. The config directory is now removed only when the key
  is also removed; otherwise installer-managed files (`glm.toml`,
  `config.json`) are deleted while the key is kept.
- `glm config show` listed only ten keys and `config set` accepted only six,
  so users could not configure `proxy_enabled`, `proxy_port`,
  `proxy_idle_timeout`, `effort`, `system_prompt`, or
  `exclude_dynamic_sections` through the CLI. Both commands now cover the full
  set of writable keys.
- Shell completions (`completions/glm.bash`, `completions/glm.fish`) were
  missing the `pipeline` and `mcp` subcommands, the `--tier`,
  `--system-prompt`, `--constraint`, and `--continue-on-error` flags, and the
  `default` permission mode. Status filter included a non-existent `cancelled`
  value. All fixed.
- README listed the obsolete `slots OK 0/3 slots in use` and `proxy OK ...`
  lines for `glm doctor`. The example now matches the actual output.
- README recommended `glm config set api_rps 5` even though `api_rps` is a
  removed key. Replaced with currently-supported keys and listed the full
  `config set` vocabulary.
- `docs/architecture.svg` was generated before PR #1 and still showed the
  legacy `max_parallel slots / flock` model. Redrawn with the rate-limiting
  proxy as a separate participant and `[models]`-based per-model concurrency.

## Read order for a new session

1. This file.
2. [10_cli.md](10_cli.md) - command dispatch, flags, exit codes.
3. [21_job_lifecycle.md](21_job_lifecycle.md) - job FSM (most referenced).
4. [23_proxy.md](23_proxy.md) - if touching proxy or rate-limiting.
5. [40_dag.md](40_dag.md) - if touching pipeline execution.
6. [30_mcp.md](30_mcp.md) + [31_mcp_tools.md](31_mcp_tools.md) - if touching MCP.

## Smoke test (local)

```bash
cd /home/veschin/work/GoLeM
go build -o /tmp/glm ./cmd/glm/                  # binary, no errors
go vet ./...                                     # no issues
go test ./... -short                             # all 17 packages pass
bash docs/llm/validate.sh                        # OK: N links valid
```

## Smoke test (Docker, real Z.AI key)

```bash
docker build -f test/Dockerfile.smoke -t golem-smoke:test .
docker run --rm \
  -v "$HOME/.config/GoLeM/zai_api_key:/home/testuser/.config/GoLeM/zai_api_key:ro" \
  golem-smoke:test
```

The container builds glm from the current source tree, installs the Anthropic
`claude` CLI via npm, runs `glm _install` non-interactively, calls
`glm run` and `glm chain` through the rate-limiting proxy against Z.AI, drives
the MCP server over stdio, and finally exercises `glm _uninstall`.

## Outstanding (post-release)

- `internal/event` is not yet bridged into the MCP server. `glm_start`
  invocations do not emit JSON-RPC progress notifications. See the `// TODO`
  in `cmd/glm/main.go` near `cmdMCP`.
- `internal/config/provider.go` (`LoadProvider`) is not called by any CLI
  command. Wiring it would let users switch between multiple Anthropic-
  compatible API backends via config.
- Provider/multi-backend coverage and Windows support are out of scope for
  v2.0.0.

## Agent error to log

None this session.
