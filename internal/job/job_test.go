package job

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/event"
)

// featureSeedBase is the path to the job package test fixtures.
const featureSeedBase = "testdata"

func seedPath(parts ...string) string {
	return filepath.Join(append([]string{featureSeedBase}, parts...)...)
}

// copySeedDir copies the entire seedName directory from the feature seed base
// into a fresh temporary directory and returns the destination path.
func copySeedDir(t *testing.T, seedName string) string {
	t.Helper()
	src := seedPath(seedName)
	dst := filepath.Join(t.TempDir(), seedName)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("copySeedDir: mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("copySeedDir: readdir %s: %v", src, err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("copySeedDir: read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("copySeedDir: write %s: %v", e.Name(), err)
		}
	}
	return dst
}

// assertFileContains asserts that the file at path has exactly the given content.
func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("assertFileContains: read %s: %v", path, err)
	}
	got := string(data)
	if got != want {
		t.Errorf("file %s: got %q, want %q", filepath.Base(path), got, want)
	}
}

// assertFileExists asserts that the given file path exists.
func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s to exist: %v", filepath.Base(path), err)
	}
}

// assertFileNotExists asserts that the given file path does NOT exist.
func assertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected file %s to NOT exist, but it does", filepath.Base(path))
	}
}

// ---------------------------------------------------------------------------
// Job ID validation (path traversal prevention)
// ---------------------------------------------------------------------------

// TestValidateJobIDAcceptsValidIDs covers valid job IDs with lowercase
// alphanumeric, hyphens, and underscores.
func TestValidateJobIDAcceptsValidIDs(t *testing.T) {
	validIDs := []string{
		"job-20260227-143205-a8f3b1c2",
		"job_test",
		"abc123",
		"a",
		"my-job-id",
		"job_with_underscores",
	}
	for _, id := range validIDs {
		if err := ValidateJobID(id); err != nil {
			t.Errorf("ValidateJobID(%q) returned error: %v", id, err)
		}
	}
}

// TestValidateJobIDRejectsPathTraversal ensures IDs with ".." are rejected.
func TestValidateJobIDRejectsPathTraversal(t *testing.T) {
	err := ValidateJobID("../../etc")
	if err == nil {
		t.Fatal("expected error for path traversal ID, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// TestValidateJobIDRejectsDots ensures IDs containing dots are rejected.
func TestValidateJobIDRejectsDots(t *testing.T) {
	err := ValidateJobID("job..foo")
	if err == nil {
		t.Fatal("expected error for dotted ID, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// TestValidateJobIDRejectsSlash ensures IDs containing slashes are rejected.
func TestValidateJobIDRejectsSlash(t *testing.T) {
	err := ValidateJobID("job/foo")
	if err == nil {
		t.Fatal("expected error for slash ID, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// TestValidateJobIDRejectsEmpty ensures empty IDs are rejected.
func TestValidateJobIDRejectsEmpty(t *testing.T) {
	err := ValidateJobID("")
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// TestNewJobRejectsUnsafeJobID ensures NewJob returns an error for unsafe IDs.
func TestNewJobRejectsUnsafeJobID(t *testing.T) {
	root := t.TempDir()
	_, err := NewJob(root, "project-123", "../escape")
	if err == nil {
		t.Fatal("expected error for unsafe job ID in NewJob, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// TestFindJobDirRejectsUnsafeJobID ensures FindJobDir returns an error for unsafe IDs.
func TestFindJobDirRejectsUnsafeJobID(t *testing.T) {
	root := t.TempDir()
	_, err := FindJobDir(root, "project-123", "../escape")
	if err == nil {
		t.Fatal("expected error for unsafe job ID in FindJobDir, got nil")
	}
	if !strings.Contains(err.Error(), "err:validation") {
		t.Errorf("expected err:validation prefix, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC1: Job ID generation
// ---------------------------------------------------------------------------

// TestGenerateJobIDInCorrectFormat covers:
//   Scenario: Generate job ID in correct format
func TestGenerateJobIDInCorrectFormat(t *testing.T) {
	id := GenerateJobID()

	pattern := regexp.MustCompile(`^job-\d{8}-\d{6}-[0-9a-f]{8}$`)
	if !pattern.MatchString(id) {
		t.Errorf("job ID %q does not match pattern job-YYYYMMDD-HHMMSS-XXXXXXXX", id)
	}

	// Timestamp portion reflects current time (allow ±5 seconds).
	now := time.Now().UTC()
	datePart := now.Format("20060102")
	if !strings.Contains(id, "job-"+datePart) {
		t.Errorf("job ID %q does not contain today's date %s", id, datePart)
	}

	// Hex suffix is exactly 8 hex characters.
	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 dash-separated parts, got %d in %q", len(parts), id)
	}
	hexSuffix := parts[3]
	if len(hexSuffix) != 8 {
		t.Errorf("hex suffix %q should be 8 characters, got %d", hexSuffix, len(hexSuffix))
	}
	hexPattern := regexp.MustCompile(`^[0-9a-f]{8}$`)
	if !hexPattern.MatchString(hexSuffix) {
		t.Errorf("hex suffix %q is not lowercase hex", hexSuffix)
	}
}

// TestTwoJobsInSameSecondHaveUniqueIDs covers:
//   Scenario: Two jobs created in the same second have unique IDs
func TestTwoJobsInSameSecondHaveUniqueIDs(t *testing.T) {
	id1 := GenerateJobID()
	id2 := GenerateJobID()

	if id1 == id2 {
		t.Errorf("expected unique IDs, got identical: %q", id1)
	}

	// Both share the same timestamp prefix (first three dash-separated segments).
	prefix := func(id string) string {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return id
		}
		return strings.Join(parts[:3], "-")
	}

	p1, p2 := prefix(id1), prefix(id2)
	if p1 != p2 {
		t.Errorf("expected same timestamp prefix, got %q vs %q", p1, p2)
	}

	// The random hex suffixes must differ.
	suffix := func(id string) string {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return ""
		}
		return parts[3]
	}
	if suffix(id1) == suffix(id2) {
		t.Errorf("expected different hex suffixes, got same: %q", suffix(id1))
	}
}

// ---------------------------------------------------------------------------
// AC2: Project ID resolution
// ---------------------------------------------------------------------------

// TestResolveProjectIDForGitRepository covers:
//   Scenario: Resolve project ID for a git repository
func TestResolveProjectIDForGitRepository(t *testing.T) {
	absPath := "/home/veschin/work/my-express-app"
	projectID := ResolveProjectID(absPath)

	wantBase := "my-express-app"
	wantCksum := crc32.ChecksumIEEE([]byte(absPath))
	want := fmt.Sprintf("%s-%d", wantBase, wantCksum)

	if projectID != want {
		t.Errorf("ResolveProjectID(%q) = %q, want %q", absPath, projectID, want)
	}

	// Format must be {basename}-{cksum}. The cksum is a decimal suffix after the
	// last '-'. Everything before the last '-' is the basename.
	lastDash := strings.LastIndex(projectID, "-")
	if lastDash < 0 {
		t.Fatalf("project ID %q does not contain '-'", projectID)
	}
	if projectID[:lastDash] != wantBase {
		t.Errorf("basename part: got %q, want %q", projectID[:lastDash], wantBase)
	}
}

// TestResolveProjectIDForNonGitDirectory covers:
//   Scenario: Resolve project ID for a non-git directory
func TestResolveProjectIDForNonGitDirectory(t *testing.T) {
	absPath := "/tmp/scratch-work"
	projectID := ResolveProjectID(absPath)

	wantBase := "scratch-work"
	wantCksum := crc32.ChecksumIEEE([]byte(absPath))
	want := fmt.Sprintf("%s-%d", wantBase, wantCksum)

	if projectID != want {
		t.Errorf("ResolveProjectID(%q) = %q, want %q", absPath, projectID, want)
	}

	// Format must be {basename}-{cksum}.
	lastDash := strings.LastIndex(projectID, "-")
	if lastDash < 0 {
		t.Fatalf("project ID %q does not contain '-'", projectID)
	}
	if projectID[:lastDash] != wantBase {
		t.Errorf("basename part: got %q, want %q", projectID[:lastDash], wantBase)
	}
}

// ---------------------------------------------------------------------------
// AC3: Job directory creation
// ---------------------------------------------------------------------------

// TestCreateJobDirectoryWithInitialStatus covers:
//   Scenario: Create job directory with initial status
func TestCreateJobDirectoryWithInitialStatus(t *testing.T) {
	root := t.TempDir()
	projectID := "my-express-app-1234567890"
	jobID := "job-20260227-143205-a8f3b1c2"

	j, err := NewJob(root, projectID, jobID)
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	expectedDir := filepath.Join(root, projectID, jobID)
	if j.Dir != expectedDir {
		t.Errorf("Job.Dir = %q, want %q", j.Dir, expectedDir)
	}

	info, err := os.Stat(expectedDir)
	if err != nil {
		t.Fatalf("job directory %s does not exist: %v", expectedDir, err)
	}
	if !info.IsDir() {
		t.Errorf("expected %s to be a directory", expectedDir)
	}

	assertFileContains(t, filepath.Join(expectedDir, "status"), "queued")
}

// ---------------------------------------------------------------------------
// AC4: Status state machine
// ---------------------------------------------------------------------------

// TestTransitionFromQueuedToRunning covers:
//   Scenario: Transition from queued to running
func TestTransitionFromQueuedToRunning(t *testing.T) {
	dir := copySeedDir(t, "job_queued")
	j := &Job{ID: "job_queued", Dir: dir}

	if err := j.StatusTransition(StatusRunning); err != nil {
		t.Fatalf("StatusTransition queued->running: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "running")
}

// TestTransitionFromRunningToDone covers:
//   Scenario: Transition from running to done
func TestTransitionFromRunningToDone(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	if err := j.StatusTransition(StatusDone); err != nil {
		t.Fatalf("StatusTransition running->done: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "done")
}

// TestTransitionFromRunningToFailed covers:
//   Scenario: Transition from running to failed
func TestTransitionFromRunningToFailed(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	if err := j.StatusTransition(StatusFailed); err != nil {
		t.Fatalf("StatusTransition running->failed: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "failed")
}

// TestTransitionFromRunningToTimeout covers:
//   Scenario: Transition from running to timeout
func TestTransitionFromRunningToTimeout(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	if err := j.StatusTransition(StatusTimeout); err != nil {
		t.Fatalf("StatusTransition running->timeout: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "timeout")
}

// TestTransitionFromRunningToKilled covers:
//   Scenario: Transition from running to killed
func TestTransitionFromRunningToKilled(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	if err := j.StatusTransition(StatusKilled); err != nil {
		t.Fatalf("StatusTransition running->killed: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "killed")
}

// TestTransitionFromRunningToPermissionError covers:
//   Scenario: Transition from running to permission_error
func TestTransitionFromRunningToPermissionError(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	if err := j.StatusTransition(StatusPermissionError); err != nil {
		t.Fatalf("StatusTransition running->permission_error: %v", err)
	}

	assertFileContains(t, filepath.Join(dir, "status"), "permission_error")
}

// ---------------------------------------------------------------------------
// AC5: Atomic file writes
// ---------------------------------------------------------------------------

// TestStatusFileIsWrittenAtomically covers:
//   Scenario: Status file is written atomically
func TestStatusFileIsWrittenAtomically(t *testing.T) {
	dir := copySeedDir(t, "job_running")
	j := &Job{ID: "job_running", Dir: dir}

	statusPath := filepath.Join(dir, "status")
	expectedTmp := fmt.Sprintf("%s.tmp.%d", statusPath, os.Getpid())

	// After the transition completes the temp file must be gone (renamed away).
	if err := j.StatusTransition(StatusDone); err != nil {
		t.Fatalf("StatusTransition running->done: %v", err)
	}

	// The final status file must exist with the correct value.
	assertFileContains(t, statusPath, "done")

	// The temporary file must not remain on disk after a successful rename.
	if _, err := os.Stat(expectedTmp); err == nil {
		t.Errorf("temporary file %s still exists after atomic write", expectedTmp)
	}
}

// ---------------------------------------------------------------------------
// AC6: Job artifacts
// ---------------------------------------------------------------------------

// TestCompletedJobHasAllExpectedArtifacts covers:
//   Scenario: Completed job has all expected artifacts
func TestCompletedJobHasAllExpectedArtifacts(t *testing.T) {
	dir := copySeedDir(t, "job_done")

	assertFileContains(t, filepath.Join(dir, "status"), "done")
	assertFileContains(t, filepath.Join(dir, "pid.txt"), "48721")
	assertFileExists(t, filepath.Join(dir, "prompt.txt"))
	assertFileContains(t, filepath.Join(dir, "workdir.txt"), "/home/veschin/work/my-express-app")
	assertFileContains(t, filepath.Join(dir, "permission_mode.txt"), "bypassPermissions")
	assertFileContains(t, filepath.Join(dir, "model.txt"), "opus=glm-5 sonnet=glm-5 haiku=glm-5")
	assertFileContains(t, filepath.Join(dir, "started_at.txt"), "2026-02-27T14:32:05+03:00")
	assertFileContains(t, filepath.Join(dir, "finished_at.txt"), "2026-02-27T14:33:47+03:00")
	assertFileExists(t, filepath.Join(dir, "raw.json"))
	assertFileExists(t, filepath.Join(dir, "stdout.txt"))
	assertFileExists(t, filepath.Join(dir, "stderr.txt"))
	assertFileExists(t, filepath.Join(dir, "changelog.txt"))
}

// TestRunningJobHasPartialArtifacts covers:
//   Scenario: Running job has partial artifacts
func TestRunningJobHasPartialArtifacts(t *testing.T) {
	dir := copySeedDir(t, "job_running")

	assertFileContains(t, filepath.Join(dir, "status"), "running")
	assertFileContains(t, filepath.Join(dir, "pid.txt"), "51203")
	assertFileExists(t, filepath.Join(dir, "prompt.txt"))
	assertFileExists(t, filepath.Join(dir, "started_at.txt"))
	assertFileNotExists(t, filepath.Join(dir, "finished_at.txt"))
	assertFileNotExists(t, filepath.Join(dir, "raw.json"))
	assertFileNotExists(t, filepath.Join(dir, "stdout.txt"))
}

// TestQueuedJobHasMinimalArtifacts covers:
//   Scenario: Queued job has minimal artifacts
func TestQueuedJobHasMinimalArtifacts(t *testing.T) {
	dir := copySeedDir(t, "job_queued")

	assertFileContains(t, filepath.Join(dir, "status"), "queued")
	assertFileExists(t, filepath.Join(dir, "prompt.txt"))
	assertFileNotExists(t, filepath.Join(dir, "pid.txt"))
	assertFileNotExists(t, filepath.Join(dir, "started_at.txt"))
}

// TestFailedJobHasExitCodeAndStderr covers:
//   Scenario: Failed job has exit code and stderr
func TestFailedJobHasExitCodeAndStderr(t *testing.T) {
	dir := copySeedDir(t, "job_failed")

	assertFileContains(t, filepath.Join(dir, "status"), "failed")
	assertFileContains(t, filepath.Join(dir, "exit_code.txt"), "1")
	assertFileExists(t, filepath.Join(dir, "stderr.txt"))
}

// TestTimedOutJobHasStderrWithTimeoutMessage covers:
//   Scenario: Timed out job has stderr with timeout message
func TestTimedOutJobHasStderrWithTimeoutMessage(t *testing.T) {
	dir := copySeedDir(t, "job_timeout")

	assertFileContains(t, filepath.Join(dir, "status"), "timeout")
	assertFileContains(t, filepath.Join(dir, "stderr.txt"), "[GoLeM] Job exceeded 3000s timeout")
}

// TestKilledJobHasStderrWithKillMessage covers:
//   Scenario: Killed job has stderr with kill message
func TestKilledJobHasStderrWithKillMessage(t *testing.T) {
	dir := copySeedDir(t, "job_killed")

	assertFileContains(t, filepath.Join(dir, "status"), "killed")
	assertFileContains(t, filepath.Join(dir, "stderr.txt"), "Killed by user")
}

// TestPermissionErrorJobHasStderrWithPermissionMessage covers:
//   Scenario: Permission error job has stderr with permission message
func TestPermissionErrorJobHasStderrWithPermissionMessage(t *testing.T) {
	dir := copySeedDir(t, "job_permission_error")

	assertFileContains(t, filepath.Join(dir, "status"), "permission_error")
	assertFileContains(t, filepath.Join(dir, "stderr.txt"), "Error: not allowed to execute bash commands")
}

// ---------------------------------------------------------------------------
// AC7: find_job_dir
// ---------------------------------------------------------------------------

// TestFindJobInCurrentProjectDirectory covers:
//   Scenario: Find job in current project directory
func TestFindJobInCurrentProjectDirectory(t *testing.T) {
	root := t.TempDir()
	projectID := "my-express-app-1234567890"
	jobID := "job-20260227-143205-a8f3b1c2"

	jobDir := filepath.Join(root, projectID, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := FindJobDir(root, projectID, jobID)
	if err != nil {
		t.Fatalf("FindJobDir: %v", err)
	}
	if got != jobDir {
		t.Errorf("FindJobDir = %q, want %q", got, jobDir)
	}
}

// TestFindJobInLegacyFlatDirectory covers:
//   Scenario: Find job in legacy flat directory
func TestFindJobInLegacyFlatDirectory(t *testing.T) {
	root := t.TempDir()
	jobID := "job-20260227-143205-a8f3b1c2"
	currentProject := "my-express-app-1234567890"

	// Place the job directly under root (legacy flat).
	jobDir := filepath.Join(root, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := FindJobDir(root, currentProject, jobID)
	if err != nil {
		t.Fatalf("FindJobDir: %v", err)
	}
	if got != jobDir {
		t.Errorf("FindJobDir = %q, want %q", got, jobDir)
	}
}

// TestFindJobAcrossAllProjectDirectories covers:
//   Scenario: Find job across all project directories
func TestFindJobAcrossAllProjectDirectories(t *testing.T) {
	root := t.TempDir()
	currentProject := "my-express-app-1234567890"
	otherProject := "other-project-9876543210"
	jobID := "job-20260227-143205-a8f3b1c2"

	// Job lives under another project.
	jobDir := filepath.Join(root, otherProject, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := FindJobDir(root, currentProject, jobID)
	if err != nil {
		t.Fatalf("FindJobDir: %v", err)
	}
	if got != jobDir {
		t.Errorf("FindJobDir = %q, want %q", got, jobDir)
	}
}

// TestJobNotFoundReturnsError covers:
//   Scenario: Job not found returns error
func TestJobNotFoundReturnsError(t *testing.T) {
	root := t.TempDir()

	_, err := FindJobDir(root, "my-express-app-1234567890", "job-20260227-999999-deadbeef")
	if err == nil {
		t.Fatal("expected err:not_found, got nil")
	}
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC8: Deleting a job
// ---------------------------------------------------------------------------

// TestDeleteJobRemovesEntireDirectory covers:
//   Scenario: Delete job removes entire directory
func TestDeleteJobRemovesEntireDirectory(t *testing.T) {
	dir := copySeedDir(t, "job_done")

	// Confirm directory exists before deletion.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("setup: job dir does not exist: %v", err)
	}

	if err := DeleteJob(dir); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	if _, err := os.Stat(dir); err == nil {
		t.Errorf("job directory %s still exists after DeleteJob", dir)
	}

	// Confirm artifact files are gone too.
	for _, artifact := range []string{"status", "pid.txt", "prompt.txt", "stdout.txt"} {
		if _, err := os.Stat(filepath.Join(dir, artifact)); err == nil {
			t.Errorf("artifact %s still exists after DeleteJob", artifact)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestCorruptedJobDirectoryWithMissingStatusFile covers:
//   Scenario: Corrupted job directory with missing status file
func TestCorruptedJobDirectoryWithMissingStatusFile(t *testing.T) {
	dir := copySeedDir(t, "job_corrupted")

	// Confirm there is no status file (the seed has only prompt.txt).
	if _, err := os.Stat(filepath.Join(dir, "status")); err == nil {
		t.Fatal("setup: status file should not exist in job_corrupted seed")
	}

	status := ReadStatus(dir)
	if status != StatusFailed {
		t.Errorf("ReadStatus with missing file = %q, want %q", status, StatusFailed)
	}
}

// TestStatusFileContainsUnexpectedValue covers:
//   Scenario: Status file contains unexpected value
func TestStatusFileContainsUnexpectedValue(t *testing.T) {
	dir := t.TempDir()

	// Write an invalid status value.
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("banana"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	status := ReadStatus(dir)
	if status != StatusFailed {
		t.Errorf("ReadStatus with unknown value = %q, want %q", status, StatusFailed)
	}
}

// TestNewJobsAlwaysUseProjectScopedDirectories covers:
//   Scenario: New jobs always use project-scoped directories
func TestNewJobsAlwaysUseProjectScopedDirectories(t *testing.T) {
	root := t.TempDir()
	projectID := "my-express-app-1234567890"
	// Use a fixed job ID so the flat-path assertion is deterministic even when
	// GenerateJobID is a stub. The real implementation must produce a valid ID
	// via GenerateJobID and place it under the project-scoped directory.
	jobID := "job-20260227-143205-a8f3b1c2"

	j, err := NewJob(root, projectID, jobID)
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	// Job directory must be under root/<projectID>/<jobID>.
	expectedDir := filepath.Join(root, projectID, jobID)
	if j.Dir != expectedDir {
		t.Errorf("Job.Dir = %q, want %q", j.Dir, expectedDir)
	}

	// The job must NOT be directly at root/<jobID>.
	flatDir := filepath.Join(root, jobID)
	if _, err := os.Stat(flatDir); err == nil {
		t.Errorf("job was created at flat path %s — must be project-scoped", flatDir)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: StatusTransition flock
// ---------------------------------------------------------------------------

// TestConcurrentStatusTransitionOnlyOneWins verifies that when multiple
// goroutines race to transition a job from queued->running, exactly one
// succeeds and the rest get an "invalid transition" error (because the job
// is already in "running" state by the time they acquire the lock).
func TestConcurrentStatusTransitionOnlyOneWins(t *testing.T) {
	dir := copySeedDir(t, "job_queued")
	const goroutines = 10
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		successes int
		failures  int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			j := &Job{ID: "job_queued", Dir: dir}
			err := j.StatusTransition(StatusRunning)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else {
				failures++
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 successful transition, got %d (failures: %d)", successes, failures)
	}
	if failures != goroutines-1 {
		t.Errorf("expected %d failures, got %d", goroutines-1, failures)
	}
	assertFileContains(t, filepath.Join(dir, "status"), "running")
}

// ---------------------------------------------------------------------------
// Event producer tests (P2.1)
// ---------------------------------------------------------------------------

// TestEmitQueued_PublishesEvent verifies that EmitQueued publishes a JobQueued
// event with the correct JobID and Data payload.
func TestEmitQueued_PublishesEvent(t *testing.T) {
	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.JobQueued)

	dir := t.TempDir()
	j, err := NewJob(dir, "test-project", "job-test-001")
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	j.SetBus(bus)
	j.EmitQueued()

	select {
	case e := <-ch:
		if e.Type != event.JobQueued {
			t.Errorf("type = %v, want %v", e.Type, event.JobQueued)
		}
		if e.JobID != "job-test-001" {
			t.Errorf("job_id = %v, want job-test-001", e.JobID)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data is not map[string]any: %T", e.Data)
		}
		if data["project_id"] != "test-project" {
			t.Errorf("project_id = %v, want test-project", data["project_id"])
		}
		if data["job_id"] != "job-test-001" {
			t.Errorf("data job_id = %v, want job-test-001", data["job_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JobQueued event")
	}
}

// TestEmitQueued_NilBus_NoPanic verifies that EmitQueued does not panic when
// Bus is nil (the default zero value).
func TestEmitQueued_NilBus_NoPanic(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJob(dir, "test-project", "job-test-002")
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	// Bus is nil (default). Must not panic.
	j.EmitQueued()
}

// TestEmitQueued_NoBusSet_NoPanic verifies that EmitQueued does not panic when
// SetBus was never called.
func TestEmitQueued_NoBusSet_NoPanic(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJob(dir, "test-project", "job-test-003")
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	// No SetBus called. Must not panic.
	j.EmitQueued()
}

// TestSetBus_Job_ReturnsReceiverForChaining verifies that SetBus returns the
// Job receiver for method chaining.
func TestSetBus_Job_ReturnsReceiverForChaining(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJob(dir, "test-project", "job-test-004")
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	bus := event.NewBus()
	defer bus.Close()

	got := j.SetBus(bus)
	if got != j {
		t.Error("SetBus did not return the receiver")
	}
}
