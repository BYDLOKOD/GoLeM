package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/veschin/GoLeM/internal/cmd"
)

// StatusHandler returns a ToolHandler that checks job status.
func StatusHandler(tc *ToolContext) *StatusTool {
	return &StatusTool{tc: tc}
}

// StatusTool implements mcp.ToolHandler for job status queries.
type StatusTool struct {
	tc *ToolContext
}

// Handle executes the glm_status tool.
func (h *StatusTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input StatusInput
	if err := parseInput(raw, &input); err != nil {
		return nil, err
	}

	if input.JobID == "" {
		return nil, NewToolError("err:user", "job_id is required")
	}

	var stdout bytes.Buffer
	result, err := cmd.StatusCmd(input.JobID, h.tc.SubagentsRoot, h.tc.ProjectID, &stdout)
	if err != nil {
		return nil, NewToolError("err:not_found", err.Error())
	}

	output := StatusOutput{
		Status: result.Status,
	}

	return marshalOutput(output)
}

// StatusDefinition returns the input schema for glm_status.
func StatusDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"job_id": map[string]any{
				"type":        "string",
				"description": "The job identifier to check",
			},
		},
		"required": []string{"job_id"},
	}
}
