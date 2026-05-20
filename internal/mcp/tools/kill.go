package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/job"
)

// KillHandler returns a ToolHandler that terminates a running job.
func KillHandler(tc *ToolContext) *KillTool {
	return &KillTool{tc: tc}
}

// KillTool implements mcp.ToolHandler for job termination.
type KillTool struct {
	tc *ToolContext
}

// Handle executes the glm_kill tool.
func (h *KillTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input KillInput
	if err := parseInput(raw, &input); err != nil {
		return nil, err
	}

	if input.JobID == "" {
		return nil, NewToolError("err:user", "job_id is required")
	}

	// Read the current status before killing (for the response).
	jobDir, err := job.FindJobDir(h.tc.SubagentsRoot, h.tc.ProjectID, input.JobID)
	if err != nil {
		return nil, NewToolError("err:not_found", "job not found: "+input.JobID)
	}
	previousStatus := string(job.ReadStatus(jobDir))

	// Kill the job using production signal and sleep functions.
	if err := cmd.KillCmd(
		h.tc.SubagentsRoot, h.tc.ProjectID, input.JobID,
		productionSignalFn, productionSleepFn,
	); err != nil {
		// Distinguish user errors (job not running) from system errors.
		errMsg := err.Error()
		if strings.Contains(errMsg, "err:user") {
			return nil, NewToolError("err:user", errMsg)
		}
		if strings.Contains(errMsg, "err:not_found") {
			return nil, NewToolError("err:not_found", errMsg)
		}
		return nil, NewToolError("err:execution", errMsg)
	}

	output := KillOutput{
		JobID:          input.JobID,
		PreviousStatus: previousStatus,
	}

	return marshalOutput(output)
}

// KillDefinition returns the input schema for glm_kill.
func KillDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"job_id": map[string]any{
				"type":        "string",
				"description": "The job identifier to terminate",
			},
		},
		"required": []string{"job_id"},
	}
}
