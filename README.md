<p align="center">
  <img src="GoLeM.png" width="600" alt="GoLeM - a tiny wizard commanding clay golems to do the heavy lifting" />
</p>

<h1 align="center">GoLeM</h1>

<p align="center">
  <strong>One wizard. Unlimited golems. Zero Anthropic API costs.</strong>
</p>

<p align="center">
  Spawn autonomous Claude Code agents powered by GLM-5.1 via Z.AI.<br>
  Each golem is a full Claude Code instance - reads files, edits code, runs tests, uses MCP servers and skills.<br>
  You stay on Opus. Your golems run free and parallel through Z.AI. Ship faster.
</p>

<p align="center">
  <a href="https://github.com/veschin/GoLeM/releases"><img alt="Release" src="https://img.shields.io/github/v/release/veschin/GoLeM?display_name=tag&sort=semver"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/veschin/GoLeM"></a>
  <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/veschin/GoLeM"></a>
  <a href="#installation"><img alt="Platforms" src="https://img.shields.io/badge/platforms-linux%20%7C%20macos%20%7C%20wsl-blue"></a>
</p>

---

![Architecture](docs/architecture.svg?v=5)

## TL;DR (60 seconds)

```bash
# 1. Install (needs Go 1.25+ and the Claude Code CLI on PATH)
go install github.com/veschin/GoLeM/cmd/glm@latest
glm _install                       # prompts for your Z.AI key, wires everything up

# 2. Spawn a golem from the shell
glm run --dir "$PWD" --timeout 300 "add a unit test for parseFlags and run it"

# 3. Or let Claude Code call them via the MCP server (registered automatically)
#    The host Opus session now has glm_run / glm_start / glm_chain / glm_pipeline tools.
```

That's it. The rest of this README is the reference.

## Installation

### Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) on `PATH`
- [Z.AI Coding Plan](https://z.ai/subscribe) key
- Go 1.25+
- Linux, macOS, or WSL (Windows native is not supported - flock and Unix sockets)

### Via install script (one-liner)

```bash
curl -fsSL https://raw.githubusercontent.com/veschin/GoLeM/main/install.sh | bash
```

The script clones the repo to `~/.local/share/GoLeM`, builds the binary,
installs shell completions, and runs the interactive `glm _install` for you.

### Via `go install`

```bash
go install github.com/veschin/GoLeM/cmd/glm@latest
glm _install
```

`go install` places the binary in `$(go env GOPATH)/bin`, which should already
be in your `PATH`. Shell completions are not installed by this path - use the
script above for autocompletion.

### From source

```bash
git clone https://github.com/veschin/GoLeM.git ~/projects/GoLeM
cd ~/projects/GoLeM
go install ./cmd/glm/
glm _install
```

### What `_install` does

The interactive installer completes these steps in order:

1. Prompts for your Z.AI API key and saves it to `~/.config/GoLeM/zai_api_key` (mode 0600)
2. Prompts for permission mode and writes `~/.config/GoLeM/glm.toml`
3. Writes `~/.config/GoLeM/config.json` with install metadata
4. Injects the GLM subagent section into `~/.claude/CLAUDE.md` between `<!-- GLM-SUBAGENT-START -->` and `<!-- GLM-SUBAGENT-END -->` markers
5. Creates `~/.claude/subagents/` directory
6. Registers the MCP server (`golem`) under `mcpServers` in `~/.claude/settings.json`

Re-running `_install` is idempotent: existing markers are replaced, existing
MCP entries are updated, and you can choose to keep your existing API key.

### Manual installation (without interactive installer)

For those who prefer explicit control:

```bash
# Install binary
go install github.com/veschin/GoLeM/cmd/glm@latest

# Create config directory
mkdir -p ~/.config/GoLeM

# Write API key (replace YOUR_KEY with your Z.AI key)
printf '%s' 'YOUR_KEY' > ~/.config/GoLeM/zai_api_key
chmod 0600 ~/.config/GoLeM/zai_api_key

# Write minimal config
printf 'permission_mode = "bypassPermissions"\n' > ~/.config/GoLeM/glm.toml

# Create subagents directory
mkdir -p ~/.claude/subagents

# Register MCP server - add this to ~/.claude/settings.json under "mcpServers"
```

MCP registration entry for `~/.claude/settings.json` - use the full path to the binary:

```bash
# Find the binary path
which glm
```

```json
{
  "mcpServers": {
    "golem": {
      "command": "/path/from/which/glm",
      "args": ["mcp"]
    }
  }
}
```

### Verify

```bash
glm doctor
```

Expected output (7 checks):

```text
claude_cli       OK    2.1.146 (Claude Code) found at /home/user/.local/bin/claude
api_key          OK    API key configured via /home/user/.config/GoLeM/zai_api_key
zai_reachable    OK    https://api.z.ai/api/anthropic responded with 200 in 714ms
models           OK    opus=glm-5.1, sonnet=glm-5.1, haiku=glm-5.1
slots            OK    0 running (unlimited)
platform         OK    linux/amd64
proxy            WARN  proxy not running
```

`proxy` reports `WARN` until the first job auto-starts the rate-limiting proxy;
after that it switches to `OK active=N queued=M total=K uptime=...`.

### Uninstall

```bash
glm _uninstall
```

Strips the GLM section from `~/.claude/CLAUDE.md`, deregisters the MCP server from `~/.claude/settings.json`, and prompts before removing the API key and job artifacts. Remove the binary manually via `go clean -i github.com/veschin/GoLeM/cmd/glm` if needed.

## Commands

| Command | Usage | Description |
| --- | --- | --- |
| `session` | `glm session [flags] [claude-flags]` | Interactive Claude Code session via Z.AI |
| `run` | `glm run --dir DIR --timeout SEC "prompt"` | Synchronous execution - blocks until done |
| `start` | `glm start --dir DIR --timeout SEC "prompt"` | Asynchronous execution - returns job ID |
| `chain` | `glm chain --dir DIR --timeout SEC "p1" "p2" ...` | Sequential chain - stdout flows to next prompt |
| `pipeline` | `glm pipeline FILE` | DAG pipeline from JSON file |
| `status` | `glm status JOB_ID` | Check job status (queued/running/done/failed/timeout) |
| `result` | `glm result JOB_ID` | Get job text output |
| `log` | `glm log JOB_ID` | Show file changelog (edits, writes, deletes) |
| `list` | `glm list [--status S] [--since D]` | List all jobs with optional filters |
| `clean` | `glm clean [--days N]` | Remove old job data |
| `kill` | `glm kill JOB_ID` | Terminate a running job |
| `update` | `glm update` | Self-update from GitHub |
| `doctor` | `glm doctor` | System health check |
| `config` | `glm config {show\|set KEY VAL}` | View or modify configuration |
| `mcp` | `glm mcp` | Start MCP server (JSON-RPC over stdio) |
| `_install` | `glm _install` | Interactive installer |
| `_uninstall` | `glm _uninstall` | Interactive uninstaller |

### Flags

Apply to `session`, `run`, `start`, and `chain`.

| Flag | Description |
| --- | --- |
| `--dir DIR` | Working directory (absolute path, mandatory for run/start/chain) |
| `--timeout SEC` | Timeout in seconds (mandatory for run/start/chain) |
| `-m, --model MODEL` | Override model for all three slots |
| `--opus MODEL` | Override opus model slot |
| `--sonnet MODEL` | Override sonnet model slot |
| `--haiku MODEL` | Override haiku model slot |
| `--tier TIER` | Complexity-based routing: `light`, `medium`, `heavy`, `auto` (default: `auto`) |
| `--unsafe` | Bypass all permission checks |
| `--mode MODE` | Set permission mode |
| `--system-prompt TEXT` | Override the system prompt for this invocation |
| `--constraint KEY` | Add a behavior constraint (repeatable); see [Constraints](#constraints) |
| `--json` | JSON output format |

Claude Code uses three model slots internally: heavy tasks go to opus, normal tasks to sonnet, fast tasks to haiku. All three default to `glm-5.1`. `--model` changes all at once; `--opus`/`--sonnet`/`--haiku` change them individually.

`session` passes unknown flags directly to `claude` - for example `--resume`, `--verbose`.

`chain` also accepts `--continue-on-error`: without it, the chain stops on the first failed step. When using the MCP `glm_chain` tool, the `steps` field (array of step objects) replaces `prompts` and enables per-step `validate` and `retry` - see [Chain validation and auto-retry](#chain-validation-and-auto-retry).

### System Prompt and Constraints

`--system-prompt TEXT` injects additional instructions into the subagent's system prompt for the duration of that invocation, overriding the `system_prompt` value from `glm.toml`. Use it to give the subagent context-specific rules without editing the global config.

`--constraint KEY` appends a predefined behavior restriction to the system prompt. The flag is repeatable - pass it multiple times to combine constraints.

| Constraint | Effect |
| --- | --- |
| `readonly` | Prohibits all file writes and deletes; the agent may only read and report |
| `no-create` | Prohibits creating new files; existing files may be read or modified |
| `plan-first` | Requires the agent to output a detailed plan and wait for approval before making any changes |
| `scope:<path>` | Restricts operations to files under `<path>`; reads and writes outside are forbidden |

Constraints and `--system-prompt` compose: constraints are prepended to the system prompt text, separated by blank lines.

**CLI example:**

```bash
glm run --dir /home/user/project --timeout 300 \
  --constraint readonly \
  --constraint scope:/home/user/project/internal \
  --system-prompt "Focus only on security vulnerabilities." \
  "audit the codebase for injection risks"
```

**MCP tool fields:** `glm_run`, `glm_start`, `glm_chain`, and `glm_pipeline` all accept `system_prompt` (string) and `constraints` (array of strings) in their input objects with the same semantics as the CLI flags.

## Usage Examples

### Sync execution

```bash
glm run --dir /home/user/project --timeout 300 "add unit tests for the auth module"
```

### Async execution with monitoring

```bash
JOB=$(glm start --dir /home/user/project --timeout 600 "refactor database layer")
glm status $JOB        # check progress
glm list               # overview of all jobs
glm result $JOB        # read output when done
glm log $JOB           # see what files were changed
```

### Parallel agents

```bash
JOB1=$(glm start --dir /path --timeout 300 "write tests for module A")
JOB2=$(glm start --dir /path --timeout 300 "write tests for module B")
JOB3=$(glm start --dir /path --timeout 300 "write tests for module C")
glm list               # monitor all three
```

### Sequential chain

```bash
glm chain --dir /path --timeout 600 \
  "analyze the codebase and list all API endpoints" \
  "write integration tests for each endpoint found"
```

### DAG pipeline

```bash
cat > pipeline.json << 'EOF'
{
  "steps": [
    {"id": "analyze", "prompt": "analyze the codebase structure"},
    {"id": "tests", "prompt": "write unit tests", "depends_on": ["analyze"]},
    {"id": "docs", "prompt": "write API documentation", "depends_on": ["analyze"]},
    {"id": "review", "prompt": "review all changes", "depends_on": ["tests", "docs"]}
  ]
}
EOF
glm pipeline pipeline.json
```

Steps without `depends_on` run in parallel. A failed step causes all steps that depend on it to be skipped.

### Chain validation and auto-retry

Both chains and pipelines support per-step output validation and automatic retries.

**Validation rules** check a step's stdout after execution. All conditions use AND logic - every condition must pass.

| Field | Type | Effect |
| --- | --- | --- |
| `contains` | `[]string` | All strings must appear in stdout |
| `not_contains` | `[]string` | None of the strings may appear in stdout |
| `matches` | `string` | Stdout must match the regular expression |

**Auto-retry** re-runs the step with an extended prompt when validation fails or the step exits non-zero.

| Field | Type | Effect |
| --- | --- | --- |
| `max_attempts` | `int` | Maximum number of attempts (including the first run) |
| `feedback` | `string` | Text appended to the prompt on each retry |

**Gate steps** (pipeline only) validate the output of upstream steps without invoking Claude. Use them as checkpoints between pipeline stages. A gate step requires `"type": "gate"`, at least one `depends_on` entry, and a `validate` rule.

#### Pipeline with validation and a gate step

```json
{
  "steps": [
    {
      "id": "analyze",
      "prompt": "Analyze the codebase and list all exported functions",
      "validate": {
        "contains": ["func "],
        "not_contains": ["ERROR"]
      },
      "retry": {
        "max_attempts": 2,
        "feedback": "Your previous output did not list any exported functions. Try again, focusing on Go source files."
      }
    },
    {
      "id": "check-analysis",
      "type": "gate",
      "depends_on": ["analyze"],
      "validate": {
        "matches": "func [A-Z]"
      }
    },
    {
      "id": "write-tests",
      "prompt": "Write unit tests for the exported functions identified above",
      "depends_on": ["check-analysis"]
    }
  ]
}
```

#### MCP chain with steps, validation and retry

```json
{
  "tool": "glm_chain",
  "dir": "/home/user/project",
  "timeout": 600,
  "steps": [
    {
      "prompt": "List all TODO comments in the codebase",
      "validate": {
        "contains": ["TODO"],
        "not_contains": ["no TODOs found"]
      },
      "retry": {
        "max_attempts": 3,
        "feedback": "The output did not contain any TODO comments. Search all .go files recursively and list every TODO line."
      }
    },
    {
      "prompt": "For each TODO found, create a GitHub issue stub"
    }
  ]
}
```

## Configuration

### Config file

Path: `~/.config/GoLeM/glm.toml`

```toml
model = "glm-5.1"
opus_model = "glm-5.1"
sonnet_model = "glm-5.1"
haiku_model = "glm-5.1"
permission_mode = "bypassPermissions"
proxy_enabled = true
proxy_idle_timeout = 600
proxy_port = 0
effort = ""
exclude_dynamic_sections = false
system_prompt = ""   # optional default system prompt injected into every invocation

[routing]
light = "glm-5.1"
medium = "glm-5.1"
heavy = "glm-5.1"

[models]
"glm-5.1" = 10   # per-model concurrency limit (0 = unlimited)
```

The `api_rps` and `max_parallel` keys are silently accepted for backward
compatibility but have no effect. Use the `[models]` section above.

Priority: CLI flag > environment variable > glm.toml > hardcoded default.

```bash
glm config show                   # all values with source labels: (default), (config), (env)
glm config set model glm-5.1
glm config set proxy_port 18080
glm config set debug true
```

`config set` accepts: `model`, `opus_model`, `sonnet_model`, `haiku_model`,
`permission_mode`, `debug`, `proxy_enabled`, `proxy_idle_timeout`, `proxy_port`,
`effort`, `system_prompt`, `exclude_dynamic_sections`. Other keys (e.g. the
`[routing]` / `[models]` sections) must be edited in `glm.toml` directly.

### Environment variable overrides

| Variable | Config key |
| --- | --- |
| `GLM_MODEL` | `model` |
| `GLM_OPUS_MODEL` | `opus_model` |
| `GLM_SONNET_MODEL` | `sonnet_model` |
| `GLM_HAIKU_MODEL` | `haiku_model` |
| `GLM_PERMISSION_MODE` | `permission_mode` |
| `GLM_DEBUG` | debug logging |
| `GLM_ROUTING_LIGHT` | `routing.light` |
| `GLM_ROUTING_MEDIUM` | `routing.medium` |
| `GLM_ROUTING_HEAVY` | `routing.heavy` |
| `GLM_MODEL_CONCURRENCY` | `[models]` (format: `"name:N,name2:M"`) |
| `GLM_EFFORT` | `effort` |
| `GLM_SYSTEM_PROMPT` | `system_prompt` |
| `GLM_EXCLUDE_DYNAMIC_SECTIONS` | `exclude_dynamic_sections` |
| `GLM_LOG_FORMAT` | log format (`json` or human) |
| `GLM_LOG_FILE` | log file path |

### File locations

| File | Purpose |
| --- | --- |
| `~/.config/GoLeM/glm.toml` | Main configuration |
| `~/.config/GoLeM/zai_api_key` | Z.AI API key (mode 0600) |
| `~/.claude/subagents/<project>/<job>/` | Job storage |
| `~/.claude/settings.json` | MCP server registration |
| `~/.claude/CLAUDE.md` | System prompt injection |

## MCP Server

GoLeM can run as an MCP server for Claude Code, exposing tools via JSON-RPC over stdio. This lets Claude Code use GoLeM natively without shell commands.

```bash
glm mcp    # start the MCP server (normally auto-started by Claude Code)
```

### Tools

| Tool | Description |
| --- | --- |
| `glm_run` | Sync subagent execution |
| `glm_start` | Async subagent execution |
| `glm_status` | Check job status |
| `glm_result` | Get job output |
| `glm_list` | List jobs with filters |
| `glm_kill` | Terminate job |
| `glm_chain` | Sequential chain |
| `glm_pipeline` | DAG pipeline with parallel execution |

### Registration

The installer auto-registers. For manual registration, add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "golem": {
      "command": "/absolute/path/to/glm",
      "args": ["mcp"]
    }
  }
}
```

## System Prompt Example

After `glm _install`, this section is injected into `~/.claude/CLAUDE.md`. Copy and customize it to tune how the host Claude Code session delegates work to GLM agents.

```markdown
<!-- GLM-SUBAGENT-START -->
## GLM Subagent Bridge - Usage Instructions

You have access to `glm` - a CLI tool that spawns Claude Code agents powered by GLM models via Z.AI.
Each golem is a full Claude Code instance with file access, test execution, and MCP server support.

### Decision: GoLeM vs built-in Agent tool

Use **GoLeM** (`glm`) when:

- Task is routine (boilerplate, simple refactors, test generation, documentation)
- You need true parallel execution (multiple independent tasks)
- Cost optimization matters - GLM models are cheaper than Opus
- Task doesn't require Opus-level reasoning

Use the **built-in Agent tool** when:

- Task requires deep architectural reasoning
- Task needs access to conversation context
- Task involves user interaction or approval workflows
- You need specialist agents (kube-pilot, chart-builder, etc.)

### Command reference

#### Sync execution (blocks, returns result)

```bash
glm run --dir /absolute/path --timeout 300 "your task description"
```

#### Async execution (returns job ID immediately)

```bash
JOB=$(glm start --dir /absolute/path --timeout 300 "your task description")
```

#### Monitoring async jobs

```bash
# Check specific job
glm status $JOB_ID

# List all jobs (shows status, duration, proxy stats)
glm list

# Get output when status is "done"
glm result $JOB_ID

# See file changes made by the agent
glm log $JOB_ID
```

#### Sequential chain (output flows to next step)

```bash
glm chain --dir /absolute/path --timeout 600 \
  "analyze the codebase" \
  "implement changes based on analysis"
```

#### DAG pipeline (parallel steps with dependencies)

Write a JSON file, then execute:

```bash
glm pipeline /path/to/pipeline.json
```

### Critical rules

- **Always use `--dir` with absolute path** - golems work in that directory
- **Always use `--timeout`** - prevents runaway jobs (300s for quick tasks, 600s for longer)
- **Flags before prompt** - prompt is positional and must come last
- **Poll async jobs** - after `glm start`, check with `glm status` or `glm list`
- **Use `--json` when parsing** - structured output for programmatic processing
- **Rate limiting is automatic** - per-model concurrency limits live in the `[models]` section of `glm.toml`; the proxy queues and retries on 429/5xx

### Error handling

- Job status `failed` -> check `glm result JOB_ID` for error details
- Job status `timeout` -> increase `--timeout` or break the task into smaller pieces
- Exit code 124 -> timeout
- Exit code 1 -> user error (bad arguments, missing config)
- Exit code 127 -> dependency missing (claude CLI not found)

### Parallel execution pattern

```bash
# Launch independent tasks in parallel
JOB1=$(glm start --dir /path --timeout 300 "task 1")
JOB2=$(glm start --dir /path --timeout 300 "task 2")
JOB3=$(glm start --dir /path --timeout 300 "task 3")

# Monitor progress
glm list

# Collect results when all are done
glm result $JOB1
glm result $JOB2
glm result $JOB3
```

### Complexity-based routing

```bash
# Auto-detect complexity (default)
glm run --dir /path --timeout 300 "task"

# Force specific tier
glm run --dir /path --timeout 300 --tier light "simple task"
glm run --dir /path --timeout 600 --tier heavy "complex architectural task"
```
<!-- GLM-SUBAGENT-END -->
```

## Architecture

```text
Opus / MCP client -> glm CLI -> claude subprocess -> glm proxy -> Z.AI -> GLM-5.1
                            ↳ file-based job storage (~/.claude/subagents/<project>/<job>/)
                            ↳ per-model concurrency via [models] in glm.toml
                            ↳ exponential-backoff retry on 429/5xx, Unix-socket slot wakeups
```

**session** is `syscall.Exec` - `glm session` replaces itself with the `claude` process. No job directory, no output capture.

**run** is synchronous. Creates a job dir, runs `claude -p --no-session-persistence --output-format json` with a timeout context, captures output, parses result, prints stdout, removes the job dir.

**start** does the same but in a goroutine - job ID is printed immediately.

**chain** runs steps sequentially. The result of step N is prepended to the prompt of step N+1 as context. Each step supports an optional `validate` rule (checked against stdout) and a `retry` config that re-runs the step with extended feedback on validation failure or non-zero exit.

**pipeline** parses a JSON DAG, runs steps with no dependencies in parallel, and passes outputs to dependent steps. Steps support `validate`, `retry`, and `type: "gate"`. Gate steps validate upstream output without invoking Claude and block downstream steps if the check fails.

The project ID is derived from the working directory: `{basename}-{crc32(path)}`. This groups jobs by project without collisions.

## Project Structure

```text
cmd/glm/main.go            CLI entry point (17 subcommands)
internal/
  artifact/                Typed artifact persistence (save/load)
  channel/                 HTTP notification client and event bridge
  claude/                  Claude CLI execution and JSON output parsing
  cmd/                     Subcommand implementations
  config/                  TOML config, env overrides, multi-provider support
  dag/                     DAG pipeline engine (dependency resolution, parallel execution)
  e2e/                     End-to-end tests (go:build e2e)
  event/                   Publish/subscribe event bus
  exitcode/                Exit code constants and typed errors
  job/                     Job lifecycle, status FSM, reconciliation
  log/                     Structured logging (human/JSON)
  mcp/                     MCP server (JSON-RPC over stdio, 8 tools)
    tools/                 MCP tool handlers (8 tools)
  proxy/                   Rate-limiting reverse proxy for Z.AI API
  retry/                   Exponential backoff with jitter
  router/                  Complexity-based model routing
  slot/                    File-based concurrency control (flock + notify)
```

## Development

```bash
go build -o glm ./cmd/glm/     # build binary
go test ./...                  # unit tests (no API calls)
go test -race ./...            # with race detector
go vet ./...                   # static analysis
go test -tags e2e ./internal/e2e/... -v   # e2e tests (requires claude CLI and API key)
```

Conventions:

- TDD: tests first, implementation second
- No external dependencies - stdlib only
- No mocks for internal code - real files, `t.TempDir()`, `httptest.NewServer`
- Errors prefixed: `err:user`, `err:config`, `err:timeout`

## Platforms

Linux (amd64, arm64), macOS (amd64, arm64), WSL.

## Exit Codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | User error (bad arguments, invalid config) |
| 3 | Not found (job does not exist) |
| 124 | Timeout |
| 127 | Dependency missing (claude CLI not found) |

Errors are written to stderr as `err:<category> "message"` for programmatic parsing.

## Troubleshooting

```bash
glm doctor    # runs all 7 checks and shows details
```

| Symptom | Fix |
| --- | --- |
| `claude CLI not found` | Install Claude Code and add it to PATH |
| `credentials not found` | Run `glm _install` |
| Empty output after run | Check `glm result JOB_ID` or `~/.claude/subagents/.../stderr.txt` |
| `glm: command not found` | Ensure `$(go env GOPATH)/bin` is in your PATH |
| Jobs stuck in queued | Run `glm doctor` to inspect slots; clear stale jobs with `glm clean --days 0` |
| Status `permission_error` | Add `--unsafe` or set `permission_mode` to `bypassPermissions` |

## Migration notes

The default model has changed from `glm-5` to `glm-5.1`. To pin the previous
model, add `model = "glm-5"` to `~/.config/GoLeM/glm.toml`.
