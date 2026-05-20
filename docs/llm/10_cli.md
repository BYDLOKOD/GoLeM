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
| `doctor` | `cmdDoctor` | System health check (7 checks: `claude_cli`, `api_key`, `zai_reachable`, `models`, `slots`, `platform`, `proxy`). |
| `update` | `cmdUpdate` | Self-update from GitHub. |
| `config show\|set` | `cmdConfig` | Show or mutate a config key. `config show` prints each key with a source annotation: `(default)`, `(config)`, or `(env)`. `config set KEY VALUE` validates KEY against `KnownConfigKeys` and writes to `glm.toml`. |
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
(e.g. `1h`, `24h`). In text mode, if the proxy is running, a header line
is printed before the job list with proxy statistics fetched from
`/health`: `active`, `queued`, `total`, `uptime` (`main.go:543-561`).

`clean` accepts `--days N` (remove jobs older than N days).

`pipeline` accepts `--system-prompt TEXT` and `--constraint KEY`.

## `_proxy` flags

The `_proxy` subcommand (internal, spawned by `ensureProxy`) accepts:

| Flag | Type | Description |
|------|------|-------------|
| `--port N` | int | Proxy listen port (default from config). |
| `--idle-timeout SEC` | int | Auto-shutdown after N seconds idle. |
| `--target URL` | string | Upstream API base URL. |
| `--config-dir DIR` | string | Directory for PID/port files. |
| `--concurrency N` | int | **Deprecated.** Accepted and discarded for backward compatibility with running proxy daemons. |

Parsed inline in `main.go:1047-1082`.

## JSON output

When `--json` is passed, commands emit structured JSON instead of human-readable
text. The types are defined in `internal/cmd/json.go`.

### `run --json` / `result --json`

Emits a `JobResultJSON` object:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Job ID. |
| `status` | string | Terminal status (`completed` / `failed`). |
| `stdout` | string | Agent stdout. |
| `stderr` | string | Agent stderr (empty for successful jobs). |
| `changelog` | string | File changes summary. |
| `duration_seconds` | int | Wall-clock duration in seconds. |
| `exit_code` | *int | Subprocess exit code; omitted when not recorded. |

Populated by `cmd.ResultJSON` (`json.go:285-318`). In `cmdRun`, JSON mode is
selected by `hasFlag(args, "--json")` at `main.go:299-300`.

### `log --json`

Emits a `JobLogJSON` object:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Job ID. |
| `changes` | []string | Changelog split into lines. Empty array if no changes. |

Populated by `cmd.LogJSON` (`json.go:321-345`).

### `status --json`

Emits a `JobStatusJSON` object (`json.go:26-31`): `id`, `status`, `pid`,
`started_at`.

### `list --json`

Emits a JSON array of `JobListItem` objects (`json.go:18-23`): `id`, `status`,
`started_at`, `project_id`. Empty result outputs `[]` (never `null`).

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

### `doctor` checks

`DoctorCmd` (`internal/cmd/doctor.go:82-101`) runs 7 diagnostic checks:

1. **claude_cli** -- `claude` binary found in PATH and version queried.
2. **api_key** -- API key file exists and is non-empty.
3. **zai_reachable** -- HEAD request to `ZaiBaseURL` succeeds within timeout.
4. **models** -- Reports configured opus/sonnet/haiku model names.
5. **slots** -- Counts running jobs under `SubagentDir`.
6. **platform** -- `GOOS/GOARCH`.
7. **proxy** -- Checks if proxy daemon is alive and queries `/health` for stats.

Each check outputs `OK`, `FAIL`, or `WARN` with a detail line. Doctor always
exits 0 (check failures are reported, not fatal).

### `config show|set`

`config show` prints each configuration key with its current value and a source
annotation (`doctor.go:339-434`):
- `(default)` -- hardcoded default.
- `(config)` -- value from `glm.toml`.
- `(env)` -- value from an environment variable (overrides TOML).

`config set KEY VALUE` validates the key against `KnownConfigKeys` and the
value against per-key rules, then writes the key into `glm.toml`
(`doctor.go:456-525`). Accepted keys: `model`, `opus_model`, `sonnet_model`,
`haiku_model`, `permission_mode`, `debug`. Invalid keys produce `err:user`.

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
