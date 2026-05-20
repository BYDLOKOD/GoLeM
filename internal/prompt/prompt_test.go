// Package prompt_test verifies constraint expansion and system prompt assembly.
package prompt_test

import (
	"strings"
	"testing"

	"github.com/veschin/GoLeM/internal/prompt"
)

// --------------------------------------------------------------------------
// ExpandConstraint — known constraints
// --------------------------------------------------------------------------

// TestExpandConstraintReadonly verifies that "readonly" expands to the
// correct prohibition text.
func TestExpandConstraintReadonly(t *testing.T) {
	got, err := prompt.ExpandConstraint("readonly")
	if err != nil {
		t.Fatalf("ExpandConstraint(%q) error = %v", "readonly", err)
	}
	want := "You MUST NOT create, modify, or delete any files. You may only read files and report findings."
	if got != want {
		t.Errorf("ExpandConstraint(%q) = %q, want %q", "readonly", got, want)
	}
}

// TestExpandConstraintNoCreate verifies that "no-create" expands correctly.
func TestExpandConstraintNoCreate(t *testing.T) {
	got, err := prompt.ExpandConstraint("no-create")
	if err != nil {
		t.Fatalf("ExpandConstraint(%q) error = %v", "no-create", err)
	}
	want := "You MUST NOT create any new files. You may only read or modify existing files."
	if got != want {
		t.Errorf("ExpandConstraint(%q) = %q, want %q", "no-create", got, want)
	}
}

// TestExpandConstraintPlanFirst verifies that "plan-first" expands correctly.
func TestExpandConstraintPlanFirst(t *testing.T) {
	got, err := prompt.ExpandConstraint("plan-first")
	if err != nil {
		t.Fatalf("ExpandConstraint(%q) error = %v", "plan-first", err)
	}
	want := "Before making any changes, you MUST output a detailed plan of what you intend to do and wait for approval."
	if got != want {
		t.Errorf("ExpandConstraint(%q) = %q, want %q", "plan-first", got, want)
	}
}

// TestExpandConstraintScopeWithPath verifies that "scope:<path>" expands to
// a path-restriction sentence.
func TestExpandConstraintScopeWithPath(t *testing.T) {
	got, err := prompt.ExpandConstraint("scope:internal/proxy/")
	if err != nil {
		t.Fatalf("ExpandConstraint(%q) error = %v", "scope:internal/proxy/", err)
	}
	want := "You MUST only operate on files under the path: internal/proxy/. Do not read or modify any files outside this directory."
	if got != want {
		t.Errorf("ExpandConstraint(%q) = %q, want %q", "scope:internal/proxy/", got, want)
	}
}

// --------------------------------------------------------------------------
// ExpandConstraint — error cases
// --------------------------------------------------------------------------

// TestExpandConstraintUnknownKeyReturnsError verifies that an unrecognised
// key returns a descriptive error and an empty string.
func TestExpandConstraintUnknownKeyReturnsError(t *testing.T) {
	got, err := prompt.ExpandConstraint("totally-unknown")
	if err == nil {
		t.Fatalf("ExpandConstraint(%q) expected error, got nil (result=%q)", "totally-unknown", got)
	}
	if got != "" {
		t.Errorf("ExpandConstraint(%q) result = %q, want empty string on error", "totally-unknown", got)
	}
	if !strings.Contains(err.Error(), "totally-unknown") {
		t.Errorf("error %q does not mention the unknown key", err.Error())
	}
}

// TestExpandConstraintScopeWithoutPathReturnsError verifies that "scope:"
// (empty path suffix) returns an error.
func TestExpandConstraintScopeWithoutPathReturnsError(t *testing.T) {
	got, err := prompt.ExpandConstraint("scope:")
	if err == nil {
		t.Fatalf("ExpandConstraint(%q) expected error, got nil (result=%q)", "scope:", got)
	}
	if got != "" {
		t.Errorf("ExpandConstraint(%q) result = %q, want empty string on error", "scope:", got)
	}
}

// --------------------------------------------------------------------------
// AssembleSystemPrompt
// --------------------------------------------------------------------------

// TestAssembleSystemPromptConstraintsOnly verifies that when constraints are
// provided but systemPrompt is empty, expanded texts are joined with "\n\n".
func TestAssembleSystemPromptConstraintsOnly(t *testing.T) {
	constraints := []string{"readonly", "plan-first"}
	got, err := prompt.AssembleSystemPrompt(constraints, "")
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}

	readonly := "You MUST NOT create, modify, or delete any files. You may only read files and report findings."
	planFirst := "Before making any changes, you MUST output a detailed plan of what you intend to do and wait for approval."
	want := readonly + "\n\n" + planFirst

	if got != want {
		t.Errorf("AssembleSystemPrompt constraints-only =\n%q\nwant\n%q", got, want)
	}
}

// TestAssembleSystemPromptSystemPromptOnly verifies that when no constraints
// are provided, the systemPrompt is returned as-is (trimmed).
func TestAssembleSystemPromptSystemPromptOnly(t *testing.T) {
	got, err := prompt.AssembleSystemPrompt(nil, "You are a Go expert.")
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}
	want := "You are a Go expert."
	if got != want {
		t.Errorf("AssembleSystemPrompt prompt-only = %q, want %q", got, want)
	}
}

// TestAssembleSystemPromptBothConstraintsAndSystemPrompt verifies that when
// both are present, the expanded constraints come first, then a blank line,
// then the system prompt.
func TestAssembleSystemPromptBothConstraintsAndSystemPrompt(t *testing.T) {
	constraints := []string{"no-create"}
	systemPrompt := "Focus only on the proxy package."
	got, err := prompt.AssembleSystemPrompt(constraints, systemPrompt)
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}

	noCreate := "You MUST NOT create any new files. You may only read or modify existing files."
	want := noCreate + "\n\n" + systemPrompt

	if got != want {
		t.Errorf("AssembleSystemPrompt both =\n%q\nwant\n%q", got, want)
	}
}

// TestAssembleSystemPromptNeitherConstraintsNorSystemPrompt verifies that
// empty inputs produce an empty string with no error.
func TestAssembleSystemPromptNeitherConstraintsNorSystemPrompt(t *testing.T) {
	got, err := prompt.AssembleSystemPrompt(nil, "")
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}
	if got != "" {
		t.Errorf("AssembleSystemPrompt empty = %q, want empty string", got)
	}
}

// TestAssembleSystemPromptUnknownConstraintReturnsError verifies that an
// unknown constraint in the list causes AssembleSystemPrompt to return an
// error.
func TestAssembleSystemPromptUnknownConstraintReturnsError(t *testing.T) {
	constraints := []string{"readonly", "not-a-thing"}
	got, err := prompt.AssembleSystemPrompt(constraints, "")
	if err == nil {
		t.Fatalf("AssembleSystemPrompt expected error for unknown constraint, got nil (result=%q)", got)
	}
	if !strings.Contains(err.Error(), "not-a-thing") {
		t.Errorf("error %q does not mention the unknown key", err.Error())
	}
}

// --------------------------------------------------------------------------
// Whitespace trimming
// --------------------------------------------------------------------------

// TestAssembleSystemPromptTrimsLeadingAndTrailingWhitespaceFromSystemPrompt
// verifies that leading/trailing whitespace in systemPrompt is stripped.
func TestAssembleSystemPromptTrimsLeadingAndTrailingWhitespaceFromSystemPrompt(t *testing.T) {
	got, err := prompt.AssembleSystemPrompt(nil, "  \n  You are careful.  \n  ")
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}
	want := "You are careful."
	if got != want {
		t.Errorf("AssembleSystemPrompt trim = %q, want %q", got, want)
	}
}

// TestAssembleSystemPromptTrimsWhitespaceWhenCombined verifies that whitespace
// in systemPrompt is trimmed even when constraints are present.
func TestAssembleSystemPromptTrimsWhitespaceWhenCombined(t *testing.T) {
	got, err := prompt.AssembleSystemPrompt([]string{"no-create"}, "  Be concise.  ")
	if err != nil {
		t.Fatalf("AssembleSystemPrompt error = %v", err)
	}
	noCreate := "You MUST NOT create any new files. You may only read or modify existing files."
	want := noCreate + "\n\n" + "Be concise."
	if got != want {
		t.Errorf("AssembleSystemPrompt trim+constraints =\n%q\nwant\n%q", got, want)
	}
}
