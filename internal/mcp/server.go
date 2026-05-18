package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
)

// Server implements the MCP server protocol. It handles the four core
// methods (initialize, notifications/initialized, tools/list, tools/call)
// and dispatches tool calls to registered handlers.
type Server struct {
	transport *StdioTransport
	tools     map[string]*toolEntry
	mu        sync.RWMutex
	// ServerVersion is advertised in the initialize response.
	ServerVersion string
	// Logger is used for diagnostic messages. Nil disables logging.
	Logger *log.Logger
}

// NewServer creates a new MCP server that communicates over the given transport.
func NewServer(transport *StdioTransport) *Server {
	return &Server{
		transport:     transport,
		tools:         make(map[string]*toolEntry),
		ServerVersion: "0.1.0",
	}
}

// RegisterTool adds a tool definition and its handler to the server.
// It is safe to call from any goroutine, including while Serve is running.
func (s *Server) RegisterTool(def ToolDefinition, h ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[def.Name] = &toolEntry{def: def, handler: h}
}

// Serve starts the server loop. It reads JSON-RPC messages from the
// transport, dispatches them, and writes responses. It blocks until
// the context is cancelled or the transport returns an error (e.g., EOF).
func (s *Server) Serve(ctx context.Context) error {
	for {
		// Check for context cancellation before blocking on read.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := s.transport.ReadMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("err:mcp: read message: %w", err)
		}

		// Try to parse as JSON. If it fails, send a parse error and continue.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(msg, &raw); err != nil {
			s.sendError(nil, CodeParseError, fmt.Sprintf("parse error: %v", err))
			continue
		}

		// Determine if this is a notification (no "id" field) or a request.
		_, hasID := raw["id"]
		if !hasID {
			var notif Notification
			_ = json.Unmarshal(msg, &notif)
			s.handleNotification(notif)
			continue
		}

		// It is a request with an ID — parse fully, dispatch, respond.
		var req Request
		if err := json.Unmarshal(msg, &req); err != nil {
			s.sendError(nil, CodeParseError, fmt.Sprintf("parse error: %v", err))
			continue
		}

		resp := s.dispatchRequest(ctx, req)
		if err := s.transport.WriteResponse(resp); err != nil {
			return fmt.Errorf("err:mcp: write response: %w", err)
		}
	}
}

// handleNotification processes a JSON-RPC notification. No response is sent.
func (s *Server) handleNotification(notif Notification) {
	switch notif.Method {
	case "notifications/initialized":
		// No-op acknowledgment.
	default:
		s.debugf("unknown notification method: %s", notif.Method)
	}
}

// dispatchRequest routes a JSON-RPC request to the appropriate handler
// and returns the response.
func (s *Server) dispatchRequest(ctx context.Context, req Request) Response {
	// Validate JSONRPC version.
	if req.JSONRPC != "2.0" {
		return s.errorResponse(req.ID, CodeInvalidRequest,
			"Invalid Request: jsonrpc must be \"2.0\"")
	}

	// Validate method is present.
	if req.Method == "" {
		return s.errorResponse(req.ID, CodeInvalidRequest,
			"Invalid Request: method is required")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return s.errorResponse(req.ID, CodeMethodNotFound,
			fmt.Sprintf("Method not found: %s", req.Method))
	}
}

// handleInitialize returns the server capabilities.
func (s *Server) handleInitialize(req Request) Response {
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: struct{}{},
		},
		ServerInfo: ServerInfo{
			Name:    "golem",
			Version: s.ServerVersion,
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, CodeInternalError,
			"err:mcp: failed to marshal initialize result")
	}
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  data,
	}
}

// handleToolsList returns all registered tool definitions.
func (s *Server) handleToolsList(req Request) Response {
	s.mu.RLock()
	defer s.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(s.tools))
	for _, entry := range s.tools {
		defs = append(defs, entry.def)
	}

	result := ToolsListResult{Tools: defs}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, CodeInternalError,
			"err:mcp: failed to marshal tools list")
	}
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  data,
	}
}

// handleToolsCall dispatches to the registered handler for the named tool.
func (s *Server) handleToolsCall(ctx context.Context, req Request) Response {
	var params ToolsCallParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.errorResponse(req.ID, CodeInvalidParams,
				fmt.Sprintf("err:mcp: invalid params: %v", err))
		}
	}

	if params.Name == "" {
		return s.errorResponse(req.ID, CodeInvalidParams,
			"err:mcp: tool name is required")
	}

	// Look up handler.
	s.mu.RLock()
	entry, ok := s.tools[params.Name]
	s.mu.RUnlock()

	if !ok {
		return s.errorResponse(req.ID, CodeMethodNotFound,
			fmt.Sprintf("err:mcp: tool not found: %s", params.Name))
	}

	// Call handler.
	result, err := entry.handler.Handle(ctx, params.Arguments)
	if err != nil {
		return s.errorResponse(req.ID, CodeInternalError,
			fmt.Sprintf("err:mcp: tool %s: %v", params.Name, err))
	}

	// Wrap result in MCP content array format.
	var text string
	if result != nil {
		text = string(result)
	}
	content := ToolsCallResult{
		Content: []ToolResultContent{
			{Type: "text", Text: text},
		},
	}
	data, marshalErr := json.Marshal(content)
	if marshalErr != nil {
		return s.errorResponse(req.ID, CodeInternalError,
			"err:mcp: failed to marshal tool result")
	}

	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  data,
	}
}

// errorResponse creates a JSON-RPC error response.
func (s *Server) errorResponse(id any, code int, message string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
}

// sendError writes an error response directly to the transport.
// Used for parse errors where we could not determine the request ID.
func (s *Server) sendError(id any, code int, message string) {
	resp := s.errorResponse(id, code, message)
	if err := s.transport.WriteResponse(resp); err != nil {
		s.debugf("err:mcp: failed to send error response: %v", err)
	}
}

// debugf logs a debug message if a logger is configured.
func (s *Server) debugf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
	}
}
