// Package job manages the lifecycle of subagent jobs on the filesystem.
package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// staleRecoveredMarker is written to a job's stderr to distinguish
// auto-recovered jobs from manually killed jobs (for glm clean --stale).
const staleRecoveredMarker = "__stale_recovered__"

// staleQueueThreshold is the duration after which a queued job is considered stuck.
const staleQueueThreshold = 5 * time.Minute

// Reconcile scans all job directories under subagentsDir, detects stale jobs
// (dead PID, missing pid.txt, or stuck in queue), updates their status to
// "failed", appends a diagnostic message to stderr.txt, and resets the slot
// counter file to the number of actually-running jobs.
//
// Reconcile is intended to be called exactly once at process startup.
// now is injected so tests can control the clock.
func Reconcile(subagentsDir string, now time.Time) error {
	// Acquire exclusive lock to prevent concurrent reconciliation.
	lockPath := filepath.Join(subagentsDir, ".reconcile.lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("reconcile lock: %w", err)
	}
	defer func() { _ = lf.Close() }()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("reconcile flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		return err
	}
	runningCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip special files/directories
		if name == ".running_count" || name == ".counter.lock" || name == ".reconcile.lock" || strings.HasSuffix(name, ".d") {
			continue
		}
		dir := filepath.Join(subagentsDir, name)

		// Check if this is a job dir (has status file) or a project dir.
		if _, err := os.Stat(filepath.Join(dir, "status")); err == nil {
			// Flat layout: direct job directory.
			n, err := reconcileJob(dir, now)
			if err != nil {
				return err
			}
			runningCount += n
		} else {
			// Project-scoped layout: scan for job-* subdirectories.
			subentries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, sub := range subentries {
				if !sub.IsDir() || !strings.HasPrefix(sub.Name(), "job-") {
					continue
				}
				jobDir := filepath.Join(dir, sub.Name())
				n, err := reconcileJob(jobDir, now)
				if err != nil {
					return err
				}
				runningCount += n
			}
		}
	}
	return writeSlotCounter(filepath.Join(subagentsDir, ".running_count"), runningCount)
}

// reconcileJob processes a single job directory, returning 1 if the job is
// alive and running, 0 otherwise.
func reconcileJob(jobDir string, now time.Time) (int, error) {
	status := readStatus(jobDir)
	switch status {
	case "running":
		pid, err := readPID(jobDir)
		if err != nil || !pidAlive(pid) {
			if err := writeStatus(jobDir, "failed"); err != nil {
				return 0, err
			}
			pidStr := strconv.Itoa(pid)
			if err := appendStderr(jobDir, fmt.Sprintf("[GoLeM] Process died unexpectedly (PID %s)", pidStr)); err != nil {
				return 0, err
			}
			if err := appendStderr(jobDir, staleRecoveredMarker); err != nil {
				return 0, err
			}
			return 0, nil
		}
		return 1, nil
	case "queued":
		stale, err := IsStaleQueued(jobDir, now)
		if err != nil {
			return 0, err
		}
		if stale {
			if err := writeStatus(jobDir, "failed"); err != nil {
				return 0, err
			}
			if err := appendStderr(jobDir, "[GoLeM] Job stuck in queue for over 5 minutes"); err != nil {
				return 0, err
			}
			if err := appendStderr(jobDir, staleRecoveredMarker); err != nil {
				return 0, err
			}
		}
	}
	return 0, nil
}

// CheckJobPID reads the pid.txt for the job at jobDir, checks whether the
// process is alive (via signal 0), and — if dead — updates status to "failed"
// and appends a stderr message.  It does NOT perform a full reconciliation.
// Returns the current (possibly updated) status string.
func CheckJobPID(jobDir string) (string, error) {
	status := readStatus(jobDir)
	if status != "running" {
		return status, nil
	}
	pid, err := readPID(jobDir)
	if err != nil || !pidAlive(pid) {
		if err := writeStatus(jobDir, "failed"); err != nil {
			return status, err
		}
		pidStr := strconv.Itoa(pid)
		if err := appendStderr(jobDir, fmt.Sprintf("[GoLeM] Process died unexpectedly (PID %s)", pidStr)); err != nil {
			return status, err
		}
		if err := appendStderr(jobDir, staleRecoveredMarker); err != nil {
			return status, err
		}
		return "failed", nil
	}
	return status, nil
}

// IsStaleQueued reports whether the queued job at jobDir has been waiting
// longer than staleQueueThreshold relative to now.
func IsStaleQueued(jobDir string, now time.Time) (bool, error) {
	data, err := os.ReadFile(filepath.Join(jobDir, "created_at.txt"))
	if err != nil {
		return false, fmt.Errorf("read created_at.txt: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return false, fmt.Errorf("parse created_at.txt: %w", err)
	}
	return now.Sub(createdAt) > staleQueueThreshold, nil
}

// readStatus reads the "status" file inside jobDir and returns its trimmed
// content.  Missing file returns "failed".
func readStatus(jobDir string) string {
	data, err := os.ReadFile(filepath.Join(jobDir, "status"))
	if err != nil {
		return "failed"
	}
	s := strings.TrimSpace(string(data))
	switch s {
	case "queued", "running", "done", "failed", "killed", "timeout", "permission_error":
		return s
	default:
		return "failed"
	}
}

// writeStatus atomically writes status to jobDir/status using a tmp file.
func writeStatus(jobDir, status string) error {
	tmp := fmt.Sprintf("%s.tmp.%d", filepath.Join(jobDir, "status"), os.Getpid())
	if err := os.WriteFile(tmp, []byte(status), 0o644); err != nil {
		return fmt.Errorf("atomic write (temp): %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(jobDir, "status")); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write (rename): %w", err)
	}
	return nil
}

// appendStderr appends msg (with trailing newline) to jobDir/stderr.txt.
func appendStderr(jobDir, msg string) error {
	f, err := os.OpenFile(filepath.Join(jobDir, "stderr.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(msg + "\n"); err != nil {
		return err
	}
	return nil
}

// readPID reads jobDir/pid.txt and parses the integer PID.
// Returns (0, err) when the file is missing or the content is non-numeric.
func readPID(jobDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(jobDir, "pid.txt"))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("non-numeric pid: %w", err)
	}
	return pid, nil
}

// pidAlive sends signal 0 to pid and returns true when the process exists.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// readSlotCounter reads the integer in counterPath; returns 0 on any error.
func readSlotCounter(counterPath string) int {
	data, err := os.ReadFile(counterPath)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// writeSlotCounter writes n to counterPath atomically.
func writeSlotCounter(counterPath string, n int) error {
	tmp := fmt.Sprintf("%s.tmp.%d", counterPath, os.Getpid())
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(n)), 0o644); err != nil {
		return fmt.Errorf("atomic write (temp): %w", err)
	}
	if err := os.Rename(tmp, counterPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write (rename): %w", err)
	}
	return nil
}

// CleanStale removes all job directories under subagentsDir that were
// auto-recovered by Reconcile (i.e. their stderr.txt contains
// staleRecoveredMarker). Manually killed or otherwise failed jobs are left
// intact.
func CleanStale(subagentsDir string) error {
	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip special files/directories
		if name == ".running_count" || name == ".counter.lock" || name == ".reconcile.lock" || strings.HasSuffix(name, ".d") {
			continue
		}
		dir := filepath.Join(subagentsDir, name)

		// Check if this is a job dir (has status file) or a project dir.
		if _, err := os.Stat(filepath.Join(dir, "status")); err == nil {
			// Flat layout: direct job directory.
			if err := cleanStaleJob(dir); err != nil {
				return err
			}
		} else {
			// Project-scoped layout: scan for job-* subdirectories.
			subentries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, sub := range subentries {
				if !sub.IsDir() || !strings.HasPrefix(sub.Name(), "job-") {
					continue
				}
				jobDir := filepath.Join(dir, sub.Name())
				if err := cleanStaleJob(jobDir); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// cleanStaleJob removes a single job directory if it was auto-recovered by
// Reconcile (i.e. its stderr.txt contains staleRecoveredMarker).
func cleanStaleJob(jobDir string) error {
	data, err := os.ReadFile(filepath.Join(jobDir, "stderr.txt"))
	if err != nil {
		// If stderr.txt doesn't exist, leave the job alone.
		return nil
	}
	if strings.Contains(string(data), staleRecoveredMarker) {
		if err := os.RemoveAll(jobDir); err != nil {
			return err
		}
	}
	return nil
}
