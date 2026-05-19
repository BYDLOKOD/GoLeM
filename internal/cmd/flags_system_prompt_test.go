package cmd

import (
	"testing"
)

func TestParseFlags_SystemPrompt(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--system-prompt", "Only read files", "do work"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.SystemPrompt != "Only read files" {
		t.Errorf("SystemPrompt = %q, want %q", f.SystemPrompt, "Only read files")
	}
	if f.Prompt != "do work" {
		t.Errorf("Prompt = %q, want %q", f.Prompt, "do work")
	}
}

func TestParseFlags_SystemPromptMissingValue(t *testing.T) {
	t.Parallel()
	_, err := ParseFlags([]string{"--system-prompt"})
	if err == nil {
		t.Fatal("expected error for --system-prompt without value")
	}
	want := `err:user "Missing value for --system-prompt flag"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestParseFlags_SingleConstraint(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--constraint", "readonly", "do work"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if len(f.Constraints) != 1 {
		t.Fatalf("Constraints length = %d, want 1", len(f.Constraints))
	}
	if f.Constraints[0] != "readonly" {
		t.Errorf("Constraints[0] = %q, want %q", f.Constraints[0], "readonly")
	}
}

func TestParseFlags_MultipleConstraints(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--constraint", "readonly", "--constraint", "no-create", "do work"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if len(f.Constraints) != 2 {
		t.Fatalf("Constraints length = %d, want 2", len(f.Constraints))
	}
	if f.Constraints[0] != "readonly" {
		t.Errorf("Constraints[0] = %q, want %q", f.Constraints[0], "readonly")
	}
	if f.Constraints[1] != "no-create" {
		t.Errorf("Constraints[1] = %q, want %q", f.Constraints[1], "no-create")
	}
}

func TestParseFlags_ConstraintMissingValue(t *testing.T) {
	t.Parallel()
	_, err := ParseFlags([]string{"--constraint"})
	if err == nil {
		t.Fatal("expected error for --constraint without value")
	}
	want := `err:user "Missing value for --constraint flag"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestParseFlags_SystemPromptAndConstraints(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{
		"--system-prompt", "Only read files",
		"--constraint", "readonly",
		"--constraint", "no-create",
		"do work",
	})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.SystemPrompt != "Only read files" {
		t.Errorf("SystemPrompt = %q, want %q", f.SystemPrompt, "Only read files")
	}
	if len(f.Constraints) != 2 {
		t.Fatalf("Constraints length = %d, want 2", len(f.Constraints))
	}
	if f.Constraints[0] != "readonly" {
		t.Errorf("Constraints[0] = %q, want %q", f.Constraints[0], "readonly")
	}
	if f.Constraints[1] != "no-create" {
		t.Errorf("Constraints[1] = %q, want %q", f.Constraints[1], "no-create")
	}
	if f.Prompt != "do work" {
		t.Errorf("Prompt = %q, want %q", f.Prompt, "do work")
	}
}

func TestParseFlags_SystemPromptWithOtherFlags(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{
		"-d", "/tmp/project",
		"-t", "300",
		"--system-prompt", "Only read files",
		"--tier", "light",
		"do work",
	})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.Dir != "/tmp/project" {
		t.Errorf("Dir = %q, want %q", f.Dir, "/tmp/project")
	}
	if f.Timeout != 300 {
		t.Errorf("Timeout = %d, want 300", f.Timeout)
	}
	if f.SystemPrompt != "Only read files" {
		t.Errorf("SystemPrompt = %q, want %q", f.SystemPrompt, "Only read files")
	}
	if f.Tier != "light" {
		t.Errorf("Tier = %q, want %q", f.Tier, "light")
	}
	if f.Prompt != "do work" {
		t.Errorf("Prompt = %q, want %q", f.Prompt, "do work")
	}
}
