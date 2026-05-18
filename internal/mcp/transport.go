package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// maxMessageSize is the maximum size of a single JSON-RPC message in bytes.
const maxMessageSize = 1024 * 1024 // 1 MB

// StdioTransport reads newline-delimited JSON-RPC messages from in and
// writes JSON responses to out. Both are injected for testability.
// Writes are serialized with a mutex so that concurrent goroutines
// (Server.Serve and NotificationSender) can safely write to the same output.
type StdioTransport struct {
	scanner *bufio.Scanner
	mu      sync.Mutex
	encoder *json.Encoder
}

// NewStdioTransport creates a transport that reads from in and writes to out.
func NewStdioTransport(in io.Reader, out io.Writer) *StdioTransport {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageSize)
	return &StdioTransport{
		scanner: scanner,
		encoder: json.NewEncoder(out),
	}
}

// ReadMessage reads the next newline-delimited JSON message.
// Blank lines are skipped. Returns io.EOF when the input is closed.
func (t *StdioTransport) ReadMessage() (json.RawMessage, error) {
	for {
		if !t.scanner.Scan() {
			if err := t.scanner.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		line := t.scanner.Bytes()
		if len(line) == 0 {
			continue // skip blank lines
		}
		// Make a copy because scanner buffer is reused.
		msg := make([]byte, len(line))
		copy(msg, line)
		return json.RawMessage(msg), nil
	}
}

// WriteResponse writes a JSON-RPC response as a single newline-terminated
// JSON line to the output. Writes are mutex-protected for concurrent use.
func (t *StdioTransport) WriteResponse(resp Response) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.encoder.Encode(resp)
}

// WriteNotification writes a JSON-RPC notification to the output.
// Notifications have no "id" field and do not expect a response.
// Writes are mutex-protected for concurrent use.
func (t *StdioTransport) WriteNotification(notif Notification) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.encoder.Encode(notif)
}
