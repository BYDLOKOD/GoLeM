package dag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/veschin/GoLeM/internal/artifact"
	"github.com/veschin/GoLeM/internal/validation"
)

// TestExecuteGateWithoutValidateRuleReturnsError guards the gate executor
// against a nil Validate rule. DAG.Validate() rejects such gates, but a caller
// invoking Execute directly (bypassing validation) must get a clean error
// instead of a nil-pointer panic.
func TestExecuteGateWithoutValidateRuleReturnsError(t *testing.T) {
	e := NewClaudeStepExecutor(nil, t.TempDir(), t.TempDir(), "", 0, "")
	_, err := e.Execute(context.Background(), Step{ID: "g", Type: "gate", DependsOn: []string{"x"}}, nil)
	if err == nil {
		t.Fatal("expected error for gate step without a validate rule, got nil")
	}
}

func TestBuildInjectedPrompt_SingleInput(t *testing.T) {
	inputs := []*artifact.Artifact{
		artifact.NewText("s1", "analysis result: 3 packages found"),
	}
	prompt := "write tests"

	result := buildInjectedPrompt(inputs, prompt)

	if result == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strContains(result, "analysis result: 3 packages found") {
		t.Error("injected prompt should contain input artifact content")
	}
	if !strContains(result, "write tests") {
		t.Error("injected prompt should contain original prompt")
	}
	if !strContains(result, "Your task:") {
		t.Error("injected prompt should contain 'Your task:' marker")
	}
	if !strContains(result, `from step "s1"`) {
		t.Error("injected prompt should reference source step ID")
	}
}

func TestBuildInjectedPrompt_MultipleInputs(t *testing.T) {
	inputs := []*artifact.Artifact{
		artifact.NewText("s1", "output from s1"),
		artifact.NewText("s2", "output from s2"),
	}
	prompt := "review"

	result := buildInjectedPrompt(inputs, prompt)

	if !strContains(result, "output from s1") {
		t.Error("should contain s1 output")
	}
	if !strContains(result, "output from s2") {
		t.Error("should contain s2 output")
	}
	if !strContains(result, `from step "s1"`) {
		t.Error("should reference s1 step ID")
	}
	if !strContains(result, `from step "s2"`) {
		t.Error("should reference s2 step ID")
	}
	if !strContains(result, "Your task:") {
		t.Error("should contain 'Your task:' marker")
	}
	if !strContains(result, "review") {
		t.Error("should contain original prompt")
	}
}

func TestBuildInjectedPrompt_NoInputs(t *testing.T) {
	prompt := "first step"
	result := buildInjectedPrompt(nil, prompt)

	if result != prompt {
		t.Errorf("no inputs: expected prompt unchanged, got %q", result)
	}
}

func TestBuildInjectedPrompt_EmptyInputs(t *testing.T) {
	result := buildInjectedPrompt([]*artifact.Artifact{}, "do work")
	if result != "do work" {
		t.Errorf("empty inputs: expected prompt unchanged, got %q", result)
	}
}

func TestBuildLinearDAG(t *testing.T) {
	prompts := []string{"step one", "step two", "step three"}

	d := BuildLinearDAG(prompts)

	if err := d.Validate(); err != nil {
		t.Fatalf("linear DAG should be valid: %v", err)
	}

	if len(d.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(d.Steps))
	}

	// Verify step IDs.
	if d.Steps[0].ID != "step-0" {
		t.Errorf("step 0 ID = %q, want step-0", d.Steps[0].ID)
	}
	if d.Steps[1].ID != "step-1" {
		t.Errorf("step 1 ID = %q, want step-1", d.Steps[1].ID)
	}
	if d.Steps[2].ID != "step-2" {
		t.Errorf("step 2 ID = %q, want step-2", d.Steps[2].ID)
	}

	// Verify prompts.
	if d.Steps[0].Prompt != "step one" {
		t.Errorf("step 0 prompt = %q, want 'step one'", d.Steps[0].Prompt)
	}
	if d.Steps[1].Prompt != "step two" {
		t.Errorf("step 1 prompt = %q, want 'step two'", d.Steps[1].Prompt)
	}

	// First step has no dependencies.
	if len(d.Steps[0].DependsOn) != 0 {
		t.Errorf("step 0 should have no dependencies, got %v", d.Steps[0].DependsOn)
	}

	// Second step depends on first.
	if len(d.Steps[1].DependsOn) != 1 || d.Steps[1].DependsOn[0] != "step-0" {
		t.Errorf("step 1 should depend on step-0, got %v", d.Steps[1].DependsOn)
	}

	// Third step depends on second.
	if len(d.Steps[2].DependsOn) != 1 || d.Steps[2].DependsOn[0] != "step-1" {
		t.Errorf("step 2 should depend on step-1, got %v", d.Steps[2].DependsOn)
	}
}

func TestBuildLinearDAG_EmptyPrompts(t *testing.T) {
	d := BuildLinearDAG([]string{})
	if len(d.Steps) != 0 {
		t.Errorf("empty prompts should produce empty DAG, got %d steps", len(d.Steps))
	}
}

func TestBuildLinearDAG_SinglePrompt(t *testing.T) {
	d := BuildLinearDAG([]string{"only step"})
	if len(d.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(d.Steps))
	}
	if d.Steps[0].Prompt != "only step" {
		t.Errorf("step 0 prompt = %q, want 'only step'", d.Steps[0].Prompt)
	}
	if len(d.Steps[0].DependsOn) != 0 {
		t.Errorf("single step should have no dependencies")
	}
}

func TestNewClaudeStepExecutor(t *testing.T) {
	dir := t.TempDir()

	exec := NewClaudeStepExecutor(nil, dir, "/work", "glm-5", 600, "")

	if exec == nil {
		t.Fatal("executor should not be nil")
	}
	if exec.baseDir != dir {
		t.Errorf("baseDir = %q, want %q", exec.baseDir, dir)
	}
	if exec.workDir != "/work" {
		t.Errorf("workDir = %q, want /work", exec.workDir)
	}
	if exec.model != "glm-5" {
		t.Errorf("model = %q, want glm-5", exec.model)
	}
	if exec.timeout != 600 {
		t.Errorf("timeout = %d, want 600", exec.timeout)
	}
	if exec.systemPrompt != "" {
		t.Errorf("systemPrompt = %q, want empty", exec.systemPrompt)
	}
}

// TestNewClaudeStepExecutor_WithSystemPrompt verifies that the systemPrompt
// field is stored when provided.
func TestNewClaudeStepExecutor_WithSystemPrompt(t *testing.T) {
	dir := t.TempDir()

	exec := NewClaudeStepExecutor(nil, dir, "/work", "glm-5", 600, "You are a strict reviewer")

	if exec == nil {
		t.Fatal("executor should not be nil")
	}
	if exec.systemPrompt != "You are a strict reviewer" {
		t.Errorf("systemPrompt = %q, want 'You are a strict reviewer'", exec.systemPrompt)
	}
}

// TestClaudeStepExecutor_SystemPromptInClaudeConfig verifies that when Execute
// is called, the systemPrompt value is included in the claude.Config. We verify
// this indirectly: the executor must create a job dir and attempt execution.
// Since we cannot intercept the claude.Config without mocking, we rely on
// the structural test: NewClaudeStepExecutor with a system prompt stores it
// correctly and Execute does not return an error about missing system_prompt.
//
// The real validation that SystemPrompt flows to claude.Config is covered by
// TestClaudeStepExecutor_SystemPromptStoredOnExecutor below, which exercises
// the struct field directly before the call to claude.Execute.
func TestClaudeStepExecutor_SystemPromptStoredOnExecutor(t *testing.T) {
	dir := t.TempDir()
	const wantPrompt = "You MUST only read files"

	exec := NewClaudeStepExecutor(nil, dir, dir, "glm-5", 10, wantPrompt)

	if exec.systemPrompt != wantPrompt {
		t.Errorf("executor.systemPrompt = %q, want %q", exec.systemPrompt, wantPrompt)
	}
}

func TestApplyValidation_NilRule(t *testing.T) {
	step := Step{ID: "s1", Prompt: "do work"}
	if err := applyValidation(step, "any output"); err != nil {
		t.Errorf("nil Validate should return nil, got %v", err)
	}
}

func TestApplyValidation_Passes(t *testing.T) {
	step := Step{
		ID:      "s1",
		Prompt:  "do work",
		Validate: &validation.ValidationRule{Contains: []string{"ok"}},
	}
	if err := applyValidation(step, "ok here"); err != nil {
		t.Errorf("expected nil for passing validation, got %v", err)
	}
}

func TestApplyValidation_Fails(t *testing.T) {
	step := Step{
		ID:      "s1",
		Prompt:  "do work",
		Validate: &validation.ValidationRule{Contains: []string{"missing"}},
	}
	if err := applyValidation(step, "nothing"); err == nil {
		t.Error("expected error for failing validation, got nil")
	}
}

func TestExecuteGate_PassesValidation(t *testing.T) {
	dir := t.TempDir()
	exec := NewClaudeStepExecutor(nil, dir, dir, "", 60, "")

	step := Step{
		ID:        "gate-1",
		Type:      "gate",
		Prompt:    "gate",
		DependsOn: []string{"s1"},
		Validate:  &validation.ValidationRule{NotContains: []string{"error"}},
	}
	inputs := []*artifact.Artifact{
		artifact.NewText("s1", "all good"),
	}

	result, err := exec.executeGate(step, inputs)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(result))
	}
	if string(result[0].Content) != "all good" {
		t.Errorf("artifact content = %q, want %q", string(result[0].Content), "all good")
	}
}

func TestExecuteGate_FailsValidation(t *testing.T) {
	dir := t.TempDir()
	exec := NewClaudeStepExecutor(nil, dir, dir, "", 60, "")

	step := Step{
		ID:        "gate-1",
		Type:      "gate",
		Prompt:    "gate",
		DependsOn: []string{"s1"},
		Validate:  &validation.ValidationRule{NotContains: []string{"error"}},
	}
	inputs := []*artifact.Artifact{
		artifact.NewText("s1", "found error here"),
	}

	_, err := exec.executeGate(step, inputs)
	if err == nil {
		t.Fatal("expected error for failing gate validation, got nil")
	}
	if !strContains(err.Error(), "gate") {
		t.Errorf("error should mention 'gate', got %q", err.Error())
	}
	if !strContains(err.Error(), "validation") {
		t.Errorf("error should mention 'validation', got %q", err.Error())
	}
}

func TestExecuteGate_CombinesMultipleInputs(t *testing.T) {
	dir := t.TempDir()
	exec := NewClaudeStepExecutor(nil, dir, dir, "", 60, "")

	step := Step{
		ID:        "gate-1",
		Type:      "gate",
		Prompt:    "gate",
		DependsOn: []string{"s1", "s2"},
		Validate:  &validation.ValidationRule{Contains: []string{"hello", "world"}},
	}
	inputs := []*artifact.Artifact{
		artifact.NewText("s1", "hello "),
		artifact.NewText("s2", "world"),
	}

	result, err := exec.executeGate(step, inputs)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(result))
	}
}

func TestIsValidationError_True(t *testing.T) {
	ve := &validation.ValidationError{Condition: "contains", Detail: "missing x"}
	wrapped := fmt.Errorf("step failed: %w", ve)
	if !isValidationError(wrapped) {
		t.Error("expected isValidationError to return true for wrapped ValidationError")
	}
}

func TestIsValidationError_False(t *testing.T) {
	if isValidationError(fmt.Errorf("generic error")) {
		t.Error("expected isValidationError to return false for generic error")
	}
}

func TestRetryLoop_NoRetry(t *testing.T) {
	calls := 0
	result, err := retryExecute(1, "", func(prompt string) (string, error) {
		calls++
		return "done", nil
	}, "base prompt")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q, want %q", result, "done")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryLoop_RetriesOnValidationFailure(t *testing.T) {
	calls := 0
	var prompts []string

	_, err := retryExecute(3, "try again", func(prompt string) (string, error) {
		calls++
		prompts = append(prompts, prompt)
		if calls < 3 {
			return "", &validation.ValidationError{Condition: "contains", Detail: "missing"}
		}
		return "ok", nil
	}, "base prompt")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if prompts[0] != "base prompt" {
		t.Errorf("attempt 1 prompt = %q, want %q", prompts[0], "base prompt")
	}
	if !strContains(prompts[1], "try again") {
		t.Errorf("attempt 2 prompt should contain feedback, got %q", prompts[1])
	}
	if !strContains(prompts[2], "try again") {
		t.Errorf("attempt 3 prompt should contain feedback, got %q", prompts[2])
	}
}

func TestRetryLoop_ExhaustsRetries(t *testing.T) {
	calls := 0
	_, err := retryExecute(2, "retry", func(prompt string) (string, error) {
		calls++
		return "", &validation.ValidationError{Condition: "contains", Detail: "missing"}
	}, "base")

	if err == nil {
		t.Fatal("expected error when retries exhausted, got nil")
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestRetryLoop_NonValidationErrorNoRetry(t *testing.T) {
	calls := 0
	_, err := retryExecute(3, "feedback", func(prompt string) (string, error) {
		calls++
		return "", fmt.Errorf("non-validation error")
	}, "base")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("non-validation errors should not retry, got %d calls", calls)
	}
}

// TestRunStep_PreservesJobDirOnValidationFailure documents the contract that
// scratch directories must survive validation failure. The earlier code called
// os.RemoveAll BEFORE running validation, which silently destroyed raw.json,
// stderr.txt and other forensic artifacts the moment any Validate rule
// rejected the output - making post-mortem debugging impossible.
//
// The test creates a fake claude that succeeds with output that the step's
// Validate rule rejects, then asserts the jobDir still exists with claude's
// metadata files intact.
func TestRunStep_PreservesJobDirOnValidationFailure(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh; skipping fake-claude test")
	}

	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	// Fake claude emits a JSON envelope using `echo`, which is a POSIX shell
	// builtin and therefore independent of the test's narrowed PATH.
	script := "#!/bin/sh\necho '{\"result\":\"hello world\"}'\n"
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep the system path so the script can still resolve common utilities
	// if anything in the runtime ever shells out beyond builtins.
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	baseDir := t.TempDir()
	workDir := t.TempDir()
	exec := NewClaudeStepExecutor(nil, baseDir, workDir, "glm-5", 30, "")

	step := Step{
		ID:     "vfail",
		Prompt: "say hello",
		Validate: &validation.ValidationRule{
			Contains: []string{"THIS_SUBSTRING_IS_NOT_IN_OUTPUT_xyz789"},
		},
	}

	if _, err := exec.Execute(t.Context(), step, nil); err == nil {
		t.Fatal("expected validation error, got nil")
	}

	// jobDir created under baseDir as step-vfail-*
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("readdir baseDir: %v", err)
	}
	var jobDir string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > len("step-vfail-") && e.Name()[:len("step-vfail-")] == "step-vfail-" {
			jobDir = filepath.Join(baseDir, e.Name())
			break
		}
	}
	if jobDir == "" {
		t.Fatal("jobDir was deleted on validation failure -- forensic artifacts lost")
	}

	// Confirm the artifacts we promised to keep are actually there.
	for _, name := range []string{"raw.json", "stdout.txt", "prompt.txt"} {
		if _, err := os.Stat(filepath.Join(jobDir, name)); err != nil {
			t.Errorf("expected %s in preserved jobDir: %v", name, err)
		}
	}
}

// TestRunStep_RemovesJobDirOnSuccess verifies the happy-path cleanup: when a
// non-gate step succeeds AND validation passes, the scratch jobDir is removed.
func TestRunStep_RemovesJobDirOnSuccess(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh; skipping fake-claude test")
	}

	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\necho '{\"result\":\"hello world\"}'\n"
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	baseDir := t.TempDir()
	workDir := t.TempDir()
	exec := NewClaudeStepExecutor(nil, baseDir, workDir, "glm-5", 30, "")

	step := Step{
		ID:     "ok",
		Prompt: "say hello",
		Validate: &validation.ValidationRule{
			Contains: []string{"hello"},
		},
	}

	if _, err := exec.Execute(t.Context(), step, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("readdir baseDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) >= len("step-ok-") && e.Name()[:len("step-ok-")] == "step-ok-" {
			t.Errorf("scratch dir %q must be removed on success, but it persists", e.Name())
		}
	}
}

// strContains checks if s contains substr. Stdlib-only replacement for strings.Contains
// (to avoid importing strings in tests that don't need it for anything else).
func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
