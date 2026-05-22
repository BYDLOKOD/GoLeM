package cmd_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
)

// ---------- helpers ----------

// makeJob creates a job directory under root with the given status.
// Returns the job directory path.
func makeJob(t *testing.T, root, jobID, status string) string {
	t.Helper()
	dir := filepath.Join(root, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeJob MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("makeJob WriteFile status: %v", err)
	}
	return dir
}

// makeJobInProject creates a job under root/<projectID>/<jobID>.
func makeJobInProject(t *testing.T, root, projectID, jobID, status string) string {
	t.Helper()
	dir := filepath.Join(root, projectID, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeJobInProject MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("makeJobInProject WriteFile status: %v", err)
	}
	return dir
}

// makeJobWithStarted creates a job with a started_at file and the given status.
func makeJobWithStarted(t *testing.T, root, jobID, status, startedAt string) string {
	t.Helper()
	dir := makeJob(t, root, jobID, status)
	if startedAt != "" {
		if err := os.WriteFile(filepath.Join(dir, "started_at"), []byte(startedAt), 0o644); err != nil {
			t.Fatalf("makeJobWithStarted WriteFile started_at: %v", err)
		}
	}
	return dir
}

// makeJobInProjectWithStarted creates a project-scoped job with a started_at file.
func makeJobInProjectWithStarted(t *testing.T, root, projectID, jobID, status, startedAt string) string {
	t.Helper()
	dir := makeJobInProject(t, root, projectID, jobID, status)
	if startedAt != "" {
		if err := os.WriteFile(filepath.Join(dir, "started_at"), []byte(startedAt), 0o644); err != nil {
			t.Fatalf("makeJobInProjectWithStarted WriteFile started_at: %v", err)
		}
	}
	return dir
}

// makePidFile writes a pid.txt file into the job directory.
func makePidFile(t *testing.T, jobDir string, pid int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(jobDir, "pid.txt"), []byte(fmt.Sprintf("%d", pid)), 0o644); err != nil {
		t.Fatalf("makePidFile: %v", err)
	}
}

// readStatus reads the status file from the given job directory.
func readStatus(t *testing.T, jobDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(jobDir, "status"))
	if err != nil {
		t.Fatalf("readStatus: %v", err)
	}
	return string(data)
}

// noopSignal is a signal function that does nothing (simulates dead process).
func noopSignal(_ int, _ os.Signal) error { return nil }

// errSignal is a signal function that always returns an error (simulates dead process).
func errSignal(_ int, _ os.Signal) error { return fmt.Errorf("no such process") }

// noopSleep is a sleep function that does nothing.
func noopSleep() {}

// ---------- AC1: glm list - tabular output ----------

func TestListShowsAllJobsInTabularFormatSortedByStartTime(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Seed: list_output.json scenario - 5 jobs
	makeJobWithStarted(t, root, "job-20260227-103000-a3b4c5d6", "queued", "")
	makeJobWithStarted(t, root, "job-20260227-102000-c9d0e1f2", "failed", "2026-02-27T10:20:00+03:00")
	makeJobWithStarted(t, root, "job-20260227-101500-e5f6a7b8", "running", "2026-02-27T10:15:00+03:00")
	makeJobWithStarted(t, root, "job-20260227-100000-a1b2c3d4", "done", "2026-02-27T10:00:00+03:00")
	makeJobWithStarted(t, root, "job-20260227-094500-f7e8d9c0", "timeout", "2026-02-27T09:45:00+03:00")

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	output := buf.String()

	// Must have header columns
	if !strings.Contains(output, "JOB_ID") {
		t.Errorf("output missing JOB_ID column header: %q", output)
	}
	if !strings.Contains(output, "STATUS") {
		t.Errorf("output missing STATUS column header: %q", output)
	}
	if !strings.Contains(output, "STARTED") {
		t.Errorf("output missing STARTED column header: %q", output)
	}

	// All 5 job IDs must appear
	for _, id := range []string{
		"job-20260227-103000-a3b4c5d6",
		"job-20260227-102000-c9d0e1f2",
		"job-20260227-101500-e5f6a7b8",
		"job-20260227-100000-a1b2c3d4",
		"job-20260227-094500-f7e8d9c0",
	} {
		if !strings.Contains(output, id) {
			t.Errorf("output missing job %q", id)
		}
	}

	// Sorted newest-first: 103000 must appear before 094500
	idx103000 := strings.Index(output, "job-20260227-103000-a3b4c5d6")
	idx094500 := strings.Index(output, "job-20260227-094500-f7e8d9c0")
	if idx103000 == -1 || idx094500 == -1 {
		t.Fatalf("expected both jobs to appear in output")
	}
	if idx103000 > idx094500 {
		t.Errorf("jobs not sorted newest-first: 103000 at %d, 094500 at %d", idx103000, idx094500)
	}
}

// ---------- AC2: List scans both project-scoped and legacy dirs ----------

func TestListFindsJobsInProjectScopedDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projectID := "my-app-1234567890"
	jobID := "job-20260227-101500-e5f6a7b8"

	makeJobInProjectWithStarted(t, root, projectID, jobID, "running", "2026-02-27T10:15:00+03:00")

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	if !strings.Contains(buf.String(), jobID) {
		t.Errorf("project-scoped job %q not found in output: %q", jobID, buf.String())
	}
}

func TestListFindsJobsInLegacyFlatDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-a1b2c3d4"

	// Legacy flat: root/<jobID>/status
	makeJobWithStarted(t, root, jobID, "done", "2026-02-27T10:00:00+03:00")

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	if !strings.Contains(buf.String(), jobID) {
		t.Errorf("legacy flat job %q not found in output: %q", jobID, buf.String())
	}
}

func TestListMergesJobsFromAllDirectoryTypes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	projectID := "my-app-1234567890"
	projJobID := "job-20260227-101500-e5f6a7b8"
	legacyJobID := "job-20260227-094500-f7e8d9c0"

	makeJobInProjectWithStarted(t, root, projectID, projJobID, "running", "2026-02-27T10:15:00+03:00")
	makeJobWithStarted(t, root, legacyJobID, "timeout", "2026-02-27T09:45:00+03:00")

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, projJobID) {
		t.Errorf("project-scoped job %q missing from merged output", projJobID)
	}
	if !strings.Contains(output, legacyJobID) {
		t.Errorf("legacy flat job %q missing from merged output", legacyJobID)
	}

	// Sorted newest-first: 101500 before 094500
	idxProj := strings.Index(output, projJobID)
	idxLeg := strings.Index(output, legacyJobID)
	if idxProj > idxLeg {
		t.Errorf("merged list not sorted newest-first")
	}
}

// ---------- AC3: List checks PID liveness for running jobs ----------

func TestListUpdatesStaleRunningJobsToFailed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJobWithStarted(t, root, jobID, "running", "2026-02-27T10:15:00+03:00")
	// Write a dead PID (PID 1 exists but is not our process; use a known-dead PID)
	// We pick PID 99999999 which is almost certainly not alive.
	makePidFile(t, dir, 99999999)

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	// Status file must be updated to failed
	if got := readStatus(t, dir); got != "failed" {
		t.Errorf("expected stale running job status to be updated to 'failed', got %q", got)
	}

	// Output must show failed
	if !strings.Contains(buf.String(), "failed") {
		t.Errorf("output does not show 'failed' for stale running job: %q", buf.String())
	}
}

// ---------- AC4: Empty list ----------

func TestEmptyJobListPrintsNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty job list, got: %q", buf.String())
	}
}

// ---------- AC5: glm log - print changelog ----------

func TestLogPrintsChangelogContents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJob(t, root, jobID, "done")
	changelog := "EDIT /home/veschin/work/my-app/src/api/routes/users.ts: 342 chars\n" +
		"EDIT /home/veschin/work/my-app/src/api/routes/users.ts: 128 chars\n" +
		"WRITE /home/veschin/work/my-app/src/api/middleware/validateBody.ts\n" +
		"EDIT /home/veschin/work/my-app/src/api/routes/posts.ts: 256 chars\n" +
		"EDIT /home/veschin/work/my-app/src/api/routes/posts.ts: 89 chars\n" +
		"EDIT /home/veschin/work/my-app/src/api/routes/comments.ts: 197 chars\n" +
		"FS: mkdir -p /home/veschin/work/my-app/src/api/validators\n" +
		"WRITE /home/veschin/work/my-app/src/api/validators/userSchema.ts\n" +
		"WRITE /home/veschin/work/my-app/src/api/validators/postSchema.ts\n" +
		"EDIT /home/veschin/work/my-app/src/api/index.ts: 64 chars\n"
	if err := os.WriteFile(filepath.Join(dir, "changelog.txt"), []byte(changelog), 0o644); err != nil {
		t.Fatalf("WriteFile changelog.txt: %v", err)
	}

	var buf bytes.Buffer
	if err := cmd.LogCmd(root, "", jobID, &buf); err != nil {
		t.Fatalf("LogCmd error: %v", err)
	}

	if got := buf.String(); got != changelog {
		t.Errorf("LogCmd output mismatch\ngot:  %q\nwant: %q", got, changelog)
	}
}

func TestLogPrintsFallbackWhenChangelogDoesNotExist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	// Job exists but no changelog.txt
	makeJob(t, root, jobID, "done")

	var buf bytes.Buffer
	if err := cmd.LogCmd(root, "", jobID, &buf); err != nil {
		t.Fatalf("LogCmd error: %v", err)
	}

	if !strings.Contains(buf.String(), "(no changelog)") {
		t.Errorf("expected '(no changelog)', got: %q", buf.String())
	}
}

// ---------- AC6: Log job not found ----------

func TestLogOnNonExistentJobReturnsNotFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	var buf bytes.Buffer
	err := cmd.LogCmd(root, "", "job-20260227-999999-deadbeef", &buf)
	if err == nil {
		t.Fatal("expected error for non-existent job, got nil")
	}
	if !strings.Contains(err.Error(), "err:not_found") {
		t.Errorf("expected err:not_found, got: %q", err.Error())
	}
}

// ---------- AC7: glm clean - remove terminal jobs ----------

func TestCleanWithoutFlagsRemovesTerminalStatusJobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	// Seed: clean_by_status.json
	type entry struct {
		jobID  string
		status string
		keep   bool
	}
	entries := []entry{
		{"job-20260225-140000-aabb1122", "done", false},
		{"job-20260225-143000-ccdd3344", "failed", false},
		{"job-20260225-150000-eeff5566", "timeout", false},
		{"job-20260226-090000-a1b2c3d4", "killed", false},
		{"job-20260227-101500-e5f6a7b8", "running", true},
		{"job-20260227-103000-a3b4c5d6", "queued", true},
	}
	for _, e := range entries {
		makeJob(t, root, e.jobID, e.status)
	}

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "Cleaned 4 jobs" {
		t.Errorf("CleanCmd output: got %q, want %q", got, "Cleaned 4 jobs")
	}

	for _, e := range entries {
		dir := filepath.Join(root, e.jobID)
		_, err := os.Stat(dir)
		if e.keep {
			if err != nil {
				t.Errorf("job %q (status=%q) should be kept but was removed", e.jobID, e.status)
			}
		} else {
			if err == nil {
				t.Errorf("job %q (status=%q) should be removed but still exists", e.jobID, e.status)
			}
		}
	}
}

// TestCleanRemovesProjectScopedTerminalJobs guards the production storage
// layout: jobs live at root/<projectID>/<jobID>/, not flat at root/<jobID>/.
// CleanCmd must descend one level into project directories.
func TestCleanRemovesProjectScopedTerminalJobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	projectID := "myproj-123456"
	doneJob := makeJobInProject(t, root, projectID, "job-20260225-140000-aabb1122", "done")
	runningJob := makeJobInProject(t, root, projectID, "job-20260227-101500-e5f6a7b8", "running")

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "Cleaned 1 jobs" {
		t.Errorf("CleanCmd output: got %q, want %q", got, "Cleaned 1 jobs")
	}
	if _, err := os.Stat(doneJob); err == nil {
		t.Errorf("project-scoped done job should be removed but still exists")
	}
	if _, err := os.Stat(runningJob); err != nil {
		t.Errorf("project-scoped running job should be kept but was removed: %v", err)
	}
}

// TestCleanRemovesEmptiedProjectDir verifies that a project directory left
// empty after all its jobs are cleaned is itself removed, so orphan project
// directories do not accumulate over time.
func TestCleanRemovesEmptiedProjectDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	projectID := "emptyproj-999"
	makeJobInProject(t, root, projectID, "job-20260225-140000-aabb1122", "done")

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, projectID)); err == nil {
		t.Errorf("project dir emptied by clean should be removed but still exists")
	}
}

func TestCleanAlsoRemovesPermissionErrorJobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	makeJob(t, root, "job-20260225-140000-perm0001", "permission_error")

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	dir := filepath.Join(root, "job-20260225-140000-perm0001")
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("permission_error job should be removed but still exists")
	}
	if !strings.Contains(buf.String(), "Cleaned 1 jobs") {
		t.Errorf("expected 'Cleaned 1 jobs', got: %q", buf.String())
	}
}

// ---------- AC8: glm clean --days N ----------

func TestCleanWithDaysRemovesOldJobsRegardlessOfStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Reference date: 2026-02-27T12:00:00+03:00
	loc := time.FixedZone("UTC+3", 3*60*60)
	refTime := time.Date(2026, 2, 27, 12, 0, 0, 0, loc)

	type entry struct {
		jobID      string
		status     string
		modifiedAt time.Time
		keep       bool
	}
	entries := []entry{
		{
			"job-20260220-083000-old1old1", "done",
			time.Date(2026, 2, 20, 8, 35, 12, 0, loc), false,
		},
		{
			"job-20260222-141500-old2old2", "failed",
			time.Date(2026, 2, 22, 14, 15, 30, 0, loc), false,
		},
		{
			"job-20260223-200000-old3old3", "timeout",
			time.Date(2026, 2, 23, 20, 50, 0, 0, loc), false,
		},
		{
			"job-20260225-110000-new1new1", "done",
			time.Date(2026, 2, 25, 11, 5, 22, 0, loc), true,
		},
		{
			"job-20260227-090000-new2new2", "done",
			time.Date(2026, 2, 27, 9, 12, 45, 0, loc), true,
		},
		{
			"job-20260226-160000-new3new3", "running",
			time.Date(2026, 2, 26, 16, 30, 0, 0, loc), true,
		},
	}

	for _, e := range entries {
		dir := makeJob(t, root, e.jobID, e.status)
		// Set the directory modification time to the expected modifiedAt value.
		if err := os.Chtimes(dir, e.modifiedAt, e.modifiedAt); err != nil {
			t.Fatalf("Chtimes %q: %v", dir, err)
		}
	}

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, 3, refTime, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "Cleaned 3 jobs" {
		t.Errorf("CleanCmd output: got %q, want %q", got, "Cleaned 3 jobs")
	}

	for _, e := range entries {
		dir := filepath.Join(root, e.jobID)
		_, err := os.Stat(dir)
		if e.keep {
			if err != nil {
				t.Errorf("job %q should be kept but was removed", e.jobID)
			}
		} else {
			if err == nil {
				t.Errorf("job %q should be removed but still exists", e.jobID)
			}
		}
	}
}

// ---------- AC9: Clean prints count ----------

func TestCleanPrintsCountOfRemovedJobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	for i := 0; i < 5; i++ {
		makeJob(t, root, fmt.Sprintf("job-2026022%d-140000-aabb%04d", i, i), "done")
	}

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "Cleaned 5 jobs" {
		t.Errorf("CleanCmd output: got %q, want %q", got, "Cleaned 5 jobs")
	}
}

// ---------- AC10: Clean --days validation ----------

func TestCleanWithInvalidDaysValueReturnsError(t *testing.T) {
	t.Parallel()
	// days = -2 is used as sentinel for "invalid days string" scenario.
	// The caller layer (CLI parsing) validates and passes -2 to indicate invalid.
	// Here we test that CleanCmd rejects a non-negative but programmatically
	// injected invalid value. Per BDD: "glm clean --days abc" -> err:user, exit 1.
	//
	// Since CleanCmd takes an int, invalid string parsing happens at the CLI layer.
	// We test the boundary the CLI layer enforces: days value < -1 means invalid.
	root := t.TempDir()
	now := time.Now()

	var buf bytes.Buffer
	err := cmd.CleanCmd(root, -2, now, &buf)
	if err == nil {
		t.Fatal("expected error for invalid --days value, got nil")
	}
	if !strings.Contains(err.Error(), "err:user") {
		t.Errorf("expected err:user, got: %q", err.Error())
	}
}

func TestCleanWithNegativeDaysValueReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	var buf bytes.Buffer
	// -2 signals an invalid (explicitly negative) days value passed from CLI
	err := cmd.CleanCmd(root, -2, now, &buf)
	if err == nil {
		t.Fatal("expected error for negative --days value, got nil")
	}
	if !strings.Contains(err.Error(), "err:user") {
		t.Errorf("expected err:user, got: %q", err.Error())
	}
}

// ---------- AC11: glm kill - terminate running job ----------

func TestKillSendsSIGTERMThenSIGKILLToProcessGroup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJob(t, root, jobID, "running")
	makePidFile(t, dir, 51203)

	var signals []struct {
		pid int
		sig os.Signal
	}
	captureSignal := func(pid int, sig os.Signal) error {
		signals = append(signals, struct {
			pid int
			sig os.Signal
		}{pid, sig})
		return fmt.Errorf("no such process") // simulates dead process after SIGTERM
	}

	sleptCount := 0
	countSleep := func() { sleptCount++ }

	if err := cmd.KillCmd(root, "", jobID, captureSignal, countSleep); err != nil {
		t.Fatalf("KillCmd error: %v", err)
	}

	// First signal must be SIGTERM to process group -51203
	if len(signals) == 0 {
		t.Fatal("no signals sent")
	}
	if signals[0].pid != -51203 {
		t.Errorf("SIGTERM target: got pid %d, want -51203", signals[0].pid)
	}
	if signals[0].sig != syscall.SIGTERM {
		t.Errorf("first signal: got %v, want SIGTERM", signals[0].sig)
	}

	// Sleep must have been called once
	if sleptCount != 1 {
		t.Errorf("sleep called %d times, want 1", sleptCount)
	}
}

func TestKillSendsSIGKILLWhenProcessStillAliveAfterSIGTERM(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJob(t, root, jobID, "running")
	makePidFile(t, dir, 51203)

	callCount := 0
	// Process is alive for SIGTERM, dead for SIGKILL
	signalFn := func(pid int, sig os.Signal) error {
		callCount++
		if sig == syscall.SIGTERM {
			return nil // process still alive
		}
		return fmt.Errorf("no such process")
	}

	var signals []os.Signal
	trackSignals := func(pid int, sig os.Signal) error {
		signals = append(signals, sig)
		return signalFn(pid, sig)
	}

	if err := cmd.KillCmd(root, "", jobID, trackSignals, noopSleep); err != nil {
		t.Fatalf("KillCmd error: %v", err)
	}

	// Must have sent SIGTERM then SIGKILL
	if len(signals) < 2 {
		t.Fatalf("expected at least 2 signals (SIGTERM + SIGKILL), got %d", len(signals))
	}
	if signals[0] != syscall.SIGTERM {
		t.Errorf("first signal: got %v, want SIGTERM", signals[0])
	}
	if signals[1] != syscall.SIGKILL {
		t.Errorf("second signal: got %v, want SIGKILL", signals[1])
	}
}

// ---------- AC12: Kill updates status to killed ----------

func TestKillUpdatesJobStatusToKilled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-e5f6a7b8"

	dir := makeJob(t, root, jobID, "running")
	makePidFile(t, dir, 51203)

	if err := cmd.KillCmd(root, "", jobID, errSignal, noopSleep); err != nil {
		t.Fatalf("KillCmd error: %v", err)
	}

	if got := readStatus(t, dir); got != "killed" {
		t.Errorf("expected status 'killed', got %q", got)
	}
}

// ---------- AC13: Kill error cases ----------

func TestKillOnNonExistentJobReturnsNotFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	err := cmd.KillCmd(root, "", "job-20260227-999999-deadbeef", noopSignal, noopSleep)
	if err == nil {
		t.Fatal("expected error for non-existent job, got nil")
	}
	if err.Error() != "err:not_found" {
		t.Errorf("expected exactly 'err:not_found', got: %q", err.Error())
	}
}

func TestKillOnNonRunningJobReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-a1b2c3d4"

	// Seed: kill_not_running.json - status "done"
	makeJob(t, root, jobID, "done")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil {
		t.Fatal("expected error for non-running job, got nil")
	}
	if !strings.Contains(err.Error(), "err:user") {
		t.Errorf("expected err:user, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("expected 'Job is not running' in error, got: %q", err.Error())
	}
}

func TestKillRejectsNonRunningStatusDone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-done0001"
	makeJob(t, root, jobID, "done")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil || !strings.Contains(err.Error(), "err:user") {
		t.Errorf("done: expected err:user, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("done: expected 'Job is not running', got: %q", err.Error())
	}
}

func TestKillRejectsNonRunningStatusFailed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-fail0001"
	makeJob(t, root, jobID, "failed")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil || !strings.Contains(err.Error(), "err:user") {
		t.Errorf("failed: expected err:user, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("failed: expected 'Job is not running', got: %q", err.Error())
	}
}

func TestKillRejectsNonRunningStatusTimeout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-time0001"
	makeJob(t, root, jobID, "timeout")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil || !strings.Contains(err.Error(), "err:user") {
		t.Errorf("timeout: expected err:user, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("timeout: expected 'Job is not running', got: %q", err.Error())
	}
}

func TestKillRejectsNonRunningStatusKilled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-kill0001"
	makeJob(t, root, jobID, "killed")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil || !strings.Contains(err.Error(), "err:user") {
		t.Errorf("killed: expected err:user, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("killed: expected 'Job is not running', got: %q", err.Error())
	}
}

func TestKillRejectsNonRunningStatusQueued(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-100000-queu0001"
	makeJob(t, root, jobID, "queued")

	err := cmd.KillCmd(root, "", jobID, noopSignal, noopSleep)
	if err == nil || !strings.Contains(err.Error(), "err:user") {
		t.Errorf("queued: expected err:user, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Job is not running") {
		t.Errorf("queued: expected 'Job is not running', got: %q", err.Error())
	}
}

// ---------- AC14: Kill exit code ----------

func TestKillReturnsExitCode0OnSuccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-valid001"

	dir := makeJob(t, root, jobID, "running")
	makePidFile(t, dir, 51203)

	err := cmd.KillCmd(root, "", jobID, errSignal, noopSleep)
	if err != nil {
		t.Errorf("KillCmd returned error on success: %v", err)
	}
}

// ---------- Edge Cases ----------

func TestKillOnJobWhoseProcessAlreadyDied(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-dead0001"

	dir := makeJob(t, root, jobID, "running")
	makePidFile(t, dir, 51203)

	// Signal always fails - process already dead
	if err := cmd.KillCmd(root, "", jobID, errSignal, noopSleep); err != nil {
		t.Errorf("KillCmd on already-dead process returned error: %v", err)
	}

	if got := readStatus(t, dir); got != "killed" {
		t.Errorf("expected status 'killed' after kill of dead process, got %q", got)
	}
}

func TestCleanWithNoJobsToClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	// Only active jobs
	makeJob(t, root, "job-20260227-103000-queu0001", "queued")
	makeJob(t, root, "job-20260227-101500-run00001", "running")

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, -1, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "Cleaned 0 jobs" {
		t.Errorf("expected 'Cleaned 0 jobs', got: %q", got)
	}
}

func TestListWithCorruptedJobDirsShowsUnknownStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := "job-20260227-101500-corrupt1"

	// Create job directory but do NOT write a status file
	dir := filepath.Join(root, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	var buf bytes.Buffer
	if err := cmd.ListCmd(root, &buf); err != nil {
		t.Fatalf("ListCmd error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, jobID) {
		t.Errorf("corrupted job %q not in output: %q", jobID, output)
	}
	if !strings.Contains(output, "unknown") {
		t.Errorf("expected status 'unknown' for corrupted job, output: %q", output)
	}
}

func TestCleanDays0RemovesAllJobsRegardlessOfAge(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()

	// Jobs of various ages and statuses
	newDir := makeJob(t, root, "job-20260227-090000-new00001", "running")
	oldDir := makeJob(t, root, "job-20260201-090000-old00001", "done")
	if err := os.Chtimes(newDir, now, now); err != nil {
		t.Fatalf("Chtimes new: %v", err)
	}
	oldTime := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	var buf bytes.Buffer
	if err := cmd.CleanCmd(root, 0, now, &buf); err != nil {
		t.Fatalf("CleanCmd error: %v", err)
	}

	// Both jobs must be gone
	for _, dir := range []string{newDir, oldDir} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("job %q should be removed by --days 0 but still exists", dir)
		}
	}
	if !strings.Contains(buf.String(), "Cleaned 2 jobs") {
		t.Errorf("expected 'Cleaned 2 jobs', got: %q", buf.String())
	}
}
