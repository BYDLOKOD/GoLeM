package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
)

// ListHandler returns a ToolHandler that lists all jobs.
func ListHandler(tc *ToolContext) *ListTool {
	return &ListTool{tc: tc}
}

// ListTool implements mcp.ToolHandler for job listing.
type ListTool struct {
	tc *ToolContext
}

// Handle executes the glm_list tool.
func (h *ListTool) Handle(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input ListInput
	// Input is optional -- empty raw means "list all".
	if raw != nil {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, NewToolError("err:user", "invalid input: "+err.Error())
		}
	}

	var opts []*cmd.FilterOptions
	if input.Status != "" || input.Since != "" {
		opt := &cmd.FilterOptions{}
		if input.Status != "" {
			statuses, err := cmd.ParseStatusFilter(input.Status)
			if err != nil {
				return nil, NewToolError("err:user", err.Error())
			}
			opt.Statuses = statuses
		}
		if input.Since != "" {
			sinceTime, err := time.Parse(time.RFC3339, input.Since)
			if err != nil {
				return nil, NewToolError("err:user", "invalid since timestamp: "+err.Error())
			}
			opt.Since = sinceTime
		}
		opts = append(opts, opt)
	}

	var buf bytes.Buffer
	if err := cmd.ListCmd(h.tc.SubagentsRoot, &buf, opts...); err != nil {
		return nil, NewToolError("err:execution", err.Error())
	}

	// Parse tabular output into structured entries.
	jobs := parseListOutput(buf.String())

	output := ListOutput{Jobs: jobs}
	return marshalOutput(output)
}

// parseListOutput converts the tabular output of ListCmd into structured
// ListJobEntry values. The header line is skipped. Each subsequent line has
// the format: JOB_ID  STATUS  STARTED (with fixed-width columns).
func parseListOutput(text string) []ListJobEntry {
	if text == "" {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= 1 {
		// Only header or empty.
		return nil
	}

	var jobs []ListJobEntry
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entry := ListJobEntry{
			JobID:  fields[0],
			Status: fields[1],
		}
		if len(fields) >= 3 && fields[2] != "-" {
			entry.StartedAt = fields[2]
		}
		jobs = append(jobs, entry)
	}
	return jobs
}

// ListDefinition returns the input schema for glm_list.
func ListDefinition() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type":        "string",
				"description": "Filter by job status (queued, running, done, failed, etc.)",
			},
			"since": map[string]any{
				"type":        "string",
				"description": "Filter to jobs started after this RFC3339 timestamp",
			},
		},
	}
}
