---
id: mcp-tools
kind: spec
touches: internal/mcp/tools/
---

# MCP Tools - Eight Tool Handlers

See also: [30_mcp.md](30_mcp.md) · [40_dag.md](40_dag.md) · [42_prompt.md](42_prompt.md).

## Common patterns

All handlers live in `internal/mcp/tools/`. Each follows the same shape:

1. `parseInput(raw, &input)` - JSON unmarshal; returns `ToolError{err:user}`
   on failure.
2. Apply defaults from `ToolContext.Cfg` where input fields are empty.
3. Call the relevant `internal/cmd` or `internal/dag` function.
4. `marshalOutput(output)` - JSON marshal the result struct.

`ToolContext` carries `Cfg *config.Config`, `SubagentsRoot string`,
`ProjectID string`. Created once per `glm mcp` invocation.

`ToolError` implements `error` with `Code string` and `Message string`.

## glm_run

Handler: `RunTool.Handle` (`tools/run.go`).

Executes a subagent synchronously. Mirrors `cmdRun`:

1. Validates `prompt` non-empty.
2. Applies defaults: `dir="."`, `model`, `permissionMode`, `timeout`,
   `systemPrompt` from config.
3. `cmd.Validate(flags)`.
4. `job.Reconcile` + `slot.NewSlotManager(0).WaitForSlot()`.
5. `cmd.ExecuteJob(ctx, ...)` with `AutoDelete: true`.
6. Returns `RunOutput{stdout, stderr, exit_code, job_id}`.

Input fields: `prompt` (required), `dir`, `timeout`, `model`,
`permission_mode`, `system_prompt`, `constraints []string`.

## glm_start

Handler: `StartTool.Handle` (`tools/start.go`).

Starts a subagent asynchronously. Returns `StartOutput{job_id}` immediately.
Launches `cmd.ExecuteJob` in a goroutine with `AutoDelete: false`.

Input fields: same as `glm_run`.

## glm_status

Handler: `StatusTool.Handle` (`tools/status.go`).

Reads the `status` file for a job. Uses `job.FindJobDir` with an empty
`projectID` (scans all projects). Returns `StatusOutput{status}`.

Input fields: `job_id` (required).

## glm_result

Handler: `ResultTool.Handle` (`tools/result.go`).

Retrieves the output of a completed job. Reads `stdout.txt`, `stderr.txt`,
`exit_code.txt`. Optionally deletes the job directory when `Deleted: true`
(current implementation always returns `Deleted: false`; deletion logic is
not present in the tool handler).

Returns `ResultOutput{stdout, stderr, exit_code, deleted}`.

Input fields: `job_id` (required).

## glm_list

Handler: `ListTool.Handle` (`tools/list.go`).

Lists jobs across all projects under `SubagentsRoot`. Applies optional
`status` and `since` (RFC3339) filters. Returns
`ListOutput{jobs: [{job_id, status, started_at}]}`.

Input fields: `status` (optional), `since` (optional RFC3339 timestamp).

## glm_kill

Handler: `KillTool.Handle` (`tools/kill.go`).

Terminates a running job. Reads `pid.txt`, sends SIGTERM then SIGKILL via
`TerminateProcessGroup`, transitions status to `killed`.

Returns `KillOutput{job_id, previous_status}`.

Input fields: `job_id` (required).

## glm_chain

Handler: `ChainTool.Handle` (`tools/chain.go`).

Executes multiple prompts sequentially. Two input modes:

- `prompts []string` - simple list of prompt strings.
- `steps []ChainInputStep` - structured steps with optional `validate` and
  `retry` fields; takes precedence over `prompts`.

Internally calls `cmd.ChainCmd`. Returns `ChainOutput{final_stdout,
exit_code, steps_executed, steps_skipped, job_dirs}`.

Input fields: `prompts` or `steps` (at least one required), `dir`, `timeout`,
`model`, `permission_mode`, `continue_on_error`, `system_prompt`,
`constraints`.

## glm_pipeline

Handler: `PipelineHandler.Handle` (`tools/pipeline.go`).

Accepts an inline DAG definition (not a file path) and executes it via the
`dag.Scheduler`. The DAG is validated with `dag.Validate()` before execution.

Returns `PipelineOutput{results: {stepID: {status, exit_code, stdout, error}}, status}`.

Overall `status` values: `"completed"`, `"partial"` (some steps failed),
`"failed"`.

Input fields: `dag` (required, inline DAG object), `dir`, `timeout`,
`system_prompt`, `constraints`.

The `dag` object follows the same schema as the JSON pipeline file (see
[40_dag.md](40_dag.md)): `{"steps": [{id, prompt, depends_on, model,
timeout, type, validate, retry}]}`.

## Input/output type reference (`tools/types.go`)

All input and output structs are defined in `internal/mcp/tools/types.go`.
`ChainInputStep` embeds `*validation.ValidationRule` and `*dag.RetryConfig`
for step-level validation and retry in the chain tool.
