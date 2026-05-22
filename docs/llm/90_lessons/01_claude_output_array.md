---
id: claude-output-array
kind: lesson
---

# Claude Code output-shape flip blanked golem output (v1.4.0)

Context: shipped in v1.4.0. A Claude Code update changed what
`claude --output-format json` emits, and every golem silently returned an
empty result until the parser was taught both shapes.

See also: [../20_claude_execution.md](../20_claude_execution.md).

## Measured gap

| Item | Expected | Actual (before fix) |
|------|----------|---------------------|
| `glm run` result text | the agent's final answer | empty `stdout.txt` |
| Output shapes handled | every shape claude emits | 1, while claude now emitted 2 |
| Failure signal | a parse error | none -- silent blank, exit 0 |

The old `--output-format json` was a single JSON object with top-level
`result` and `messages[]`. Current Claude Code (2.1+) emits an **array of
typed events** instead: `[{type:system}, {type:assistant, message:{...}},
{type:result, result:"..."}]`. The parser `json.Unmarshal`-ed into the object
struct; against an array that fails, and the error path wrote an empty
`stdout.txt` -- so `glm run`/`start`/`chain` returned nothing with a success
exit code. The blank was indistinguishable from "the agent said nothing".

## Root causes

1. The parser hard-coded one wire shape of an **external, independently
   versioned tool** (`claude` is a separate binary) and assumed it was stable.
   Its output format is not a frozen contract.
2. A shape mismatch degraded to empty-but-successful instead of a loud error,
   so the regression shipped invisibly.

## Rules

1. When parsing an external tool's output, sniff and support every shape it is
   known to emit; do not bind to one. `extractResult` (`parser.go:97`) now
   branches on the first non-space byte (`[` = array stream, else legacy
   object) and is covered by tests for both forms.
2. A parse that finds no result is suspicious: support every known shape rather
   than silently writing empty output. Treat "external format changed" as a
   first-class, tested failure mode, not an edge case.
