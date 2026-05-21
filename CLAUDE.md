# GoLeM -- GLM Subagent Bridge for Claude Code

GoLeM lets Claude Code (Opus) delegate work to GLM (Z.AI Chinese AI models) as subagents.
Opus orchestrates, GLM models execute tasks via the `glm` CLI.

- **Origin**: Forked from [github.com/veschin/GoLeM](https://github.com/veschin/GoLeM)
- **Language**: Go, stdlib-only (zero external dependencies)
- **Go version**: 1.25.7

## Git Workflow

**Working directly in `main` is ALLOWED for this project.** This overrides the global "never commit to main" rule.

Use worktrees (Agent tool with `isolation: "worktree"`) for larger features or parallel work.

### Commit format

```text
type(scope): message
```

| Types | Scopes |
| --- | --- |
| feat, fix, refactor, test, docs, chore, perf | mcp, proxy, slot, dag, channel, router, config, claude, job, cmd, log, prompt, validation |

Examples: `feat(mcp): add JSON-RPC transport`, `fix(proxy): handle rate limit 429 retry`

### Signing

- Always `--signoff`
- Never `--no-gpg-sign`

## Project Structure

```text
cmd/glm/main.go             -- CLI entry point (17 subcommands)
internal/
  artifact/                  -- Typed artifact persistence (text/JSON/file_ref)
  channel/                   -- HTTP notification client + event bridge
  claude/                    -- Claude CLI execution & JSON parsing
  cmd/                       -- Subcommand implementations + install/uninstall/update
  config/                    -- TOML config, env overrides, multi-provider support
  dag/                       -- DAG pipeline engine (topological sort, parallel scheduler)
  e2e/                       -- End-to-end tests (go:build e2e)
  event/                     -- Publish/subscribe event bus
  exitcode/                  -- Exit code constants and typed errors
  job/                       -- Job lifecycle, status FSM, reconciliation
  log/                       -- Structured logging (human/JSON)
  mcp/                       -- MCP server (JSON-RPC over stdio)
    tools/                   -- Eight MCP tool handlers
  prompt/                    -- Constraint expansion and system prompt assembly
  proxy/                     -- Rate-limiting reverse proxy for Z.AI API
  retry/                     -- Exponential backoff with jitter
  router/                    -- Complexity-based model routing
  slot/                      -- Concurrency control (flock + Unix socket notify)
  validation/                -- Chain output validation (contains, not_contains, matches)
```

## Build and Test

| Action | Command |
| --- | --- |
| Build | `go build -o glm ./cmd/glm/` |
| Test all | `go test ./...` |
| Test specific | `go test ./internal/proxy/` |
| Test with race detector | `go test -race ./...` |
| Vet | `go vet ./...` |

## Development Conventions

### Testing

- TDD: tests first, implementation second.
- No mocks for internal code -- use real files, `t.TempDir()`, real goroutines.
- HTTP tests use `httptest.NewServer`.
- I/O tests use `bytes.Buffer`.
- Filesystem tests use `t.TempDir()`.

### Error handling

Prefixed errors: `err:user`, `err:config`, `err:timeout`, etc.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | User error |
| 124 | Timeout |
| 127 | Dependency missing |

### Dependencies

No external dependencies. Stdlib only. If it is not in the Go standard library, it does not go in.

### Job storage

File-based at `~/.claude/subagents/<project-id>/<job-id>/`.

## Architecture

### Communication flow

```text
Opus --> glm CLI (Bash tool) --> subprocess `claude` CLI --> raw.json --> parse --> stdout.txt
```

### Concurrency

- File-based slot manager: flock + counter.
- Proxy semaphore channel for rate limiting.

### System prompts and constraints

Constraints and free-text system prompt are assembled by `internal/prompt/` and passed to `claude` via `--append-system-prompt`.

- CLI: `--system-prompt TEXT`, `--constraint KEY` (repeatable)
- MCP: `system_prompt` and `constraints` fields in all tool inputs
- Config: `system_prompt` field in `glm.toml` sets a default for all jobs

Constraint vocabulary:

| Key | Effect |
| --- | --- |
| `readonly` | No file writes or shell commands that mutate state |
| `no-create` | No new file creation |
| `plan-first` | Output a plan and wait for approval before acting |
| `scope:<path>` | Restrict work to the given path |

### Chain validation and retry

Step outputs can be validated by `internal/validation/` before the chain advances.

- Expressions: `contains:<text>`, `not_contains:<text>`, `matches:<regexp>`
- `dag.Step.Validate` - list of expressions checked against step stdout
- `dag.Step.Retry` / `dag.RetryConfig` - `MaxAttempts` and optional `Feedback` prompt injected on failure
- Gate steps (`type: "gate"`) validate without invoking Claude - zero-cost checks in pipelines
- MCP types mirror the same fields: `ChainInputStep.Validate`, `ChainInputStep.Retry`

### Configuration

- Config file: `~/.config/GoLeM/glm.toml` (TOML format, env var overrides supported)
- API key: `~/.config/GoLeM/zai_api_key`
- `system_prompt` - default system prompt prepended to every job (optional)

## Status

The merged work (PR #1) delivered: MCP server, DAG pipeline + chain validation,
complexity-based routing, per-model proxy concurrency, event bus, typed
artifacts, retry with jitter, prompt constraints. See `docs/llm/handoff.md`
for the current next-session action plan and `docs/llm/00_index.md` for the
package reference graph.

## Design Decisions

- Keep CLI working alongside any new interfaces (backward compatibility).
- MCP server is the highest priority improvement.
- All new packages follow existing patterns: `internal/`, own `_test.go` files, no mocks.

## Further documentation

See `docs/llm/00_index.md` for per-package specs (one file per domain, with code references).
