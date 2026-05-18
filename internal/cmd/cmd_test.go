package cmd_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/job"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// makeJobDir creates a real job directory under root/<projectID>/<jobID>/
// with the given status and optional extra files.
func makeJobDir(t *testing.T, root, projectID, jobID, status string) string {
	t.Helper()
	dir := filepath.Join(root, projectID, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeJobDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("makeJobDir write status: %v", err)
	}
	return dir
}

// writePID writes a PID integer to pid.txt inside dir.
func writePID(t *testing.T, dir string, pid int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pid.txt"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("writePID: %v", err)
	}
}

// writeJobFile writes content to filename inside the job dir.
func writeJobFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	// Reuse writeFile(t, path, content) from chain_test.go helpers.
	writeFile(t, filepath.Join(dir, filename), content)
}

// selfPID returns the PID of the current test process (always alive).
func selfPID() int { return os.Getpid() }

// deadPID returns a PID that is guaranteed not to be alive.
// We use a large number that the OS would not normally assign.
func deadPID() int { return 2<<22 - 1 } // 8388607

// ─── AC1: Flag parsing ────────────────────────────────────────────────────────

// Scenario: Parse all flags for run command
// seed: flags_happy_run.json
func TestParseAllFlagsForRunCommand(t *testing.T) {
	args := []string{
		"run",
		"-d", "/home/veschin/work/project",
		"-t", "600",
		"-m", "glm-4.5",
		"Analyze src/main.go and find performance issues",
	}
	// Strip the subcommand name — ParseFlags receives only the flag/arg portion.
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if f.Dir != "/home/veschin/work/project" {
		t.Errorf("Dir: got %q, want %q", f.Dir, "/home/veschin/work/project")
	}
	if f.Timeout != 600 {
		t.Errorf("Timeout: got %d, want 600", f.Timeout)
	}
	if f.Model != "glm-4.5" {
		t.Errorf("Model: got %q, want %q", f.Model, "glm-4.5")
	}
	if f.Prompt != "Analyze src/main.go and find performance issues" {
		t.Errorf("Prompt: got %q, want %q", f.Prompt, "Analyze src/main.go and find performance issues")
	}
}

// Scenario: Parse per-slot model override flags
// seed: flags_per_slot.json
func TestParsePerSlotModelOverrideFlags(t *testing.T) {
	args := []string{
		"run",
		"--opus", "glm-5",
		"--sonnet", "glm-4.5",
		"--haiku", "glm-4.0",
		"Write tests",
	}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if f.OpusModel != "glm-5" {
		t.Errorf("OpusModel: got %q, want %q", f.OpusModel, "glm-5")
	}
	if f.SonnetModel != "glm-4.5" {
		t.Errorf("SonnetModel: got %q, want %q", f.SonnetModel, "glm-4.5")
	}
	if f.HaikuModel != "glm-4.0" {
		t.Errorf("HaikuModel: got %q, want %q", f.HaikuModel, "glm-4.0")
	}
	if f.Prompt != "Write tests" {
		t.Errorf("Prompt: got %q, want %q", f.Prompt, "Write tests")
	}
}

// Scenario: Parse --unsafe flag sets bypassPermissions
// seed: flags_unsafe.json
func TestParseUnsafeFlagSetsBypassPermissions(t *testing.T) {
	args := []string{
		"run",
		"--unsafe",
		"Deploy to staging",
	}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if f.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode: got %q, want %q", f.PermissionMode, "bypassPermissions")
	}
	if f.Prompt != "Deploy to staging" {
		t.Errorf("Prompt: got %q, want %q", f.Prompt, "Deploy to staging")
	}
}

// Scenario: Parse --mode flag sets explicit permission mode
func TestParseModeFlagSetsExplicitPermissionMode(t *testing.T) {
	args := []string{"run", "--mode", "plan", "Do something"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if f.PermissionMode != "plan" {
		t.Errorf("PermissionMode: got %q, want %q", f.PermissionMode, "plan")
	}
}

// Scenario: Default working directory is current directory
func TestDefaultWorkingDirectoryIsCurrentDirectory(t *testing.T) {
	args := []string{"run", "Do something"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if f.Dir != "." {
		t.Errorf("Dir: got %q, want %q", f.Dir, ".")
	}
}

// Scenario: Default timeout comes from config
func TestDefaultTimeoutComesFromConfig(t *testing.T) {
	args := []string{"run", "Do something"}
	configDefaultTimeout := 1800

	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When no -t flag was provided, ParseFlags should leave Timeout == 0
	// so the caller can substitute the config value.
	if f.Timeout != 0 {
		t.Fatalf("expected Timeout==0 (unset) so config can be applied, got %d", f.Timeout)
	}
	// Caller applies config default.
	if f.Timeout == 0 {
		f.Timeout = configDefaultTimeout
	}
	if f.Timeout != configDefaultTimeout {
		t.Errorf("Timeout after config default: got %d, want %d", f.Timeout, configDefaultTimeout)
	}
}

// Scenario: Remaining arguments after flags are joined as prompt
func TestRemainingArgumentsAfterFlagsJoinedAsPrompt(t *testing.T) {
	args := []string{"run", "-d", "/tmp", "Fix", "the", "bug", "in", "main.go", "please"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "Fix the bug in main.go please"
	if f.Prompt != want {
		t.Errorf("Prompt: got %q, want %q", f.Prompt, want)
	}
}

// ─── AC2: Directory validation ────────────────────────────────────────────────

// Scenario: Non-existent directory returns error
// seed: flags_bad_dir.json
func TestNonExistentDirectoryReturnsError(t *testing.T) {
	args := []string{"run", "-d", "/nonexistent/path", "Do something"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("ParseFlags unexpected error: %v", err)
	}

	err = cmd.Validate(f)
	if err == nil {
		t.Fatal("Validate: expected error, got nil")
	}

	want := `err:user "Directory not found: /nonexistent/path"`
	if err.Error() != want {
		t.Errorf("error: got %q, want %q", err.Error(), want)
	}
}

// ─── AC3: Timeout validation ──────────────────────────────────────────────────

// Scenario: Non-numeric timeout returns error
// seed: flags_bad_timeout.json
func TestNonNumericTimeoutReturnsError(t *testing.T) {
	args := []string{"run", "-t", "abc", "Do something"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		// Parsing itself may return the error for non-numeric -t.
		want := `err:user "Timeout must be a positive number: abc"`
		if err.Error() != want {
			t.Errorf("ParseFlags error: got %q, want %q", err.Error(), want)
		}
		return
	}

	err = cmd.Validate(f)
	if err == nil {
		t.Fatal("Validate: expected error, got nil")
	}
	want := `err:user "Timeout must be a positive number: abc"`
	if err.Error() != want {
		t.Errorf("Validate error: got %q, want %q", err.Error(), want)
	}
}

// Scenario: Negative timeout returns error
func TestNegativeTimeoutReturnsError(t *testing.T) {
	args := []string{"run", "-t", "-5", "Do something"}
	f, parseErr := cmd.ParseFlags(args[1:])
	if parseErr != nil {
		want := `err:user "Timeout must be a positive number: -5"`
		if parseErr.Error() != want {
			t.Errorf("ParseFlags error: got %q, want %q", parseErr.Error(), want)
		}
		return
	}

	err := cmd.Validate(f)
	if err == nil {
		t.Fatal("Validate: expected error for negative timeout, got nil")
	}
	want := `err:user "Timeout must be a positive number: -5"`
	if err.Error() != want {
		t.Errorf("Validate error: got %q, want %q", err.Error(), want)
	}
}

// Scenario: Zero timeout returns error
func TestZeroTimeoutReturnsError(t *testing.T) {
	args := []string{"run", "-t", "0", "Do something"}
	f, parseErr := cmd.ParseFlags(args[1:])
	if parseErr != nil {
		want := `err:user "Timeout must be a positive number: 0"`
		if parseErr.Error() != want {
			t.Errorf("ParseFlags error: got %q, want %q", parseErr.Error(), want)
		}
		return
	}

	err := cmd.Validate(f)
	if err == nil {
		t.Fatal("Validate: expected error for zero timeout, got nil")
	}
	want := `err:user "Timeout must be a positive number: 0"`
	if err.Error() != want {
		t.Errorf("Validate error: got %q, want %q", err.Error(), want)
	}
}

// ─── AC4: Missing prompt ──────────────────────────────────────────────────────

// Scenario: No prompt provided returns error
// seed: flags_no_prompt.json
func TestNoPromptProvidedReturnsError(t *testing.T) {
	args := []string{"run", "-d", "/tmp"}
	f, err := cmd.ParseFlags(args[1:])
	if err != nil {
		t.Fatalf("ParseFlags unexpected error: %v", err)
	}

	err = cmd.Validate(f)
	if err == nil {
		t.Fatal("Validate: expected error, got nil")
	}
	want := `err:user "No prompt provided"`
	if err.Error() != want {
		t.Errorf("error: got %q, want %q", err.Error(), want)
	}
}

// ─── AC10: glm status — print current status ──────────────────────────────────

// Scenario Outline: Status command prints correct status word
// seed: status_responses.json
func TestStatusCommandPrintsCorrectStatusWordDone(t *testing.T) {
	testStatusWord(t, "done", "job-20260227-100000-a1b2c3d4")
}

func TestStatusCommandPrintsCorrectStatusWordRunning(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJobDir(t, root, projectID, jobID, "running")
	writePID(t, dir, selfPID()) // alive PID

	var stdoutBuf bytes.Buffer
	result, err := cmd.StatusCmd(jobID, root, projectID, &stdoutBuf)
	if err != nil {
		t.Fatalf("StatusCmd unexpected error: %v", err)
	}
	if result.Status != "running" {
		t.Errorf("Status: got %q, want %q", result.Status, "running")
	}
	if stdoutBuf.String() != "running\n" {
		t.Errorf("stdout: got %q, want %q", stdoutBuf.String(), "running\n")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", result.ExitCode)
	}
}

func TestStatusCommandPrintsCorrectStatusWordFailed(t *testing.T) {
	testStatusWord(t, "failed", "job-20260227-102000-c9d0e1f2")
}

func TestStatusCommandPrintsCorrectStatusWordTimeout(t *testing.T) {
	testStatusWord(t, "timeout", "job-20260227-094500-f7e8d9c0")
}

func TestStatusCommandPrintsCorrectStatusWordKilled(t *testing.T) {
	testStatusWord(t, "killed", "job-20260227-103000-b5a6c7d8")
}

func TestStatusCommandPrintsCorrectStatusWordPermissionError(t *testing.T) {
	testStatusWord(t, "permission_error", "job-20260227-104500-d9e0f1a2")
}

// testStatusWord is a helper for the status outline scenarios.
func testStatusWord(t *testing.T, status, jobID string) {
	t.Helper()
	root := t.TempDir()
	projectID := "proj"
	makeJobDir(t, root, projectID, jobID, status)

	var stdoutBuf bytes.Buffer
	result, err := cmd.StatusCmd(jobID, root, projectID, &stdoutBuf)
	if err != nil {
		t.Fatalf("StatusCmd unexpected error: %v", err)
	}
	if result.Status != status {
		t.Errorf("Status: got %q, want %q", result.Status, status)
	}
	if stdoutBuf.String() != status+"\n" {
		t.Errorf("stdout: got %q, want %q", stdoutBuf.String(), status+"\n")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", result.ExitCode)
	}
}

// ─── AC11: Status checks PID liveness ────────────────────────────────────────

// Scenario: Status detects dead PID and updates to failed
// seed: status_responses.json scenario "running_stale"
func TestStatusDetectsDeadPIDAndUpdatesToFailed(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-101500-stale000"

	dir := makeJobDir(t, root, projectID, jobID, "running")
	writePID(t, dir, deadPID()) // dead PID

	var stdoutBuf bytes.Buffer
	result, err := cmd.StatusCmd(jobID, root, projectID, &stdoutBuf)
	if err != nil {
		t.Fatalf("StatusCmd unexpected error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("Status: got %q, want %q (dead PID should cause failed)", result.Status, "failed")
	}
	if stdoutBuf.String() != "failed\n" {
		t.Errorf("stdout: got %q, want %q", stdoutBuf.String(), "failed\n")
	}

	// Verify the status file was updated on disk.
	statusOnDisk := job.ReadStatus(dir)
	if statusOnDisk != job.StatusFailed {
		t.Errorf("status file on disk: got %q, want %q", statusOnDisk, job.StatusFailed)
	}
}

// Scenario: Status confirms running job with live PID
// seed: status_responses.json scenario "running"
func TestStatusConfirmsRunningJobWithLivePID(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-101500-alive001"

	dir := makeJobDir(t, root, projectID, jobID, "running")
	writePID(t, dir, selfPID()) // alive PID

	var stdoutBuf bytes.Buffer
	result, err := cmd.StatusCmd(jobID, root, projectID, &stdoutBuf)
	if err != nil {
		t.Fatalf("StatusCmd unexpected error: %v", err)
	}

	if result.Status != "running" {
		t.Errorf("Status: got %q, want %q", result.Status, "running")
	}
	if stdoutBuf.String() != "running\n" {
		t.Errorf("stdout: got %q, want %q", stdoutBuf.String(), "running\n")
	}
}

// ─── AC12: Status job not found ───────────────────────────────────────────────

// Scenario: Status on non-existent job returns not_found
// seed: status_responses.json scenario "not_found"
func TestStatusOnNonExistentJobReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-999999-deadbeef"

	var stdoutBuf bytes.Buffer
	result, err := cmd.StatusCmd(jobID, root, projectID, &stdoutBuf)

	if err == nil {
		t.Fatal("StatusCmd: expected error for non-existent job, got nil")
	}
	wantErr := fmt.Sprintf(`err:not_found "Job not found: %s"`, jobID)
	if err.Error() != wantErr {
		t.Errorf("error: got %q, want %q", err.Error(), wantErr)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode: got %d, want 3", result.ExitCode)
	}
}

// ─── AC13: Result on running/queued job ──────────────────────────────────────

// Scenario: Result on running job returns error
// seed: result_on_running.json
func TestResultOnRunningJobReturnsError(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100003-d4e5f6a7"
	makeJobDir(t, root, projectID, jobID, "running")

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)

	if err == nil {
		t.Fatal("ResultCmd: expected error for running job, got nil")
	}
	want := `err:user "Job is still running"`
	if err.Error() != want {
		t.Errorf("error: got %q, want %q", err.Error(), want)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode: got %d, want 1", result.ExitCode)
	}
}

// Scenario: Result on queued job returns error
func TestResultOnQueuedJobReturnsError(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100004-e5f6a7b8"
	makeJobDir(t, root, projectID, jobID, "queued")

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)

	if err == nil {
		t.Fatal("ResultCmd: expected error for queued job, got nil")
	}
	want := `err:user "Job is still queued"`
	if err.Error() != want {
		t.Errorf("error: got %q, want %q", err.Error(), want)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode: got %d, want 1", result.ExitCode)
	}
}

// ─── AC14: Result on failed/timeout/permission_error prints warning ───────────

// Scenario: Result on failed job prints stderr as warning
func TestResultOnFailedJobPrintsStderrAsWarning(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100005-f6a7b8c9"
	dir := makeJobDir(t, root, projectID, jobID, "failed")
	writeJobFile(t, dir, "stderr.txt", "error: subprocess crashed")
	writeJobFile(t, dir, "stdout.txt", "partial output")

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}

	if !strings.Contains(stderrBuf.String(), "error: subprocess crashed") {
		t.Errorf("stderr: expected warning with %q, got %q", "error: subprocess crashed", stderrBuf.String())
	}
	if !strings.Contains(stdoutBuf.String(), "partial output") {
		t.Errorf("stdout: expected %q, got %q", "partial output", stdoutBuf.String())
	}
}

// Scenario: Result on timed-out job prints stderr as warning
func TestResultOnTimedOutJobPrintsStderrAsWarning(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100006-a7b8c9d0"
	dir := makeJobDir(t, root, projectID, jobID, "timeout")
	writeJobFile(t, dir, "stderr.txt", "timed out after 600s")
	writeJobFile(t, dir, "stdout.txt", "")

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}

	if !strings.Contains(stderrBuf.String(), "timed out after 600s") {
		t.Errorf("stderr: expected warning, got %q", stderrBuf.String())
	}
}

// Scenario: Result on permission_error job prints stderr as warning
func TestResultOnPermissionErrorJobPrintsStderrAsWarning(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100007-b8c9d0e1"
	dir := makeJobDir(t, root, projectID, jobID, "permission_error")
	writeJobFile(t, dir, "stderr.txt", "permission denied: not allowed to write")
	writeJobFile(t, dir, "stdout.txt", "")

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}

	if !strings.Contains(stderrBuf.String(), "permission denied") {
		t.Errorf("stderr: expected permission warning, got %q", stderrBuf.String())
	}
}

// ─── AC15: Result prints stdout and auto-deletes ──────────────────────────────

// Scenario: Result prints stdout and deletes job directory
func TestResultPrintsStdoutAndDeletesJobDirectory(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100008-c9d0e1f2"
	dir := makeJobDir(t, root, projectID, jobID, "done")
	writeJobFile(t, dir, "stdout.txt", "Task completed successfully")

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}

	if !strings.Contains(stdoutBuf.String(), "Task completed successfully") {
		t.Errorf("stdout: got %q, expected to contain %q", stdoutBuf.String(), "Task completed successfully")
	}

	// Job directory must be auto-deleted.
	if !result.Deleted {
		t.Error("ResultCmd: Deleted should be true")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("ResultCmd: job directory %q should be deleted", dir)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", result.ExitCode)
	}
}

// ─── AC16: Result exit codes ──────────────────────────────────────────────────

// Scenario: Result returns exit code 0 on success
func TestResultReturnsExitCode0OnSuccess(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100009-d0e1f2a3"
	dir := makeJobDir(t, root, projectID, jobID, "done")
	writeJobFile(t, dir, "stdout.txt", "")

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", result.ExitCode)
	}
}

// Scenario: Result returns exit code 3 if job not found
func TestResultReturnsExitCode3IfJobNotFound(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-999999-deadbeef"

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)

	if err == nil {
		t.Fatal("ResultCmd: expected error for non-existent job, got nil")
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode: got %d, want 3", result.ExitCode)
	}
}

// ─── Edge Cases ───────────────────────────────────────────────────────────────

// Scenario: Result on already-deleted job returns not_found
func TestResultOnAlreadyDeletedJobReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100010-e1f2a3b4"

	// First call: create job, retrieve result (auto-deletes).
	dir := makeJobDir(t, root, projectID, jobID, "done")
	writeJobFile(t, dir, "stdout.txt", "output")

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("first ResultCmd unexpected error: %v", err)
	}

	// Second call: job directory is gone → should return not_found.
	stdoutBuf.Reset()
	stderrBuf.Reset()
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)

	if err == nil {
		t.Fatal("second ResultCmd: expected not_found error, got nil")
	}
	if !strings.Contains(err.Error(), "err:not_found") {
		t.Errorf("error: got %q, want err:not_found", err.Error())
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode: got %d, want 3", result.ExitCode)
	}
}

// Scenario: Empty stdout.txt prints nothing
func TestEmptyStdoutTxtPrintsNothing(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100011-f2a3b4c5"
	dir := makeJobDir(t, root, projectID, jobID, "done")
	writeJobFile(t, dir, "stdout.txt", "")

	var stdoutBuf, stderrBuf bytes.Buffer
	result, err := cmd.ResultCmd(jobID, root, projectID, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("ResultCmd unexpected error: %v", err)
	}

	if stdoutBuf.Len() != 0 {
		t.Errorf("stdout: expected empty, got %q", stdoutBuf.String())
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", result.ExitCode)
	}
}

// Scenario: Status called concurrently with execution completing
func TestStatusCalledConcurrentlyWithExecutionCompleting(t *testing.T) {
	root := t.TempDir()
	projectID := "proj"
	jobID := "job-20260227-100012-a3b4c5d6"
	dir := makeJobDir(t, root, projectID, jobID, "running")
	writePID(t, dir, selfPID())

	// Concurrently write "done" and call status.
	done := make(chan string, 1)
	go func() {
		// Simulate the job completing.
		time.Sleep(10 * time.Millisecond)
		_ = job.AtomicWrite(filepath.Join(dir, "status"), []byte("done"))
	}()

	go func() {
		var buf bytes.Buffer
		result, err := cmd.StatusCmd(jobID, root, projectID, &buf)
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- result.Status
	}()

	status := <-done
	// The result must be either "running" or "done" — never corrupted.
	if status != "running" && status != "done" {
		t.Errorf("concurrent status: got corrupted value %q", status)
	}
}
