---
id: dag
kind: spec
touches: internal/dag/
---

# DAG - Pipeline Format, Scheduler, Gate Steps, Retry

See also: [31_mcp_tools.md](31_mcp_tools.md) · [51_artifact.md](51_artifact.md) · [52_validation.md](52_validation.md).

## File loading (`dag/pipeline.go`)

`dag.LoadDAGFromFile(path string) (*DAG, error)` is the exported entry point
for loading a DAG from disk. It reads the file, checks the extension, and
delegates to the private `loadDAGFromJSON(data []byte)` for JSON parsing.
Only `.json` is supported; other extensions return
`fmt.Errorf("dag: unsupported file format ...")`.

`pipeline.go` is intentionally small -- it isolates serialization from the
DAG type logic in `dag.go` and the execution logic in `executor.go`.

## Pipeline file format

`glm pipeline FILE` loads a `.json` file via `LoadDAGFromFile`. The top-level
structure:

```json
{
  "steps": [
    {
      "id": "fetch",
      "prompt": "Fetch the data from ...",
      "depends_on": [],
      "model": "",
      "timeout": 0,
      "type": "",
      "validate": {"contains": ["SUCCESS"], "not_contains": ["ERROR"], "matches": ""},
      "retry": {"max_attempts": 3, "feedback": "Output must contain SUCCESS"}
    }
  ]
}
```

All fields except `id` and `prompt` are optional.

## Step fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier within the DAG (required). |
| `prompt` | string | Instruction for the claude subprocess (required for non-gate steps). |
| `depends_on` | []string | IDs of steps that must complete before this one. |
| `model` | string | Model override for this step (empty = executor default). |
| `timeout` | int | Per-step timeout in seconds (0 = executor default). |
| `type` | string | `"gate"` for gate steps; empty for normal steps. |
| `validate` | object | `ValidationRule` - checked against step stdout. |
| `retry` | object | `RetryConfig{max_attempts, feedback}` - retry on validation failure. |
| `condition` | string | Reserved for conditional execution (not yet evaluated). |

## Validation (`DAG.Validate`)

Checks performed in order:
1. At least one step exists.
2. All step IDs are non-empty and unique.
3. All prompts are non-empty (unless `type == "gate"`).
4. Gate steps have at least one dependency and a non-nil `validate` rule.
5. All `depends_on` references point to existing step IDs.
6. No cycles (Kahn's algorithm - builds in-degree map, drains queue, checks
   `len(sorted) == len(steps)`).

Topological order is cached in `DAG.topoOrder` on first successful `Validate`
call. `TopologicalSort()` returns the cache or calls `Validate` if empty.

Error format: `err:dag <description>`.

## Scheduler (`dag/scheduler.go`)

The `StepExecutor` interface (`scheduler.go`):
```go
type StepExecutor interface {
    Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error)
}
```

`NewScheduler(executor StepExecutor, maxConcurrent int)` - `maxConcurrent == 0`
means unlimited; negative values are clamped to 1.

`Run(ctx, dag)` returns:
- `map[string][]*artifact.Artifact` - step outputs (nil for failed/skipped).
- `map[string]error` - per-step errors.
- `error` - top-level error (invalid DAG or context cancellation).

Concurrency model: a single coordinator goroutine owns all mutable state.
Step goroutines communicate via a `completions` channel. No shared mutable
state between goroutines.

Root steps (in-degree 0) are launched first. As each step completes, its
dependents' in-degrees are decremented. A dependent is launched when its
in-degree reaches 0 and no dependency has failed.

A `chan struct{}` semaphore of size `maxConcurrent` bounds parallel execution
(nil when unlimited). The `completions` channel is buffered to the step count
(not `maxConcurrent`): each step sends exactly one completion, so a sender
never blocks even after the coordinator returns early on context cancellation.
Sizing it to `maxConcurrent` would strand semaphore-blocked goroutines on their
send and leak them.

## Failure propagation

When a step fails:
1. Its entry is set in `stepErrors`.
2. All direct dependents with newly-zero in-degree check `hasFailed()`.
3. Any dependent with a failed dependency is marked skipped
   (`err:dag skipped due to failed dependency`) without being launched.
4. `propagateSkip` recursively applies this to transitive dependents.

## Gate steps

A gate step (`type == "gate"`) validates the combined text content of its
dependency artifacts against its `validate` rule. If validation passes, it
passes through its input artifacts unchanged. If it fails, the gate step
itself fails (and propagates to its dependents).

Gate steps do not invoke the claude subprocess.

## ClaudeStepExecutor (`dag/executor.go`)

Implements `StepExecutor`. Created by `dag.NewClaudeStepExecutor(cfg, baseDir,
workDir, model, timeout, systemPrompt)`.

For each non-gate step:
1. Creates a temp job directory under `baseDir` (`step-<id>-*`).
2. Builds `claude.Config` with step overrides for model and timeout.
3. Calls `claude.Execute(ctx, claudeCfg)`.
4. Calls `claude.ParseRawJSON(jobDir)` to extract stdout.
5. If `exitCode != 0`: returns error, preserves job directory for debugging.
6. If `step.Validate != nil`: calls package-level `applyValidation`; on failure,
   preserves job directory.
7. On success: removes job directory, wraps stdout in `artifact.NewText`.

Retry within `ClaudeStepExecutor` (via `retryExecute`): only validation
errors trigger retry. On each retry after the first, `step.Retry.Feedback`
is appended to the prompt with `"\n\n"` separator. Other errors (subprocess
failure, context cancel) return immediately.

Input artifact injection: dependency outputs are prepended to the prompt:
```
Previous agent result (from step "fetch"):
<content>

Your task:
<original prompt>
```

## BuildLinearDAG

`dag.BuildLinearDAG(prompts []string)` creates a simple chain DAG with step
IDs `"step-0"`, `"step-1"`, etc. Used by `cmd.ChainCmd` to convert the chain
command's prompt list into a DAG.
