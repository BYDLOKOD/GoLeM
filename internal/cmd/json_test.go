package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Helpers
// =============================================================================

// makeJobDir creates a minimal job directory under subagentsRoot/projectID/jobID
// with the given status file. Returns the job directory path.
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

// writeFile writes content to name inside dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}

// mustDecodeArray parses the output as a JSON array and returns the raw elements.
func mustDecodeArray(t *testing.T, data string) []json.RawMessage {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &arr); err != nil {
		t.Fatalf("output is not a valid JSON array: %v\noutput: %s", err, data)
	}
	return arr
}

// mustDecodeObject parses the output as a JSON object into the target.
func mustDecodeObject(t *testing.T, data string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), target); err != nil {
		t.Fatalf("output is not a valid JSON object: %v\noutput: %s", err, data)
	}
}

// isValidJSON returns true if data is syntactically valid JSON.
func isValidJSON(data string) bool {
	var v any
	return json.Unmarshal([]byte(strings.TrimSpace(data)), &v) == nil
}

// =============================================================================
// AC1: --json flag accepted by list command
// =============================================================================

// Scenario: --json flag is accepted by list command
func TestJsonFlagAcceptedByListCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var buf bytes.Buffer
	err := ListJSON(root, &FilterOptions{}, &buf)
	if err != nil {
		t.Fatalf("ListJSON returned error: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if !isValidJSON(out) {
		t.Errorf("expected valid JSON, got: %q", out)
	}
}

// Scenario: --json flag is accepted by status command
func TestJsonFlagAcceptedByStatusCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-142800-e5f6a7b8"
	dir := makeJobDir(t, root, "proj", jobID, "running")
	writeFile(t, dir, "pid.txt", "12345")
	writeFile(t, dir, "started_at.txt", "2026-02-27T14:28:00+03:00")

	var buf bytes.Buffer
	err := StatusJSON(root, "proj", jobID, &buf)
	if err != nil {
		t.Fatalf("StatusJSON returned error: %v", err)
	}
	if !isValidJSON(buf.String()) {
		t.Errorf("expected valid JSON, got: %q", buf.String())
	}
}

// Scenario: --json flag is accepted by result command
func TestJsonFlagAcceptedByResultCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-143205-a8f3b1c2"
	dir := makeJobDir(t, root, "proj", jobID, "done")
	writeFile(t, dir, "stdout.txt", "output")
	writeFile(t, dir, "stderr.txt", "")
	writeFile(t, dir, "changelog.txt", "EDIT file.go")
	writeFile(t, dir, "started_at.txt", "2026-02-27T14:32:05+03:00")
	writeFile(t, dir, "finished_at.txt", "2026-02-27T14:37:37+03:00")

	var buf bytes.Buffer
	err := ResultJSON(root, "proj", jobID, &buf)
	if err != nil {
		t.Fatalf("ResultJSON returned error: %v", err)
	}
	if !isValidJSON(buf.String()) {
		t.Errorf("expected valid JSON, got: %q", buf.String())
	}
}

// Scenario: --json flag is accepted by log command
func TestJsonFlagAcceptedByLogCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-143205-a8f3b1c2"
	dir := makeJobDir(t, root, "proj", jobID, "done")
	writeFile(t, dir, "changelog.txt", "EDIT src/utils/validate.ts: 142 chars\nWRITE src/utils/validate.test.ts\nFS: mkdir -p src/utils/__tests__")

	var buf bytes.Buffer
	err := LogJSON(root, "proj", jobID, &buf)
	if err != nil {
		t.Fatalf("LogJSON returned error: %v", err)
	}
	if !isValidJSON(buf.String()) {
		t.Errorf("expected valid JSON, got: %q", buf.String())
	}
}

// =============================================================================
// AC2: list --json outputs JSON array of job objects
// =============================================================================

// Scenario: list --json outputs array of job objects
func TestListJsonOutputsArrayOfJobObjects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	type jobSpec struct {
		projectID string
		jobID     string
		status    string
		startedAt string
	}
	jobs := []jobSpec{
		{"my-app-1234567890", "job-20260227-143205-a8f3b1c2", "done", "2026-02-27T14:32:05+03:00"},
		{"my-app-1234567890", "job-20260227-142800-e5f6a7b8", "running", "2026-02-27T14:28:00+03:00"},
		{"api-server-9876543210", "job-20260227-141500-c3d4e5f6", "failed", "2026-02-27T14:15:00+03:00"},
	}

	for _, j := range jobs {
		dir := makeJobDir(t, root, j.projectID, j.jobID, j.status)
		writeFile(t, dir, "started_at.txt", j.startedAt)
	}

	var buf bytes.Buffer
	err := ListJSON(root, &FilterOptions{}, &buf)
	if err != nil {
		t.Fatalf("ListJSON returned error: %v", err)
	}

	elems := mustDecodeArray(t, buf.String())
	if len(elems) != 3 {
		t.Fatalf("expected 3 elements in JSON array, got %d", len(elems))
	}

	// Verify each element has required fields.
	for i, raw := range elems {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("element %d is not a JSON object: %v", i, err)
		}
		for _, field := range []string{"id", "status", "started_at", "project_id"} {
			if _, ok := obj[field]; !ok {
				t.Errorf("element %d missing field %q", i, field)
			}
		}
	}

	// First element should be newest: job-20260227-143205-a8f3b1c2
	var first map[string]any
	if err := json.Unmarshal(elems[0], &first); err != nil {
		t.Fatalf("first element decode: %v", err)
	}
	if first["id"] != "job-20260227-143205-a8f3b1c2" {
		t.Errorf("first element id: got %q, want %q", first["id"], "job-20260227-143205-a8f3b1c2")
	}
	if first["status"] != "done" {
		t.Errorf("first element status: got %q, want %q", first["status"], "done")
	}
	if first["started_at"] != "2026-02-27T14:32:05+03:00" {
		t.Errorf("first element started_at: got %q, want %q", first["started_at"], "2026-02-27T14:32:05+03:00")
	}
	if first["project_id"] != "my-app-1234567890" {
		t.Errorf("first element project_id: got %q, want %q", first["project_id"], "my-app-1234567890")
	}
}

// =============================================================================
// AC3: status --json outputs JSON object with job details
// =============================================================================

// Scenario: status --json outputs job status object
func TestStatusJsonOutputsJobStatusObject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-142800-e5f6a7b8"
	dir := makeJobDir(t, root, "proj", jobID, "running")
	// Use this test process's own PID so the job is genuinely alive: StatusJSON
	// reconciles "running" jobs by liveness, so a dead PID would (correctly)
	// flip the status to "failed". A live PID proves the still-running path.
	livePID := os.Getpid()
	writeFile(t, dir, "pid.txt", strconv.Itoa(livePID))
	writeFile(t, dir, "started_at.txt", "2026-02-27T14:28:00+03:00")

	var buf bytes.Buffer
	if err := StatusJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("StatusJSON: %v", err)
	}

	var obj JobStatusJSON
	mustDecodeObject(t, buf.String(), &obj)

	if obj.ID != jobID {
		t.Errorf("id: got %q, want %q", obj.ID, jobID)
	}
	if obj.Status != "running" {
		t.Errorf("status: got %q, want %q", obj.Status, "running")
	}
	if obj.PID != livePID {
		t.Errorf("pid: got %d, want %d", obj.PID, livePID)
	}
	if obj.StartedAt != "2026-02-27T14:28:00+03:00" {
		t.Errorf("started_at: got %q, want %q", obj.StartedAt, "2026-02-27T14:28:00+03:00")
	}
}

// =============================================================================
// AC4: result --json outputs JSON object with full job result
// =============================================================================

// Scenario: result --json for a completed job
func TestResultJsonForCompletedJob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-143205-a8f3b1c2"
	dir := makeJobDir(t, root, "proj", jobID, "done")

	stdout := "Created 8 test cases in src/utils/__tests__/validate.test.ts.\nAll tests passing.\n\nCoverage: 94% statements, 87% branches."
	changelog := "EDIT src/utils/validate.ts: 142 chars\nWRITE src/utils/validate.test.ts\nFS: mkdir -p src/utils/__tests__"

	writeFile(t, dir, "stdout.txt", stdout)
	writeFile(t, dir, "stderr.txt", "")
	writeFile(t, dir, "changelog.txt", changelog)
	// duration: started 14:32:05, finished 14:37:37 = 332 seconds
	writeFile(t, dir, "started_at.txt", "2026-02-27T14:32:05+03:00")
	writeFile(t, dir, "finished_at.txt", "2026-02-27T14:37:37+03:00")
	// Alternatively, duration_seconds file if the implementation uses it.
	writeFile(t, dir, "duration_seconds.txt", "332")

	var buf bytes.Buffer
	if err := ResultJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("ResultJSON: %v", err)
	}

	var obj JobResultJSON
	mustDecodeObject(t, buf.String(), &obj)

	if obj.ID != jobID {
		t.Errorf("id: got %q, want %q", obj.ID, jobID)
	}
	if obj.Status != "done" {
		t.Errorf("status: got %q, want %q", obj.Status, "done")
	}
	if obj.Stdout != stdout {
		t.Errorf("stdout: got %q, want %q", obj.Stdout, stdout)
	}
	if obj.Stderr != "" {
		t.Errorf("stderr: got %q, want empty string", obj.Stderr)
	}
	if obj.Changelog != changelog {
		t.Errorf("changelog: got %q, want %q", obj.Changelog, changelog)
	}
	if obj.DurationSeconds != 332 {
		t.Errorf("duration_seconds: got %d, want 332", obj.DurationSeconds)
	}
}

// =============================================================================
// AC5: log --json outputs JSON object with changes array
// =============================================================================

// Scenario: log --json outputs changelog as structured array
func TestLogJsonOutputsChangelogAsStructuredArray(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-143205-a8f3b1c2"
	dir := makeJobDir(t, root, "proj", jobID, "done")

	changelogContent := "EDIT src/utils/validate.ts: 142 chars\nWRITE src/utils/validate.test.ts\nFS: mkdir -p src/utils/__tests__"
	writeFile(t, dir, "changelog.txt", changelogContent)

	var buf bytes.Buffer
	if err := LogJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("LogJSON: %v", err)
	}

	var obj JobLogJSON
	mustDecodeObject(t, buf.String(), &obj)

	if obj.ID != jobID {
		t.Errorf("id: got %q, want %q", obj.ID, jobID)
	}
	if len(obj.Changes) != 3 {
		t.Fatalf("changes: got %d elements, want 3", len(obj.Changes))
	}

	wantChanges := []string{
		"EDIT src/utils/validate.ts: 142 chars",
		"WRITE src/utils/validate.test.ts",
		"FS: mkdir -p src/utils/__tests__",
	}
	for i, want := range wantChanges {
		if obj.Changes[i] != want {
			t.Errorf("changes[%d]: got %q, want %q", i, obj.Changes[i], want)
		}
	}
}

// =============================================================================
// AC6: JSON output to stdout, errors to stderr in text format
// =============================================================================

// Scenario: Errors go to stderr in text format even with --json
func TestErrorsGoToStderrInTextFormatEvenWithJson(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// job-nonexistent does not exist.
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// StatusJSON should return an error for a non-existent job.
	err := StatusJSON(root, "proj", "job-nonexistent", &stdout)
	if err == nil {
		t.Fatal("expected error for non-existent job, got nil")
	}

	// Write the error to stderr (as the CLI layer would).
	stderr.WriteString(err.Error())

	stderrOut := stderr.String()
	if !strings.Contains(stderrOut, "err:not_found") {
		t.Errorf("stderr should contain err:not_found, got: %q", stderrOut)
	}
	// stderr must be plain text, not JSON.
	if isValidJSON(stderrOut) {
		t.Errorf("stderr must be plain text (not JSON), got: %q", stderrOut)
	}
	// stdout must be empty.
	if stdout.Len() != 0 {
		t.Errorf("stdout must be empty on error, got: %q", stdout.String())
	}
}

// Scenario: JSON output goes to stdout only
func TestJsonOutputGoesToStdoutOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	makeJobDir(t, root, "proj", "job-20260227-143205-a8f3b1c2", "done")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := ListJSON(root, &FilterOptions{}, &stdout); err != nil {
		// Write error to stderr, not stdout.
		stderr.WriteString(err.Error())
		t.Fatalf("ListJSON error: %v", err)
	}

	stdoutOut := stdout.String()
	stderrOut := stderr.String()

	if !isValidJSON(stdoutOut) {
		t.Errorf("stdout must be valid JSON, got: %q", stdoutOut)
	}
	// stderr must not contain the JSON array content.
	if strings.Contains(stderrOut, "[{") || strings.Contains(stderrOut, "[\"") {
		t.Errorf("stderr must not contain JSON array content, got: %q", stderrOut)
	}
}

// =============================================================================
// AC7: Empty list produces [], not null
// =============================================================================

// Scenario: list --json with no jobs outputs empty array
func TestListJsonWithNoJobsOutputsEmptyArray(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var buf bytes.Buffer
	if err := ListJSON(root, &FilterOptions{}, &buf); err != nil {
		t.Fatalf("ListJSON: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if out != "[]" {
		t.Errorf("expected exactly [], got: %q", out)
	}
	if out == "null" {
		t.Error("output must not be null")
	}
	if out == "" {
		t.Error("output must not be empty string")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

// Scenario: result --json on a failed job includes stderr and exit_code
func TestResultJsonOnFailedJobIncludesStderrAndExitCode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-141500-c3d4e5f6"
	dir := makeJobDir(t, root, "proj", jobID, "failed")

	stderrContent := "Error: permission denied - cannot write to src/db/pool.go\n[GoLeM] Job failed with exit code 1"
	writeFile(t, dir, "stdout.txt", "")
	writeFile(t, dir, "stderr.txt", stderrContent)
	writeFile(t, dir, "changelog.txt", "(no file changes)")
	writeFile(t, dir, "duration_seconds.txt", "72")
	writeFile(t, dir, "exit_code.txt", "1")

	var buf bytes.Buffer
	if err := ResultJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("ResultJSON: %v", err)
	}

	var obj map[string]any
	mustDecodeObject(t, buf.String(), &obj)

	if obj["id"] != jobID {
		t.Errorf("id: got %q, want %q", obj["id"], jobID)
	}
	if obj["status"] != "failed" {
		t.Errorf("status: got %q, want %q", obj["status"], "failed")
	}
	if obj["stdout"] != "" {
		t.Errorf("stdout: got %q, want empty string", obj["stdout"])
	}
	if obj["stderr"] != stderrContent {
		t.Errorf("stderr: got %q, want %q", obj["stderr"], stderrContent)
	}
	if obj["changelog"] != "(no file changes)" {
		t.Errorf("changelog: got %q, want %q", obj["changelog"], "(no file changes)")
	}
	// duration_seconds should be 72
	if dur, ok := obj["duration_seconds"].(float64); !ok || int(dur) != 72 {
		t.Errorf("duration_seconds: got %v, want 72", obj["duration_seconds"])
	}
	// exit_code should be 1
	if ec, ok := obj["exit_code"].(float64); !ok || int(ec) != 1 {
		t.Errorf("exit_code: got %v, want 1", obj["exit_code"])
	}
}

// Scenario: status --json on stale job reconciles before output
func TestStatusJsonOnStaleJobReconcilesBeforeOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-080000-aabbccdd"
	dir := makeJobDir(t, root, "proj", jobID, "running")
	// Write a PID that cannot be alive (very large/invalid).
	writeFile(t, dir, "pid.txt", "999999999")
	writeFile(t, dir, "started_at.txt", "2026-02-27T08:00:00+03:00")

	var buf bytes.Buffer
	// StatusJSON must reconcile stale running job before producing output.
	if err := StatusJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("StatusJSON: %v", err)
	}

	var obj map[string]any
	mustDecodeObject(t, buf.String(), &obj)

	// After reconciliation, status should be "failed".
	if obj["status"] != "failed" {
		t.Errorf("reconciled status: got %q, want %q", obj["status"], "failed")
	}
}

// Scenario: Special characters in stdout are properly escaped in JSON
func TestSpecialCharactersInStdoutAreProperlyEscapedInJson(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-143000-special0"
	dir := makeJobDir(t, root, "proj", jobID, "done")

	// Embedded JSON and unicode.
	embeddedContent := `{"key": "value"}` + "\n" + "Unicode: \u4e2d\u6587"
	writeFile(t, dir, "stdout.txt", embeddedContent)
	writeFile(t, dir, "stderr.txt", "")
	writeFile(t, dir, "changelog.txt", "")
	writeFile(t, dir, "duration_seconds.txt", "10")

	var buf bytes.Buffer
	if err := ResultJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("ResultJSON: %v", err)
	}

	// The output must be valid JSON (embedded JSON and unicode are escaped).
	if !isValidJSON(buf.String()) {
		t.Errorf("expected valid JSON output, got: %q", buf.String())
	}

	var obj map[string]any
	mustDecodeObject(t, buf.String(), &obj)

	if stdoutVal, ok := obj["stdout"].(string); !ok || stdoutVal != embeddedContent {
		t.Errorf("stdout field: got %q, want %q", obj["stdout"], embeddedContent)
	}
}

// Scenario: result --json on a timed out job
func TestResultJsonOnTimedOutJob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-120000-timeout0"
	dir := makeJobDir(t, root, "proj", jobID, "timeout")
	writeFile(t, dir, "stderr.txt", "process killed after 3000s timeout")
	writeFile(t, dir, "stdout.txt", "")
	writeFile(t, dir, "changelog.txt", "")
	writeFile(t, dir, "duration_seconds.txt", "3000")

	var buf bytes.Buffer
	if err := ResultJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("ResultJSON: %v", err)
	}

	var obj map[string]any
	mustDecodeObject(t, buf.String(), &obj)

	if obj["status"] != "timeout" {
		t.Errorf("status: got %q, want %q", obj["status"], "timeout")
	}
	// Must include available stderr content.
	if obj["stderr"] == nil {
		t.Error("expected stderr field in result")
	}
}

// Scenario: result --json on a permission_error job
func TestResultJsonOnPermissionErrorJob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-130000-permerr0"
	dir := makeJobDir(t, root, "proj", jobID, "permission_error")
	writeFile(t, dir, "stderr.txt", "permission denied: cannot write to file")
	writeFile(t, dir, "stdout.txt", "")
	writeFile(t, dir, "changelog.txt", "")
	writeFile(t, dir, "duration_seconds.txt", "5")

	var buf bytes.Buffer
	if err := ResultJSON(root, "proj", jobID, &buf); err != nil {
		t.Fatalf("ResultJSON: %v", err)
	}

	var obj map[string]any
	mustDecodeObject(t, buf.String(), &obj)

	if obj["status"] != "permission_error" {
		t.Errorf("status: got %q, want %q", obj["status"], "permission_error")
	}
	if stderrVal, ok := obj["stderr"].(string); !ok || !strings.Contains(stderrVal, "permission") {
		t.Errorf("stderr: expected content about permission issues, got %q", obj["stderr"])
	}
}

// Verify JSONOutput encodes non-null empty arrays correctly.
func TestFormatJsonProducesValidJson(t *testing.T) {
	t.Parallel()
	var arr []JobListItem // nil slice
	data, err := FormatJSON(arr)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	_ = time.Now // keep time import used
	if string(data) == "null" {
		t.Error("FormatJSON for nil slice must not produce null")
	}
	if !isValidJSON(string(data)) {
		t.Errorf("FormatJSON produced invalid JSON: %q", string(data))
	}
}
