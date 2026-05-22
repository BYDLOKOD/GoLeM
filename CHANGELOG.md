# Changelog

## v1.5.1 - 2026-05-23

Nine correctness fixes; no new features. Each shipped test-first, reviewed by
an adversarial critic, and verified under the race detector.

- **Fixed: `status --json` reported a crashed job as `running` forever.** The
  staleness check was gated on a leftover test marker (the job ID containing
  "dead"), so real jobs were never reconciled. It now verifies PID liveness and
  reports `failed` for a dead process.
- **Fixed: `glm clean` removed nothing.** It walked only the legacy flat
  layout, but jobs live under `<project-id>/<job-id>/`. It now walks both
  layouts and prunes a project directory once cleaning empties it.
- **Fixed: one malformed job aborted reconciliation of all others** and left
  the slot counter wrong. Per-job errors are now logged and skipped.
- **Fixed: `proxy_port` was ignored.** `EnsureRunning` hard-coded `--port 0`,
  so a configured port did nothing and every proxy restart bound a new random
  port -- the root cause of intermittent `API Error: ... (ConnectionRefused)`
  in long-lived MCP sessions. A fixed `proxy_port` is now honored end-to-end
  and the daemon rebinds the same port across idle-timeout restarts.
- **Fixed: `glm doctor` reported a healthy API as unreachable.** Only HTTP 200
  counted as reachable, but an unauthenticated HEAD on the API returns 4xx. Any
  completed HTTP response now counts as reachable; only a connection-level
  error is a failure, and it reports the real error instead of always "timed
  out".
- **Fixed: DAG pipelines could leak goroutines on cancellation** (the
  completion buffer was undersized for bounded schedulers), and a gate step
  with no validation rule silently passed everything instead of erroring.
- **Fixed: a TOML value containing quotes could be corrupted** -- the config
  parser stripped a character set rather than a matched surrounding pair.
- **Hardened: the final job status is written atomically** (tmp+rename), so a
  concurrent `status --json` reader never observes a truncated status file.

## v1.5.0 - 2026-05-22

- **GoLeM is now a Claude Code skill instead of a CLAUDE.md injection.**
  `_install` writes the operating manual to `~/.claude/skills/golem/SKILL.md`
  (frontmatter `name: golem`) and removes any legacy `<!-- GLM-SUBAGENT-* -->`
  section it previously injected into `~/.claude/CLAUDE.md`. The skill loads on
  demand - triggered when the user says "golem"/"голем" or asks to delegate -
  rather than occupying context in every session. `_uninstall` removes the
  skill, and `update` rewrites it. The skill is detailed and English-only so a
  golem is driven without guesswork.
- Removed the now-dead `InjectClaudeMD` / `glmSubagentTemplate` /
  `loadGLMTemplate` code paths that the CLAUDE.md injection used.

## v1.4.0 - 2026-05-22

- **Fixed: golem output was blank on current Claude Code.** Recent `claude`
  versions emit `--output-format json` as an array of typed events
  (`[{type:system},{type:assistant},{type:result,result:"..."}]`) rather than a
  single object. The parser expected the object form, so the result text never
  reached `stdout.txt` and `glm run`/`start`/`chain` returned nothing. The
  parser now handles both forms (array and legacy object), reading the result
  from the `type:result` event and tool_use blocks from `type:assistant`
  events - so a Claude Code update flipping the shape no longer blanks output.
- **Z.AI image-vision MCP server is now attached to golems by default.** Golems
  can read screenshots, scans, and diagrams out of the box (`image_analysis`,
  `extract_text_from_screenshot`, `video_analysis`, ...) with no setup. GoLeM
  generates the [server config](https://docs.z.ai/devpack/mcp/vision-mcp-server)
  and fills in the Z.AI API key automatically, writing it to
  `~/.config/GoLeM/golem-vision-mcp.json` at mode 0600 - the key is never put on
  the command line. Requires `npx` and Node >= 22; if either is missing the golem
  still runs without the vision tools (claude tolerates a failing MCP server).
  Disable with `vision_mcp = false` in `glm.toml` or `GLM_VISION_MCP=0`.
- **Fixed: `--mcp-config` swallowed the prompt.** claude's `--mcp-config` is
  variadic and, placed before the positional prompt, consumed the prompt as
  another config value (introduced in v1.3.0). The prompt is now separated with
  `--` whenever an MCP config is attached. Multiple MCP configs (the built-in
  vision plus a user-supplied one) are passed as repeated `--mcp-config` flags,
  which claude accumulates.

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
