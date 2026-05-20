package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/slot"
)

// StartHandler returns a ToolHandler that starts a subagent asynchronously.
// It mirrors the cmdStart() path in main.go: pre-create the job (so the caller
// receives a stable job ID immediately), then launch a goroutine that reconciles,
// acquires a slot, and calls cmd.ExecuteJob.
func StartHandler(tc *ToolContext) *StartTool {
	return &StartTool{tc: tc}
}

// StartTool implements mcp.ToolHandler for async subagent execution.
type StartTool struct {
	tc *ToolContext
}

// Handle executes the glm_start tool.
func (h *StartTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input StartInput
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

	projectID := h.tc.ProjectID

	// Pre-create the job so the caller gets a valid job ID immediately
	// (same pattern as cmdStart() in main.go).
	jobID := job.GenerateJobID()
	j, err := job.NewJob(h.tc.SubagentsRoot, projectID, jobID)
	if err != nil {
		return nil, NewToolError("err:execution", fmt.Sprintf("create job: %v", err))
	}

	// Write our PID before returning the job ID (mirrors cmdStart).
	pid := os.Getpid()
	_ = os.WriteFile(filepath.Join(j.Dir, "pid.txt"), []byte(strconv.Itoa(pid)), 0o644)

	// Capture values needed inside the goroutine (avoid closing over h.tc
	// fields that could theoretically change, even though in practice they
	// are immutable after construction).
	subagentsRoot := h.tc.SubagentsRoot
	cfg := h.tc.Cfg
	jobDir := j.Dir

	// Launch background goroutine — reconcile, wait for slot, then execute.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_ = os.WriteFile(filepath.Join(jobDir, "status"), []byte("failed"), 0o644)
				_ = os.WriteFile(filepath.Join(jobDir, "stderr.txt"),
					fmt.Appendf(nil, "panic: %v", r), 0o644)
			}
		}()

		if err := job.Reconcile(subagentsRoot, time.Now()); err != nil {
			// Non-fatal: log and continue.
			fmt.Printf("warn: reconcile: %v\n", err)
		}
		sm := slot.NewSlotManager(subagentsRoot, 0) // 0 = unlimited
		if err := sm.Init(); err != nil {
			_ = os.WriteFile(filepath.Join(jobDir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(jobDir, "stderr.txt"),
				fmt.Appendf(nil, "slot init: %v", err), 0o644)
			return
		}
		if err := sm.WaitForSlot(); err != nil {
			_ = os.WriteFile(filepath.Join(jobDir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(jobDir, "stderr.txt"),
				fmt.Appendf(nil, "slot wait: %v", err), 0o644)
			return
		}

		// ExecuteJob releases the slot via defer when done.
		// Intentional: glm_start is fire-and-forget — the job must outlive
		// the MCP request that created it, so we deliberately decouple from
		// the caller's context here. Cancellation of the calling MCP request
		// must not cancel an already-launched async job.
		_, execErr := cmd.ExecuteJob(context.Background(), cmd.ExecuteJobParams{
			Cfg:           cfg,
			Flags:         flags,
			SubagentsRoot: subagentsRoot,
			ProjectID:     projectID,
			AutoDelete:    false,
			SlotManager:   sm,
			JobID:         jobID,
		})
		if execErr != nil {
			_ = os.WriteFile(filepath.Join(jobDir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(jobDir, "stderr.txt"),
				[]byte(execErr.Error()), 0o644)
		}
	}()

	output := StartOutput{
		JobID: jobID,
	}

	return marshalOutput(output)
}

// StartDefinition returns the input schema for glm_start.
func StartDefinition() map[string]any {
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
				"description": "Execution timeout in seconds",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model override",
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
