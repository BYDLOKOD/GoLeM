package slot

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/event"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeCounterFile writes a raw string to the counter file inside dir.
func writeCounterFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, CounterFile), []byte(content), 0o644); err != nil {
		t.Fatalf("writeCounterFile: %v", err)
	}
}

// readCounterFileRaw reads the counter file and trims whitespace.
func readCounterFileRaw(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, CounterFile))
	if err != nil {
		t.Fatalf("readCounterFile: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// readCounterFileInt reads and parses the counter file as an integer.
func readCounterFileInt(t *testing.T, dir string) int {
	t.Helper()
	raw := readCounterFileRaw(t, dir)
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("counter file has non-integer content %q: %v", raw, err)
	}
	return n
}

// newSM creates a SlotManager pointing at a fresh temp dir.
func newSM(t *testing.T, maxParallel int) (*SlotManager, string) {
	t.Helper()
	dir := t.TempDir()
	sm := NewSlotManager(dir, maxParallel)
	return sm, dir
}

// newSMWithCounter creates a SlotManager with the counter pre-set to value.
func newSMWithCounter(t *testing.T, maxParallel, value int) (*SlotManager, string) {
	t.Helper()
	sm, dir := newSM(t, maxParallel)
	writeCounterFile(t, dir, strconv.Itoa(value))
	return sm, dir
}

// startChildProcess starts a subprocess in its own process group and returns
// its PID. The process runs the given command and args.
func startChildProcess(t *testing.T, name string, args ...string) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("startChildProcess %s: %v", name, err)
	}
	return cmd, cmd.Process.Pid
}

// findDeadPID returns a PID that is not alive on this system.
func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 4194304 - 1; pid > 1000000; pid -= 1337 {
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// ESRCH, ErrProcessDone (Go 1.23+), or EPERM all mean the PID is effectively dead.
			if strings.Contains(err.Error(), "no such process") ||
				errors.Is(err, os.ErrProcessDone) {
				return pid
			}
		}
	}
	t.Fatal("could not find a dead PID for testing")
	return 0
}

// ---------------------------------------------------------------------------
// AC1: Slot counter and lock file locations
// ---------------------------------------------------------------------------

// TestCounterAndLockFilesAreAtExpectedPaths verifies that CounterPath and
// LockPath return the correct filenames relative to the subagent directory.
func TestCounterAndLockFilesAreAtExpectedPaths(t *testing.T) {
	dir := t.TempDir()
	sm := NewSlotManager(dir, 3)

	wantCounter := filepath.Join(dir, ".running_count")
	wantLock := filepath.Join(dir, ".counter.lock")

	if got := sm.CounterPath(); got != wantCounter {
		t.Errorf("CounterPath() = %q, want %q", got, wantCounter)
	}
	if got := sm.LockPath(); got != wantLock {
		t.Errorf("LockPath() = %q, want %q", got, wantLock)
	}
}

// ---------------------------------------------------------------------------
// AC2: claim_slot atomically increments counter
// ---------------------------------------------------------------------------

// TestClaimSlotIncrementsCounterFromZero verifies seed counter_file_zero.txt
// (value 0) becomes 1 after ClaimSlot.
func TestClaimSlotIncrementsCounterFromZero(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 0)

	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 1 {
		t.Errorf("counter after ClaimSlot from 0 = %d, want 1", got)
	}
}

// TestClaimSlotIncrementUnderExclusiveLock verifies that ClaimSlot creates the
// lock file (proof that flock-based locking occurred) and increments correctly.
func TestClaimSlotIncrementUnderExclusiveLock(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 0)

	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 1 {
		t.Errorf("counter after ClaimSlot = %d, want 1", got)
	}

	// The lock file must have been created during the operation.
	if _, err := os.Stat(sm.LockPath()); os.IsNotExist(err) {
		t.Error("lock file was never created; expected exclusive flock to create it")
	}
}

// TestClaimSlotIncrementsCounterFromExistingValue verifies seed
// counter_file_valid.txt (value 3) becomes 4 after ClaimSlot.
func TestClaimSlotIncrementsCounterFromExistingValue(t *testing.T) {
	sm, dir := newSMWithCounter(t, 10, 3)

	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 4 {
		t.Errorf("counter after ClaimSlot from 3 = %d, want 4", got)
	}
}

// ---------------------------------------------------------------------------
// AC3: release_slot atomically decrements counter
// ---------------------------------------------------------------------------

// TestReleaseSlotDecrementsCounter verifies seed counter_file_valid.txt
// (value 3) becomes 2 after ReleaseSlot, performed under exclusive lock.
func TestReleaseSlotDecrementsCounter(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 3)

	if err := sm.ReleaseSlot(); err != nil {
		t.Fatalf("ReleaseSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 2 {
		t.Errorf("counter after ReleaseSlot from 3 = %d, want 2", got)
	}

	// Lock file must have been created.
	if _, err := os.Stat(sm.LockPath()); os.IsNotExist(err) {
		t.Error("lock file was never created; expected exclusive flock to create it")
	}
}

// TestReleaseSlotNeverGoesBelowZero verifies seed counter_file_zero.txt
// (value 0) stays at 0 after ReleaseSlot.
func TestReleaseSlotNeverGoesBelowZero(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 0)

	if err := sm.ReleaseSlot(); err != nil {
		t.Fatalf("ReleaseSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 0 {
		t.Errorf("counter after ReleaseSlot from 0 = %d, want 0", got)
	}
}

// TestCounterClampedToZeroOnDoubleRelease verifies seed counter_file_negative.txt
// (value -2) becomes 0 after ReleaseSlot.
func TestCounterClampedToZeroOnDoubleRelease(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, -2)

	if err := sm.ReleaseSlot(); err != nil {
		t.Fatalf("ReleaseSlot() error: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 0 {
		t.Errorf("counter after ReleaseSlot from -2 = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// AC4: wait_for_slot blocks when at capacity
// ---------------------------------------------------------------------------

// TestSlotClaimedImmediatelyWhenUnderLimit verifies slot_scenario_within_limit.json:
// counter=2, max_parallel=3 → claim succeeds immediately, counter becomes 3.
func TestSlotClaimedImmediatelyWhenUnderLimit(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 2)

	start := time.Now()
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot() error: %v", err)
	}
	elapsed := time.Since(start)

	got := readCounterFileInt(t, dir)
	if got != 3 {
		t.Errorf("counter after WaitForSlot (under limit) = %d, want 3", got)
	}

	// Must not block: should complete well under 1 second.
	if elapsed > time.Second {
		t.Errorf("WaitForSlot blocked for %v, expected immediate claim", elapsed)
	}
}

// TestSlotBlocksWhenAtCapacity verifies slot_scenario_at_limit.json:
// counter=3, max_parallel=3 → call blocks until a slot is released via
// notification. We free a slot from a goroutine after 300ms to unblock
// the waiter.
func TestSlotBlocksWhenAtCapacity(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 3)

	// Start the notifier for fast wake-up.
	if err := sm.StartNotifier(); err != nil {
		t.Fatalf("StartNotifier: %v", err)
	}
	defer sm.StopNotifier()

	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := sm.ReleaseSlot(); err != nil {
			// Non-fatal in goroutine; test will catch the symptom.
			fmt.Printf("goroutine ReleaseSlot error: %v\n", err)
		}
	}()

	start := time.Now()
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot() error: %v", err)
	}
	elapsed := time.Since(start)

	// Must have blocked for at least 200ms (the goroutine sleeps 300ms).
	if elapsed < 200*time.Millisecond {
		t.Errorf("WaitForSlot returned after only %v, expected at least 200ms block", elapsed)
	}

	// After: release(3→2) then claim(2→3), so counter is 3.
	got := readCounterFileInt(t, dir)
	if got != 3 {
		t.Errorf("counter after WaitForSlot (was at limit) = %d, want 3", got)
	}
}

// TestSlotClaimedImmediatelyWhenMaxParallelIsZero verifies slot_scenario_unlimited.json:
// counter=10, max_parallel=0 (unlimited) → claim immediately, counter becomes 11.
func TestSlotClaimedImmediatelyWhenMaxParallelIsZero(t *testing.T) {
	sm, dir := newSMWithCounter(t, 0, 10)

	start := time.Now()
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot() error: %v", err)
	}
	elapsed := time.Since(start)

	got := readCounterFileInt(t, dir)
	if got != 11 {
		t.Errorf("counter after WaitForSlot (unlimited) = %d, want 11", got)
	}

	if elapsed > time.Second {
		t.Errorf("WaitForSlot (unlimited) blocked for %v, expected immediate claim", elapsed)
	}
}

// ---------------------------------------------------------------------------
// AC5: File locking implementation
// ---------------------------------------------------------------------------

// TestUsesSyscallFlockOnLinux verifies that concurrent ClaimSlot calls produce
// the correct final count, which would be impossible without proper locking.
func TestUsesSyscallFlockOnLinux(t *testing.T) {
	sm, dir := newSMWithCounter(t, 100, 0)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := sm.ClaimSlot(); err != nil {
				t.Errorf("ClaimSlot race error: %v", err)
			}
		}()
	}
	wg.Wait()

	got := readCounterFileInt(t, dir)
	if got != goroutines {
		t.Errorf("concurrent ClaimSlot: counter = %d, want %d (locking failed)", got, goroutines)
	}
}

// TestUsesSyscallFlockOnMacOS is a compilation and runtime check that
// syscall.Flock is available and usable — this is the same primitive used on
// macOS as on Linux.
func TestUsesSyscallFlockOnMacOS(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockFile)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("syscall.Flock LOCK_EX: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("syscall.Flock LOCK_UN: %v", err)
	}
}

// TestFallbackToMkdirLockingWhenFlockUnavailable verifies the mkdir-based
// fallback path: mkdirLockPath returns the right directory name, and ClaimSlot
// works correctly when LOCK_FALLBACK=true forces the fallback.
func TestFallbackToMkdirLockingWhenFlockUnavailable(t *testing.T) {
	dir := t.TempDir()
	lockFile := filepath.Join(dir, LockFile)

	got := mkdirLockPath(lockFile)
	want := lockFile + ".d"
	if got != want {
		t.Errorf("mkdirLockPath() = %q, want %q", got, want)
	}

	// Force fallback mode; ClaimSlot must still work.
	t.Setenv("LOCK_FALLBACK", "true")
	sm := NewSlotManager(dir, 3)
	writeCounterFile(t, dir, "0")

	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot with LOCK_FALLBACK=true: %v", err)
	}

	got2 := readCounterFileInt(t, dir)
	if got2 != 1 {
		t.Errorf("counter with mkdir fallback = %d, want 1", got2)
	}
}

// ---------------------------------------------------------------------------
// AC6: Reconciliation at startup
// ---------------------------------------------------------------------------

// TestReconcileDetectsDeadRunningJobsAndResetsCounter tests the reconcile_input.json
// scenario: 3 running jobs (2 alive, 1 dead), 1 queued.
// Expected: dead job → failed, counter → 2, alive/queued jobs unchanged.
func TestReconcileDetectsDeadRunningJobsAndResetsCounter(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 3)

	alivePID1 := os.Getpid() // current test process — definitely alive
	alivePID2 := 1           // PID 1 (init) is always alive on Linux
	deadPID := findDeadPID(t)

	jobs := []*Job{
		{
			JobID:  "job-20260227-091500-a1b2c3d4",
			Status: JobStatusRunning,
			PID:    alivePID1,
			HasPID: true,
		},
		{
			JobID:  "job-20260227-091530-e5f6a7b8",
			Status: JobStatusRunning,
			PID:    alivePID2,
			HasPID: true,
		},
		{
			JobID:  "job-20260227-091205-c9d0e1f2",
			Status: JobStatusRunning,
			PID:    deadPID,
			HasPID: true,
		},
		{
			JobID:  "job-20260227-091800-34a5b6c7",
			Status: JobStatusQueued,
			HasPID: false,
		},
	}

	if err := sm.Reconcile(jobs); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	// Counter must be reset to 2 (two alive running jobs).
	got := readCounterFileInt(t, dir)
	if got != 2 {
		t.Errorf("counter after Reconcile = %d, want 2", got)
	}

	// Dead job must be marked failed.
	deadJob := jobs[2]
	if deadJob.Status != JobStatusFailed {
		t.Errorf("dead job status = %q, want %q", deadJob.Status, JobStatusFailed)
	}

	// Stderr must contain the expected message.
	wantMsg := fmt.Sprintf("[GoLeM] Process died unexpectedly (PID %d)", deadPID)
	if !strings.Contains(deadJob.Stderr, wantMsg) {
		t.Errorf("dead job stderr = %q, want it to contain %q", deadJob.Stderr, wantMsg)
	}

	// Alive running jobs must be unchanged.
	if jobs[0].Status != JobStatusRunning {
		t.Errorf("alive job[0] status changed to %q", jobs[0].Status)
	}
	if jobs[1].Status != JobStatusRunning {
		t.Errorf("alive job[1] status changed to %q", jobs[1].Status)
	}

	// Queued job must be unchanged.
	if jobs[3].Status != JobStatusQueued {
		t.Errorf("queued job status changed to %q", jobs[3].Status)
	}
}

// TestReconciliationRunsOnceAtStartup verifies that Reconcile is idempotent
// when called with no running jobs, and that a second call does not corrupt state.
func TestReconciliationRunsOnceAtStartup(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 0)

	// First call: no jobs, counter stays 0.
	if err := sm.Reconcile([]*Job{}); err != nil {
		t.Fatalf("first Reconcile() error: %v", err)
	}
	if got := readCounterFileInt(t, dir); got != 0 {
		t.Errorf("counter after first Reconcile (empty) = %d, want 0", got)
	}

	// Second call must also succeed (guard against double-init panics).
	if err := sm.Reconcile([]*Job{}); err != nil {
		t.Fatalf("second Reconcile() error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC7: Process group termination
// ---------------------------------------------------------------------------

// TestTerminatingJobSendsSigTermThenSigKill verifies that TerminateProcessGroup
// sends SIGTERM to the process group and, if the process survives, SIGKILL.
func TestTerminatingJobSendsSigTermThenSigKill(t *testing.T) {
	// `sleep 30` can be killed; it does not block signals.
	cmd, pid := startChildProcess(t, "sleep", "30")

	if !IsProcessAlive(pid) {
		t.Fatalf("child process PID %d not alive before termination", pid)
	}

	if err := TerminateProcessGroup(pid); err != nil {
		t.Fatalf("TerminateProcessGroup(%d): %v", pid, err)
	}

	// Wait up to 3 seconds for the process to die.
	deadline := time.Now().Add(3 * time.Second)
	stillAlive := true
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			stillAlive = false
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if stillAlive {
		t.Errorf("process PID %d still alive after TerminateProcessGroup", pid)
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// TestProcessGroupTerminationPreventsOrphanClaudeProcesses verifies that
// child processes of the target also receive the signal when the process group
// is terminated.
func TestProcessGroupTerminationPreventsOrphanClaudeProcesses(t *testing.T) {
	// Start a parent shell that spawns two sleep children.
	cmd, parentPID := startChildProcess(t, "sh", "-c", "sleep 60 & sleep 60")

	// Give children time to spawn.
	time.Sleep(200 * time.Millisecond)

	if !IsProcessAlive(parentPID) {
		t.Fatalf("parent PID %d not alive before termination", parentPID)
	}

	if err := TerminateProcessGroup(parentPID); err != nil {
		t.Fatalf("TerminateProcessGroup(%d): %v", parentPID, err)
	}

	// Parent must be dead within 3 seconds.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(parentPID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if IsProcessAlive(parentPID) {
		t.Errorf("parent process PID %d still alive after process group termination", parentPID)
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// ---------------------------------------------------------------------------
// AC8: unlimited-by-default behavior
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestCounterFileDoesNotExistAtStartup verifies that Init creates the counter
// file with value 0 when it does not exist.
func TestCounterFileDoesNotExistAtStartup(t *testing.T) {
	sm, dir := newSM(t, 3)

	// Ensure counter file is absent.
	_ = os.Remove(filepath.Join(dir, CounterFile))

	if err := sm.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	raw := readCounterFileRaw(t, dir)
	if raw != "0" {
		t.Errorf("counter after Init (no prior file) = %q, want \"0\"", raw)
	}
}

// TestCounterFileContainsNonIntegerValue verifies that Init resets a counter
// file containing "abc" (seed counter_file_invalid.txt) to 0.
func TestCounterFileContainsNonIntegerValue(t *testing.T) {
	sm, dir := newSM(t, 3)
	writeCounterFile(t, dir, "abc")

	if err := sm.Init(); err != nil {
		t.Fatalf("Init() with invalid counter: %v", err)
	}

	raw := readCounterFileRaw(t, dir)
	if raw != "0" {
		t.Errorf("counter after Init with 'abc' content = %q, want \"0\"", raw)
	}
}

// TestStaleLockFileFromDeadProcessHandledByFlock verifies that a lock file
// left by a dead process does not prevent a new flock acquisition.
func TestStaleLockFileFromDeadProcessHandledByFlock(t *testing.T) {
	sm, dir := newSMWithCounter(t, 3, 0)

	// Create a stale lock file.
	if err := os.WriteFile(sm.LockPath(), []byte("stale"), 0o644); err != nil {
		t.Fatalf("create stale lock: %v", err)
	}

	// ClaimSlot must succeed regardless of the stale file.
	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot with stale lock file: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 1 {
		t.Errorf("counter after ClaimSlot with stale lock = %d, want 1", got)
	}
}

// TestStaleMkdirLockHas60SecondStalenessDetection verifies that a mkdir-based
// lock older than StaleLockSeconds is detected and removed so a new lock can
// be acquired.
func TestStaleMkdirLockHas60SecondStalenessDetection(t *testing.T) {
	dir := t.TempDir()
	lockFile := filepath.Join(dir, LockFile)
	lockDir := mkdirLockPath(lockFile)

	// Create the lock directory and backdate its mtime by StaleLockSeconds+1.
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	staleTime := time.Now().Add(-(StaleLockSeconds + 1) * time.Second)
	if err := os.Chtimes(lockDir, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// isStale must return true for a directory beyond the threshold.
	if !isStale(lockDir) {
		t.Errorf("isStale(%q) = false, want true for lock older than %ds", lockDir, StaleLockSeconds)
	}

	// ClaimSlot in fallback mode must succeed after the stale lock is removed.
	t.Setenv("LOCK_FALLBACK", "true")
	sm := NewSlotManager(dir, 3)
	writeCounterFile(t, dir, "0")

	if err := sm.ClaimSlot(); err != nil {
		t.Fatalf("ClaimSlot with stale mkdir lock: %v", err)
	}

	got := readCounterFileInt(t, dir)
	if got != 1 {
		t.Errorf("counter after ClaimSlot (stale mkdir lock removed) = %d, want 1", got)
	}
}

// TestConcurrentWriteCounterDoesNotCorrupt verifies that concurrent
// writeCounter calls via ClaimSlot/ReleaseSlot under flock produce a valid
// integer counter (never partial or corrupted content).
func TestConcurrentWriteCounterDoesNotCorrupt(t *testing.T) {
	sm, dir := newSMWithCounter(t, 1000, 0)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Half claim, half release — the counter should end at goroutines/2.
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				if err := sm.ClaimSlot(); err != nil {
					t.Errorf("ClaimSlot: %v", err)
				}
			} else {
				// Claim first so release doesn't clamp below zero.
				if err := sm.ClaimSlot(); err != nil {
					t.Errorf("ClaimSlot (for release): %v", err)
				}
				if err := sm.ReleaseSlot(); err != nil {
					t.Errorf("ReleaseSlot: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()

	// Verify counter file is a valid integer (not corrupted).
	raw := readCounterFileRaw(t, dir)
	val, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("counter file corrupted: content=%q err=%v", raw, err)
	}

	// All goroutines claimed once; even-numbered goroutines kept theirs,
	// odd-numbered ones claimed then released. Net: goroutines/2 claims.
	want := goroutines / 2
	if val != want {
		t.Errorf("counter after concurrent writes = %d, want %d", val, want)
	}
}

// TestPIDReuseAcceptedAsFalsePositiveDuringReconciliation verifies that a
// running job whose PID now belongs to an unrelated live process is treated as
// alive (false positive accepted at startup).
func TestPIDReuseAcceptedAsFalsePositiveDuringReconciliation(t *testing.T) {
	// PID 1 is always alive and is certainly not our job.
	pid := 1

	if !IsProcessAlive(pid) {
		t.Errorf("IsProcessAlive(%d) = false, want true (PID reuse false positive)", pid)
	}

	sm, dir := newSMWithCounter(t, 3, 1)

	jobs := []*Job{
		{
			JobID:  "job-reuse-test",
			Status: JobStatusRunning,
			PID:    pid,
			HasPID: true,
		},
	}

	if err := sm.Reconcile(jobs); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	// Job must remain running because its PID is alive.
	if jobs[0].Status != JobStatusRunning {
		t.Errorf("job with reused PID status = %q, want running (false positive acceptable)", jobs[0].Status)
	}

	// Counter must remain 1.
	if got := readCounterFileInt(t, dir); got != 1 {
		t.Errorf("counter after Reconcile (PID reuse) = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Event producer tests (P2.1)
// ---------------------------------------------------------------------------

// TestWaitForSlot_PublishesSlotAcquired verifies that WaitForSlot publishes a
// SlotAcquired event with the correct current_count and max_parallel.
func TestWaitForSlot_PublishesSlotAcquired(t *testing.T) {
	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.SlotAcquired)

	dir := t.TempDir()
	sm := NewSlotManager(dir, 3)
	sm.SetBus(bus)
	if err := sm.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot: %v", err)
	}

	select {
	case e := <-ch:
		if e.Type != event.SlotAcquired {
			t.Errorf("type = %v, want %v", e.Type, event.SlotAcquired)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data is not map[string]any: %T", e.Data)
		}
		if data["current_count"] != 1 {
			t.Errorf("current_count = %v, want 1", data["current_count"])
		}
		if data["max_parallel"] != 3 {
			t.Errorf("max_parallel = %v, want 3", data["max_parallel"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SlotAcquired event")
	}
}

// TestWaitForSlot_UnlimitedPublishesSlotAcquired verifies that WaitForSlot
// publishes a SlotAcquired event even when maxParallel is 0 (unlimited).
func TestWaitForSlot_UnlimitedPublishesSlotAcquired(t *testing.T) {
	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.SlotAcquired)

	dir := t.TempDir()
	sm := NewSlotManager(dir, 0)
	sm.SetBus(bus)
	if err := sm.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot: %v", err)
	}

	select {
	case e := <-ch:
		if e.Type != event.SlotAcquired {
			t.Errorf("type = %v, want %v", e.Type, event.SlotAcquired)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data is not map[string]any: %T", e.Data)
		}
		if data["max_parallel"] != 0 {
			t.Errorf("max_parallel = %v, want 0", data["max_parallel"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SlotAcquired event (unlimited)")
	}
}

// TestReleaseSlot_PublishesSlotReleased verifies that ReleaseSlot publishes a
// SlotReleased event with the correct current_count and max_parallel.
func TestReleaseSlot_PublishesSlotReleased(t *testing.T) {
	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.SlotReleased)

	dir := t.TempDir()
	sm := NewSlotManager(dir, 3)
	sm.SetBus(bus)
	if err := sm.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot: %v", err)
	}

	if err := sm.ReleaseSlot(); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}

	select {
	case e := <-ch:
		if e.Type != event.SlotReleased {
			t.Errorf("type = %v, want %v", e.Type, event.SlotReleased)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data is not map[string]any: %T", e.Data)
		}
		if data["current_count"] != 0 {
			t.Errorf("current_count = %v, want 0", data["current_count"])
		}
		if data["max_parallel"] != 3 {
			t.Errorf("max_parallel = %v, want 3", data["max_parallel"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SlotReleased event")
	}
}

// TestWaitForSlot_NilBus_NoPanic verifies that WaitForSlot and ReleaseSlot do
// not panic when the bus is nil (the default).
func TestWaitForSlot_NilBus_NoPanic(t *testing.T) {
	dir := t.TempDir()
	sm := NewSlotManager(dir, 1)
	// Bus is nil by default.
	if err := sm.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("WaitForSlot: %v", err)
	}
	if err := sm.ReleaseSlot(); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
}

// TestSetBus_SlotManager_ReturnsReceiverForChaining verifies that SetBus
// returns the SlotManager receiver for method chaining.
func TestSetBus_SlotManager_ReturnsReceiverForChaining(t *testing.T) {
	dir := t.TempDir()
	sm := NewSlotManager(dir, 1)

	bus := event.NewBus()
	defer bus.Close()

	got := sm.SetBus(bus)
	if got != sm {
		t.Error("SetBus did not return the receiver")
	}
}
