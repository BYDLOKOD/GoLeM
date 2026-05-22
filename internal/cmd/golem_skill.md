---
name: golem
description: Delegate bulk or parallel work to GLM subagents ("golems") through the `glm` CLI and its MCP tools (server name "golem"), powered by Z.AI. Use whenever the user says "golem" / "голем", asks to spawn parallel agents or subagents, wants to offload high-volume work (generate tests or docs across many modules, codebase-wide edits or migrations, OCR / vision over many images), or wants cheaper agents so the Opus usage limit is not spent on routine work.
---

# GoLeM - delegating work to GLM subagents

`glm` spawns autonomous Claude Code agents ("golems") that run on GLM models through Z.AI instead of Anthropic. Each golem is a full Claude Code instance: it reads and edits files, runs commands and tests, uses MCP servers, and (by default) can read images. Golems exist to absorb bulk, parallelizable, or routine work so the orchestrating Opus session is spent on planning and reasoning rather than on volume - and so the Opus usage limit is not burned on mechanical tasks.

This skill is the authoritative operating manual. Follow the rules exactly; the failure modes below are real and each rule prevents one.

## Decision: golem vs the built-in Agent/Task tool

Use a **golem** (`glm`) when ALL of these hold:

- the work is high-volume, repetitive, or mechanical (generate tests/docs for many modules, rename an API across hundreds of files, migrate a framework version, transform many files, OCR a stack of scans);
- the units of work are independent and can run in parallel;
- the task does not require Opus-level reasoning;
- the task does not need this conversation's in-memory context.

Use the **built-in Agent/Task tool** (NOT a golem) when the subtask needs deep reasoning, must share the current conversation's context, or involves interactive approval. Do the work **directly** (no delegation) when it is a small, one-shot change.

A golem starts cold: it sees only the working directory and the prompt you give it. It does not see this conversation. Put everything it needs into the prompt and the `--dir`.

## Two ways to drive golems

1. **MCP tools** (preferred when orchestrating from inside Claude Code). The `golem` MCP server exposes eight tools: `glm_run`, `glm_start`, `glm_status`, `glm_result`, `glm_list`, `glm_kill`, `glm_chain`, `glm_pipeline`. Call them directly.
2. **The `glm` CLI** via the Bash tool. Same capabilities, useful in shells and scripts.

Both spawn identical golems. Prefer the MCP tools when they are available; otherwise use the CLI.

## CLI commands

| Command | Usage | What it does |
| --- | --- | --- |
| `run` | `glm run --dir DIR --timeout SEC "prompt"` | Synchronous: blocks until the golem finishes, prints its result. |
| `start` | `JOB=$(glm start --dir DIR --timeout SEC "prompt")` | Prints a job ID immediately and runs the golem; poll for the result. |
| `chain` | `glm chain --dir DIR --timeout SEC "p1" "p2" ...` | Runs prompts in sequence; each step's output is prepended to the next prompt. |
| `pipeline` | `glm pipeline FILE.json` | Runs a JSON DAG: independent steps in parallel, dependents after their inputs. |
| `status` | `glm status JOB_ID` | Prints the job status. |
| `result` | `glm result JOB_ID` | Prints the job's text output. |
| `log` | `glm log JOB_ID` | Prints the file-change changelog (edits, writes, deletes). |
| `list` | `glm list [--status S] [--since D]` | Lists jobs for the current project, plus proxy stats. |
| `kill` | `glm kill JOB_ID` | Terminates a running golem. |
| `clean` | `glm clean [--days N]` | Removes old finished jobs. |
| `doctor` | `glm doctor` | Health check (claude CLI, API key, Z.AI reachability, models, slots, platform, proxy). |
| `session` | `glm session [flags] [claude flags]` | Opens an interactive Claude Code session on a GLM model. |
| `config` | `glm config {show \| set KEY VALUE}` | Views or edits configuration. |

## Critical rules (each prevents a real failure)

- **Always pass an absolute `--dir`.** Golems operate inside that directory. A relative or wrong directory makes the golem edit the wrong files. Example: `--dir "$PWD"` or `--dir /home/user/project`.
- **Always pass `--timeout SEC`.** Without a sensible timeout a golem can hang and block a slot. Use `--timeout 300` (5 min) for quick tasks, `--timeout 600`-`1800` for large ones. Exceeding the timeout yields exit code 124.
- **The prompt is positional and must be the LAST argument, quoted.** Put every flag before the prompt. (Flags are also accepted before the subcommand, e.g. `glm --opus MODEL run ... "prompt"`, but the prompt always comes last.)
- **`glm start` is asynchronous: poll, then read.** After it prints a job ID, check `glm status JOB_ID` (or `glm list`) until the status is `done`, then read `glm result JOB_ID`. Do not assume the result is ready immediately.
- **Give the golem self-contained instructions.** It has no access to this conversation. State the goal, the relevant files, and the acceptance criteria in the prompt.
- **Use `--json`** (`glm run --json ...`, `glm list --json`, etc.) when you will parse the output.
- **Permission mode defaults to `acceptEdits`**: the golem applies file edits without asking but pauses for confirmation before destructive shell commands. For full autonomy on a trusted task, add `--unsafe` (or set `permission_mode = "bypassPermissions"` in `glm.toml`). A golem run non-interactively that hits a confirmation it cannot answer will stop; pass `--unsafe` if the task legitimately needs destructive operations.

## Flags (apply to run / start / chain / session)

| Flag | Meaning |
| --- | --- |
| `-d, --dir DIR` | Working directory (absolute path). |
| `-t, --timeout SEC` | Timeout in seconds. |
| `-m, --model MODEL` | Set all three model slots. |
| `--opus / --sonnet / --haiku MODEL` | Set an individual slot. |
| `--tier {light\|medium\|heavy\|auto}` | Pick the model by task complexity (default `auto`). |
| `--unsafe` | Bypass all permission checks. |
| `--mode MODE` | Set permission mode (`acceptEdits`, `bypassPermissions`, `plan`, `default`). |
| `--system-prompt TEXT` | Extra system-prompt instructions for this golem. |
| `--constraint KEY` | Add a behavior constraint (repeatable; see below). |
| `--mcp-config FILE` | Attach extra MCP servers to this golem only (file path or inline JSON). |
| `--json` | JSON output. |

## Constraints (sandbox a golem)

`--constraint KEY` (repeatable) restricts a golem's behavior. They compose.

| Constraint | Effect |
| --- | --- |
| `readonly` | The golem may only read and report; no writes, edits, or deletes. |
| `no-create` | The golem may modify existing files but must not create new ones. |
| `plan-first` | The golem must output a plan and wait for approval before changing anything. |
| `scope:<path>` | The golem may only touch files under `<path>`. |

Example: `glm run --dir /repo --timeout 300 --constraint readonly --constraint scope:/repo/api "audit the API package for injection risks"`.

## Vision: reading images

The Z.AI image-vision MCP server is attached to **every golem by default**, so a golem can read screenshots, scans, photographed documents, and diagrams. Just point it at the file in the prompt:

```bash
glm run --dir /abs/dir --timeout 120 "read scan.png and extract all of its text"
```

The golem calls the vision tools (`image_analysis`, `extract_text_from_screenshot`, `video_analysis`, ...). No setup is needed - GoLeM fills in the Z.AI key automatically. Vision needs `npx` and Node >= 22 on the machine; if missing the golem still runs, just without vision tools. Disable vision globally with `vision_mcp = false` in `glm.toml` or the `GLM_VISION_MCP=0` environment variable.

To process many images, fan out one golem per file with `glm start` and collect the results.

## Parallel work

Launch independent golems with `glm start`, monitor with `glm list`, collect with `glm result`:

```bash
JOB1=$(glm start --dir /repo --timeout 300 "write unit tests for the auth module")
JOB2=$(glm start --dir /repo --timeout 300 "write unit tests for the billing module")
glm list
glm result "$JOB1"
glm result "$JOB2"
```

A rate-limiting proxy serializes API calls per model, so launching many golems at once will not trip Z.AI rate limits; surplus requests queue.

## Chains

A chain runs prompts sequentially; the output of step N is prepended to the prompt of step N+1. Use it when a later step needs an earlier step's result:

```bash
glm chain --dir /repo --timeout 600 \
  "list every TODO comment in the codebase" \
  "for each TODO found above, write a short GitHub issue stub"
```

`chain` also accepts `--continue-on-error` to keep going past a failed step.

## Pipelines (DAG)

A pipeline is a JSON file describing steps with dependencies. Steps with no `depends_on` run in parallel; dependents run after their inputs. A failed step skips its dependents.

```json
{
  "steps": [
    {"id": "analyze", "prompt": "analyze the codebase structure"},
    {"id": "tests", "prompt": "write unit tests", "depends_on": ["analyze"]},
    {"id": "docs",  "prompt": "write API docs",  "depends_on": ["analyze"]},
    {"id": "review", "prompt": "review all changes", "depends_on": ["tests", "docs"]}
  ]
}
```

Run with `glm pipeline pipeline.json`.

### Validation, retry, and gate steps

A step may carry a `validate` rule (checked against its stdout) and a `retry` config (re-runs with feedback on failure). A `gate` step validates upstream output without calling a model (a zero-cost checkpoint) and blocks downstream steps if the check fails.

```json
{
  "steps": [
    {
      "id": "list",
      "prompt": "list all exported functions",
      "validate": {"contains": ["func "], "not_contains": ["ERROR"]},
      "retry": {"max_attempts": 2, "feedback": "No functions listed; focus on Go source files."}
    },
    {"id": "gate", "type": "gate", "depends_on": ["list"], "validate": {"matches": "func [A-Z]"}},
    {"id": "tests", "prompt": "write tests for the functions above", "depends_on": ["gate"]}
  ]
}
```

Validation fields: `contains` ([]string, all must appear), `not_contains` ([]string, none may appear), `matches` (regex). All conditions are AND-ed.

## MCP tool inputs

- `glm_run`, `glm_start`: `prompt` (required), `dir`, `timeout`, `model`, `permission_mode`, `system_prompt`, `constraints` ([]string). `glm_run` returns the output; `glm_start` returns a `job_id`.
- `glm_status`, `glm_result`, `glm_kill`: `job_id` (required).
- `glm_list`: optional `status`, `since` (RFC3339).
- `glm_chain`: `prompts` ([]string) OR `steps` (objects with `prompt`, optional `validate`, `retry`); plus `dir`, `timeout`, `model`, `permission_mode`, `continue_on_error`, `system_prompt`, `constraints`. If `steps` is given it takes precedence over `prompts`.
- `glm_pipeline`: `dag` ({steps:[...]}) (required), plus `dir`, `timeout`, `system_prompt`, `constraints`.

Golems spawned through the MCP tools get the same defaults as the CLI, including vision.

## Job lifecycle and status

A job moves through: `queued` -> `running` -> one of `done`, `failed`, `timeout`, `killed`, `permission_error`. Read the final output only after the status is terminal.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | User error (bad arguments, invalid config) |
| 3 | Not found (no such job) |
| 124 | Timeout |
| 127 | Dependency missing (claude CLI not on PATH) |

Errors are printed to stderr as `err:<category> "message"`.

## When something goes wrong

- Status `failed` -> read `glm result JOB_ID` (and the job's `stderr.txt`) for the error.
- Status `timeout` -> raise `--timeout`, or split the task into smaller golems.
- Status `permission_error` -> the golem hit a permission wall; re-run with `--unsafe` or set `permission_mode`.
- Empty output -> run `glm doctor`; check the claude CLI is installed and the Z.AI key is configured.
- Vision tools not working -> ensure `npx` and Node >= 22 are installed; the golem still completes without them.

## Health check

`glm doctor` runs seven checks: `claude_cli`, `api_key`, `zai_reachable`, `models`, `slots`, `platform`, `proxy`. Run it first when diagnosing setup problems.
