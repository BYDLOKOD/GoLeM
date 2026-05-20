package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/veschin/GoLeM/internal/cmd"
)

// ResultHandler returns a ToolHandler that retrieves completed job output.
func ResultHandler(tc *ToolContext) *ResultTool {
	return &ResultTool{tc: tc}
}

// ResultTool implements mcp.ToolHandler for job result retrieval.
type ResultTool struct {
	tc *ToolContext
}

// Handle executes the glm_result tool.
func (h *ResultTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input ResultInput
	if err := parseInput(raw, &input); err != nil {
		return nil, err
	}

	if input.JobID == "" {
		return nil, NewToolError("err:user", "job_id is required")
	}

	var stdout, stderr bytes.Buffer
	result, err := cmd.ResultCmd(input.JobID, h.tc.SubagentsRoot, h.tc.ProjectID, &stdout, &stderr)
	if err != nil {
		return nil, NewToolError("err:execution", err.Error())
	}

	output := ResultOutput{
		Stdout:   result.Stdout,
		ExitCode: result.ExitCode,
		Deleted:  result.Deleted,
	}
	if result.Stderr != "" {
		output.Stderr = result.Stderr
	}

	return marshalOutput(output)
}

// ResultDefinition returns the input schema for glm_result.
func ResultDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"job_id": map[string]any{
				"type":        "string",
				"description": "The job identifier to retrieve results for",
			},
		},
		"required": []string{"job_id"},
	}
}
