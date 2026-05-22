---
id: claude-execution
kind: spec
touches: internal/claude/
---

# Claude Execution - Subprocess Invocation and Output Parsing

See also: [21_job_lifecycle.md](21_job_lifecycle.md) · [11_config.md](11_config.md) · [42_prompt.md](42_prompt.md).

## Overview

`internal/claude` is responsible for three things:

1. Building the environment (`BuildEnv`) and CLI flags (`BuildFlags`) for the
   `claude` subprocess.
2. Running the subprocess (`Execute`), capturing stdout/stderr, and writing
   metadata files to the job directory.
3. Parsing the JSON output (`ParseRawJSON`) into `stdout.txt` and
   `changelog.txt`.

## Environment construction (`BuildEnv`)

Starts from `os.Environ()`, strips nesting-detection variables
(`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`) and any previously set Anthropic
variables, then appends:

```
ANTHROPIC_AUTH_TOKEN    = cfg.ZAIAPIKey
ANTHROPIC_BASE_URL      = cfg.ZAIBaseURL
API_TIMEOUT_MS          = cfg.ZAIAPITimeoutMS
ANTHROPIC_DEFAULT_OPUS_MODEL    = cfg.OpusModel
ANTHROPIC_DEFAULT_SONNET_MODEL  = cfg.SonnetModel
ANTHROPIC_DEFAULT_HAIKU_MODEL   = cfg.HaikuModel
```

In addition to the nesting-detection variables and Anthropic credentials,
`API_TIMEOUT_MS` is also stripped (blocked map in `claude.go:51-60`). This
prevents the parent process's Anthropic credentials and timeout configuration
from leaking into the subprocess when `glm` is itself running inside a Claude
Code session.

## Flag construction (`BuildFlags`)

Always present: `-p --no-session-persistence --output-format json`.

Conditional:

| Condition | Flag(s) added |
|-----------|--------------|
| `cfg.Model != ""` | `--model <model>` |
| `cfg.SystemPrompt != ""` | `--append-system-prompt <text>` |
| `cfg.Effort != ""` | `--effort <effort>` |
| `cfg.ExcludeDynamicSections` | `--exclude-dynamic-system-prompt-sections` |
| `cfg.PermissionMode == "bypassPermissions"` | `--dangerously-skip-permissions` |
| `cfg.PermissionMode != "" && != "bypassPermissions"` | `--permission-mode <mode>` |
| `cfg.VisionMCPConfig != ""` | `--mcp-config <vision-path>` (first) |
| `cfg.MCPConfig != ""` | `--mcp-config <value>` |
| `hasMCPConfig(cfg) && cfg.MCPStrict` | `--strict-mcp-config` |

### Golem-scoped MCP servers (`MCPConfig`, `VisionMCPConfig`, `MCPStrict`)

These attach MCP servers to the golem's `claude` subprocess only -- the host
`~/.claude/settings.json` is never touched. `mcpConfigValues(cfg)` returns the
ordered list (the built-in vision config first, then any user-supplied
`MCPConfig`), and `BuildFlags` emits one `--mcp-config` flag per value; `claude`
accumulates repeated flags. `MCPConfig` accepts a file path or inline JSON.
`MCPStrict` adds `--strict-mcp-config` so the golem ignores all other MCP
configuration.

`VisionMCPConfig` is a *path*, not a toggle. The toggle is `config.VisionMCP`
(default on); `internal/cmd/execute.go` and `session.go` call
`WriteVisionMCPConfig(configDir, apiKey)` (`internal/cmd/vision.go`) to generate
`~/.config/GoLeM/golem-vision-mcp.json` (Z.AI vision server, API key filled in,
mode 0600, written atomically) and pass its path here. See [11_config.md](11_config.md).

## Execute

Signature: `Execute(parent context.Context, cfg Config) (int, error)`

Steps:

1. If `parent == nil`, replaces it with `context.Background()` so the contract
   remains safe (`claude.go:135-137`).
2. `exec.LookPath("claude")` - returns `(127, err:dependency)` if not found.
3. `os.Stat(cfg.WorkDir)` - returns `(1, err:user)` if missing.
4. Writes pre-execution metadata files to `cfg.JobDir`:
   `prompt.txt`, `workdir.txt`, `permission_mode.txt`, `model.txt`,
   `started_at.txt`.
5. Creates a `context.WithTimeout` derived from `parent` using
   `cfg.TimeoutSecs` (default 600 s when <= 0).
6. Runs `claude <flags> <prompt>` with `Setpgid: true` so the whole process
   group can be killed. When any MCP config is attached (`hasMCPConfig(cfg)`),
   a `--` separator is inserted before the prompt (`claude.go:210-211`):
   `claude`'s `--mcp-config` is variadic and would otherwise swallow the
   positional prompt as another config value.
7. Waits on a goroutine or context cancellation.
8. On timeout: `syscall.Kill(-pid, SIGKILL)` then waits for exit.
9. Writes `finished_at.txt`, `raw.json` (stdout), `stderr.txt`.
10. Determines exit code: context expiry -> 124; `ExitError` -> process code;
    signal -> 128 + signal number; other -> 1.
11. Publishes `event.JobRunning` before start, then `JobDone`/`JobFailed`/
    `JobTimeout` after completion.
12. Writes `exit_code.txt` only when exit code != 0.

## Output parsing (`ParseRawJSON` / `extractResult`)

`claude --output-format json` emits **two shapes** depending on the Claude Code
version. `extractResult(data)` (`parser.go:97`) handles both by sniffing the
first non-space byte; a Claude Code update flipping the shape no longer silently
blanks the golem's output (see [90_lessons/01_claude_output_array.md](90_lessons/01_claude_output_array.md)).

**Array form** (current, claude 2.1+) -- a stream of typed events:

```json
[
  {"type": "system",    "...": "..."},
  {"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Edit", "input": {}}]}},
  {"type": "result",    "result": "<text output>"}
]
```

The final text is the `result` field of the `type == "result"` event; tool_use
blocks live under `message.content` of `type == "assistant"` events.

**Legacy object form** -- a single object with top-level `result` and
`messages[].content[]`. Parsed into `rawOutput`.

`ParseRawJSON(jobDir)`:
1. Reads `raw.json`.
2. `extractResult` returns `(result, toolUses, err)`, transparently handling
   either shape.
3. Writes `stdout.txt` from `result`.
4. Calls `GenerateChangelog(jobDir, toolUses)`.

On malformed JSON: writes empty `stdout.txt`, calls `GenerateChangelog` with
nil, logs a warning to stderr, and returns the error from `GenerateChangelog`.
For read/write failures, returns the underlying OS error directly.

## Changelog generation (`GenerateChangelog`)

Writes `changelog.txt`. One line per recognized tool use:

| Tool name | Output line |
|-----------|-------------|
| `Edit` | `EDIT <file_path>: <len(new_string)> chars` |
| `Write` | `WRITE <file_path>` |
| `Bash` (delete) | `DELETE via bash: <truncated cmd>` |
| `Bash` (compound) | skipped (contains `&&`, `\|\|`, `;`, or `\|`) |
| `Bash` (simple) | `FS: <truncated cmd>` |
| `NotebookEdit` | `NOTEBOOK <notebook_path>` |

When no recognized tool uses are present, writes `(no file changes)`.
Commands are truncated to 80 characters before classification.

## Status mapping (`MapStatus`)

Converts exit code + stderr text to a job status string:

| Exit code | Condition | Status |
|-----------|-----------|--------|
| 0 | - | `done` |
| 124 | - | `timeout` |
| other | stderr contains permission keyword | `permission_error` |
| other | - | `failed` |

Permission keywords (case-insensitive): `permission`, `not allowed`, `denied`,
`unauthorized`.

## Metadata helpers

Three exported helpers write individual metadata files to the job directory.
They are used by the chain/pipeline machinery that needs finer-grained control
than the monolithic `Execute` function.

| Function | Signature | Behaviour |
|----------|-----------|-----------|
| `WriteMetadata` | `(cfg Config)` | Writes `prompt.txt`, `workdir.txt`, `permission_mode.txt`, `model.txt`, `started_at.txt` to `cfg.JobDir`. No-op on error (ignores return value of `os.WriteFile`). `claude.go:293` |
| `WriteFinishedAt` | `(jobDir string)` | Writes current UTC time in RFC3339 to `finished_at.txt`. No-op on error. `claude.go:309` |
| `WriteExitCode` | `(jobDir string, code int)` | Writes decimal exit code to `exit_code.txt`. No-op when `code == 0` (success does not produce a file). No-op on write error. `claude.go:316` |
