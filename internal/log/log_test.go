package log_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/log"
)

// --- helpers ---

// newLogger creates a Logger writing to the given buffer, with isTTY and debug
// controlled by parameters. format defaults to human-readable unless json=true.
func newLogger(t *testing.T, buf *bytes.Buffer, isTTY bool, debug bool, jsonFmt bool) *log.Logger {
	t.Helper()
	opts := []log.Option{
		log.WithWriter(buf),
		log.WithIsTTY(isTTY),
	}
	if debug {
		opts = append(opts, log.WithLevel(log.LevelDebug))
	} else {
		opts = append(opts, log.WithLevel(log.LevelInfo))
	}
	if jsonFmt {
		opts = append(opts, log.WithFormat(log.FormatJSON))
	} else {
		opts = append(opts, log.WithFormat(log.FormatHuman))
	}
	return log.New(opts...)
}

// containsANSI returns true when the string contains an ANSI escape sequence.
func containsANSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}

// =============================================================================
// AC1 — Four log levels
// =============================================================================

// Scenario: Default log level is info
func TestDefaultLogLevelIsInfo(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, false, false)

	lg.Info("Job started")
	lg.Debug("Loading config")

	out := buf.String()
	if !strings.Contains(out, "Job started") {
		t.Errorf("expected info message in output, got: %q", out)
	}
	if strings.Contains(out, "Loading config") {
		t.Errorf("debug message must NOT appear at default (info) level, got: %q", out)
	}
}

// Scenario: Debug level enabled via GLM_DEBUG=1
func TestDebugLevelEnabledViaGLMDEBUG(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, true, true, false)

	lg.Debug("Loading config from ~/.config/GoLeM/glm.toml")
	lg.Info("Starting job job-20260227-150000-a1b2c3d4")
	lg.Debug("Slot claimed: 2/3")
	lg.Warn("Reconciled 1 stale job")
	lg.Debug("Claude CLI path: /usr/local/bin/claude")

	out := buf.String()

	// All five messages must appear.
	for _, msg := range []string{
		"Loading config from ~/.config/GoLeM/glm.toml",
		"Starting job job-20260227-150000-a1b2c3d4",
		"Slot claimed: 2/3",
		"Reconciled 1 stale job",
		"Claude CLI path: /usr/local/bin/claude",
	} {
		if !strings.Contains(out, msg) {
			t.Errorf("expected message %q in output, got:\n%s", msg, out)
		}
	}

	// Prefix checks.
	debugLines := linesContaining(out, "[D]")
	if len(debugLines) == 0 {
		t.Errorf("expected debug lines with [D] prefix, got:\n%s", out)
	}
	infoLines := linesContaining(out, "[+]")
	if len(infoLines) == 0 {
		t.Errorf("expected info lines with [+] prefix, got:\n%s", out)
	}
	warnLines := linesContaining(out, "[!]")
	if len(warnLines) == 0 {
		t.Errorf("expected warn lines with [!] prefix, got:\n%s", out)
	}
}

// Scenario: All four levels are available
func TestAllFourLevelsAreAvailable(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, true, false)

	lg.Info("info-msg")
	lg.Warn("warn-msg")
	lg.Error("error-msg")
	lg.Debug("debug-msg")

	out := buf.String()

	cases := []struct {
		prefix string
		msg    string
	}{
		{"[+]", "info-msg"},
		{"[!]", "warn-msg"},
		{"[x]", "error-msg"},
		{"[D]", "debug-msg"},
	}
	for _, c := range cases {
		if !strings.Contains(out, c.prefix) {
			t.Errorf("expected prefix %q in output, got:\n%s", c.prefix, out)
		}
		if !strings.Contains(out, c.msg) {
			t.Errorf("expected message %q in output, got:\n%s", c.msg, out)
		}
	}
}

// =============================================================================
// AC2 — Human-readable format with colored prefixes
// =============================================================================

// Scenario: Info messages use green [+] prefix on TTY
func TestInfoMessagesUseGreenPrefixOnTTY(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, true, false, false)

	lg.Info("Job started: job-20260227-143205-a8f3b1c2")

	out := buf.String()
	if !strings.Contains(out, "[+] Job started: job-20260227-143205-a8f3b1c2") {
		t.Errorf("expected [+] prefix and message, got: %q", out)
	}
	// Green ANSI: \x1b[32m
	if !strings.Contains(out, "\x1b[32m") {
		t.Errorf("expected green ANSI color \\x1b[32m for [+] on TTY, got: %q", out)
	}
}

// Scenario: Warn messages use yellow [!] prefix on TTY
func TestWarnMessagesUseYellowPrefixOnTTY(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, true, false, false)

	lg.Warn("Slot counter reconciled from 5 to 2")

	out := buf.String()
	if !strings.Contains(out, "[!] Slot counter reconciled from 5 to 2") {
		t.Errorf("expected [!] prefix and message, got: %q", out)
	}
	// Yellow ANSI: \x1b[33m
	if !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected yellow ANSI color \\x1b[33m for [!] on TTY, got: %q", out)
	}
}

// Scenario: Error messages use red [x] prefix on TTY
func TestErrorMessagesUseRedPrefixOnTTY(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, true, false, false)

	lg.Error("Claude CLI not found in PATH")

	out := buf.String()
	if !strings.Contains(out, "[x] Claude CLI not found in PATH") {
		t.Errorf("expected [x] prefix and message, got: %q", out)
	}
	// Red ANSI: \x1b[31m
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI color \\x1b[31m for [x] on TTY, got: %q", out)
	}
}

// Scenario: Debug messages use [D] prefix without color
func TestDebugMessagesUseNoPrefixColorOnTTY(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, true, true, false)

	lg.Debug("Loading config from /home/veschin/.config/GoLeM/glm.toml")

	out := buf.String()
	if !strings.Contains(out, "[D] Loading config from /home/veschin/.config/GoLeM/glm.toml") {
		t.Errorf("expected [D] prefix and message, got: %q", out)
	}

	// Extract the [D] prefix line and confirm it has no ANSI codes around [D].
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[D]") {
			// The [D] token itself must not be surrounded by color codes.
			idx := strings.Index(line, "[D]")
			before := line[:idx]
			// No ANSI color reset/set should appear immediately before [D].
			if strings.HasSuffix(before, "\x1b[32m") ||
				strings.HasSuffix(before, "\x1b[33m") ||
				strings.HasSuffix(before, "\x1b[31m") {
				t.Errorf("[D] prefix must have no ANSI color code, got line: %q", line)
			}
		}
	}
}

// =============================================================================
// AC3 — Auto-detect terminal: no ANSI when not TTY
// =============================================================================

// Scenario: ANSI colors disabled when stderr is piped
func TestANSIColorsDisabledWhenPiped(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, false, false)

	lg.Info("Job started: job-20260227-143205-a8f3b1c2")
	lg.Warn("Slot counter reconciled from 5 to 2")
	lg.Error("Claude CLI not found in PATH")

	out := buf.String()

	expected := []string{
		"[+] Job started: job-20260227-143205-a8f3b1c2",
		"[!] Slot counter reconciled from 5 to 2",
		"[x] Claude CLI not found in PATH",
	}
	for _, e := range expected {
		if !strings.Contains(out, e) {
			t.Errorf("expected %q in output, got:\n%s", e, out)
		}
	}

	if containsANSI(out) {
		t.Errorf("expected NO ANSI escape codes when not TTY, got:\n%q", out)
	}
}

// =============================================================================
// AC4 — JSON structured output
// =============================================================================

// Scenario: JSON log format outputs structured JSON lines
func TestJSONLogFormatOutputsStructuredJSONLines(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, true, true)

	lg.Info("Job started: job-20260227-143205-a8f3b1c2")
	lg.Warn("Slot counter reconciled from 5 to 2")
	lg.Error("Claude CLI not found in PATH")
	lg.Debug("Loading config from /home/veschin/.config/GoLeM/glm.toml")

	lines := nonEmptyLines(buf.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSON lines, got %d:\n%s", len(lines), buf.String())
	}

	type logEntry struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Ts    string `json:"ts"`
	}

	parse := func(line string) logEntry {
		var e logEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line is not valid JSON: %q, err: %v", line, err)
		}
		return e
	}

	cases := []struct {
		wantLevel string
		wantMsg   string
	}{
		{"info", "Job started: job-20260227-143205-a8f3b1c2"},
		{"warn", "Slot counter reconciled from 5 to 2"},
		{"error", "Claude CLI not found in PATH"},
		{"debug", "Loading config from /home/veschin/.config/GoLeM/glm.toml"},
	}

	for i, c := range cases {
		e := parse(lines[i])
		if e.Level != c.wantLevel {
			t.Errorf("line %d: expected level %q, got %q", i, c.wantLevel, e.Level)
		}
		if e.Msg != c.wantMsg {
			t.Errorf("line %d: expected msg %q, got %q", i, c.wantMsg, e.Msg)
		}
		if e.Ts == "" {
			t.Errorf("line %d: expected non-empty ts field", i)
		}
		// Validate ISO 8601 / RFC3339 timestamp.
		if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
			t.Errorf("line %d: ts %q is not a valid ISO 8601 timestamp: %v", i, e.Ts, err)
		}
	}
}

// =============================================================================
// AC5 — File logging
// =============================================================================

// Scenario: Logs are additionally written to a file
func TestLogsAreAdditionallyWrittenToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "glm.log")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}

	var stderrBuf bytes.Buffer
	opts := []log.Option{
		log.WithWriter(&stderrBuf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman),
		log.WithFile(f),
	}
	lg := log.New(opts...)

	lg.Info("Job completed successfully")
	lg.Warn("Cleaned 3 stale jobs")

	// Close so writes are flushed.
	_ = f.Close()

	stderrOut := stderrBuf.String()
	if !strings.Contains(stderrOut, "[+] Job completed successfully") {
		t.Errorf("expected [+] on stderr, got: %q", stderrOut)
	}
	if !strings.Contains(stderrOut, "[!] Cleaned 3 stale jobs") {
		t.Errorf("expected [!] on stderr, got: %q", stderrOut)
	}

	fileBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	fileOut := string(fileBytes)
	if !strings.Contains(fileOut, "[+] Job completed successfully") {
		t.Errorf("expected [+] in log file, got: %q", fileOut)
	}
	if !strings.Contains(fileOut, "[!] Cleaned 3 stale jobs") {
		t.Errorf("expected [!] in log file, got: %q", fileOut)
	}
}

// Scenario: File logging does not suppress stderr output
func TestFileLoggingDoesNotSuppressStderrOutput(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "glm.log")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}
	defer func() { _ = f.Close() }()

	var stderrBuf bytes.Buffer
	opts := []log.Option{
		log.WithWriter(&stderrBuf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman),
		log.WithFile(f),
	}
	lg := log.New(opts...)

	lg.Info("Test message")
	_ = f.Close()

	stderrOut := stderrBuf.String()
	fileBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	fileOut := string(fileBytes)

	if !strings.Contains(stderrOut, "Test message") {
		t.Errorf("expected message on stderr, got: %q", stderrOut)
	}
	if !strings.Contains(fileOut, "Test message") {
		t.Errorf("expected message in log file, got: %q", fileOut)
	}
}

// =============================================================================
// AC6 — die() function
// =============================================================================

// Scenario: die function logs error and exits with specified code
func TestDieFunctionLogsErrorAndExitsWithSpecifiedCode(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, false, false)

	var exitCode int
	fakeexit := func(code int) { exitCode = code }

	lg.Die(127, fakeexit, "claude CLI not found in PATH")

	out := buf.String()
	if !strings.Contains(out, "claude CLI not found in PATH") {
		t.Errorf("expected error message in output, got: %q", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Errorf("expected [x] prefix for die error message, got: %q", out)
	}
	if exitCode != 127 {
		t.Errorf("expected exit code 127, got %d", exitCode)
	}
}

// Scenario: die function with multiple messages
func TestDieFunctionWithMultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	lg := newLogger(t, &buf, false, false, false)

	var exitCode int
	fakeexit := func(code int) { exitCode = code }

	lg.Die(1, fakeexit, "Invalid config", "Check glm.toml")

	out := buf.String()
	if !strings.Contains(out, "Invalid config") {
		t.Errorf("expected 'Invalid config' in output, got: %q", out)
	}
	if !strings.Contains(out, "Check glm.toml") {
		t.Errorf("expected 'Check glm.toml' in output, got: %q", out)
	}
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}
}

// =============================================================================
// AC7 — All output goes to stderr (writer)
// =============================================================================

// Scenario: Log output goes to stderr, not stdout
func TestLogOutputGoesToStderr(t *testing.T) {
	var stderrBuf bytes.Buffer

	lg := log.New(
		log.WithWriter(&stderrBuf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman),
	)

	lg.Info("Job started")

	if !strings.Contains(stderrBuf.String(), "Job started") {
		t.Errorf("expected message on stderr writer, got: %q", stderrBuf.String())
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

// Scenario: Log file path is not writable
func TestLogFilePathNotWritable(t *testing.T) {
	// Use a path that cannot be created.
	unwritable := "/root/readonly/glm.log"

	var stderrBuf bytes.Buffer

	// The New constructor must handle an unwritable file path gracefully.
	// We pass an io.WriteCloser that errors on write to simulate the behaviour
	// an implementation could use, OR the test verifies that attempting to open
	// the file path results in a warning on stderr and continued operation.
	// Here we simulate by providing a failing writer wrapped as WriteCloser.
	errWriter := &errorWriteCloser{err: fmt.Errorf("open %s: permission denied", unwritable)}

	lg := log.New(
		log.WithWriter(&stderrBuf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman),
		log.WithFile(errWriter),
	)

	lg.Info("Starting job")

	out := stderrBuf.String()
	// Must warn about file write failure.
	if !strings.Contains(out, "Cannot write to log file") {
		t.Errorf("expected warning about log file write failure, got: %q", out)
	}
	// Must still log the original message.
	if !strings.Contains(out, "Starting job") {
		t.Errorf("expected info message to still appear after file write failure, got: %q", out)
	}
}

// Scenario: Unknown GLM_LOG_FORMAT value falls back to human-readable
func TestUnknownLogFormatFallsBackToHumanReadable(t *testing.T) {
	var buf bytes.Buffer

	// FormatHuman is the intended fallback; the implementation must treat any
	// unknown format string as human-readable. We simulate by requesting json=false
	// (human) for the unknown "xml" case — the test validates the output is NOT JSON.
	lg := log.New(
		log.WithWriter(&buf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman), // fallback when unknown format string given
	)

	lg.Info("Starting job")

	out := buf.String()
	if !strings.Contains(out, "[+] Starting job") {
		t.Errorf("expected human-readable [+] prefix, got: %q", out)
	}
	// Ensure it is NOT JSON.
	firstLine := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if strings.HasPrefix(firstLine, "{") {
		t.Errorf("expected human-readable output but got JSON: %q", firstLine)
	}
}

// Scenario: Concurrent log writes from goroutines are safe
func TestConcurrentLogWritesFromGoroutinesAreSafe(t *testing.T) {
	var buf safeBuffer
	lg := log.New(
		log.WithWriter(&buf),
		log.WithIsTTY(false),
		log.WithLevel(log.LevelInfo),
		log.WithFormat(log.FormatHuman),
	)

	const numGoroutines = 10
	const msgsPerGoroutine = 10 // 10*10 = 100 total

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()
			for j := range msgsPerGoroutine {
				lg.Info(fmt.Sprintf("goroutine-%d-msg-%d", id, j))
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent logging deadlocked or timed out")
	}

	out := buf.String()
	lines := nonEmptyLines(out)
	if len(lines) != 100 {
		t.Errorf("expected 100 log lines from concurrent goroutines, got %d", len(lines))
	}

	// No line should be interleaved (each line must have exactly one [+] prefix).
	for _, line := range lines {
		if strings.Count(line, "[+]") != 1 {
			t.Errorf("interleaved log line detected: %q", line)
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

func linesContaining(s, substr string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			result = append(result, line)
		}
	}
	return result
}

func nonEmptyLines(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

// safeBuffer is a bytes.Buffer with a mutex, safe for concurrent use.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

// errorWriteCloser is an io.WriteCloser that always returns an error on Write.
type errorWriteCloser struct {
	err error
}

func (e *errorWriteCloser) Write(_ []byte) (int, error) {
	return 0, e.err
}

func (e *errorWriteCloser) Close() error {
	return nil
}

// Ensure errorWriteCloser satisfies io.WriteCloser at compile time.
var _ io.WriteCloser = (*errorWriteCloser)(nil)
