---
id: mcp
kind: spec
touches: internal/mcp/
---

# MCP - JSON-RPC 2.0 Server and Stdio Transport

See also: [31_mcp_tools.md](31_mcp_tools.md) · [10_cli.md](10_cli.md).

## Overview

`glm mcp` starts a JSON-RPC 2.0 server over stdio. It is designed to be
registered as an MCP server in Claude Desktop or similar MCP hosts. The
server exposes eight tools that wrap GoLeM's subagent operations.

Entry point: `cmdMCP()` in `cmd/glm/main.go`. It loads config, starts the
proxy, creates the transport and server, registers all tools, then calls
`srv.Serve(ctx)` which blocks until stdin closes or SIGINT/SIGTERM arrives.

## Protocol

Protocol version advertised during `initialize`: `"2024-11-05"`.

The server implements exactly four methods:

| Method | Description |
|--------|-------------|
| `initialize` | Returns server capabilities and info. |
| `notifications/initialized` | No-op acknowledgment (notification, no response). |
| `tools/list` | Returns all registered tool definitions. |
| `tools/call` | Dispatches to a named tool handler. |

Any other method returns JSON-RPC error code `-32601` (Method Not Found).

## Message types (`internal/mcp/protocol.go`)

```go
// internal/mcp/protocol.go
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}
```

Standard JSON-RPC error codes used:

| Constant | Code | When |
|----------|------|------|
| `CodeParseError` | -32700 | JSON unmarshal failure. |
| `CodeInvalidRequest` | -32600 | Missing `jsonrpc` or `method`. |
| `CodeMethodNotFound` | -32601 | Unknown method or tool name. |
| `CodeInvalidParams` | -32602 | Invalid tool params. |
| `CodeInternalError` | -32603 | Marshal failure or tool panic. |

## Transport (`internal/mcp/transport.go`)

`StdioTransport` reads newline-delimited JSON from `in` (`os.Stdin`) and
writes JSON responses to `out` (`os.Stdout`).

- Max message size: 1 MB (`maxMessageSize`).
- Blank lines are skipped.
- `ReadMessage()` returns `io.EOF` when input closes.
- `WriteResponse` and `WriteNotification` are mutex-protected for concurrent
  use.
- Scanner buffer starts at 64 KB, grows up to `maxMessageSize`.

## Server (`internal/mcp/server.go`)

`NewServer(transport)` creates the server. `RegisterTool(def, handler)` adds
a tool under `def.Name` - safe to call concurrently with `Serve`.

`Serve(ctx)` loop:
1. Checks for context cancellation (non-blocking select).
2. Calls `transport.ReadMessage()` - blocks until a message arrives or EOF.
3. Unmarshals into `map[string]json.RawMessage`. On failure: sends parse
   error, continues.
4. If no `"id"` field: treats as notification, calls `handleNotification`.
5. Otherwise: unmarshals as `Request`, calls `dispatchRequest`, writes
   `Response`.

Tool call result is wrapped in:
```json
{"content": [{"type": "text", "text": "<json string>"}]}
```

## Tool registration pattern

All tools are registered in `cmdMCP()` (`cmd/glm/main.go:1185-1224`):

```go
tc := tools.NewToolContext(cfg, cfg.SubagentDir, "")
srv.RegisterTool(mcp.ToolDefinition{
    Name:        "glm_run",
    Description: "...",
    InputSchema: tools.RunDefinition(),
}, tools.RunHandler(tc))
```

`ToolContext` carries `Cfg`, `SubagentsRoot`, and `ProjectID`. When created
with an empty string, `NewToolContext` defaults `ProjectID` to `"mcp"`.

## Additional types

Exported types beyond `Request`/`Response` (all in `internal/mcp/`):

| Type | File | Purpose |
|------|------|---------|
| `Notification` | `protocol.go:32` | JSON-RPC notification (no `id` field). Used by `handleNotification`. |
| `ToolsCallParams` | `protocol.go:74` | Params for `tools/call`: `Name string` + `Arguments json.RawMessage`. |
| `ToolsCallResult` | `protocol.go:80` | Result wrapper for `tools/call`: `Content []ToolResultContent`. |
| `ToolResultContent` | `protocol.go:85` | Single content block: `Type string` (`"text"`) + `Text string`. |
| `ToolDefinition` | `handler.go:17` | Tool metadata for `tools/list`: `Name`, `Description`, `InputSchema`. |
| `ToolHandler` | `handler.go:12` | Interface: `Handle(ctx, json.RawMessage) (json.RawMessage, error)`. |
| `errMCPInternal` | `errors.go:6` | Sentinel builder: returns `fmt.Errorf("err:mcp: %s", msg)`. |

## Test injection helpers (`internal/mcp/tools/context.go`)

`productionSignalFn` (line 41) wraps `syscall.Kill(-pid, sig)` to send a signal
to a process group. `productionSleepFn` (line 46) sleeps for 1 second.

Both are passed as arguments to `cmd.KillCmd` (`tools/kill.go:43`) so tests in
`internal/cmd/` can inject deterministic fakes instead of real signals or sleeps.

## Known gap

Event bus is created in `cmdMCP` but not connected to the MCP transport for
progress notifications. There is a `// TODO` comment at
`cmd/glm/main.go:1181`. `glm_start` jobs do not emit JSON-RPC notifications
when they transition to `running` or `done`.
