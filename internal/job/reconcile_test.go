package job

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeJob creates a minimal job directory inside base and returns its path.
// status, pid (0 = omit pid.txt), createdAt ("" = omit created_at.txt),
// and staleRecovered control which files are written.
func makeJob(t *testing.T, base, jobID, status string, pid int, createdAt string, staleRecovered bool) string {
	t.Helper()
	dir := filepath.Join(base, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeJob MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(dir, "status"), status)
	if pid > 0 {
		writeFile(t, filepath.Join(dir, "pid.txt"), strconv.Itoa(pid))
	}
	if createdAt != "" {
		writeFile(t, filepath.Join(dir, "created_at.txt"), createdAt)
	}
	if staleRecovered {
		appendToFile(t, filepath.Join(dir, "stderr.txt"), staleRecoveredMarker+"\n")
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

func appendToFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("appendToFile open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("appendToFile write %s: %v", path, err)
	}
}

func readFileContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFileContent %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func writeSlotCounterFile(t *testing.T, counterPath string, n int) {
	t.Helper()
	writeFile(t, counterPath, strconv.Itoa(n))
}

// selfPID returns the current process PID - guaranteed to be alive.
func selfPID() int { return os.Getpid() }

// deadPID returns a PID that is guaranteed to be dead - near pid_max.
func deadPID() int { return 4194200 }

// ---------------------------------------------------------------------------
// AC1: Reconciliation runs once at startup
// ---------------------------------------------------------------------------

// TestReconciliationRunsOnceAtStartup verifies that Reconcile processes all
// running jobs, marks dead-PID jobs as "failed", and leaves alive jobs intact.
// TestReconcileContinuesPastJobThatErrors guards against one malformed job
// aborting the entire reconciliation sweep. The first job (sorted earlier) is
// queued with no created_at.txt, which makes IsStaleQueued error; the second
// is a running job with a dead PID that must still be reconciled to "failed".
func TestReconcileContinuesPastJobThatErrors(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	makeJob(t, base, "job-20260101-000000-aaaaaaaa", "queued", 0, "", false)
	jobB := makeJob(t, base, "job-20260101-000001-bbbbbbbb", "running", 999999999, "", false)

	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile must not abort on a single bad job: %v", err)
	}
	if got := readStatus(jobB); got != "failed" {
		t.Errorf("job b status: got %q, want %q (a bad sibling aborted the sweep)", got, "failed")
	}
}

func TestReconciliationRunsOnceAtStartup(t *testing.T) {
	base := t.TempDir()
	counterPath := filepath.Join(base, ".running_count")
	writeSlotCounterFile(t, counterPath, 2)

	deadDir := makeJob(t, base, "job-20260227-080000-dead1234", "running", deadPID(), "", false)
	aliveDir := makeJob(t, base, "job-20260227-080100-alive567", "running", selfPID(), "", false)

	now := time.Date(2026, 2, 27, 8, 5, 0, 0, time.UTC)
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	// dead PID job must become "failed"
	if got := readFileContent(t, filepath.Join(deadDir, "status")); got != "failed" {
		t.Errorf("dead job status = %q, want %q", got, "failed")
	}

	// alive PID job must remain "running"
	if got := readFileContent(t, filepath.Join(aliveDir, "status")); got != "running" {
		t.Errorf("alive job status = %q, want %q", got, "running")
	}
}

// ---------------------------------------------------------------------------
// AC2: Running job with dead PID is detected as stale
// ---------------------------------------------------------------------------

// TestRunningJobWithDeadPIDIsDetectedAsStale confirms that a job whose
// pid.txt points to a non-existent PID is transitioned to "failed".
func TestRunningJobWithDeadPIDIsDetectedAsStale(t *testing.T) {
	base := t.TempDir()
	jobDir := makeJob(t, base, "job-20260227-080000-dead1234", "running", deadPID(), "", false)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "failed" {
		t.Errorf("status = %q, want %q", got, "failed")
	}
}

// ---------------------------------------------------------------------------
// AC2: Running job with missing pid.txt is detected as stale
// ---------------------------------------------------------------------------

// TestRunningJobWithMissingPidTxtIsDetectedAsStale confirms that a "running"
// job without a pid.txt file is transitioned to "failed".
func TestRunningJobWithMissingPidTxtIsDetectedAsStale(t *testing.T) {
	base := t.TempDir()
	// pid=0 means makeJob does NOT write pid.txt
	jobDir := makeJob(t, base, "job-20260227-090000-nopid000", "running", 0, "", false)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "failed" {
		t.Errorf("status = %q, want %q", got, "failed")
	}
}

// ---------------------------------------------------------------------------
// AC2: Queued job stuck for over 5 minutes is detected as stale
// ---------------------------------------------------------------------------

// TestQueuedJobStuckOver5MinutesIsDetectedAsStale confirms that a "queued"
// job whose created_at is more than 5 minutes in the past is failed.
func TestQueuedJobStuckOver5MinutesIsDetectedAsStale(t *testing.T) {
	base := t.TempDir()
	createdAt := "2026-02-27T07:00:00+03:00"
	jobDir := makeJob(t, base, "job-20260227-070000-stuck890", "queued", 0, createdAt, false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:10:00+03:00")
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "failed" {
		t.Errorf("status = %q, want %q", got, "failed")
	}
}

// ---------------------------------------------------------------------------
// AC2: Queued job under 5 minutes is NOT stale
// ---------------------------------------------------------------------------

// TestQueuedJobUnder5MinutesIsNotStale confirms that a recently-queued job
// is left in "queued" status after reconciliation.
func TestQueuedJobUnder5MinutesIsNotStale(t *testing.T) {
	base := t.TempDir()
	createdAt := "2026-02-27T07:30:00+03:00"
	jobDir := makeJob(t, base, "job-20260227-073000-fresh000", "queued", 0, createdAt, false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:33:00+03:00")
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "queued" {
		t.Errorf("status = %q, want %q", got, "queued")
	}
}

// ---------------------------------------------------------------------------
// AC3: Dead PID job gets stderr annotation
// ---------------------------------------------------------------------------

// TestDeadPIDJobGetsStderrAnnotation verifies that reconciliation appends the
// correct message to stderr.txt when a running job has a dead PID.
func TestDeadPIDJobGetsStderrAnnotation(t *testing.T) {
	base := t.TempDir()
	jobDir := makeJob(t, base, "job-20260227-080000-dead1234", "running", deadPID(), "", false)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(jobDir, "stderr.txt"))
	if err != nil {
		t.Fatalf("stderr.txt not created: %v", err)
	}
	want := "[GoLeM] Process died unexpectedly (PID " + strconv.Itoa(deadPID()) + ")"
	if !strings.Contains(string(data), want) {
		t.Errorf("stderr.txt = %q, want to contain %q", string(data), want)
	}
}

// ---------------------------------------------------------------------------
// AC4: Stuck queued job gets stderr annotation
// ---------------------------------------------------------------------------

// TestStuckQueuedJobGetsStderrAnnotation verifies that reconciliation appends
// the correct stuck-queue message to stderr.txt.
func TestStuckQueuedJobGetsStderrAnnotation(t *testing.T) {
	base := t.TempDir()
	createdAt := "2026-02-27T07:00:00+03:00"
	jobDir := makeJob(t, base, "job-20260227-070000-stuck890", "queued", 0, createdAt, false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:10:00+03:00")
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(jobDir, "stderr.txt"))
	if err != nil {
		t.Fatalf("stderr.txt not created: %v", err)
	}
	want := "[GoLeM] Job stuck in queue for over 5 minutes"
	if !strings.Contains(string(data), want) {
		t.Errorf("stderr.txt = %q, want to contain %q", string(data), want)
	}
}

// ---------------------------------------------------------------------------
// AC5: Slot counter is reset to count of actually running jobs
// ---------------------------------------------------------------------------

// TestSlotCounterIsResetAfterReconciliation verifies that after reconciliation
// the counter file reflects only truly-alive running jobs.
func TestSlotCounterIsResetAfterReconciliation(t *testing.T) {
	base := t.TempDir()
	counterPath := filepath.Join(base, ".running_count")
	writeSlotCounterFile(t, counterPath, 5)

	makeJob(t, base, "job-20260227-080000-dead1234", "running", deadPID(), "", false)
	makeJob(t, base, "job-20260227-080100-alive567", "running", selfPID(), "", false)
	makeJob(t, base, "job-20260227-070000-stuck890", "queued", 0, "2026-02-27T07:00:00+03:00", false)
	makeJob(t, base, "job-20260227-090000-nopid000", "running", 0, "", false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:10:00+03:00")
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := readSlotCounter(counterPath)
	if got != 1 {
		t.Errorf("slot counter = %d, want 1", got)
	}
}

// TestAllStaleJobsResultInCounterResetToZero verifies that when all running
// jobs are stale the counter is written as 0.
func TestAllStaleJobsResultInCounterResetToZero(t *testing.T) {
	base := t.TempDir()
	counterPath := filepath.Join(base, ".running_count")
	writeSlotCounterFile(t, counterPath, 3)

	makeJob(t, base, "job-20260227-080000-stale1", "running", deadPID(), "", false)
	makeJob(t, base, "job-20260227-080001-stale2", "running", deadPID(), "", false)
	makeJob(t, base, "job-20260227-080002-stale3", "running", deadPID(), "", false)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := readSlotCounter(counterPath)
	if got != 0 {
		t.Errorf("slot counter = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// AC6: Per-command single-job PID check (CheckJobPID)
// ---------------------------------------------------------------------------

// TestStatusCommandChecksIndividualJobPIDWithoutFullReconciliation verifies
// that CheckJobPID on a dead-PID job returns "failed" and updates the status
// file without running a full reconciliation over all jobs.
func TestStatusCommandChecksIndividualJobPIDWithoutFullReconciliation(t *testing.T) {
	base := t.TempDir()
	// Second job that should NOT be touched by CheckJobPID.
	untouchedDir := makeJob(t, base, "job-unrelated-should-not-change", "running", deadPID(), "", false)

	jobDir := makeJob(t, base, "job-20260227-080000-dead1234", "running", deadPID(), "", false)

	status, err := CheckJobPID(jobDir)
	if err != nil {
		t.Fatalf("CheckJobPID: %v", err)
	}
	if status != "failed" {
		t.Errorf("CheckJobPID returned %q, want %q", status, "failed")
	}
	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "failed" {
		t.Errorf("status file = %q, want %q", got, "failed")
	}

	// The unrelated job's status file must not have been touched.
	if got := readFileContent(t, filepath.Join(untouchedDir, "status")); got != "running" {
		t.Errorf("unrelated job status = %q, want %q (CheckJobPID must not reconcile all jobs)", got, "running")
	}
}

// TestStatusCommandReturnsRunningForAlivePID verifies that CheckJobPID returns
// "running" and leaves the status file unchanged when the PID is alive.
func TestStatusCommandReturnsRunningForAlivePID(t *testing.T) {
	base := t.TempDir()
	jobDir := makeJob(t, base, "job-20260227-080100-alive567", "running", selfPID(), "", false)

	status, err := CheckJobPID(jobDir)
	if err != nil {
		t.Fatalf("CheckJobPID: %v", err)
	}
	if status != "running" {
		t.Errorf("CheckJobPID returned %q, want %q", status, "running")
	}
	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "running" {
		t.Errorf("status file = %q, want %q", got, "running")
	}
}

// ---------------------------------------------------------------------------
// AC7: glm clean --stale removes only auto-recovered jobs
// ---------------------------------------------------------------------------

// TestCleanStaleRemovesOnlyAutoRecoveredJobs verifies that a job marked with
// the stale-recovered marker is removed by CleanStale, while a job that was
// failed via glm kill is preserved.
func TestCleanStaleRemovesOnlyAutoRecoveredJobs(t *testing.T) {
	base := t.TempDir()
	// Auto-recovered job: status=failed + staleRecoveredMarker in stderr.
	autoRecoveredDir := makeJob(t, base, "job-20260227-080000-dead1234", "failed", 0, "", true)
	// Manually killed job: status=failed but NO staleRecoveredMarker.
	killedDir := makeJob(t, base, "job-20260227-100000-killed00", "failed", 0, "", false)
	writeFile(t, filepath.Join(killedDir, "stderr.txt"), "Killed by user")

	if err := CleanStale(base); err != nil {
		t.Fatalf("CleanStale: %v", err)
	}

	// Auto-recovered dir must be gone.
	if _, err := os.Stat(autoRecoveredDir); !os.IsNotExist(err) {
		t.Errorf("auto-recovered job dir still exists after CleanStale")
	}

	// Manually killed dir must be preserved.
	if _, err := os.Stat(killedDir); err != nil {
		t.Errorf("killed job dir was removed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestNoJobsExistMakesReconciliationANoOp confirms that Reconcile on an empty
// directory does not error and leaves the counter at 0.
func TestNoJobsExistMakesReconciliationANoOp(t *testing.T) {
	base := t.TempDir()
	counterPath := filepath.Join(base, ".running_count")
	writeSlotCounterFile(t, counterPath, 0)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile on empty dir: %v", err)
	}

	got := readSlotCounter(counterPath)
	if got != 0 {
		t.Errorf("slot counter = %d, want 0", got)
	}
}

// TestPidTxtContainsNonNumericValueTreatedAsDeadPID confirms that a job whose
// pid.txt is non-numeric is treated as stale and failed.
func TestPidTxtContainsNonNumericValueTreatedAsDeadPID(t *testing.T) {
	base := t.TempDir()
	jobDir := filepath.Join(base, "job-20260227-090000-badinput")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(jobDir, "status"), "running")
	writeFile(t, filepath.Join(jobDir, "pid.txt"), "not_a_number")

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "failed" {
		t.Errorf("status = %q, want %q", got, "failed")
	}
}

// TestPIDReuseByOSAcceptedAsFalsePositive confirms that a job whose PID is
// alive (even if it's a different process) remains "running" - this is an
// accepted startup-only false positive.
func TestPIDReuseByOSAcceptedAsFalsePositive(t *testing.T) {
	base := t.TempDir()
	// selfPID() is alive and definitely a different process than "claude".
	jobDir := makeJob(t, base, "job-20260227-080100-reuse000", "running", selfPID(), "", false)

	now := time.Now()
	if err := Reconcile(base, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := readFileContent(t, filepath.Join(jobDir, "status")); got != "running" {
		t.Errorf("status = %q, want %q (alive PID should remain running)", got, "running")
	}
}

// ---------------------------------------------------------------------------
// IsStaleQueued unit tests
// ---------------------------------------------------------------------------

// TestIsStaleQueuedReturnsTrueWhenOver5Minutes checks the helper directly.
func TestIsStaleQueuedReturnsTrueWhenOver5Minutes(t *testing.T) {
	base := t.TempDir()
	createdAt := "2026-02-27T07:00:00+03:00"
	jobDir := makeJob(t, base, "job-stuck", "queued", 0, createdAt, false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:10:00+03:00")
	stale, err := IsStaleQueued(jobDir, now)
	if err != nil {
		t.Fatalf("IsStaleQueued: %v", err)
	}
	if !stale {
		t.Errorf("IsStaleQueued = false, want true (job is 10 min old)")
	}
}

// TestIsStaleQueuedReturnsFalseWhenUnder5Minutes checks the helper directly.
func TestIsStaleQueuedReturnsFalseWhenUnder5Minutes(t *testing.T) {
	base := t.TempDir()
	createdAt := "2026-02-27T07:30:00+03:00"
	jobDir := makeJob(t, base, "job-fresh", "queued", 0, createdAt, false)

	now, _ := time.Parse(time.RFC3339, "2026-02-27T07:33:00+03:00")
	stale, err := IsStaleQueued(jobDir, now)
	if err != nil {
		t.Fatalf("IsStaleQueued: %v", err)
	}
	if stale {
		t.Errorf("IsStaleQueued = true, want false (job is only 3 min old)")
	}
}

// ---------------------------------------------------------------------------
// Concurrency: Reconcile flock
// ---------------------------------------------------------------------------

// TestConcurrentReconcileProducesConsistentCounter verifies that when multiple
// goroutines call Reconcile simultaneously, the flock serialises them and the
// final slot counter is correct (not corrupted by interleaving).
func TestConcurrentReconcileProducesConsistentCounter(t *testing.T) {
	base := t.TempDir()
	counterPath := filepath.Join(base, ".running_count")
	writeSlotCounterFile(t, counterPath, 5)

	// One alive job and two dead jobs.
	makeJob(t, base, "job-20260227-080000-dead0001", "running", deadPID(), "", false)
	makeJob(t, base, "job-20260227-080001-dead0002", "running", deadPID(), "", false)
	makeJob(t, base, "job-20260227-080100-alive001", "running", selfPID(), "", false)

	const goroutines = 10
	now := time.Now()
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = Reconcile(base, now)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Reconcile goroutine %d returned error: %v", i, err)
		}
	}

	// Regardless of how many concurrent calls ran, the counter must reflect
	// the actual number of alive running jobs: 1.
	got := readSlotCounter(counterPath)
	if got != 1 {
		t.Errorf("slot counter = %d after concurrent Reconcile, want 1", got)
	}
}
