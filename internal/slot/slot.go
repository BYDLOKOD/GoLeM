// Package slot manages concurrency tracking for GoLeM subagents.
// It uses file-based counters with exclusive flock locking to track
// the number of simultaneously running subagents.
package slot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/veschin/GoLeM/internal/event"
)

const (
	// CounterFile is the filename for the running counter.
	CounterFile = ".running_count"
	// LockFile is the filename for the exclusive file lock.
	LockFile = ".counter.lock"
	// PollInterval is the polling interval in seconds when waiting for a slot.
	PollInterval = 2
	// StaleLockSeconds is the staleness threshold for mkdir-based locks.
	StaleLockSeconds = 60
)

// JobStatus represents the lifecycle state of a subagent job.
type JobStatus string

const (
	JobStatusQueued  JobStatus = "queued"
	JobStatusRunning JobStatus = "running"
	JobStatusFailed  JobStatus = "failed"
	JobStatusDone    JobStatus = "done"
)

// Job holds metadata for a single subagent job used during reconciliation.
type Job struct {
	JobID     string
	Status    JobStatus
	PID       int  // 0 means no PID (e.g. queued)
	HasPID    bool // false for queued jobs with null PID
	Stderr    string
}

// SlotManager controls concurrent access to subagent slots.
type SlotManager struct {
	dir         string
	maxParallel int
	notifier    *SlotNotifier // optional fast wake-up for WaitForSlot
	bus         *event.Bus    // optional event bus
}

// SetBus sets the event bus on the SlotManager. Returns the receiver for chaining.
func (sm *SlotManager) SetBus(bus *event.Bus) *SlotManager {
	sm.bus = bus
	return sm
}

// NewSlotManager creates a SlotManager that stores its counter and lock files
// inside dir and enforces the given maxParallel limit (0 = unlimited).
func NewSlotManager(dir string, maxParallel int) *SlotManager {
	return &SlotManager{
		dir:         dir,
		maxParallel: maxParallel,
		notifier:    NewSlotNotifier(dir),
	}
}

// StartNotifier starts the Unix domain socket listener for fast slot
// notification. If this is not called, WaitForSlot falls back to polling.
func (sm *SlotManager) StartNotifier() error {
	return sm.notifier.Start()
}

// StopNotifier stops the Unix domain socket listener and cleans up.
func (sm *SlotManager) StopNotifier() {
	sm.notifier.Stop()
}

// CounterPath returns the absolute path of the running counter file.
func (sm *SlotManager) CounterPath() string {
	return filepath.Join(sm.dir, CounterFile)
}

// LockPath returns the absolute path of the exclusive lock file.
func (sm *SlotManager) LockPath() string {
	return filepath.Join(sm.dir, LockFile)
}

// Init ensures the counter file exists, creating it with value 0 if absent.
// It also handles non-integer content by resetting to 0 and logging a warning.
func (sm *SlotManager) Init() error {
	counterPath := sm.CounterPath()

	// Create lock file parent directory if needed
	lockPath := sm.LockPath()
	lockDir := filepath.Dir(lockPath)
	if lockDir != "." && lockDir != sm.dir {
		if err := os.MkdirAll(lockDir, 0o755); err != nil {
			return fmt.Errorf("create lock parent dir: %w", err)
		}
	}

	// Check if counter file exists
	if _, err := os.Stat(counterPath); os.IsNotExist(err) {
		return sm.writeCounter(0)
	}

	// File exists - validate content
	val, err := sm.readCounter()
	if err != nil {
		// Invalid content, reset to 0
		log.Printf("[WARNING] Invalid counter file content, resetting to 0: %v", err)
		return sm.writeCounter(0)
	}
	// Valid integer, ensure it's not negative
	if val < 0 {
		return sm.writeCounter(0)
	}
	return nil
}

// readCounter reads the current integer value from the counter file.
// Returns 0 and resets the file if the content is not a valid integer.
func (sm *SlotManager) readCounter() (int, error) {
	data, err := os.ReadFile(sm.CounterPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	str := string(data)
	val, err := strconv.Atoi(str)
	if err != nil {
		return 0, fmt.Errorf("invalid counter content %q: %w", str, err)
	}
	return val, nil
}

// writeCounter atomically writes n to the counter file using write-to-tmp + rename.
func (sm *SlotManager) writeCounter(n int) error {
	target := sm.CounterPath()
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".counter-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(strconv.Itoa(n)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// withLock acquires an exclusive flock on LockPath, runs fn, then releases.
// On platforms where flock is unavailable it falls back to mkdir-based locking.
func (sm *SlotManager) withLock(fn func() error) error {
	// Check if fallback mode is forced
	useFallback := os.Getenv("LOCK_FALLBACK") == "true"

	if !useFallback {
		// Try flock-based locking
		f, err := os.OpenFile(sm.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
		if err == nil {
			defer func() { _ = f.Close() }()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err == nil {
				defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
				return fn()
			}
		}
	}

	// Fallback to mkdir-based locking
	lockDir := mkdirLockPath(sm.LockPath())
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			// Acquired lock
			defer func() { _ = os.Remove(lockDir) }()
			return fn()
		}
		if os.IsExist(err) {
			// Lock held, check if stale
			if isStale(lockDir) {
				_ = os.Remove(lockDir)
				continue
			}
			// Wait and retry
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return fmt.Errorf("mkdir lock failed: %w", err)
	}
}

// ClaimSlot atomically increments the running counter by 1 under exclusive lock.
func (sm *SlotManager) ClaimSlot() error {
	return sm.withLock(func() error {
		val, err := sm.readCounter()
		if err != nil {
			return err
		}
		return sm.writeCounter(val + 1)
	})
}

// ReleaseSlot atomically decrements the running counter by 1 under exclusive lock,
// clamping at 0 (never negative). After releasing, notifies waiters that a slot
// is available and publishes a SlotReleased event if a bus is set.
func (sm *SlotManager) ReleaseSlot() error {
	var releasedCount int
	err := sm.withLock(func() error {
		val, err := sm.readCounter()
		if err != nil {
			return err
		}
		newVal := max(val-1, 0)
		releasedCount = newVal
		return sm.writeCounter(newVal)
	})
	if err != nil {
		return err
	}

	// Notify waiters that a slot was released.
	sm.notifier.Notify()

	// Publish SlotReleased event (no-op if bus is nil).
	event.Publish(sm.bus, event.Event{
		Type:      event.SlotReleased,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"current_count": releasedCount,
			"max_parallel":  sm.maxParallel,
		},
	})

	return nil
}

// errNoSlot is a sentinel used internally by WaitForSlot to signal that no
// slot is available without triggering the error-return path.
var errNoSlot = fmt.Errorf("no slot available")

// WaitForSlot blocks until a slot is available (counter < maxParallel), then
// claims one. When maxParallel == 0 the limit is unlimited and the slot is
// claimed immediately. Uses Unix domain socket notification for instant wake-up
// when a slot is released; falls back to polling if the notifier is unavailable.
// Publishes a SlotAcquired event on success if a bus is set.
func (sm *SlotManager) WaitForSlot() error {
	// When maxParallel is 0, unlimited - just claim immediately.
	if sm.maxParallel == 0 {
		if err := sm.ClaimSlot(); err != nil {
			return err
		}
		event.Publish(sm.bus, event.Event{
			Type:      event.SlotAcquired,
			Timestamp: time.Now().UTC(),
			Data: map[string]any{
				"current_count": 1,
				"max_parallel":  sm.maxParallel,
			},
		})
		return nil
	}

	for {
		var acquiredCount int
		err := sm.withLock(func() error {
			val, err := sm.readCounter()
			if err != nil {
				return err
			}
			if val < sm.maxParallel {
				// Slot available, claim it.
				acquiredCount = val + 1
				return sm.writeCounter(acquiredCount)
			}
			// No slot available.
			return errNoSlot
		})
		if err == nil {
			// Successfully claimed slot.
			event.Publish(sm.bus, event.Event{
				Type:      event.SlotAcquired,
				Timestamp: time.Now().UTC(),
				Data: map[string]any{
					"current_count": acquiredCount,
					"max_parallel":  sm.maxParallel,
				},
			})
			return nil
		}
		if err != errNoSlot {
			// Real error.
			return err
		}

		// No slot available. Wait for notification instead of blind polling.
		// TODO: make wait timeout configurable via config (currently hardcoded 30s).
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = sm.notifier.Wait(ctx) // returns on notify, timeout, or error
		cancel()
		// Loop back to try claiming again.
	}
}

// Reconcile scans jobs for running entries whose PID is no longer alive,
// marks them as "failed", appends a message to their stderr, and resets the
// counter to the number of actually-alive running jobs. It should be called
// once at startup.
func (sm *SlotManager) Reconcile(jobs []*Job) error {
	aliveCount := 0
	for _, job := range jobs {
		if job.Status == JobStatusRunning && job.HasPID {
			if IsProcessAlive(job.PID) {
				aliveCount++
			} else {
				// Mark dead job as failed
				job.Status = JobStatusFailed
				msg := fmt.Sprintf("[GoLeM] Process died unexpectedly (PID %d)\n", job.PID)
				job.Stderr += msg
			}
		} else if job.Status == JobStatusRunning {
			// Running job without PID counts as alive
			aliveCount++
		}
	}

	// Reset counter to actual alive count
	return sm.withLock(func() error {
		return sm.writeCounter(aliveCount)
	})
}

// isZombieViaProc reports whether the given PID is in zombie state by
// reading /proc/<pid>/stat. Returns false when /proc is not available.
func isZombieViaProc(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// /proc/pid/stat format: pid (comm) state ...
	// state is the third field; find it after the closing ')' of (comm).
	s := string(data)
	end := len(s) - 1
	for i := end; i >= 0; i-- {
		if s[i] == ')' {
			// State field is right after ") "
			if i+2 < len(s) {
				return s[i+2] == 'Z'
			}
			break
		}
	}
	return false
}

// IsProcessAlive reports whether the process with the given PID exists and is
// alive in the current OS process table.
// ESRCH means the process does not exist (dead).
// EPERM means the process exists but we lack permission to signal it (alive).
// No error means the process exists and we have permission (alive).
// Zombie processes (state Z) are treated as dead — they have been killed but
// not yet reaped and are effectively not running.
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		// Process exists and we can signal it — check for zombie state.
		return !isZombieViaProc(pid)
	}
	// Check if the error is EPERM (permission denied but process exists)
	// This handles both direct syscall.Errno and wrapped errors.
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EPERM {
		// We cannot signal it (different user), but it exists.
		// A zombie process with EPERM is unusual but guard anyway.
		return !isZombieViaProc(pid)
	}
	return false
}

// TerminateProcessGroup sends SIGTERM to the process group of pid, waits 1
// second, then sends SIGKILL to ensure termination.
func TerminateProcessGroup(pid int) error {
	// Try SIGTERM first for graceful shutdown — ignore errors, process may already be gone.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(1 * time.Second)
	// Send SIGKILL to ensure termination — also ignore errors.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}

// mkdirLockPath returns the path of the mkdir-based fallback lock directory.
func mkdirLockPath(lockFile string) string {
	return lockFile + ".d"
}

// isStale reports whether a mkdir-based lock at dir is older than StaleLockSeconds.
func isStale(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	age := time.Since(info.ModTime())
	return age > time.Duration(StaleLockSeconds)*time.Second
}
