---
id: prompt
kind: spec
touches: internal/prompt/
---

# Prompt - Constraint Expansion and System Prompt Assembly

See also: [10_cli.md](10_cli.md) · [31_mcp_tools.md](31_mcp_tools.md) · [40_dag.md](40_dag.md).

## Purpose

`internal/prompt` provides two functions used by every subagent invocation:
expanding named constraint keys into instruction text, and assembling a final
system prompt string from constraints and an optional base prompt.

## Constraint vocabulary (`ExpandConstraint`)

`ExpandConstraint(key string) (string, error)`

| Key | Expanded text |
|-----|---------------|
| `readonly` | `You MUST NOT create, modify, or delete any files. You may only read files and report findings.` |
| `no-create` | `You MUST NOT create any new files. You may only read or modify existing files.` |
| `plan-first` | `Before making any changes, you MUST output a detailed plan of what you intend to do and wait for approval.` |
| `scope:<path>` | `You MUST only operate on files under the path: <path>. Do not read or modify any files outside this directory.` |

`scope:` requires a non-empty path after the colon; an empty path returns
`err:user`. Unknown keys return `err:user "unknown constraint %q"`.

## System prompt assembly (`AssembleSystemPrompt`)

`AssembleSystemPrompt(constraints []string, systemPrompt string) (string, error)`

Assembly rules:
- Each constraint key is expanded via `ExpandConstraint`. Unknown keys return
  an error immediately.
- Expanded constraint texts are joined with `"\n\n"`.
- `systemPrompt` is trimmed of leading/trailing whitespace.
- When both are present: `constraintBlock + "\n\n" + trimmedPrompt`.
- When only constraints: `constraintBlock`.
- When only systemPrompt: `trimmedPrompt`.
- When neither: empty string.

The assembled string is passed to `claude.Config.SystemPrompt`, which becomes
the `--append-system-prompt` flag value for the `claude` subprocess.

## Call sites

- `cmd.BuildClaudeConfig` (`internal/cmd/execute.go`) - called for every
  `run`, `start`, and `chain` invocation.
- `cmdPipeline` (`cmd/glm/main.go`) - called once before creating the DAG
  executor; the assembled prompt is passed to all pipeline steps.
- `tools.PipelineHandler.Handle` (`internal/mcp/tools/pipeline.go`) - same
  as above for the MCP pipeline tool.

## Priority

CLI `--system-prompt` flag takes precedence over `cfg.SystemPrompt`. Both
are passed to `AssembleSystemPrompt` as `systemPrompt`; the caller selects
the non-empty one. Constraints from `--constraint` flags are always applied
on top of the system prompt regardless of source.
