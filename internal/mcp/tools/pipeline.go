package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/dag"
	"github.com/veschin/GoLeM/internal/prompt"
)

// PipelineInput is the params schema for glm_pipeline.
type PipelineInput struct {
	DAG     dag.DAG  `json:"dag"`
	Dir     string   `json:"dir,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
	// SystemPrompt overrides the configured system prompt for all steps.
	SystemPrompt string   `json:"system_prompt,omitempty"`
	// Constraints are predefined behavior restrictions applied to all steps.
	Constraints  []string `json:"constraints,omitempty"`
}

// PipelineOutput is the result returned by glm_pipeline.
type PipelineOutput struct {
	Results map[string]StepResult `json:"results"`
	Status  string                `json:"status"`
}

// StepResult holds the outcome of a single DAG step.
type StepResult struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Error    string `json:"error,omitempty"`
}

// PipelineHandler is the MCP tool handler for glm_pipeline.
type PipelineHandler struct {
	cfg            *config.Config
	executor       dag.StepExecutor
	maxConcurrency int
}

// NewPipelineHandler creates a handler for the glm_pipeline tool.
// If executor is nil, Handle() creates a dag.ClaudeStepExecutor per call.
// maxConcurrency == 0 means unlimited (no cap on parallel steps).
func NewPipelineHandler(cfg *config.Config, executor dag.StepExecutor, maxConcurrency int) *PipelineHandler {
	return &PipelineHandler{
		cfg:            cfg,
		executor:       executor,
		maxConcurrency: maxConcurrency,
	}
}

// Handle processes a glm_pipeline tool call.
func (h *PipelineHandler) Handle(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var input PipelineInput
	if err := parseInput(params, &input); err != nil {
		return nil, err
	}

	// Validate the DAG.
	if err := input.DAG.Validate(); err != nil {
		return nil, fmt.Errorf("err:pipeline dag validation: %w", err)
	}

	// Set defaults.
	dir := input.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}

	// Apply overall timeout if specified.
	if input.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
		defer cancel()
	}

	// Create the step executor if not injected (for testing).
	executor := h.executor
	if executor == nil {
		// Assemble system prompt: fall back to config default when input is empty.
		systemPromptText := input.SystemPrompt
		if systemPromptText == "" {
			systemPromptText = h.cfg.SystemPrompt
		}
		assembledSystemPrompt, err := prompt.AssembleSystemPrompt(input.Constraints, systemPromptText)
		if err != nil {
			return nil, err
		}
		executor = dag.NewClaudeStepExecutor(h.cfg, dir, dir, h.cfg.Model, config.DefaultTimeout, assembledSystemPrompt)
	}

	// Create scheduler and run the DAG.
	scheduler := dag.NewScheduler(executor, h.maxConcurrency)
	results, stepErrors, schedErr := scheduler.Run(ctx, &input.DAG)

	// Build output from scheduler results.
	output := PipelineOutput{
		Results: make(map[string]StepResult, len(input.DAG.Steps)),
	}

	completedCount := 0
	failedCount := 0
	skippedCount := 0

	for _, step := range input.DAG.Steps {
		sr := StepResult{}

		artifacts := results[step.ID]
		stepErr := stepErrors[step.ID]

		if len(artifacts) > 0 {
			// Step succeeded: executor returned artifacts.
			sr.Status = "completed"
			sr.Stdout = string(artifacts[0].Content)
			completedCount++
		} else if stepErr != nil {
			// Step has a recorded error -- distinguish failed from skipped.
			errMsg := stepErr.Error()
			if strings.HasPrefix(errMsg, "err:dag skipped") {
				// Upstream dependency failed; this step was never executed.
				sr.Status = "skipped"
				sr.Error = errMsg
				skippedCount++
			} else {
				// Executor ran this step and it failed.
				sr.Status = "failed"
				sr.Error = errMsg
				sr.ExitCode = 1
				failedCount++
			}
		} else {
			// No artifacts and no error -- treat as skipped (unreachable for valid DAGs
			// but a safe default).
			sr.Status = "skipped"
			skippedCount++
		}

		output.Results[step.ID] = sr
	}

	// Determine overall status.
	if schedErr != nil {
		output.Status = "failed"
	} else if failedCount == 0 && skippedCount == 0 && completedCount > 0 {
		output.Status = "completed"
	} else if completedCount > 0 && (failedCount > 0 || skippedCount > 0) {
		output.Status = "partial"
	} else if failedCount > 0 || skippedCount > 0 {
		output.Status = "failed"
	} else if len(input.DAG.Steps) == 0 {
		output.Status = "completed"
	}

	return marshalOutput(output)
}

// PipelineDefinition returns the ToolDefinition input schema for glm_pipeline.
func PipelineDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dag": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id": map[string]any{
									"type":        "string",
									"description": "Unique step identifier",
								},
								"prompt": map[string]any{
									"type":        "string",
									"description": "Task description for the subagent",
								},
								"depends_on": map[string]any{
									"type":        "array",
									"items":       map[string]any{"type": "string"},
									"description": "Step IDs that must complete before this step",
								},
								"model": map[string]any{
									"type":        "string",
									"description": "Model override for this step",
								},
								"timeout": map[string]any{
									"type":        "integer",
									"description": "Per-step timeout in seconds",
								},
								"type": map[string]any{
									"type":        "string",
									"description": "Step type. 'gate' validates dependency outputs without running Claude. Empty or omitted means normal step.",
								},
								"validate": map[string]any{
									"type":        "object",
									"description": "Validation rule checked against step stdout. Fields: contains ([]string), not_contains ([]string), matches (string regex).",
								},
								"retry": map[string]any{
									"type":        "object",
									"description": "Retry on validation failure. Fields: max_attempts (int), feedback (string appended on retry).",
								},
							},
							"required": []string{"id", "prompt"},
						},
					},
				},
				"required": []string{"steps"},
			},
			"dir": map[string]any{
				"type":        "string",
				"description": "Working directory for all steps (default: cwd)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Overall pipeline timeout in seconds",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "System prompt to constrain golem behavior. Overrides config default.",
			},
			"constraints": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "Predefined behavior constraints. Known values: \"readonly\" (no file writes), \"no-create\" (no new files), \"plan-first\" (output plan before acting), \"scope:<path>\" (restrict to directory).",
			},
		},
		"required": []string{"dag"},
	}
}
