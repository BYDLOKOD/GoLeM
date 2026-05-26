---
id: devlog-oneof-fix-session
kind: log
---

# Devlog 03 - the oneOf schema fix session (v1.5.2)

See also: [handoff.md](../handoff.md) · [../90_lessons/02_mcp_oneof_top_level.md](../90_lessons/02_mcp_oneof_top_level.md) · [02_devlog_fix_session.md](02_devlog_fix_session.md).

## The problem

Owner's verdict, verbatim: **"раньше дохло намертво"**. Starting a Claude
Code session with the GoLeM MCP server registered crashed the session at
the first model call:

```
API Error: 400 tools.12.custom.input_schema: input_schema does not support
oneOf, allOf, or anyOf at the top level
```

The `glm_chain` tool schema used `oneOf` at the top level to encode the
"either `prompts` or `steps`" contract. The unit test
`TestChainDefinition_AcceptsEitherPromptsOrSteps` was green because it
asserted the handler's own schema constants, not what Anthropic would do
with them.

## What was already done in the working tree on entry

Prior session had landed three edits but not committed them:

- `internal/mcp/tools/chain.go` - dropped `oneOf`, both fields now plain
  optional, handler keeps the runtime validation.
- `internal/mcp/tools/tools_test.go` - rewrote
  `TestChainDefinition_AcceptsEitherPromptsOrSteps` to assert the
  *absence* of `oneOf`/`allOf`/`anyOf` and of top-level `required`.
- `docs/llm/handoff.md` - added the "Agent error: oneOf - FIXED" section and
  the "Testing gap: no end-to-end MCP verification" note.

The binary on disk (`/home/veschin/go/bin/glm`, version 1.5.1) was from
before those edits, so the fix was not live anywhere.

## What this session did

1. **Built and installed.** `go test ./internal/mcp/...` green, `go vet
   ./...` clean, `go install ./cmd/glm/` rebuilt `/home/veschin/go/bin/glm`.

2. **Verified the schema-half directly.** Spawned a fresh `glm mcp` over
   stdio, sent `initialize` + `notifications/initialized` + `tools/list`,
   parsed the response: every tool's `inputSchema` top level was exactly
   `{type, properties}` (or a subset) - no `oneOf`/`allOf`/`anyOf`
   anywhere. For `glm_chain`, top-level `required` was absent and both
   `prompts` and `steps` were present in `properties`.

3. **Verified the handler-half directly.** Sent `tools/call` for
   `glm_chain` with `{"prompts":["only one"]}`. Got JSON-RPC error -32603
   with `err:user at least 2 prompts are required for a chain` - schema
   validation didn't block, handler reached the runtime check, returned a
   proper tool error.

4. **Verified the round trip through the running Claude Code session.**
   Owner ran `/mcp` to reconnect (the old `glm mcp` process from before
   the rebuild was still holding the stale binary in memory). Invoked
   `mcp__golem__glm_chain` with the same single-prompt input. Got the
   same JSON-RPC -32603 - session stayed alive. That is the failure
   mode `oneOf` produced before, falsified end-to-end.

5. **Added one test.** `TestChainHandler_BothFieldsPresent_StepsTakePrecedence`
   in `internal/mcp/tools/tools_test.go` locks the precedence rule: when an
   input carries both `prompts` and `steps`, the handler must take the
   `steps` branch. The schema declares both fields as optional, so this
   runtime rule is the only thing keeping clients that supply both
   deterministic. The test feeds 1 step + 2 prompts and asserts the error
   message names "steps" (and does not name "prompts") - the only message
   the steps branch produces in that shape.

6. **Bumped the version.** `cmd/glm/main.go` `version = "1.5.2"`.

## Adjacent finding: proxy was on a global semaphore of 1

Owner asked, **"как понять что сейчас големы используют rps прокси
нормально?"**. They were not. The user `glm.toml` carried `api_rps = 10`,
which is a silently-ignored TOML key as of an earlier release - the proxy
constructor falls back to `Concurrency: 1` in global-semaphore mode when no
`[models]` section is present. Effect visible in the proxy log: every
request showed `model=` (no model extracted, because global mode doesn't
extract one) and `wait=` between 1m30s and 3m45s (everything serialized
through a one-slot semaphore).

Fix was local to the owner's machine - no commit:

```toml
# ~/.config/GoLeM/glm.toml (excerpt)
[models]
"glm-5.1" = 10
```

Killed the stale proxy, started a fresh `glm _proxy --port 9999
--idle-timeout 0` on the same port (so existing `glm mcp` processes kept
their cached `ZaiBaseURL`), confirmed `mode=per-model` in `proxy.log`.
Burst-tested 15 parallel POSTs through the proxy: peak `active` hit 10
exactly, hold the line, and the five surplus requests sat in queue and
flushed as the first ten finished (visible as the 2.0-2.6s tail in their
`time_total`). The 10-slot limit holds under load.

Caveat for the owner: per-model mode rejects any model not listed in
`[models]` with HTTP 400 `unknown model: <name>`. Currently only `glm-5.1`
is listed. If the router ever asks for another, that golem dies.

## Hard facts

- 1 code fix landed by the prior session, surfaced and verified end-to-end
  here. 1 new test (both-fields precedence). 1 version bump. 1 lesson, 1
  devlog (this file), handoff updated.
- `go test ./internal/mcp/...` green; `go vet ./...` clean. Full
  `go test ./...` run before release.
- `glm version` still printed `1.5.1` after `go install` against the prior
  session's edits - the version constant was bumped in this session, not
  the previous one. 1.5.2 binary built and verified after the bump.

## Seeds (article angles)

- "The schema is valid JSON-Schema; the provider just won't take it":
  cross-tool serialization keywords whose support varies by consumer are a
  silent compatibility cliff. Unit tests that bind to the schema's own
  constants do not detect this.
- "Your MCP server is held by inode, not by name": rebuilding the binary
  while a session is open does nothing until `/mcp` reconnects. The first
  call after reconnect is the first call that exercises new code.
- "api_rps = 10 silently does nothing": removed configuration keys that
  stay parse-tolerant turn into long-lived performance footguns. The
  proxy was serializing every golem on a single slot for an unknown
  length of time before anyone asked the right question.
