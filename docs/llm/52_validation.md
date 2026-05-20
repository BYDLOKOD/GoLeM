---
id: validation
kind: spec
touches: internal/validation/
---

# Validation - Output Validation Rules

See also: [40_dag.md](40_dag.md) · [31_mcp_tools.md](31_mcp_tools.md).

## Purpose

`internal/validation` provides a single `ValidationRule` struct and its
`Check` method, used by DAG gate steps, step-level validate fields, and the
`glm_chain` MCP tool to assert properties of claude subprocess stdout.

## ValidationRule

```go
// internal/validation/validation.go
type ValidationRule struct {
    Contains    []string `json:"contains,omitempty"`
    NotContains []string `json:"not_contains,omitempty"`
    Matches     string   `json:"matches,omitempty"`
}
```

## Check method

`(r *ValidationRule) Check(stdout string) error`

A nil receiver returns nil immediately (no rule = always passes).

Checks are applied in order:

1. `Contains` - every string in the list must appear as a substring of
   `stdout`. First missing string returns `ValidationError{Condition:
   "contains", Detail: "expected %q in output"}`.
2. `NotContains` - no string in the list may appear as a substring. First
   found string returns `ValidationError{Condition: "not_contains",
   Detail: "forbidden %q found in output"}`.
3. `Matches` - if non-empty, compiles as a Go regexp and tests against
   `stdout` with `MatchString`. A compile error returns a plain `error`
   (not a `ValidationError`). No match returns `ValidationError{Condition:
   "matches", Detail: "output does not match pattern %q"}`.

## ValidationError

```go
type ValidationError struct {
    Condition string
    Detail    string
}
func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed [%s]: %s", e.Condition, e.Detail)
}
```

`dag/executor.go` uses `errors.As(err, &ve)` to distinguish validation
failures from other errors - only validation errors trigger step retry in
`retryExecute`.

## Usage locations

| Call site | What is validated |
|-----------|------------------|
| `dag.ClaudeStepExecutor.applyValidation` | Step stdout after claude completes. |
| `dag.ClaudeStepExecutor.executeGate` | Combined content of all dependency artifacts. |
| `cmd.ChainCmd` (via `glm_chain` steps field) | Each chain step output when `ChainInputStep.Validate` is set. |
