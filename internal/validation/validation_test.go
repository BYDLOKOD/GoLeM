package validation

import (
	"strings"
	"testing"
)

func TestCheck_NilRule(t *testing.T) {
	var r *ValidationRule
	if err := r.Check("anything"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheck_EmptyRule(t *testing.T) {
	r := &ValidationRule{}
	if err := r.Check("anything"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheck_Contains_AllPresent(t *testing.T) {
	r := &ValidationRule{Contains: []string{"hello", "world"}}
	if err := r.Check("hello world"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheck_Contains_OneMissing(t *testing.T) {
	r := &ValidationRule{Contains: []string{"hello", "missing"}}
	err := r.Check("hello world")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "contains") {
		t.Fatalf("expected error mentioning 'contains', got %q", err.Error())
	}
}

func TestCheck_NotContains_NonePresent(t *testing.T) {
	r := &ValidationRule{NotContains: []string{"bad", "evil"}}
	if err := r.Check("good text"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheck_NotContains_Forbidden(t *testing.T) {
	r := &ValidationRule{NotContains: []string{"error"}}
	err := r.Check("found error here")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not_contains") {
		t.Fatalf("expected error mentioning 'not_contains', got %q", err.Error())
	}
}

func TestCheck_Matches_Passes(t *testing.T) {
	r := &ValidationRule{Matches: `^## Plan`}
	if err := r.Check("## Plan\ndetails"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheck_Matches_Fails(t *testing.T) {
	r := &ValidationRule{Matches: `^## Plan`}
	err := r.Check("no plan here")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "matches") {
		t.Fatalf("expected error mentioning 'matches', got %q", err.Error())
	}
}

func TestCheck_Matches_InvalidRegex(t *testing.T) {
	r := &ValidationRule{Matches: `[invalid`}
	err := r.Check("anything")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "regex") {
		t.Fatalf("expected error mentioning 'regex', got %q", err.Error())
	}
}

func TestCheck_AllConditionsMustPass(t *testing.T) {
	r := &ValidationRule{
		Contains:    []string{"ok"},
		NotContains: []string{"bad"},
		Matches:     `ok`,
	}

	if err := r.Check("ok"); err != nil {
		t.Fatalf("expected nil for valid output, got %v", err)
	}

	err := r.Check("ok bad")
	if err == nil {
		t.Fatal("expected error for output with forbidden substring, got nil")
	}
}
