// Package tools implements MCP tool handlers that wrap GoLeM's internal
// command functions into the mcp.ToolHandler interface.
package tools

import (
	"encoding/json"

	"github.com/veschin/GoLeM/internal/dag"
	"github.com/veschin/GoLeM/internal/validation"
)

// --- Shared ---

// ToolError is a structured error returned by tool handlers.
// It implements the error interface and carries a machine-readable code.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ToolError) Error() string {
	return e.Code + " " + e.Message
}

// NewToolError creates a ToolError with the given code and message.
func NewToolError(code, message string) *ToolError {
	return &ToolError{Code: code, Message: message}
}

// --- glm_run ---

// RunInput is the input for the glm_run tool.
type RunInput struct {
	// Prompt is the task description for the subagent. Required.
	Prompt string `json:"prompt"`
	// Dir is the working directory. Defaults to ".".
	Dir string `json:"dir,omitempty"`
	// Timeout is the execution timeout in seconds. 0 = use config default.
	Timeout int `json:"timeout,omitempty"`
	// Model overrides the configured model. Empty = use config default.
	Model string `json:"model,omitempty"`
	// PermissionMode overrides the configured permission mode.
	PermissionMode string `json:"permission_mode,omitempty"`
	// SystemPrompt overrides the configured system prompt for this invocation.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Constraints are predefined behavior restrictions applied to the subagent.
	Constraints []string `json:"constraints,omitempty"`
}

// RunOutput is the output for the glm_run tool.
type RunOutput struct {
	// Stdout is the captured standard output from the subagent.
	Stdout string `json:"stdout"`
	// Stderr is the captured standard error from the subagent.
	Stderr string `json:"stderr"`
	// ExitCode is the process exit code (0 = success).
	ExitCode int `json:"exit_code"`
	// JobID is the identifier of the executed job.
	JobID string `json:"job_id"`
}

// --- glm_start ---

// StartInput is the input for the glm_start tool.
type StartInput struct {
	// Prompt is the task description for the subagent. Required.
	Prompt string `json:"prompt"`
	// Dir is the working directory. Defaults to ".".
	Dir string `json:"dir,omitempty"`
	// Timeout is the execution timeout in seconds. 0 = use config default.
	Timeout int `json:"timeout,omitempty"`
	// Model overrides the configured model. Empty = use config default.
	Model string `json:"model,omitempty"`
	// PermissionMode overrides the configured permission mode.
	PermissionMode string `json:"permission_mode,omitempty"`
	// SystemPrompt overrides the configured system prompt for this invocation.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Constraints are predefined behavior restrictions applied to the subagent.
	Constraints []string `json:"constraints,omitempty"`
}

// StartOutput is the output for the glm_start tool.
type StartOutput struct {
	// JobID is the identifier of the newly created async job.
	JobID string `json:"job_id"`
}

// --- glm_status ---

// StatusInput is the input for the glm_status tool.
type StatusInput struct {
	// JobID is the identifier of the job to check. Required.
	JobID string `json:"job_id"`
}

// StatusOutput is the output for the glm_status tool.
type StatusOutput struct {
	// Status is the current job status string (queued, running, done, failed, etc.).
	Status string `json:"status"`
}

// --- glm_result ---

// ResultInput is the input for the glm_result tool.
type ResultInput struct {
	// JobID is the identifier of the job to retrieve. Required.
	JobID string `json:"job_id"`
}

// ResultOutput is the output for the glm_result tool.
type ResultOutput struct {
	// Stdout is the captured standard output from the completed job.
	Stdout string `json:"stdout"`
	// Stderr is the captured standard error (present for failed/timeout jobs).
	Stderr string `json:"stderr,omitempty"`
	// ExitCode is the process exit code.
	ExitCode int `json:"exit_code"`
	// Deleted indicates the job directory was auto-deleted.
	Deleted bool `json:"deleted"`
}

// --- glm_list ---

// ListInput is the input for the glm_list tool.
// All fields are optional filters.
type ListInput struct {
	// Status filters by job status. Empty = all statuses.
	Status string `json:"status,omitempty"`
	// Since filters to jobs started after this timestamp (RFC3339).
	Since string `json:"since,omitempty"`
}

// ListOutput is the output for the glm_list tool.
type ListOutput struct {
	// Jobs is the list of matching jobs.
	Jobs []ListJobEntry `json:"jobs"`
}

// ListJobEntry represents a single job in the list output.
type ListJobEntry struct {
	// JobID is the job identifier.
	JobID string `json:"job_id"`
	// Status is the current status string.
	Status string `json:"status"`
	// StartedAt is the RFC3339 timestamp when the job started, or empty.
	StartedAt string `json:"started_at,omitempty"`
}

// --- glm_kill ---

// KillInput is the input for the glm_kill tool.
type KillInput struct {
	// JobID is the identifier of the job to terminate. Required.
	JobID string `json:"job_id"`
}

// KillOutput is the output for the glm_kill tool.
type KillOutput struct {
	// JobID is the identifier of the killed job.
	JobID string `json:"job_id"`
	// PreviousStatus is the status the job had before being killed.
	PreviousStatus string `json:"previous_status"`
}

// --- glm_chain ---

// ChainInputStep represents a single step in a chain with optional validation and retry.
type ChainInputStep struct {
	// Prompt is the instruction to execute.
	Prompt string `json:"prompt"`
	// Validate is an optional output validation rule.
	Validate *validation.ValidationRule `json:"validate,omitempty"`
	// Retry configures automatic retries.
	Retry *dag.RetryConfig `json:"retry,omitempty"`
}

// ChainInput is the input for the glm_chain tool.
type ChainInput struct {
	// Prompts is the ordered list of prompts to execute. At least 2 required.
	Prompts []string `json:"prompts"`
	// Dir is the working directory for all steps. Defaults to ".".
	Dir string `json:"dir,omitempty"`
	// Timeout is the per-step timeout in seconds. 0 = use config default.
	Timeout int `json:"timeout,omitempty"`
	// Model overrides the configured model. Empty = use config default.
	Model string `json:"model,omitempty"`
	// PermissionMode overrides the configured permission mode.
	PermissionMode string `json:"permission_mode,omitempty"`
	// ContinueOnError instructs the chain to keep running even when a step fails.
	ContinueOnError bool `json:"continue_on_error,omitempty"`
	// SystemPrompt overrides the configured system prompt for all steps.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Constraints are predefined behavior restrictions applied to all steps.
	Constraints []string `json:"constraints,omitempty"`
	// Steps is the ordered list of step objects with optional validation and retry.
	// If provided, takes precedence over Prompts.
	Steps []ChainInputStep `json:"steps,omitempty"`
}

// ChainOutput is the output for the glm_chain tool.
type ChainOutput struct {
	// FinalStdout is the stdout from the last executed step.
	FinalStdout string `json:"final_stdout"`
	// ExitCode is 0 if all steps succeeded, 1 if any failed.
	ExitCode int `json:"exit_code"`
	// StepsExecuted is the count of steps that ran.
	StepsExecuted int `json:"steps_executed"`
	// StepsSkipped is the count of steps skipped due to failure.
	StepsSkipped int `json:"steps_skipped"`
	// JobDirs is the list of job directory paths for executed steps.
	JobDirs []string `json:"job_dirs"`
}

// --- parse and marshal helpers ---

// parseInput unmarshals raw JSON into the target struct.
// Returns a ToolError with err:user code if unmarshaling fails.
func parseInput(raw json.RawMessage, target any) error {
	if raw == nil {
		return NewToolError("err:user", "missing input parameters")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return NewToolError("err:user", "invalid input: "+err.Error())
	}
	return nil
}

// marshalOutput serializes the output struct to json.RawMessage.
func marshalOutput(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, NewToolError("err:internal", "marshal output: "+err.Error())
	}
	return json.RawMessage(data), nil
}
