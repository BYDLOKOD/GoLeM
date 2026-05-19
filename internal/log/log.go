// Package log provides leveled, colored, optionally structured logging for GoLeM.
// All log output goes to stderr. Supports human-readable format with ANSI colors,
// JSON structured output (GLM_LOG_FORMAT=json), and file logging (GLM_LOG_FILE).
package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents the logging level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Format represents the log output format.
type Format int

const (
	FormatHuman Format = iota
	FormatJSON
)

// Option is a functional option for Logger construction.
type Option func(*Logger)

// Logger is the structured logger for GoLeM.
type Logger struct {
	mu     sync.Mutex
	level  Level
	format Format
	isTTY  bool
	out    io.Writer
	file   io.WriteCloser
}

// WithLevel sets the logging level.
func WithLevel(l Level) Option {
	return func(lg *Logger) {
		lg.level = l
	}
}

// WithFormat sets the log format (human or json).
func WithFormat(f Format) Option {
	return func(lg *Logger) {
		lg.format = f
	}
}

// WithWriter sets the output writer (defaults to os.Stderr).
func WithWriter(w io.Writer) Option {
	return func(lg *Logger) {
		lg.out = w
	}
}

// WithIsTTY sets whether the output is a TTY (controls ANSI color).
func WithIsTTY(tty bool) Option {
	return func(lg *Logger) {
		lg.isTTY = tty
	}
}

// WithFile sets an additional file writer for log output.
func WithFile(w io.WriteCloser) Option {
	return func(lg *Logger) {
		lg.file = w
	}
}

// New creates a new Logger with the given options.
func New(opts ...Option) *Logger {
	l := &Logger{
		level:  LevelInfo, // default level
		format: FormatHuman,
		isTTY:  false,
		out:    os.Stderr, // default to stderr
		file:   nil,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Info logs a message at info level.
func (l *Logger) Info(msg string) {
	l.log(LevelInfo, "[+]", msg, "\x1b[32m")
}

// Warn logs a message at warn level.
func (l *Logger) Warn(msg string) {
	l.log(LevelWarn, "[!]", msg, "\x1b[33m")
}

// Error logs a message at error level.
func (l *Logger) Error(msg string) {
	l.log(LevelError, "[x]", msg, "\x1b[31m")
}

// Debug logs a message at debug level.
func (l *Logger) Debug(msg string) {
	l.log(LevelDebug, "[D]", msg, "") // no color for debug
}

// log is the internal logging method.
func (l *Logger) log(msgLevel Level, prefix, msg, colorCode string) {
	// Level filtering: only log if message level >= logger level
	if msgLevel < l.level {
		return
	}

	var output string

	if l.format == FormatJSON {
		// JSON format: {"level":"info","msg":"...","ts":"2006-01-02T15:04:05Z07:00"}\n
		entry := struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Ts    string `json:"ts"`
		}{
			Level: levelToString(msgLevel),
			Msg:   msg,
			Ts:    time.Now().Format(time.RFC3339),
		}
		data, err := json.Marshal(entry)
		if err != nil {
			output = fmt.Sprintf(`{"level":"error","msg":"failed to marshal JSON: %v"}\n`, err)
		} else {
			output = string(data) + "\n"
		}
	} else {
		// Human format: "[prefix] message\n"
		if l.isTTY && colorCode != "" {
			// Color the entire line (prefix, space, message)
			output = fmt.Sprintf("%s%s %s\x1b[0m\n", colorCode, prefix, msg)
		} else {
			output = fmt.Sprintf("%s %s\n", prefix, msg)
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Write to primary output
	_, _ = l.out.Write([]byte(output))

	// Write to file if configured
	if l.file != nil {
		if _, err := l.file.Write([]byte(output)); err != nil {
			// If file write fails, write warning to stdout first, then the original message
			_, _ = l.out.Write([]byte("[!] Cannot write to log file\n"))
		}
	}
}

// levelToString converts Level to its string representation.
func levelToString(l Level) string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// Die logs an error message and exits the process with the given code.
// exitFn is injected for testing; production callers pass os.Exit.
func (l *Logger) Die(code int, exitFn func(int), msgs ...string) {
	for _, msg := range msgs {
		l.Error(msg)
	}
	exitFn(code)
}
