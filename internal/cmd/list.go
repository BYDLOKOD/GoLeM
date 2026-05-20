// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/veschin/GoLeM/internal/job"
)

// JobEntry represents a single row in the list output.
type JobEntry struct {
	JobID     string
	Status    string
	StartedAt *time.Time // nil when the job has not started yet
	Dir       string     // absolute path to the job directory
}

// ListCmd scans subagentsRoot for all jobs (project-scoped and legacy flat),
// checks PID liveness for running jobs, and writes a tabular report to w.
//
// Columns: JOB_ID  STATUS  STARTED
// Rows are sorted newest-first (nil started_at sorts last).
// Running jobs whose PID is no longer alive are updated to "failed".
// Missing status files are reported as "unknown".
// When there are no jobs nothing is written.
func ListCmd(subagentsRoot string, w io.Writer, opts ...*FilterOptions) error {
	var filter *FilterOptions
	if len(opts) > 0 {
		filter = opts[0]
	}
	entries, err := os.ReadDir(subagentsRoot)
	if err != nil {
		// If root doesn't exist, nothing to show.
		return nil
	}

	var jobs []JobEntry

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirPath := filepath.Join(subagentsRoot, entry.Name())
		statusPath := filepath.Join(dirPath, "status")

		if _, err := os.Stat(statusPath); err == nil {
			// Legacy flat layout: subagentsRoot/<jobID>/status
			je := readListJobEntry(entry.Name(), dirPath)
			jobs = append(jobs, je)
			continue
		}

		// Check if it has a status file directly (corrupted dir with no status)
		// or is a project directory with job subdirs.
		subEntries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}

		hasJobSubdirs := false
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			subDir := filepath.Join(dirPath, sub.Name())
			subStatus := filepath.Join(subDir, "status")
			if _, err := os.Stat(subStatus); err == nil {
				hasJobSubdirs = true
				je := readListJobEntry(sub.Name(), subDir)
				jobs = append(jobs, je)
			}
		}

		// If entry.Name() looks like a jobID (starts with "job-") but has no
		// status file, treat it as a corrupted job directory with unknown status.
		if !hasJobSubdirs && strings.HasPrefix(entry.Name(), "job-") {
			je := JobEntry{
				JobID:  entry.Name(),
				Status: "unknown",
				Dir:    dirPath,
			}
			jobs = append(jobs, je)
		}
	}

	if len(jobs) == 0 {
		return nil
	}

	// Reconcile running jobs: check PID liveness.
	for i := range jobs {
		if jobs[i].Status == "running" {
			newStatus, _ := job.CheckJobPID(jobs[i].Dir)
			jobs[i].Status = newStatus
		}
	}

	// Apply filters if provided.
	if filter != nil {
		jobs = FilterJobs(jobs, filter)
	}

	if len(jobs) == 0 {
		return nil
	}

	// Sort newest-first (nil StartedAt sorts last).
	sort.Slice(jobs, func(i, j int) bool {
		ti, tj := jobs[i].StartedAt, jobs[j].StartedAt
		if ti == nil && tj == nil {
			return false
		}
		if ti == nil {
			return false
		}
		if tj == nil {
			return true
		}
		return ti.After(*tj)
	})

	// Print tabular output.
	_, _ = fmt.Fprintf(w, "%-44s  %-18s  %s\n", "JOB_ID", "STATUS", "STARTED")
	for _, j := range jobs {
		started := "-"
		if j.StartedAt != nil {
			started = j.StartedAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "%-44s  %-18s  %s\n", j.JobID, j.Status, started)
	}
	return nil
}

// readListJobEntry reads a job directory and returns a JobEntry for list display.
// Missing status file returns "unknown" status (unlike job.ReadStatus which returns "failed").
func readListJobEntry(jobID, jobDir string) JobEntry {
	status := "unknown"
	data, err := os.ReadFile(filepath.Join(jobDir, "status"))
	if err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			status = s
		}
	}

	var startedAt *time.Time
	// Try started_at.txt first, then started_at (no extension).
	for _, name := range []string{"started_at.txt", "started_at"} {
		data, err := os.ReadFile(filepath.Join(jobDir, name))
		if err == nil {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
			if err == nil {
				startedAt = &t
				break
			}
			// Try parsing as a simpler format.
			t, err = time.Parse("2006-01-02T15:04:05-07:00", strings.TrimSpace(string(data)))
			if err == nil {
				startedAt = &t
				break
			}
		}
	}

	// If no started_at file is found, parse the timestamp from the jobID as fallback.
	// Job ID format: job-YYYYMMDD-HHMMSS-XXXXXXXX
	if startedAt == nil {
		if t := parseJobIDTime(jobID); !t.IsZero() {
			startedAt = &t
		}
	}

	return JobEntry{
		JobID:     jobID,
		Status:    status,
		StartedAt: startedAt,
		Dir:       jobDir,
	}
}

// parseJobIDTime extracts the timestamp from a job ID of the form
// "job-YYYYMMDD-HHMMSS-XXXXXXXX". Returns zero time if the format doesn't match.
func parseJobIDTime(jobID string) time.Time {
	// Format: job-YYYYMMDD-HHMMSS-XXXXXXXX
	parts := strings.Split(jobID, "-")
	if len(parts) < 4 || parts[0] != "job" {
		return time.Time{}
	}
	dateStr := parts[1] // YYYYMMDD
	timeStr := parts[2] // HHMMSS
	if len(dateStr) != 8 || len(timeStr) != 6 {
		return time.Time{}
	}
	t, err := time.Parse("20060102150405", dateStr+timeStr)
	if err != nil {
		return time.Time{}
	}
	return t
}
