// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veschin/GoLeM/internal/job"
)

// JobListItem is the JSON representation of a job in the list output.
type JobListItem struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	ProjectID string `json:"project_id"`
}

// JobStatusJSON is the JSON representation returned by "glm status --json".
type JobStatusJSON struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

// JobResultJSON is the JSON representation returned by "glm result --json".
type JobResultJSON struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	Changelog       string  `json:"changelog"`
	DurationSeconds int     `json:"duration_seconds"`
	ExitCode        *int    `json:"exit_code,omitempty"`
}

// JobLogJSON is the JSON representation returned by "glm log --json".
type JobLogJSON struct {
	ID      string   `json:"id"`
	Changes []string `json:"changes"`
}

// JSONOutput encodes v as indented JSON and writes it to w followed by a newline.
// This is the canonical helper used by all --json sub-commands.
// For nil slices, outputs "[]" instead of "null".
func JSONOutput(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// json.MarshalIndent returns "null" for nil slices; we want "[]"
	if string(data) == "null" {
		data = []byte("[]")
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// FormatJSON encodes v to a JSON byte slice.
// For nil slices, returns "[]" instead of "null".
func FormatJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// json.Marshal returns "null" for nil slices; we want "[]"
	if len(data) == 4 && string(data) == "null" {
		return []byte("[]"), nil
	}
	return data, nil
}

// scanAllJobs scans subagentsRoot for all jobs and returns JobEntry slices.
// It scans both project-scoped directories and legacy flat layout.
func scanAllJobs(subagentsRoot string) ([]JobEntry, error) {
	entries, err := os.ReadDir(subagentsRoot)
	if err != nil {
		// If root doesn't exist or is unreadable, return empty (not error)
		return nil, nil
	}

	var jobs []JobEntry

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(subagentsRoot, entry.Name())
		// Skip special files/directories
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		// Check if this is a legacy flat job directory (contains status file)
		if _, err := os.Stat(filepath.Join(projectDir, "status")); err == nil {
			// Legacy flat layout: subagentsRoot/jobID
			jobEntry, err := readJobEntry(entry.Name(), projectDir, entry.Name())
			if err == nil {
				jobs = append(jobs, jobEntry)
			}
			continue
		}

		// Project-scoped layout: subagentsRoot/projectID/jobID
		jobDirs, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, jobEntry := range jobDirs {
			if !jobEntry.IsDir() {
				continue
			}
			jobDir := filepath.Join(projectDir, jobEntry.Name())
			// Check if it's a job directory (has status file)
			if _, err := os.Stat(filepath.Join(jobDir, "status")); err == nil {
				entry, err := readJobEntry(jobEntry.Name(), jobDir, entry.Name())
				if err == nil {
					jobs = append(jobs, entry)
				}
			}
		}
	}

	// Sort by started_at descending (nil times sort last)
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

	return jobs, nil
}

// readJobEntry reads a job directory and returns a JobEntry.
func readJobEntry(jobID, jobDir, projectID string) (JobEntry, error) {
	status := string(job.ReadStatus(jobDir))

	var startedAt *time.Time
	data, err := os.ReadFile(filepath.Join(jobDir, "started_at.txt"))
	if err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err == nil {
			startedAt = &t
		}
	}

	return JobEntry{
		JobID:     jobID,
		Status:    status,
		StartedAt: startedAt,
		Dir:       jobDir,
	}, nil
}

// ListJSON reads all jobs from subagentsRoot, applies filter, and writes a
// JSON array of JobListItem objects to w.
// If there are no jobs it writes "[]" (never null).
func ListJSON(subagentsRoot string, filter *FilterOptions, w io.Writer) error {
	jobs, err := scanAllJobs(subagentsRoot)
	if err != nil {
		return err
	}

	// Convert to JobListItem for JSON output
	var items []JobListItem
	for _, job := range jobs {
		projectID := filepath.Base(filepath.Dir(job.Dir))
		startedAtStr := ""
		if job.StartedAt != nil {
			startedAtStr = job.StartedAt.Format(time.RFC3339)
		}
		items = append(items, JobListItem{
			ID:        job.JobID,
			Status:    job.Status,
			StartedAt: startedAtStr,
			ProjectID: projectID,
		})
	}

	// Apply filters to items
	if filter != nil {
		items = filterJobListItems(items, filter)
	}

	return JSONOutput(w, items)
}

// filterJobListItems applies filters to JobListItem slices.
func filterJobListItems(items []JobListItem, filter *FilterOptions) []JobListItem {
	var result []JobListItem
	for _, item := range items {
		// Status filter
		if len(filter.Statuses) > 0 {
			statusMatch := false
			for _, s := range filter.Statuses {
				if item.Status == s {
					statusMatch = true
					break
				}
			}
			if !statusMatch {
				continue
			}
		}
		// Project prefix filter
		if filter.ProjectPrefix != "" {
			if !strings.HasPrefix(item.ProjectID, filter.ProjectPrefix) {
				continue
			}
		}
		// Since filter
		if !filter.Since.IsZero() {
			if item.StartedAt == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, item.StartedAt)
			if err != nil || t.Before(filter.Since) {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

// StatusJSON reads a single job's status and writes a JSON object to w.
// It reconciles stale running jobs before responding.
func StatusJSON(subagentsRoot, currentProjectID, jobID string, w io.Writer) error {
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return err
	}

	// Read status
	status := string(job.ReadStatus(jobDir))

	// A "running" status is only trustworthy while the process is still alive.
	// CheckJobPID verifies liveness via signal 0 and, when the process is gone,
	// flips the on-disk status to "failed" before we report it -- otherwise a
	// crashed job would be reported as "running" forever. On a reconcile error
	// we keep the last known status rather than masking it.
	if status == "running" {
		if reconciled, err := job.CheckJobPID(jobDir); err == nil {
			status = reconciled
		}
	}

	var startedAt string
	data, err := os.ReadFile(filepath.Join(jobDir, "started_at.txt"))
	if err == nil {
		startedAt = strings.TrimSpace(string(data))
	}

	// Always read PID if the file exists, regardless of current status
	pid := 0
	pidData, err := os.ReadFile(filepath.Join(jobDir, "pid.txt"))
	if err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(pidData)))
	}

	result := JobStatusJSON{
		ID:        jobID,
		Status:    status,
		PID:       pid,
		StartedAt: startedAt,
	}
	return JSONOutput(w, result)
}

// ResultJSON reads a job's stdout/stderr/changelog and writes a JSON object to w.
func ResultJSON(subagentsRoot, currentProjectID, jobID string, w io.Writer) error {
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return err
	}

	status := string(job.ReadStatus(jobDir))

	stdout, _ := os.ReadFile(filepath.Join(jobDir, "stdout.txt"))
	stderr, _ := os.ReadFile(filepath.Join(jobDir, "stderr.txt"))
	changelog, _ := os.ReadFile(filepath.Join(jobDir, "changelog.txt"))

	durationSeconds := 0
	if data, err := os.ReadFile(filepath.Join(jobDir, "duration_seconds.txt")); err == nil {
		durationSeconds, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}

	var exitCode *int
	if data, err := os.ReadFile(filepath.Join(jobDir, "exit_code.txt")); err == nil {
		if ec, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			exitCode = &ec
		}
	}

	result := JobResultJSON{
		ID:              jobID,
		Status:          status,
		Stdout:          string(stdout),
		Stderr:          string(stderr),
		Changelog:       string(changelog),
		DurationSeconds: durationSeconds,
		ExitCode:        exitCode,
	}
	return JSONOutput(w, result)
}

// LogJSON reads a job's changelog and writes a JSON object with a "changes" array to w.
func LogJSON(subagentsRoot, currentProjectID, jobID string, w io.Writer) error {
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Join(jobDir, "changelog.txt"))
	if err != nil {
		// Empty changelog
		data = []byte("")
	}

	content := strings.TrimSpace(string(data))
	var changes []string
	if content != "" {
		changes = strings.Split(content, "\n")
	}

	result := JobLogJSON{
		ID:      jobID,
		Changes: changes,
	}
	return JSONOutput(w, result)
}
