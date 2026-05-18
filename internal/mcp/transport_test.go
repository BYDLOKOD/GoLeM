package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewStdioTransport(strings.NewReader(""), &buf)

	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/tools/progress",
		Params: json.RawMessage(`{
			"progressToken": "job-123",
			"progress": 50,
			"total": 100
		}`),
	}

	if err := transport.WriteNotification(notif); err != nil {
		t.Fatalf("WriteNotification: %v", err)
	}

	// Read the line from buffer.
	line, err := buf.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}

	// Verify it's valid JSON.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("invalid JSON: %s: %v", line, err)
	}

	// Verify no "id" field (it's a notification, not a request/response).
	if _, hasID := decoded["id"]; hasID {
		t.Error("notification should not have an 'id' field")
	}

	if decoded["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", decoded["jsonrpc"])
	}

	if decoded["method"] != "notifications/tools/progress" {
		t.Errorf("method = %v, want notifications/tools/progress", decoded["method"])
	}
}

func TestWriteNotification_MultipleNotifications(t *testing.T) {
	var buf bytes.Buffer
	transport := NewStdioTransport(strings.NewReader(""), &buf)

	for i := 0; i < 3; i++ {
		notif := Notification{
			JSONRPC: "2.0",
			Method:  "notifications/tools/progress",
		}
		if err := transport.WriteNotification(notif); err != nil {
			t.Fatalf("WriteNotification %d: %v", i, err)
		}
	}

	// Should have 3 lines.
	lines := 0
	for buf.Len() > 0 {
		_, err := buf.ReadString('\n')
		if err != nil {
			break
		}
		lines++
	}
	if lines != 3 {
		t.Errorf("expected 3 lines, got %d", lines)
	}
}

func TestWriteResponse_ConcurrentWithNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewStdioTransport(strings.NewReader(""), &buf)

	// Write a response and a notification sequentially to verify both use the
	// same encoder and produce valid, separate JSON lines.
	resp := Response{
		JSONRPC: "2.0",
		ID:      float64(1),
		Result:  json.RawMessage(`{"ok":true}`),
	}
	if err := transport.WriteResponse(resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/tools/progress",
	}
	if err := transport.WriteNotification(notif); err != nil {
		t.Fatalf("WriteNotification: %v", err)
	}

	// Both should produce separate lines.
	line1, err := buf.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString line1: %v", err)
	}
	line2, err := buf.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString line2: %v", err)
	}

	// line1 should have "id", line2 should not.
	var decoded1 map[string]any
	if err := json.Unmarshal([]byte(line1), &decoded1); err != nil {
		t.Fatalf("invalid JSON line1: %v", err)
	}
	if _, hasID := decoded1["id"]; !hasID {
		t.Error("response should have 'id' field")
	}

	var decoded2 map[string]any
	if err := json.Unmarshal([]byte(line2), &decoded2); err != nil {
		t.Fatalf("invalid JSON line2: %v", err)
	}
	if _, hasID := decoded2["id"]; hasID {
		t.Error("notification should not have 'id' field")
	}
}
