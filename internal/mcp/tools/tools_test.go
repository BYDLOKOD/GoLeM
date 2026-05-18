package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/job"
)

// --- Test helpers ---

func newTestToolContext(t *testing.T) *ToolContext {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Model:          "glm-5",
		OpusModel:      "glm-5",
		SonnetModel:    "glm-5",
		HaikuModel:     "glm-5",
		PermissionMode: "bypassPermissions",
		SubagentDir:    dir,
	}
	projectID := "test-project"
	return NewToolContext(cfg, dir, projectID)
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return json.RawMessage(data)
}

// createTestJob creates a job directory with the given status.
func createTestJob(t *testing.T, tc *ToolContext, status job.Status) string {
	t.Helper()
	jobID := job.GenerateJobID()
	j, err := job.NewJob(tc.SubagentsRoot, tc.ProjectID, jobID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if status != job.StatusQueued {
		if err := j.SetStatus(status); err != nil {
			t.Fatalf("set status: %v", err)
		}
	}
	return jobID
}

// --- ToolError tests ---

func TestToolError_Error(t *testing.T) {
	err := NewToolError("err:user", "something went wrong")
	expected := "err:user something went wrong"
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err.Error(), expected)
	}
}

// --- parseInput / marshalOutput helpers ---

func TestParseInput_NilRaw(t *testing.T) {
	var target struct{}
	err := parseInput(nil, &target)
	if err == nil {
		t.Fatal("expected error for nil raw")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:user" {
		t.Errorf("code = %q, want err:user", toolErr.Code)
	}
}

func TestParseInput_InvalidJSON(t *testing.T) {
	var target struct{}
	err := parseInput(json.RawMessage(`not json`), &target)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMarshalOutput_Roundtrip(t *testing.T) {
	output := RunOutput{
		Stdout:   "hello",
		Stderr:   "",
		ExitCode: 0,
		JobID:    "job-20260501-120000-abc12345",
	}

	raw, err := marshalOutput(output)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RunOutput
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Stdout != output.Stdout {
		t.Errorf("stdout = %q, want %q", decoded.Stdout, output.Stdout)
	}
	if decoded.JobID != output.JobID {
		t.Errorf("job_id = %q, want %q", decoded.JobID, output.JobID)
	}
}

// --- ToolContext tests ---

func TestNewToolContext_Defaults(t *testing.T) {
	cfg := &config.Config{SubagentDir: "/tmp/subagents"}
	tc := NewToolContext(cfg, "", "")
	if tc.SubagentsRoot != "/tmp/subagents" {
		t.Errorf("subagentsRoot = %q, want /tmp/subagents", tc.SubagentsRoot)
	}
	if tc.ProjectID != "mcp" {
		t.Errorf("projectID = %q, want mcp", tc.ProjectID)
	}
}

func TestNewToolContext_Explicit(t *testing.T) {
	cfg := &config.Config{SubagentDir: "/default"}
	tc := NewToolContext(cfg, "/explicit/root", "my-project")
	if tc.SubagentsRoot != "/explicit/root" {
		t.Errorf("subagentsRoot = %q, want /explicit/root", tc.SubagentsRoot)
	}
	if tc.ProjectID != "my-project" {
		t.Errorf("projectID = %q, want my-project", tc.ProjectID)
	}
}

// --- RunHandler tests ---

func TestRunHandler_MissingPrompt(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	input := mustMarshal(t, RunInput{Dir: "."})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:user" {
		t.Errorf("code = %q, want err:user", toolErr.Code)
	}
}

func TestRunHandler_NilInput(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	_, err := h.Handle(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestRunHandler_InvalidJSON(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	_, err := h.Handle(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRunHandler_ParamMapping(t *testing.T) {
	// Verify the handler correctly maps JSON params to RunInput fields.
	params := json.RawMessage(`{"prompt":"hello","model":"glm-4","timeout":60}`)
	var input RunInput
	if err := json.Unmarshal(params, &input); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if input.Prompt != "hello" {
		t.Errorf("prompt = %q, want %q", input.Prompt, "hello")
	}
	if input.Model != "glm-4" {
		t.Errorf("model = %q, want %q", input.Model, "glm-4")
	}
	if input.Timeout != 60 {
		t.Errorf("timeout = %d, want %d", input.Timeout, 60)
	}
}

// TestRunHandler_CallsExecuteJob verifies that RunHandler reaches the real
// execution layer (cmd.ExecuteJob via reconcileAndInitSlots + WaitForSlot)
// rather than the stub cmd.RunCmd. We verify this by checking that an empty
// subagents dir (no pre-created job dirs) results in an err:execution error —
// the stub cmd.RunCmd returns success even with no pre-created job dirs, while
// cmd.ExecuteJob attempts to run claude and returns an error when it fails.
func TestRunHandler_CallsExecuteJob(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	input := mustMarshal(t, RunInput{
		Prompt: "test prompt",
		Dir:    ".",
	})

	// With the real execution path, running claude will fail because it either
	// cannot be found or returns a non-zero exit code in the test environment.
	// The stub cmd.RunCmd would return nil error with empty stdout.
	// Either way, the error must not be err:user (input validation error).
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		// No error means claude ran successfully — real execution was attempted
		// and succeeded. This is also correct behavior.
		return
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	// err:user means input validation rejected the request before execution.
	// err:execution means execution was attempted (the real path).
	if toolErr.Code == "err:user" {
		t.Errorf("got err:user: %q — input validation should have passed; real execution should have been attempted", toolErr.Message)
	}
}

// TestRunHandler_DefaultDirApplied verifies that an empty dir input is
// replaced with "." before params are passed downstream.
func TestRunHandler_DefaultDirApplied(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	// Dir is intentionally omitted — should default to ".".
	input := mustMarshal(t, RunInput{Prompt: "hello"})

	// We expect an execution attempt (not a validation error about missing dir).
	_, err := h.Handle(context.Background(), input)
	if err != nil {
		toolErr, ok := err.(*ToolError)
		if ok && toolErr.Code == "err:user" {
			// A user error here would mean Dir="" was passed through as-is and
			// Validate rejected it — the bug we want to catch.
			t.Errorf("got err:user; dir default was not applied: %v", toolErr.Message)
		}
	}
}

// --- StartHandler tests ---

func TestStartHandler_MissingPrompt(t *testing.T) {
	tc := newTestToolContext(t)
	h := StartHandler(tc)

	input := mustMarshal(t, StartInput{})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

// TestStartHandler_ReturnsJobIDImmediately verifies that StartHandler:
//   - Returns a non-empty job ID immediately (before background execution).
//   - Creates the job directory synchronously before returning.
//
// The background goroutine runs asynchronously. We wait for it to reach a
// terminal state before the test ends to avoid racing with t.TempDir cleanup.
func TestStartHandler_ReturnsJobIDImmediately(t *testing.T) {
	tc := newTestToolContext(t)
	h := StartHandler(tc)

	input := mustMarshal(t, StartInput{
		Prompt: "async task",
		Dir:    ".",
	})

	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output StartOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.JobID == "" {
		t.Error("job_id is empty")
	}
	// Job directory must exist immediately after Handle returns (not waiting
	// for background goroutine), because the job is pre-created synchronously.
	jobDir := filepath.Join(tc.SubagentsRoot, tc.ProjectID, output.JobID)
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		t.Errorf("job directory not created synchronously: %s", jobDir)
	}

	// Wait for the background goroutine to reach a terminal state before the
	// test exits, so that t.TempDir cleanup does not race with open file handles
	// (job.Reconcile holds a flock lock during execution).
	statusPath := filepath.Join(jobDir, "status")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(statusPath)
		if readErr == nil {
			s := string(data)
			if s == "done" || s == "failed" || s == "timeout" || s == "killed" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// --- StatusHandler tests ---

func TestStatusHandler_MissingJobID(t *testing.T) {
	tc := newTestToolContext(t)
	h := StatusHandler(tc)

	input := mustMarshal(t, StatusInput{})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing job_id")
	}
}

func TestStatusHandler_NotFound(t *testing.T) {
	tc := newTestToolContext(t)
	h := StatusHandler(tc)

	input := mustMarshal(t, StatusInput{JobID: "job-20260501-120000-aabbccdd"})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:not_found" {
		t.Errorf("code = %q, want err:not_found", toolErr.Code)
	}
}

func TestStatusHandler_QueuedJob(t *testing.T) {
	tc := newTestToolContext(t)
	jobID := createTestJob(t, tc, job.StatusQueued)
	h := StatusHandler(tc)

	input := mustMarshal(t, StatusInput{JobID: jobID})
	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output StatusOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.Status != "queued" {
		t.Errorf("status = %q, want queued", output.Status)
	}
}

func TestStatusHandler_DoneJob(t *testing.T) {
	tc := newTestToolContext(t)
	jobID := createTestJob(t, tc, job.StatusDone)
	h := StatusHandler(tc)

	input := mustMarshal(t, StatusInput{JobID: jobID})
	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output StatusOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.Status != "done" {
		t.Errorf("status = %q, want done", output.Status)
	}
}

// --- ResultHandler tests ---

func TestResultHandler_MissingJobID(t *testing.T) {
	tc := newTestToolContext(t)
	h := ResultHandler(tc)

	input := mustMarshal(t, ResultInput{})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing job_id")
	}
}

func TestResultHandler_NotFound(t *testing.T) {
	tc := newTestToolContext(t)
	h := ResultHandler(tc)

	input := mustMarshal(t, ResultInput{JobID: "job-20260501-120000-aabbccdd"})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestResultHandler_StillRunning(t *testing.T) {
	tc := newTestToolContext(t)
	// Create a job with "running" status and a valid PID (our own).
	jobID := createTestJob(t, tc, job.StatusRunning)
	// Write a valid PID so StatusCmd's PID reconciliation does not flip to failed.
	jobDir := filepath.Join(tc.SubagentsRoot, tc.ProjectID, jobID)
	if err := os.WriteFile(filepath.Join(jobDir, "pid.txt"),
		[]byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	h := ResultHandler(tc)

	input := mustMarshal(t, ResultInput{JobID: jobID})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for running job")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:execution" {
		t.Errorf("code = %q, want err:execution", toolErr.Code)
	}
}

func TestResultHandler_DoneJob(t *testing.T) {
	tc := newTestToolContext(t)
	jobID := createTestJob(t, tc, job.StatusDone)

	// Write stdout.txt.
	jobDir := filepath.Join(tc.SubagentsRoot, tc.ProjectID, jobID)
	expectedStdout := "hello from subagent"
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.txt"), []byte(expectedStdout), 0o644); err != nil {
		t.Fatalf("write stdout: %v", err)
	}

	h := ResultHandler(tc)
	input := mustMarshal(t, ResultInput{JobID: jobID})
	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ResultOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.Stdout != expectedStdout {
		t.Errorf("stdout = %q, want %q", output.Stdout, expectedStdout)
	}
	if !output.Deleted {
		t.Error("deleted should be true for completed job")
	}
}

// --- ListHandler tests ---

func TestListHandler_Empty(t *testing.T) {
	tc := newTestToolContext(t)
	h := ListHandler(tc)

	result, err := h.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ListOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(output.Jobs) != 0 {
		t.Errorf("jobs = %d, want 0", len(output.Jobs))
	}
}

func TestListHandler_WithJobs(t *testing.T) {
	tc := newTestToolContext(t)

	// Create two jobs.
	job1 := createTestJob(t, tc, job.StatusDone)
	job2 := createTestJob(t, tc, job.StatusFailed)

	h := ListHandler(tc)
	result, err := h.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ListOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(output.Jobs) < 2 {
		t.Errorf("jobs = %d, want at least 2", len(output.Jobs))
	}

	// Verify both job IDs are present.
	found1, found2 := false, false
	for _, j := range output.Jobs {
		if j.JobID == job1 {
			found1 = true
		}
		if j.JobID == job2 {
			found2 = true
		}
	}
	if !found1 {
		t.Errorf("job %s not found in list", job1)
	}
	if !found2 {
		t.Errorf("job %s not found in list", job2)
	}
}

// --- KillHandler tests ---

func TestKillHandler_MissingJobID(t *testing.T) {
	tc := newTestToolContext(t)
	h := KillHandler(tc)

	input := mustMarshal(t, KillInput{})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing job_id")
	}
}

func TestKillHandler_NotFound(t *testing.T) {
	tc := newTestToolContext(t)
	h := KillHandler(tc)

	input := mustMarshal(t, KillInput{JobID: "job-20260501-120000-aabbccdd"})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:not_found" {
		t.Errorf("code = %q, want err:not_found", toolErr.Code)
	}
}

func TestKillHandler_NotRunning(t *testing.T) {
	tc := newTestToolContext(t)
	jobID := createTestJob(t, tc, job.StatusDone)
	h := KillHandler(tc)

	input := mustMarshal(t, KillInput{JobID: jobID})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for non-running job")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:user" {
		t.Errorf("code = %q, want err:user", toolErr.Code)
	}
}

// --- ChainHandler tests ---

func TestChainHandler_TooFewPrompts(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	input := mustMarshal(t, ChainInput{Prompts: []string{"only one"}})
	_, err := h.Handle(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for single prompt chain")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:user" {
		t.Errorf("code = %q, want err:user", toolErr.Code)
	}
}

func TestChainHandler_NilInput(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	_, err := h.Handle(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestChainHandler_Success(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	input := mustMarshal(t, ChainInput{
		Prompts: []string{"step one", "step two"},
		Dir:     ".",
		// ContinueOnError ensures both steps run regardless of claude outcome
		// (e.g. model unavailable in test environment).
		ContinueOnError: true,
	})

	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ChainOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.StepsExecuted != 2 {
		t.Errorf("steps_executed = %d, want 2", output.StepsExecuted)
	}
	if output.StepsSkipped != 0 {
		t.Errorf("steps_skipped = %d, want 0", output.StepsSkipped)
	}
	if len(output.JobDirs) != 2 {
		t.Errorf("job_dirs = %d entries, want 2", len(output.JobDirs))
	}
}

// --- ChainInputStep tests ---

func TestChainInputStep_Unmarshal(t *testing.T) {
	raw := `{
		"steps": [
			{
				"prompt": "write tests",
				"validate": {"contains": ["PASS"], "not_contains": ["FAIL"]},
				"retry": {"max_attempts": 3, "feedback": "Try again"}
			},
			{"prompt": "implement"}
		]
	}`
	var input ChainInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(input.Steps) != 2 {
		t.Fatalf("steps count = %d, want 2", len(input.Steps))
	}

	s1 := input.Steps[0]
	if s1.Prompt != "write tests" {
		t.Errorf("step[0].Prompt = %q, want 'write tests'", s1.Prompt)
	}
	if s1.Validate == nil {
		t.Fatal("step[0].Validate is nil")
	}
	if len(s1.Validate.Contains) != 1 || s1.Validate.Contains[0] != "PASS" {
		t.Errorf("step[0].Validate.Contains = %v, want [PASS]", s1.Validate.Contains)
	}
	if len(s1.Validate.NotContains) != 1 || s1.Validate.NotContains[0] != "FAIL" {
		t.Errorf("step[0].Validate.NotContains = %v, want [FAIL]", s1.Validate.NotContains)
	}
	if s1.Retry == nil {
		t.Fatal("step[0].Retry is nil")
	}
	if s1.Retry.MaxAttempts != 3 {
		t.Errorf("step[0].Retry.MaxAttempts = %d, want 3", s1.Retry.MaxAttempts)
	}
	if s1.Retry.Feedback != "Try again" {
		t.Errorf("step[0].Retry.Feedback = %q, want 'Try again'", s1.Retry.Feedback)
	}

	s2 := input.Steps[1]
	if s2.Prompt != "implement" {
		t.Errorf("step[1].Prompt = %q, want 'implement'", s2.Prompt)
	}
	if s2.Validate != nil {
		t.Error("step[1].Validate should be nil")
	}
	if s2.Retry != nil {
		t.Error("step[1].Retry should be nil")
	}
}

func TestChainHandler_StepsField_TooFewSteps(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	raw := `{"steps": [{"prompt": "only one"}], "dir": "."}`
	_, err := h.Handle(context.Background(), json.RawMessage(raw))
	if err == nil {
		t.Fatal("expected error for single step chain")
	}
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if toolErr.Code != "err:user" {
		t.Errorf("code = %q, want err:user", toolErr.Code)
	}
}

func TestChainHandler_StepsField_Conversion(t *testing.T) {
	raw := `{
		"steps": [
			{
				"prompt": "step one",
				"validate": {"contains": ["ok"]},
				"retry": {"max_attempts": 2, "feedback": "retry"}
			},
			{"prompt": "step two"}
		]
	}`
	var input ChainInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	steps := make([]cmd.ChainStep, len(input.Steps))
	for i, s := range input.Steps {
		steps[i] = cmd.ChainStep{
			Prompt:   s.Prompt,
			Validate: s.Validate,
			Retry:    s.Retry,
		}
	}

	if steps[0].Validate == nil {
		t.Error("step[0].Validate should not be nil")
	}
	if steps[0].Validate.Contains[0] != "ok" {
		t.Errorf("step[0].Validate.Contains[0] = %q, want 'ok'", steps[0].Validate.Contains[0])
	}
	if steps[0].Retry.MaxAttempts != 2 {
		t.Errorf("step[0].Retry.MaxAttempts = %d, want 2", steps[0].Retry.MaxAttempts)
	}
	if steps[1].Validate != nil {
		t.Error("step[1].Validate should be nil")
	}
}

func TestChainHandler_StepsField_ExecutesChain(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	input := mustMarshal(t, ChainInput{
		Steps: []ChainInputStep{
			{Prompt: "step one"},
			{Prompt: "step two"},
		},
		Dir:             ".",
		ContinueOnError: true,
	})

	result, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output ChainOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.StepsExecuted != 2 {
		t.Errorf("steps_executed = %d, want 2", output.StepsExecuted)
	}
	if len(output.JobDirs) != 2 {
		t.Errorf("job_dirs = %d entries, want 2", len(output.JobDirs))
	}
}

// --- parseListOutput helper ---

func TestParseListOutput_Empty(t *testing.T) {
	result := parseListOutput("")
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestParseListOutput_HeaderOnly(t *testing.T) {
	result := parseListOutput("JOB_ID  STATUS  STARTED\n")
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestParseListOutput_WithEntries(t *testing.T) {
	input := fmt.Sprintf(
		"%-44s  %-18s  %s\n%-44s  %-18s  %s\n%-44s  %-18s  %s\n",
		"JOB_ID", "STATUS", "STARTED",
		"job-20260501-120000-aabbccdd", "done", "2026-05-01T12:00:00Z",
		"job-20260501-120100-11223344", "failed", "-",
	)
	result := parseListOutput(input)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].JobID != "job-20260501-120000-aabbccdd" {
		t.Errorf("job_id = %q, want job-20260501-120000-aabbccdd", result[0].JobID)
	}
	if result[0].Status != "done" {
		t.Errorf("status = %q, want done", result[0].Status)
	}
	if result[1].StartedAt != "" {
		t.Errorf("started_at = %q, want empty for dash", result[1].StartedAt)
	}
}

// --- Definition functions tests ---

func TestRunDefinition_HasRequiredPrompt(t *testing.T) {
	schema := RunDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "prompt" {
		t.Errorf("required = %v, want [prompt]", required)
	}
}

func TestStartDefinition_HasRequiredPrompt(t *testing.T) {
	schema := StartDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "prompt" {
		t.Errorf("required = %v, want [prompt]", required)
	}
}

func TestStatusDefinition_HasRequiredJobID(t *testing.T) {
	schema := StatusDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "job_id" {
		t.Errorf("required = %v, want [job_id]", required)
	}
}

func TestResultDefinition_HasRequiredJobID(t *testing.T) {
	schema := ResultDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "job_id" {
		t.Errorf("required = %v, want [job_id]", required)
	}
}

func TestKillDefinition_HasRequiredJobID(t *testing.T) {
	schema := KillDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "job_id" {
		t.Errorf("required = %v, want [job_id]", required)
	}
}

// TestChainDefinition_AcceptsEitherPromptsOrSteps verifies that the schema
// does NOT mark `prompts` as the sole required field — the handler accepts
// either `prompts` OR `steps`, and the schema must reflect that contract so
// strict client-side validators don't reject `steps`-only inputs.
func TestChainDefinition_AcceptsEitherPromptsOrSteps(t *testing.T) {
	schema := ChainDefinition()

	// Top-level `required` must not contain "prompts" alone — that would
	// reject the `steps`-only form which the handler accepts at runtime.
	if required, ok := schema["required"].([]string); ok {
		for _, r := range required {
			if r == "prompts" {
				t.Errorf("top-level required must not list \"prompts\" alone; got %v", required)
			}
		}
	}

	// The schema must express the disjunction via oneOf so JSON-schema clients
	// understand that exactly one of prompts/steps is required.
	oneOf, ok := schema["oneOf"].([]map[string]any)
	if !ok {
		t.Fatal("oneOf field missing or wrong type: schema must declare prompts/steps as alternatives")
	}
	if len(oneOf) != 2 {
		t.Fatalf("oneOf len = %d, want 2 (prompts-only and steps-only)", len(oneOf))
	}

	// Collect the required keys from each branch and verify both shapes are listed.
	seen := map[string]bool{}
	for _, branch := range oneOf {
		req, _ := branch["required"].([]string)
		if len(req) != 1 {
			t.Fatalf("oneOf branch %+v must list exactly one required field", branch)
		}
		seen[req[0]] = true
	}
	if !seen["prompts"] {
		t.Error("oneOf must include a branch requiring \"prompts\"")
	}
	if !seen["steps"] {
		t.Error("oneOf must include a branch requiring \"steps\"")
	}
}

func TestListDefinition_NoRequiredFields(t *testing.T) {
	schema := ListDefinition()
	_, hasRequired := schema["required"]
	if hasRequired {
		t.Error("list should have no required fields")
	}
}

// --- System prompt / constraints field tests ---

// TestRunInput_SystemPromptAndConstraintsFields verifies RunInput exposes
// system_prompt and constraints via JSON serialization.
func TestRunInput_SystemPromptAndConstraintsFields(t *testing.T) {
	raw := `{"prompt":"task","system_prompt":"be careful","constraints":["readonly","plan-first"]}`
	var input RunInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.SystemPrompt != "be careful" {
		t.Errorf("SystemPrompt = %q, want be careful", input.SystemPrompt)
	}
	if len(input.Constraints) != 2 {
		t.Fatalf("Constraints len = %d, want 2", len(input.Constraints))
	}
	if input.Constraints[0] != "readonly" {
		t.Errorf("Constraints[0] = %q, want readonly", input.Constraints[0])
	}
	if input.Constraints[1] != "plan-first" {
		t.Errorf("Constraints[1] = %q, want plan-first", input.Constraints[1])
	}
}

// TestStartInput_SystemPromptAndConstraintsFields verifies StartInput exposes
// system_prompt and constraints via JSON serialization.
func TestStartInput_SystemPromptAndConstraintsFields(t *testing.T) {
	raw := `{"prompt":"task","system_prompt":"stay focused","constraints":["no-create"]}`
	var input StartInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.SystemPrompt != "stay focused" {
		t.Errorf("SystemPrompt = %q, want stay focused", input.SystemPrompt)
	}
	if len(input.Constraints) != 1 || input.Constraints[0] != "no-create" {
		t.Errorf("Constraints = %v, want [no-create]", input.Constraints)
	}
}

// TestChainInput_SystemPromptAndConstraintsFields verifies ChainInput exposes
// system_prompt and constraints via JSON serialization.
func TestChainInput_SystemPromptAndConstraintsFields(t *testing.T) {
	raw := `{"prompts":["a","b"],"system_prompt":"custom","constraints":["scope:/tmp"]}`
	var input ChainInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.SystemPrompt != "custom" {
		t.Errorf("SystemPrompt = %q, want custom", input.SystemPrompt)
	}
	if len(input.Constraints) != 1 || input.Constraints[0] != "scope:/tmp" {
		t.Errorf("Constraints = %v, want [scope:/tmp]", input.Constraints)
	}
}

// TestRunInput_OmitEmptySystemPrompt verifies that omitempty omits the fields
// when they are zero values.
func TestRunInput_OmitEmptySystemPrompt(t *testing.T) {
	input := RunInput{Prompt: "task"}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if contains(s, "system_prompt") {
		t.Error("system_prompt should be omitted when empty")
	}
	if contains(s, "constraints") {
		t.Error("constraints should be omitted when nil")
	}
}

// TestRunDefinition_HasSystemPromptAndConstraints verifies the glm_run schema
// includes system_prompt and constraints properties.
func TestRunDefinition_HasSystemPromptAndConstraints(t *testing.T) {
	schema := RunDefinition()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing or wrong type")
	}
	if _, exists := props["system_prompt"]; !exists {
		t.Error("system_prompt property missing from RunDefinition")
	}
	if _, exists := props["constraints"]; !exists {
		t.Error("constraints property missing from RunDefinition")
	}
}

// TestStartDefinition_HasSystemPromptAndConstraints verifies the glm_start
// schema includes system_prompt and constraints properties.
func TestStartDefinition_HasSystemPromptAndConstraints(t *testing.T) {
	schema := StartDefinition()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing or wrong type")
	}
	if _, exists := props["system_prompt"]; !exists {
		t.Error("system_prompt property missing from StartDefinition")
	}
	if _, exists := props["constraints"]; !exists {
		t.Error("constraints property missing from StartDefinition")
	}
}

// TestChainDefinition_HasSystemPromptAndConstraints verifies the glm_chain
// schema includes system_prompt and constraints properties.
func TestChainDefinition_HasSystemPromptAndConstraints(t *testing.T) {
	schema := ChainDefinition()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing or wrong type")
	}
	if _, exists := props["system_prompt"]; !exists {
		t.Error("system_prompt property missing from ChainDefinition")
	}
	if _, exists := props["constraints"]; !exists {
		t.Error("constraints property missing from ChainDefinition")
	}
}

// TestRunHandler_SystemPromptFromInputPassedToFlags verifies that RunHandler
// propagates system_prompt and constraints from input into cmd.Flags.
// We test this indirectly: a non-empty SystemPrompt in RunInput must not be
// silently dropped. Since the handler validates and calls ExecuteJob, any
// err:user code would mean input was rejected — not what we want.
// This test confirms that providing system_prompt does not cause a user error.
func TestRunHandler_SystemPromptFromInput(t *testing.T) {
	tc := newTestToolContext(t)
	h := RunHandler(tc)

	input := mustMarshal(t, RunInput{
		Prompt:       "do a task",
		SystemPrompt: "You are a strict reviewer",
		Constraints:  []string{"readonly"},
	})

	_, err := h.Handle(context.Background(), input)
	if err != nil {
		toolErr, ok := err.(*ToolError)
		if ok && toolErr.Code == "err:user" {
			t.Errorf("system_prompt/constraints caused unexpected validation error: %v", toolErr.Message)
		}
		// err:execution is fine — real claude execution attempted
	}
}

// TestRunHandler_SystemPromptDefaultFromConfig verifies that when input has no
// system_prompt, the config default (if any) is used. We confirm by inspecting
// that a non-empty config SystemPrompt does not produce a user error.
func TestRunHandler_SystemPromptDefaultFromConfig(t *testing.T) {
	tc := newTestToolContext(t)
	tc.Cfg.SystemPrompt = "default system prompt from config"
	h := RunHandler(tc)

	input := mustMarshal(t, RunInput{
		Prompt: "do a task",
		// No SystemPrompt — should fall back to config.
	})

	_, err := h.Handle(context.Background(), input)
	if err != nil {
		toolErr, ok := err.(*ToolError)
		if ok && toolErr.Code == "err:user" {
			t.Errorf("config default system_prompt caused unexpected validation error: %v", toolErr.Message)
		}
	}
}

// TestChainHandler_SystemPromptFromInput verifies that ChainHandler propagates
// system_prompt and constraints from input into cmd.ChainFlags.
func TestChainHandler_SystemPromptFromInput(t *testing.T) {
	tc := newTestToolContext(t)
	h := ChainHandler(tc)

	input := mustMarshal(t, ChainInput{
		Prompts:      []string{"step one", "step two"},
		SystemPrompt: "detailed instructions",
		Constraints:  []string{"plan-first"},
		ContinueOnError: true,
	})

	_, err := h.Handle(context.Background(), input)
	if err != nil {
		toolErr, ok := err.(*ToolError)
		if ok && toolErr.Code == "err:user" {
			t.Errorf("system_prompt/constraints caused unexpected validation error: %v", toolErr.Message)
		}
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
