package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
)

// ChainHandler returns a ToolHandler that executes a chain of prompts.
func ChainHandler(tc *ToolContext) *ChainTool {
	return &ChainTool{tc: tc}
}

// ChainTool implements mcp.ToolHandler for chained subagent execution.
type ChainTool struct {
	tc *ToolContext
}

// Handle executes the glm_chain tool.
func (h *ChainTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input ChainInput
	if err := parseInput(raw, &input); err != nil {
		return nil, err
	}

	// Build chain steps from either Steps or Prompts.
	var steps []cmd.ChainStep
	if len(input.Steps) > 0 {
		if len(input.Steps) < 2 {
			return nil, NewToolError("err:user", "at least 2 steps required for chain")
		}
		for _, s := range input.Steps {
			steps = append(steps, cmd.ChainStep{
				Prompt:   s.Prompt,
				Validate: s.Validate,
				Retry:    s.Retry,
			})
		}
	} else {
		if len(input.Prompts) < 2 {
			return nil, NewToolError("err:user", "at least 2 prompts are required for a chain")
		}
		steps = cmd.ChainStepsFromPrompts(input.Prompts)
	}

	dir := input.Dir
	if dir == "" {
		dir = "."
	}
	model := input.Model
	if model == "" {
		model = h.tc.Cfg.Model
	}
	permissionMode := input.PermissionMode
	if permissionMode == "" {
		permissionMode = h.tc.Cfg.PermissionMode
	}
	timeout := input.Timeout
	if timeout <= 0 {
		timeout = config.DefaultTimeout
	}

	systemPrompt := input.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = h.tc.Cfg.SystemPrompt
	}

	chainFlags := &cmd.ChainFlags{
		Flags: &cmd.Flags{
			Dir:            dir,
			Timeout:        timeout,
			Model:          model,
			OpusModel:      h.tc.Cfg.OpusModel,
			SonnetModel:    h.tc.Cfg.SonnetModel,
			HaikuModel:     h.tc.Cfg.HaikuModel,
			PermissionMode: permissionMode,
			SystemPrompt:   systemPrompt,
			Constraints:    input.Constraints,
		},
		ContinueOnError: input.ContinueOnError,
		Steps:           steps,
	}

	var stdout, stderr bytes.Buffer
	// ChainCmd manages per-step reconcile and slot acquisition internally.
	result, err := cmd.ChainCmd(chainFlags, h.tc.Cfg, h.tc.SubagentsRoot, h.tc.ProjectID, &stdout, &stderr)
	if err != nil {
		return nil, NewToolError("err:execution", err.Error())
	}

	output := ChainOutput{
		FinalStdout:   result.FinalStdout,
		ExitCode:      result.ExitCode,
		StepsExecuted: result.StepsExecuted,
		StepsSkipped:  result.StepsSkipped,
		JobDirs:       result.JobDirs,
	}

	return marshalOutput(output)
}

// ChainDefinition returns the input schema for glm_chain.
func ChainDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Ordered list of prompts to execute sequentially",
			},
			"dir": map[string]any{
				"type":        "string",
				"description": "Working directory for all steps (default: .)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Per-step timeout in seconds",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model override",
			},
			"permission_mode": map[string]any{
				"type":        "string",
				"description": "Permission mode override",
			},
			"continue_on_error": map[string]any{
				"type":        "boolean",
				"description": "Continue executing steps even if one fails",
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
			"steps": map[string]any{
				"type":        "array",
				"description": "Alternative to prompts — array of step objects with optional validation and retry. If provided, prompts field is ignored.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":        "string",
							"description": "The step prompt",
						},
						"validate": map[string]any{
							"type":        "object",
							"description": "Validation rule with contains/not_contains/matches fields",
						},
						"retry": map[string]any{
							"type":        "object",
							"description": "Retry config with max_attempts and feedback fields",
						},
					},
					"required": []string{"prompt"},
				},
			},
		},
		// Neither `prompts` nor `steps` is hard-required at the schema level:
		// the handler accepts either form and validates at runtime that at
		// least one is populated with the required minimum step count. Listing
		// only one as required would reject the other valid shape under strict
		// schema validators.
		"oneOf": []map[string]any{
			{"required": []string{"prompts"}},
			{"required": []string{"steps"}},
		},
	}
}
