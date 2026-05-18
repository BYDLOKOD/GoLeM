package mcp

import (
	"context"
	"encoding/json"
)

// ToolHandler processes a single MCP tool call.
// Implementations receive the tool's arguments as raw JSON and return
// the result as raw JSON. If an error is returned, the server wraps it
// in a JSON-RPC error response with code -32603 (Internal error).
type ToolHandler interface {
	Handle(ctx context.Context, params json.RawMessage) (json.RawMessage, error)
}

// ToolDefinition describes a tool for the tools/list response.
type ToolDefinition struct {
	// Name is the unique tool identifier (e.g., "glm_run").
	Name string `json:"name"`
	// Description is a human-readable description shown to the LLM.
	Description string `json:"description"`
	// InputSchema is the JSON Schema object describing the tool's parameters.
	InputSchema map[string]any `json:"inputSchema"`
}

// toolEntry pairs a definition with its handler.
type toolEntry struct {
	def     ToolDefinition
	handler ToolHandler
}
