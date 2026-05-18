package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// pipeTransport returns a StdioTransport backed by an io.Pipe for the input
// and a bytes.Buffer for the output. The PipeWriter is used by test code to
// feed messages; the Buffer collects server responses.
func pipeTransport() (*StdioTransport, *io.PipeWriter, *bytes.Buffer) {
	pr, pw := io.Pipe()
	var buf bytes.Buffer
	transport := NewStdioTransport(pr, &buf)
	return transport, pw, &buf
}

// sendRequest marshals a JSON-RPC request and writes it (newline-terminated)
// to the PipeWriter so the server's transport can read it.
func sendRequest(t *testing.T, w *io.PipeWriter, method string, id any, params any) {
	t.Helper()
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		req.Params = raw
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

// readResponse polls the output buffer until a full line appears or times out.
func readResponse(t *testing.T, buf *bytes.Buffer, timeout time.Duration) Response {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		line, err := buf.ReadString('\n')
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var resp Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("invalid response JSON: %s: %v", line, err)
		}
		return resp
	}
	t.Fatal("timed out waiting for response")
	return Response{}
}

// stubHandler is a minimal ToolHandler for tests.
type stubHandler struct {
	mu     sync.Mutex
	result json.RawMessage
	err    error
	called bool
}

func (h *stubHandler) Handle(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.called = true
	return h.result, h.err
}

func (h *stubHandler) wasCalled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.called
}

// defaultTimeout is used for readResponse in most tests.
const defaultTimeout = 2 * time.Second

// ---------------------------------------------------------------------------
// Protocol types
// ---------------------------------------------------------------------------

func TestProtocolRoundtrip(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		ID:      float64(42),
		Method:  "tools/list",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	// Verify expected fields exist in the JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"jsonrpc", "id", "method"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q in marshalled request", key)
		}
	}

	// Unmarshal back and compare.
	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want %q", got.JSONRPC, "2.0")
	}
	if got.Method != "tools/list" {
		t.Errorf("Method = %q, want %q", got.Method, "tools/list")
	}
}

func TestResponseMarshal(t *testing.T) {
	result, _ := json.Marshal(map[string]string{"status": "ok"})
	resp := Response{
		JSONRPC: "2.0",
		ID:      float64(1),
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	// Verify "error" field is absent (omitempty).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["error"]; ok {
		t.Error("error field should be absent when nil")
	}
	if _, ok := raw["result"]; !ok {
		t.Error("result field should be present")
	}
}

func TestResponseMarshalError(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      float64(1),
		Error:   &RPCError{Code: CodeInternalError, Message: "boom"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["error"]; !ok {
		t.Error("error field should be present")
	}
	if _, ok := raw["result"]; ok {
		t.Error("result field should be absent when nil")
	}
}

func TestNotificationMarshal(t *testing.T) {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	data, err := json.Marshal(notif)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["id"]; ok {
		t.Error("id field must be absent in notification")
	}
	if _, ok := raw["method"]; !ok {
		t.Error("method field should be present")
	}
}

// ---------------------------------------------------------------------------
// Server: initialize
// ---------------------------------------------------------------------------

func TestInitializeResponse(t *testing.T) {
	tr, pw, buf := pipeTransport()
	srv := NewServer(tr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sendRequest(t, pw, "initialize", float64(1), struct{}{})
		_ = pw.Close()
	}()
	if err := srv.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, buf, defaultTimeout)
	if resp.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want %q", resp.JSONRPC, "2.0")
	}
	// ID should be preserved.
	idFloat, ok := resp.ID.(float64)
	if !ok || idFloat != 1 {
		t.Errorf("ID = %v, want 1", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Parse result.
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", result.ProtocolVersion, ProtocolVersion)
	}
	if result.ServerInfo.Name != "golem" {
		t.Errorf("ServerInfo.Name = %q, want %q", result.ServerInfo.Name, "golem")
	}
	if result.ServerInfo.Version != "0.1.0" {
		t.Errorf("ServerInfo.Version = %q, want %q", result.ServerInfo.Version, "0.1.0")
	}
}

// ---------------------------------------------------------------------------
// Server: notifications/initialized
// ---------------------------------------------------------------------------

func TestInitializedNotification(t *testing.T) {
	// Notification = no "id" field, so we send raw JSON without "id".
	input := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	// No response should be written for a notification.
	if buf.Len() != 0 {
		t.Errorf("expected no output for notification, got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Server: tools/list
// ---------------------------------------------------------------------------

func TestToolsList(t *testing.T) {
	tr, pw, buf := pipeTransport()
	srv := NewServer(tr)

	srv.RegisterTool(ToolDefinition{
		Name:        "glm_run",
		Description: "Run a GLM subagent",
		InputSchema: map[string]any{"type": "object"},
	}, &stubHandler{result: json.RawMessage(`"ok"`)})

	srv.RegisterTool(ToolDefinition{
		Name:        "glm_status",
		Description: "Get job status",
		InputSchema: map[string]any{"type": "object"},
	}, &stubHandler{result: json.RawMessage(`"ok"`)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sendRequest(t, pw, "tools/list", float64(1), struct{}{})
		_ = pw.Close()
	}()
	if err := srv.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, buf, defaultTimeout)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result.Tools))
	}

	// Both tools should be present (order is not guaranteed from map).
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"glm_run", "glm_status"} {
		if !names[want] {
			t.Errorf("tool %q not found in list", want)
		}
	}
}

func TestToolsListEmpty(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Tools == nil {
		t.Error("tools array should be non-nil (empty, not null)")
	}
	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
}

// ---------------------------------------------------------------------------
// Server: tools/call
// ---------------------------------------------------------------------------

func TestToolsCallDispatch(t *testing.T) {
	tr, pw, buf := pipeTransport()
	srv := NewServer(tr)

	handler := &stubHandler{result: json.RawMessage(`{"result":"ok"}`)}
	srv.RegisterTool(ToolDefinition{
		Name:        "glm_run",
		Description: "Run a GLM subagent",
		InputSchema: map[string]any{"type": "object"},
	}, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sendRequest(t, pw, "tools/call", float64(1), ToolsCallParams{
			Name:      "glm_run",
			Arguments: json.RawMessage(`{"prompt":"fix bug"}`),
		})
		_ = pw.Close()
	}()
	if err := srv.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	if !handler.wasCalled() {
		t.Fatal("handler was not called")
	}

	resp := readResponse(t, buf, defaultTimeout)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolsCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("content type = %q, want %q", result.Content[0].Type, "text")
	}
	if result.Content[0].Text != `{"result":"ok"}` {
		t.Errorf("content text = %q, want %q", result.Content[0].Text, `{"result":"ok"}`)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nonexistent"}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

// ---------------------------------------------------------------------------
// Server: error handling
// ---------------------------------------------------------------------------

func TestUnknownMethod(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"foo/bar","params":{}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestMalformedJSON(t *testing.T) {
	input := "{invalid json\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if resp.Error.Code != CodeParseError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeParseError)
	}
}

func TestInvalidRequest(t *testing.T) {
	// Valid JSON but missing "method" field.
	input := `{"jsonrpc":"2.0","id":1}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error for invalid request")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

// ---------------------------------------------------------------------------
// Server: concurrency
// ---------------------------------------------------------------------------

func TestConcurrentRegister(t *testing.T) {
	tr, pw, buf := pipeTransport()
	srv := NewServer(tr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Start Serve in a goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ctx)
	}()

	// Register tools concurrently from another goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			srv.RegisterTool(ToolDefinition{
				Name:        "tool_" + string(rune('a'+i%26)),
				Description: "concurrent tool",
				InputSchema: map[string]any{"type": "object"},
			}, &stubHandler{result: json.RawMessage(`"ok"`)})
		}
	}()

	// Send a request after some registrations may have happened.
	go func() {
		time.Sleep(20 * time.Millisecond)
		sendRequest(t, pw, "tools/list", float64(1), struct{}{})
		_ = pw.Close()
	}()

	wg.Wait()

	resp := readResponse(t, buf, defaultTimeout)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestContextCancellation(t *testing.T) {
	// Use a blocking reader that never returns data.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	var buf bytes.Buffer
	tr := NewStdioTransport(pr, &buf)
	srv := NewServer(tr)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()

	// Cancel the context — Serve should return.
	cancel()

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Errorf("Serve error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Server: tools/call with handler error
// ---------------------------------------------------------------------------

func TestToolsCallHandlerError(t *testing.T) {
	tr, pw, buf := pipeTransport()
	srv := NewServer(tr)

	handler := &stubHandler{err: errMCPInternal("something broke")}
	srv.RegisterTool(ToolDefinition{
		Name:        "glm_fail",
		Description: "Always fails",
		InputSchema: map[string]any{"type": "object"},
	}, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sendRequest(t, pw, "tools/call", float64(1), ToolsCallParams{
			Name:      "glm_fail",
			Arguments: json.RawMessage(`{}`),
		})
		_ = pw.Close()
	}()
	if err := srv.Serve(ctx); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error from failing handler")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInternalError)
	}
}

// ---------------------------------------------------------------------------
// Server: tools/call with missing params.name
// ---------------------------------------------------------------------------

func TestToolsCallMissingName(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error == nil {
		t.Fatal("expected error for missing tool name")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

// ---------------------------------------------------------------------------
// Server: null ID is a request (not notification)
// ---------------------------------------------------------------------------

func TestNullIDIsRequest(t *testing.T) {
	// "id": null is a valid request, not a notification.
	input := `{"jsonrpc":"2.0","id":null,"method":"initialize","params":{}}` + "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	// A response should be written (id: null is a request, not notification).
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	// ID should be null in the response.
	if resp.ID != nil {
		t.Errorf("ID = %v, want nil (null)", resp.ID)
	}
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

func TestTransportSkipsBlankLines(t *testing.T) {
	input := "\n\n" + `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	resp := readResponse(t, &buf, defaultTimeout)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want %q", resp.JSONRPC, "2.0")
	}
}

// ---------------------------------------------------------------------------
// Multiple messages in sequence
// ---------------------------------------------------------------------------

func TestMultipleMessages(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	reader := strings.NewReader(input)
	var buf bytes.Buffer
	tr := NewStdioTransport(reader, &buf)
	srv := NewServer(tr)

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Should get exactly 2 responses (initialize + tools/list).
	// The notification produces no response.
	resp1 := readResponse(t, &buf, defaultTimeout)
	if resp1.Error != nil {
		t.Fatalf("resp1 error: %+v", resp1.Error)
	}

	resp2 := readResponse(t, &buf, defaultTimeout)
	if resp2.Error != nil {
		t.Fatalf("resp2 error: %+v", resp2.Error)
	}

	// Verify IDs.
	if id, ok := resp1.ID.(float64); !ok || id != 1 {
		t.Errorf("resp1.ID = %v, want 1", resp1.ID)
	}
	if id, ok := resp2.ID.(float64); !ok || id != 2 {
		t.Errorf("resp2.ID = %v, want 2", resp2.ID)
	}
}
