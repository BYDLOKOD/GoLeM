---
id: logging
kind: spec
touches: internal/log/, internal/exitcode/
---

# Logging and Exit Codes

See also: [10_cli.md](10_cli.md).

## Logger (`internal/log/log.go`)

`log.New(opts ...) *Logger` constructs a logger. Default: level `Info`,
format `Human`, output `os.Stderr`.

### Levels

```
LevelDebug < LevelInfo < LevelWarn < LevelError
```

Messages below the configured level are dropped. `GLM_DEBUG=1` sets level to
`LevelDebug` in `initLogger` (`cmd/glm/main.go`).

### Formats

**Human** (default):
- TTY: `\x1b[<color>][prefix] message\x1b[0m\n`
- Non-TTY: `[prefix] message\n`

Prefixes and colors: `[+]` green (info), `[!]` yellow (warn), `[x]` red
(error), `[D]` no color (debug).

**JSON** (`GLM_LOG_FORMAT=json`):
```json
{"level":"info","msg":"...","ts":"2006-01-02T15:04:05Z07:00"}
```

### Options

| Option | Effect |
|--------|--------|
| `WithLevel(l)` | Set minimum level. |
| `WithFormat(f)` | `FormatHuman` or `FormatJSON`. |
| `WithWriter(w)` | Primary output writer (default `os.Stderr`). |
| `WithIsTTY(b)` | Enable ANSI color in human format. |
| `WithFile(w)` | Additional file writer (append). Opened from `GLM_LOG_FILE`. |

All writes are mutex-protected. File write failure prints `[!] Cannot write
to log file` to the primary writer and continues.

### Die

`Logger.Die(code int, exitFn func(int), msgs ...string)` logs each message at
error level then calls `exitFn(code)`. `exitFn` is injected for testability;
production callers pass `os.Exit`.

## Exit codes (`internal/exitcode/exitcode.go`)

### Numeric constants

| Constant | Value | Meaning |
|----------|-------|---------|
| `OK` | 0 | Success. |
| `UserError` | 1 | User/validation/internal error. |
| `NotFound` | 3 | Resource not found. |
| `Timeout` | 124 | Subprocess timed out. |
| `DependencyMissing` | 127 | Required binary not in PATH. |

### Error categories

```go
const (
    CategoryUser       Category = "user"
    CategoryNotFound   Category = "not_found"
    CategoryDependency Category = "dependency"
    CategoryValidation Category = "validation"
    CategoryInternal   Category = "internal"
    CategoryTimeout    Category = "timeout"
)
```

`exitcode.Error` implements `error`. Its `Error()` method returns
`"err:<category> <message>"`, optionally appended with `". <suggestion>"`.

`ExitCodeFor(c Category) int` maps categories to numeric exit codes:
- `user`, `validation`, `internal` -> 1
- `not_found` -> 3
- `timeout` -> 124
- `dependency` -> 127

### Error string convention

All errors in GoLeM follow the format `err:<category> "message"`. The `die`
helper in `main.go` detects the category by substring matching on the error
string:
- contains `err:not_found` -> exit 3
- contains `err:dependency` -> exit 127
- contains `err:timeout` -> exit 124
- otherwise -> exit 1

### Permission error detection

`exitcode.IsPermissionError(stderr string) bool` checks (case-insensitive)
for: `permission`, `not allowed`, `denied`, `unauthorized`. Used by
`claude.MapStatus` to classify subprocess failures as `permission_error`.
