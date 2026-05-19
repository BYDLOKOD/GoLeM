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
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		jobDir := filepath.Join(subagentsRoot, entry.Name())

		if days >= 0 {
			// Age-based mode: remove jobs whose directory mtime is at or before
			// now minus days*24h. For days=0, cutoff=now so all jobs are removed.
			info, err := os.Stat(jobDir)
			if err != nil {
				continue
			}
			cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
			// Remove if mtime <= cutoff (i.e., not after cutoff).
			if info.ModTime().After(cutoff) {
				continue
			}
			if err := os.RemoveAll(jobDir); err == nil {
				count++
			}
		} else {
			// Status-based mode: remove terminal-status jobs.
			statusPath := filepath.Join(jobDir, "status")
			statusData, err := os.ReadFile(statusPath)
			if err != nil {
				continue
			}
			status := strings.TrimSpace(string(statusData))
			if terminalStatuses[status] {
				if err := os.RemoveAll(jobDir); err == nil {
					count++
				}
			}
		}
	}

	_, _ = fmt.Fprintf(w, "Cleaned %d jobs\n", count)
	return nil
}
