package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/slot"
)

// RunHandler returns a ToolHandler that executes a subagent synchronously.
// It mirrors the cmdRun() path in main.go: reconcile stale jobs, acquire a
// concurrency slot, call cmd.ExecuteJob, and return the result.
func RunHandler(tc *ToolContext) *RunTool {
	return &RunTool{tc: tc}
}

// RunTool implements mcp.ToolHandler for synchronous subagent execution.
type RunTool struct {
	tc *ToolContext
}

// Handle executes the glm_run tool.
func (h *RunTool) Handle(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input RunInput
	if err := parseInput(raw, &input); err != nil {
		return nil, err
	}

	if input.Prompt == "" {
		return nil, NewToolError("err:user", "prompt is required")
	}

	// Apply defaults and config overrides.
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

	flags := &cmd.Flags{
		Dir:            dir,
		Timeout:        timeout,
		Model:          model,
		OpusModel:      h.tc.Cfg.OpusModel,
		SonnetModel:    h.tc.Cfg.SonnetModel,
		HaikuModel:     h.tc.Cfg.HaikuModel,
		PermissionMode: permissionMode,
		Prompt:         input.Prompt,
		SystemPrompt:   systemPrompt,
		Constraints:    input.Constraints,
	}

	if err := cmd.Validate(flags); err != nil {
		return nil, NewToolError("err:user", err.Error())
	}

	// Reconcile stale jobs and initialise the slot manager — same as cmdRun().
	if err := job.Reconcile(h.tc.SubagentsRoot, time.Now()); err != nil {
		// Non-fatal: log and continue (mirrors main.go behaviour).
		fmt.Printf("warn: reconcile: %v\n", err)
	}
	sm := slot.NewSlotManager(h.tc.SubagentsRoot, 0) // 0 = unlimited
	if err := sm.Init(); err != nil {
		return nil, NewToolError("err:execution", fmt.Sprintf("slot init: %v", err))
	}
	if err := sm.WaitForSlot(); err != nil {
		return nil, NewToolError("err:execution", fmt.Sprintf("slot wait: %v", err))
	}

	projectID := h.tc.ProjectID

	// ExecuteJob releases the slot via defer when done.
	result, err := cmd.ExecuteJob(ctx, cmd.ExecuteJobParams{
		Cfg:           h.tc.Cfg,
		Flags:         flags,
		SubagentsRoot: h.tc.SubagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    true,
		SlotManager:   sm,
	})
	if err != nil {
		return nil, NewToolError("err:execution", err.Error())
	}

	output := RunOutput{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		JobID:    result.JobID,
	}

	return marshalOutput(output)
}

// RunDefinition returns the ToolDefinition input schema for glm_run.
func RunDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The task description for the subagent",
			},
			"dir": map[string]any{
				"type":        "string",
				"description": "Working directory (default: .)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Execution timeout in seconds (default: from config)",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model override (default: from config)",
			},
			"permission_mode": map[string]any{
				"type":        "string",
				"description": "Permission mode override",
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
		"required": []string{"prompt"},
	}
}
