# Changelog

## v1.3.0 - 2026-05-22

- Golem-scoped MCP servers. Golems can now be given extra MCP servers (for
  example Z.AI's image-vision MCP) without registering them in the host
  `~/.claude/settings.json`, so the orchestrating Claude Code session is left
  untouched. The servers are passed to each golem's `claude` subprocess via
  `--mcp-config`, configurable three ways:
  - `mcp_config` in `glm.toml` (a path or inline JSON; default for all golems);
  - the `--mcp-config FILE` flag on `run`/`start`/`chain`/`session` (per-call,
    overrides the config default);
  - the `GLM_MCP_CONFIG` environment variable.
  `mcp_strict = true` (or `GLM_MCP_STRICT`) adds `--strict-mcp-config`, so a
  golem uses only the supplied servers and ignores all other MCP configuration.

## v1.2.3 - 2026-05-22

- Global flags placed before the subcommand are now tolerated. Previously
  `glm --opus MODEL session` failed with "Unknown command: --opus" because the
  first token was taken as the command; the real subcommand is now located and
  the leading flags are folded into its argument list, so both
  `glm session --opus MODEL` and `glm --opus MODEL session` work. When no
  subcommand can be found, the error now hints that flags go after the command.

## v1.2.2 - 2026-05-22

Patch: version constant aligned with the tag (v1.2.1 shipped a binary that
self-reported 1.2.0).

## v1.2.0 - 2026-05-21

Major release. All new subsystems land together - MCP, DAG pipelines, smart
routing, system prompts/constraints, event bus, typed artifacts, retry,
Unix-socket slot notifications. The release prep round also fixed a number
of small but user-visible UX bugs.

### Highlights

- **MCP server**: `glm mcp` exposes eight JSON-RPC tools (`glm_run`,
  `glm_start`, `glm_status`, `glm_result`, `glm_list`, `glm_kill`,
  `glm_chain`, `glm_pipeline`) over stdio. `_install` auto-registers it
  under `~/.claude/settings.json`, so a host Claude Code session can drive
  GLM golems natively.
- **DAG pipelines**: `glm pipeline FILE.json` runs JSON DAGs with
  topological scheduling, parallel execution, `validate` rules, `retry`
  configs, and zero-cost `gate` steps that block downstream work when an
  upstream output fails its check.
- **Chain validation and auto-retry**: `glm chain` steps and the MCP
  `glm_chain` tool now accept `validate` and `retry` blocks per step.
- **Complexity-based routing**: `--tier {light|medium|heavy|auto}` picks
  the right model per task. Configurable via `[routing]` in `glm.toml`
  and `GLM_ROUTING_*` env variables.
- **Per-model concurrency**: `[models]` section in `glm.toml`
  (`"glm-5.1" = 10`) replaces the global `api_rps` / `max_parallel` knobs
  (silently ignored for backward compatibility).
- **System prompts and constraints**: `--system-prompt TEXT` and
  `--constraint KEY` (repeatable; `readonly`, `no-create`, `plan-first`,
  `scope:<path>`). Configurable via `system_prompt =` in TOML and per
  MCP tool input.
- **Event bus, typed artifacts, retry with jitter, Unix-socket slot
  wakeups**: foundational packages under `internal/event`,
  `internal/artifact`, `internal/retry`, `internal/slot`.

### Release-prep fixes

- `glm help` now prints to stdout. Previously it went to stderr, which
  broke `glm help > doc.txt`.
- `--dir` and `--timeout` are now accepted as long aliases for `-d` and
  `-t`. The README has been recommending the long form for some time; it
  finally works.
- `glm chain` no longer treats long-flag values as additional prompts. The
  prompt extractor used a hard-coded short-flag list that missed the new
  aliases.
- `glm _uninstall` no longer wipes the config directory (and the API key
  inside it) when the user declines the "Remove credentials?" prompt.
- `glm config show` now lists sixteen keys instead of ten -
  `proxy_enabled`, `proxy_port`, `proxy_idle_timeout`, `effort`,
  `system_prompt`, and `exclude_dynamic_sections` were missing.
- `glm config set` accepts the same expanded vocabulary, with boolean and
  integer values validated.
- Shell completions now cover `pipeline`, `mcp`, `--tier`,
  `--system-prompt`, `--constraint`, `--continue-on-error`, and the
  `default` permission mode. The stale `cancelled` status value was
  replaced with the real `timeout` / `killed` set.
- Architecture diagram redrawn: the rate-limiting proxy is a first-class
  participant; the legacy `max_parallel slots / flock` notation is gone.
- README aligned with actual `glm doctor` output and the real
  `config set` vocabulary.
- `MIT LICENSE` added (the project previously shipped without one).
- Docker-based end-to-end smoke test (`test/Dockerfile.smoke` +
  `test/docker-smoke.sh`) added; it builds glm from source in a fresh
  Alpine image, installs the Claude Code CLI, runs `_install`, `doctor`,
  `run`, `chain`, the MCP server, and `_uninstall` against a real Z.AI
  account.

### Breaking changes

- `api_rps` and `max_parallel` config keys are silently ignored. Use the
  `[models]` section in `glm.toml` for per-model concurrency limits.
- Several internal packages were added or significantly extended
  (`event`, `artifact`, `dag`, `mcp`, `router`, `retry`, `prompt`,
  `validation`). External Go consumers depending on `internal/` were
  never supported, but the layout has changed substantially.

### Migration

If you were on v1.x:

- Run `glm _install` once after upgrading; it is idempotent and brings
  the CLAUDE.md / settings.json injections in line with v1.2.0.
- If your `glm.toml` set `api_rps`, move the limit into a `[models]`
  section (e.g. `"glm-5.1" = 5`).
- Default model is `glm-5.1` (unchanged since v1.1.x; if you pinned
  `glm-5`, keep the explicit `model = "glm-5"` line).

## v1.1.x and earlier

See the git log for the pre-v1.2.0 changelog.
