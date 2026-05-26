---
id: index
kind: index
---

# GoLeM LLM Docs

Operational reference graph for the `glm` CLI. English only. Each file is
self-contained, verifiable against the code as it stands in HEAD, and sized
to load cheaply. Every entry declares `id` and `kind` in front-matter.

See also: [handoff.md](handoff.md).

## Entries

- [handoff.md](handoff.md) - Next-session action plan. Read this first at
  session start.

- [10_cli.md](10_cli.md) - All subcommands, flags, exit codes, and dispatch
  logic (`cmd/glm/main.go`, `internal/cmd/`). Read before adding or changing
  a command.

- [11_config.md](11_config.md) - TOML schema, environment overrides,
  `[routing]`/`[models]`/`[providers.*]` sections, API key loading
  (`internal/config/`). Read before touching config parsing or adding a new
  setting.

- [20_claude_execution.md](20_claude_execution.md) - Claude subprocess
  invocation: env/flag building, raw.json parsing, changelog generation
  (`internal/claude/`). Read before changing how subagents are launched.

- [21_job_lifecycle.md](21_job_lifecycle.md) - Job directory layout, status
  FSM, atomic writes, project IDs, reconciliation (`internal/job/`). Read
  before modifying job state handling.

- [22_slot.md](22_slot.md) - Concurrency control: flock-based counter, Unix
  socket wakeups (`internal/slot/`). Read before changing concurrency
  primitives.

- [23_proxy.md](23_proxy.md) - Rate-limiting reverse proxy, per-model
  registry, retry with jitter, lifecycle management
  (`internal/proxy/`, `internal/retry/`). Read before changing proxy or retry
  behavior.

- [30_mcp.md](30_mcp.md) - JSON-RPC 2.0 server and stdio transport
  (`internal/mcp/`). Read before changing MCP protocol handling.

- [31_mcp_tools.md](31_mcp_tools.md) - All eight MCP tool handlers: types,
  schemas, logic (`internal/mcp/tools/`). Read before adding or modifying an
  MCP tool.

- [40_dag.md](40_dag.md) - DAG pipeline format, validation, Kahn-based
  topological sort, concurrent scheduler, gate steps
  (`internal/dag/`). Read before changing pipeline execution.

- [41_router.md](41_router.md) - Prompt complexity estimation, tier-to-model
  mapping, `--tier` flag integration (`internal/router/`,
  `internal/cmd/routing.go`). Read before changing routing.

- [42_prompt.md](42_prompt.md) - Constraint expansion, system prompt assembly
  (`internal/prompt/`). Read before adding a new constraint keyword.

- [50_event.md](50_event.md) - Pub/sub event bus, event types, nil-safe
  semantics (`internal/event/`). Read before wiring new event producers or
  consumers.

- [51_artifact.md](51_artifact.md) - Typed artifact persistence: text, JSON,
  file_ref types (`internal/artifact/`). Read when working on DAG step
  outputs.

- [52_validation.md](52_validation.md) - Output validation rules:
  contains/not_contains/matches (`internal/validation/`). Read when adding
  validation to a new context.

- [60_logging.md](60_logging.md) - Leveled logger, human/JSON formats, exit
  codes and error categories (`internal/log/`, `internal/exitcode/`). Read
  before changing logging or error handling.

- [90_lessons/01_claude_output_array.md](90_lessons/01_claude_output_array.md) -
  post-mortem: a Claude Code output-shape change silently blanked golem output
  (v1.4.0). Read before touching `claude --output-format json` parsing.

- [90_lessons/02_mcp_oneof_top_level.md](90_lessons/02_mcp_oneof_top_level.md) -
  post-mortem: a top-level `oneOf` in `glm_chain`'s input_schema crashed every
  Claude Code session at first model call (v1.5.2). Read before adding any
  JSON-Schema disjunction to an MCP tool that rides through the Anthropic API.

## Kinds

| kind | Meaning |
|------|---------|
| `index` | The `00_index.md` file - entry point for the graph. |
| `spec` | Interface, contract, domain rules - read before changing related code. |
| `guide` | Synthesized how-to or action plan (handoff). |
| `log` | Devlog entry, dry facts about what happened in a session. |
| `lesson` | Post-mortem; rules born from a specific failure. |

## Update rule

Any change to a public contract (function signature, file format, config key,
MCP tool schema, exit code) updates the matching spec in the same commit.
Adding a new package adds a new `NN_<domain>.md` in the same commit.

## Validation

`./validate.sh` checks every relative link in `docs/llm/`; exit 0 = clean.
