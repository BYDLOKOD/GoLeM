---
id: mcp-oneof-top-level
kind: lesson
---

# MCP tool schema top-level `oneOf` killed Claude Code sessions (v1.5.2)

Context: shipped in v1.5.2. The `glm_chain` tool's `input_schema` carried a
top-level `oneOf` to express that exactly one of `prompts` / `steps` was
required. Anthropic's Messages API rejects `oneOf`/`allOf`/`anyOf` at the
top level of a tool schema with HTTP 400. As a result, any Claude Code
session that tried to invoke the model with the GoLeM MCP server registered
crashed at first model call:

```
API Error: 400 tools.12.custom.input_schema: input_schema does not support
oneOf, allOf, or anyOf at the top level
```

See also: [../31_mcp_tools.md](../31_mcp_tools.md) · [../handoff.md](../handoff.md).

## Measured gap

| Item | Expected | Actual (before fix) |
|------|----------|---------------------|
| Claude Code session with `glm mcp` registered | live until idle/disconnect | killed by 400 on first model call |
| Failure surfaces in `go test ./...` | schema regression caught | all green - unit test never spoke to Anthropic |
| Path that exercised the bug | unit test asserting schema shape | only a real Claude Code session |

The unit test `TestChainDefinition_AcceptsEitherPromptsOrSteps` was happy:
the schema *did* describe the contract. The contract was just not one the
provider accepts. Unit tests bound the schema to the handler's runtime
behavior, never to the constraints of the wire the schema rides on.

## Root causes

1. **The schema encoded a structural rule with a JSON-Schema keyword whose
   support is **provider-specific**, not part of the MCP protocol.**
   `oneOf`/`allOf`/`anyOf` are valid JSON Schema, but Anthropic's Messages
   API tools layer rejects them at the top level. MCP itself does not mandate
   them either way. Binding the contract to `oneOf` couples the tool to a
   serialization detail the consumer may refuse.

2. **No test ever rendered the schema through the consumer that would reject
   it.** Schema shape was asserted against the handler's own constants, and
   the handler's runtime branch on `len(input.Steps) > 0` was tested
   independently. The pair "schema is what we register" + "Anthropic accepts
   it" was never closed - so the regression travelled through CI invisibly
   and only surfaced when an actual `claude` session loaded the server.

## Rules

1. **Express disjunction at runtime, not in the schema, for any tool whose
   schema rides through the Anthropic API.** Declare overlapping optional
   fields and let the handler enforce "exactly one is populated". Reserve
   `oneOf` (and friends) for schemas consumed only by tooling known to
   accept them.

2. **Treat the MCP-server-to-real-Claude-Code round trip as a tested path,
   not a manual one.** Until an end-to-end suite (`internal/e2e/` gated
   behind `go:build e2e`) runs on every PR, every change to a tool schema
   pays the same "ship-and-pray" cost. The unit test
   `TestChainDefinition_AcceptsEitherPromptsOrSteps` was updated in v1.5.2
   to assert the *absence* of `oneOf`/`allOf`/`anyOf` and of top-level
   `required` - it now closes the schema half of the round trip, but the
   wire half is still on the next session's plate.

3. **After rebuilding `glm`, an existing `glm mcp` subprocess keeps the old
   code.** Claude Code launches MCP servers as long-lived stdio children at
   session start and reuses them. Run `/mcp` to reconnect (or restart the
   session) before trusting that a schema fix is live. Confirm by checking
   that the start time of the `glm mcp` process serving the current session
   is later than the binary's mtime.
