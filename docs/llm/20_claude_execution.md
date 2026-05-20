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

The stripping of `ANTHROPIC_AUTH_TOKEN` etc. prevents the parent process's
Anthropic credentials from leaking into the subprocess when `glm` is itself
running inside a Claude Code session.

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

## Execute

Signature: `Execute(parent context.Context, cfg Config) (int, error)`

Steps:

1. `exec.LookPath("claude")` - returns `(127, err:dependency)` if not found.
2. `os.Stat(cfg.WorkDir)` - returns `(1, err:user)` if missing.
3. Writes pre-execution metadata files to `cfg.JobDir`:
   `prompt.txt`, `workdir.txt`, `permission_mode.txt`, `model.txt`,
   `started_at.txt`.
4. Creates a `context.WithTimeout` derived from `parent` using
   `cfg.TimeoutSecs` (default 600 s when <= 0).
5. Runs `claude <flags> <prompt>` with `Setpgid: true` so the whole process
   group can be killed.
6. Waits on a goroutine or context cancellation.
7. On timeout: `syscall.Kill(-pid, SIGKILL)` then waits for exit.
8. Writes `finished_at.txt`, `raw.json` (stdout), `stderr.txt`.
9. Determines exit code: context expiry -> 124; `ExitError` -> process code;
   signal -> 128 + signal number; other -> 1.
10. Publishes `event.JobRunning` before start, then `JobDone`/`JobFailed`/
    `JobTimeout` after completion.
11. Writes `exit_code.txt` only when exit code != 0.

## Output parsing (`ParseRawJSON`)

`claude --output-format json` writes a JSON object to stdout. The top-level
structure is:

```json
{
  "result": "<text output>",
  "messages": [
    {
      "role": "assistant",
      "content": [
        {"type": "tool_use", "name": "Edit", "input": {...}},
        ...
      ]
    }
  ]
}
```

`ParseRawJSON(jobDir)`:
1. Reads `raw.json`.
2. Writes `stdout.txt` from `result` field.
3. Collects all `type == "tool_use"` content blocks across all messages.
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
