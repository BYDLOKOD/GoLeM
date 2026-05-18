// Package mcp implements the Model Context Protocol server for GoLeM.
// It provides JSON-RPC 2.0 transport over stdio, tool registration, and
// dispatch for the four core MCP methods: initialize, notifications/initialized,
// tools/list, and tools/call.
package mcp

import "encoding/json"

// Request is a JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Notification is a JSON-RPC 2.0 notification (has no id field).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ProtocolVersion is the MCP protocol version advertised during initialize.
const ProtocolVersion = "2024-11-05"

// ServerInfo is returned in the initialize response.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools any `json:"tools,omitempty"`
}

// InitializeResult is the result of the initialize method.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// ToolsListResult is the result of the tools/list method.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolsCallParams is the params structure for tools/call.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the result wrapper for tools/call.
type ToolsCallResult struct {
	Content []ToolResultContent `json:"content"`
}

// ToolResultContent represents one content block in a tool result.
type ToolResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
