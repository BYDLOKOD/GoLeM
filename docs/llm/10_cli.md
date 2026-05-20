---
id: cli
kind: spec
touches: cmd/glm/main.go, internal/cmd/
---

# CLI - Commands, Flags, and Dispatch

See also: [11_config.md](11_config.md) · [21_job_lifecycle.md](21_job_lifecycle.md) · [60_logging.md](60_logging.md).

## Binary entry point

`cmd/glm/main.go`. `main()` calls `run(os.Args[1:])` and exits with the
returned integer. `run()` switches on `args[0]` (the subcommand) and
delegates to a `cmd*` function. No CLI framework is used; all parsing is
hand-written.

## Subcommands

| Subcommand | Function | Description |
|-----------|---------|-------------|
| `run` | `cmdRun` | Synchronous agent execution; blocks until done. |
| `start` | `cmdStart` | Async: prints job ID immediately, runs in goroutine. |
| `status JOB_ID` | `cmdStatus` | Read job status from filesystem. |
| `result JOB_ID` | `cmdResult` | Print stdout (and stderr for failed jobs). |
| `log JOB_ID` | `cmdLog` | Print changelog (file changes made by the agent). |
| `list` | `cmdList` | List all jobs with optional filters. |
| `clean` | `cmdClean` | Remove job directories. |
| `kill JOB_ID` | `cmdKill` | Send SIGTERM then SIGKILL to a running job. |
| `chain` | `cmdChain` | Sequential multi-prompt execution. |
| `pipeline FILE` | `cmdPipeline` | Execute a DAG from a JSON file. |
| `session` | `cmdSession` | Interactive Claude session (exec-replaces the process). |
| `doctor` | `cmdDoctor` | System health check. |
| `update` | `cmdUpdate` | Self-update from GitHub. |
| `config show\|set` | `cmdConfig` | Show or mutate a config key. |
| `mcp` | `cmdMCP` | Start MCP JSON-RPC server over stdio. |
| `version\|--version\|-v` | - | Print `glm <version>` and exit 0. |
| `help\|--help\|-h` | `usage()` | Print usage text and exit 0. |
| `_install` | `cmdInstall` | Internal: first-run installation. |
| `_uninstall` | `cmdUninstall` | Internal: remove GoLeM artifacts. |
| `_proxy` | `cmdProxy` | Internal: run the proxy daemon (spawned by `ensureProxy`). |

Unknown subcommands print an error and exit 1.

## Flags (run / start / chain)

Parsed by `cmd.ParseFlags` (`internal/cmd/flags.go`). Remaining positional
args after all flags are joined and become the prompt.

| Flag | Type | Description |
|------|------|-------------|
| `-d DIR` | string | Working directory (default `.`). |
| `-t SEC` | int | Timeout in seconds (default from config: 1800). |
| `-m`, `--model MODEL` | string | Set all three model slots. |
| `--opus MODEL` | string | Set only the opus model slot. |
| `--sonnet MODEL` | string | Set only the sonnet model slot. |
| `--haiku MODEL` | string | Set only the haiku model slot. |
| `--tier TIER` | string | `light`, `medium`, `heavy`, or `auto` (default: auto). |
| `--unsafe` | bool | Sets `PermissionMode = "bypassPermissions"`. |
| `--mode MODE` | string | Sets `PermissionMode` explicitly. |
| `--system-prompt TEXT` | string | Append system prompt text to the subagent. |
| `--constraint KEY` | string | Predefined constraint; repeatable. |
| `--json` | bool | JSON output format (supported by run/status/result/log/list). |

`--constraint` valid keys: `readonly`, `no-create`, `plan-first`,
`scope:<path>`. See [42_prompt.md](42_prompt.md).

`chain` additionally accepts `--continue-on-error` (bool).

`list` accepts `--status STATUS` (comma-separated) and `--since DURATION`
(e.g. `1h`, `24h`).

`clean` accepts `--days N` (remove jobs older than N days).

`pipeline` accepts `--system-prompt TEXT` and `--constraint KEY`.

## Execution flow for `run`

1. `cmd.ParseFlags` parses args.
2. `config.Load` reads `~/.config/GoLeM/glm.toml` + env vars.
3. `ensureProxy` starts or discovers the proxy daemon (if enabled).
4. `config.DefaultTimeout` applied if timeout is 0.
5. `cmd.Validate` checks prompt non-empty, dir exists, timeout > 0.
6. `job.Reconcile` cleans stale jobs at startup.
7. `slot.NewSlotManager(dir, 0)`, then `sm.Init()`, then `sm.WaitForSlot()`
   initializes the counter file and claims a concurrency slot.
8. `cmd.ExecuteJob` runs the claude subprocess and releases the slot on return.
9. Stdout/changelog/stderr printed to respective streams.

`start` creates the job directory via `job.NewJob`, writes `pid.txt`, prints
the job ID, then launches a goroutine. Inside the goroutine: `job.Reconcile`,
`sm.Init()`, `sm.WaitForSlot()`, `cmd.ExecuteJob` (no `cmd.Validate` call).
Panics inside the goroutine are recovered and written as `failed` status with
stderr output.

`session` calls `cmd.SessionCmd` to build argv, then `syscall.Exec` to
replace the current process with `claude` (no job directory is created).

## Exit codes

| Code | Constant | Meaning |
|------|----------|---------|
| 0 | `exitcode.OK` | Success. |
| 1 | `exitcode.UserError` | User error, validation error, internal error. |
| 3 | `exitcode.NotFound` | Job not found (`err:not_found` in message). |
| 124 | `exitcode.Timeout` | Subprocess timed out. |
| 127 | `exitcode.DependencyMissing` | `claude` not in PATH. |

The `die(err)` helper in `main.go` inspects the error string for
`err:not_found`, `err:dependency`, `err:timeout` substrings to choose the
right code; everything else returns 1.

## Logger initialization

`initLogger()` reads `GLM_DEBUG=1` (enables debug level), `GLM_LOG_FORMAT=json`
(switches to JSON), and `GLM_LOG_FILE=<path>` (appends to a file) before
any command logic runs. Also detects TTY via `os.Stderr.Stat()` to enable
ANSI colors. The logger is package-level (`var logger *log.Logger`).
