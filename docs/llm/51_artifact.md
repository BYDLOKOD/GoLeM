---
id: artifact
kind: spec
touches: internal/artifact/
---

# Artifact - Typed Artifact Persistence

See also: [40_dag.md](40_dag.md) · [52_validation.md](52_validation.md).

## Purpose

`internal/artifact` provides a typed, persistable container for DAG pipeline
step outputs. Artifacts flow between steps via the scheduler: a step's output
artifacts become the inputs to its dependents.

## Types

```go
// internal/artifact/artifact.go
const (
    TypeText    ArtifactType = "text"
    TypeJSON    ArtifactType = "json"
    TypeFileRef ArtifactType = "file_ref"
)
```

| Type | Content semantics |
|------|-----------------|
| `text` | UTF-8 text; `Content` is the raw bytes. |
| `json` | JSON-encoded value; `Content` is valid JSON bytes. |
| `file_ref` | Path to an external file; `Content` is the UTF-8 path. |

## Artifact struct

```go
type Artifact struct {
    ID       string            `json:"id"`
    StepID   string            `json:"step_id"`
    Type     ArtifactType      `json:"type"`
    Content  []byte            `json:"content"`
    Metadata map[string]string `json:"metadata,omitempty"`
}
```

`ID` is 16 lowercase hex characters (8 random bytes from `crypto/rand`).
`crypto/rand` failure panics - a non-unique ID would corrupt data.

## Constructors

- `NewText(stepID, content string) *Artifact`
- `NewJSON(stepID string, v any) (*Artifact, error)` - marshals `v`; returns
  `err:validation` if marshal fails.
- `NewFileRef(stepID, path string) *Artifact`

## Persistence

`Save(dir string) error` - writes `artifact-<id>.json` inside `dir` using
atomic write (temp file + rename). Requires `ID`, `StepID`, and `Type` to
be non-empty; returns `err:validation` otherwise.

`Load(dir, id string) (*Artifact, error)` - reads `artifact-<id>.json` from
`dir`. Returns the raw `os.ReadFile` error (which may be `os.IsNotExist`) or
`err:validation` for malformed JSON.

## Usage in DAG

`ClaudeStepExecutor.Execute` wraps the claude subprocess stdout in
`artifact.NewText(step.ID, stdout)` and returns it as a single-element
slice. The scheduler passes these artifacts as `inputs` to dependent steps.
`buildInjectedPrompt` in `dag/executor.go` renders each input artifact's
`Content` prefixed with `Previous agent result (from step <stepID>):`, then
`Your task:` and the step's own prompt.

Artifacts are not currently persisted to disk during normal pipeline
execution - they exist only in memory and are passed directly through the
scheduler's results map. `Save` and `Load` are available for use cases that
require checkpointing.
