package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// terminalStatuses is the set of statuses removed by CleanCmd in default mode.
var terminalStatuses = map[string]bool{
	"done":             true,
	"failed":           true,
	"timeout":          true,
	"killed":           true,
	"permission_error": true,
}

// CleanCmd removes jobs from subagentsRoot according to the following rules:
//   - Without days: remove all jobs whose status is terminal
//     (done, failed, timeout, killed, permission_error).
//   - With days >= 0: remove all jobs whose directory mtime is older than
//     now minus days*24h, regardless of status.
//     days == 0 removes all jobs.
//
// now is injected for deterministic testing (pass time.Now() in production).
// days < 0 means "no --days flag" (status-based mode).
// Prints "Cleaned N jobs" to w.
// Returns an exitcode.Error (exit 1) when days is provided but invalid.
func CleanCmd(subagentsRoot string, days int, now time.Time, w io.Writer) error {
	// days < -1 means invalid input from the CLI layer.
	if days < -1 {
		return fmt.Errorf("err:user invalid --days value: must be 0 or a positive integer")
	}

	entries, err := os.ReadDir(subagentsRoot)
	if err != nil {
		// Root doesn't exist: nothing to clean.
		_, _ = fmt.Fprintln(w, "Cleaned 0 jobs")
		return nil
	}

	count := 0

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(subagentsRoot, entry.Name())

		// Flat (legacy) layout: the entry is itself a job directory, identified
		// by a status file sitting directly inside it.
		if _, err := os.Stat(filepath.Join(dir, "status")); err == nil {
			if cleanOneJob(dir, days, now) {
				count++
			}
			continue
		}

		// Project-scoped layout (production): jobs live one level deeper at
		// subagentsRoot/<projectID>/<jobID>/. Descend into job-* subdirectories.
		subentries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, sub := range subentries {
			if !sub.IsDir() || !strings.HasPrefix(sub.Name(), "job-") {
				continue
			}
			if cleanOneJob(filepath.Join(dir, sub.Name()), days, now) {
				count++
			}
		}
		// Drop the project directory if cleaning emptied it. os.Remove only
		// succeeds on an empty directory, so this is race-safe: a job created
		// concurrently leaves the directory non-empty and the removal no-ops.
		_ = os.Remove(dir)
	}

	_, _ = fmt.Fprintf(w, "Cleaned %d jobs\n", count)
	return nil
}

// cleanOneJob decides whether a single job directory should be removed and, if
// so, removes it. It reports whether the directory was removed.
//
//   - days < 0  -> status-based mode: remove only terminal-status jobs.
//   - days >= 0 -> age-based mode: remove jobs whose mtime is at or before
//     now-days*24h (days == 0 removes everything regardless of age).
func cleanOneJob(jobDir string, days int, now time.Time) bool {
	if days >= 0 {
		info, err := os.Stat(jobDir)
		if err != nil {
			return false
		}
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		// Keep jobs whose mtime is strictly after the cutoff.
		if info.ModTime().After(cutoff) {
			return false
		}
		return os.RemoveAll(jobDir) == nil
	}

	statusData, err := os.ReadFile(filepath.Join(jobDir, "status"))
	if err != nil {
		return false
	}
	if !terminalStatuses[strings.TrimSpace(string(statusData))] {
		return false
	}
	return os.RemoveAll(jobDir) == nil
}
