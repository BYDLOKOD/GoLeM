package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/veschin/GoLeM/internal/job"
)

// ResultResult holds the outcome of a ResultCmd call.
type ResultResult struct {
	// Stdout is the content printed to stdout (from stdout.txt).
	Stdout string
	// Stderr is the content printed to stderr (from stderr.txt, as a warning).
	Stderr string
	// ExitCode is 0 on success, 1 on user error, 3 if not found.
	ExitCode int
	// Deleted is true if the job directory was auto-deleted.
	Deleted bool
}

// ResultCmd retrieves and prints the output of a completed job:
//   - Returns err:user "Job is still running" (exit 1) if status == running.
//   - Returns err:user "Job is still queued" (exit 1) if status == queued.
//   - For failed / timeout / permission_error: prints stderr.txt to stderr as a
//     warning and stdout.txt to stdout, then auto-deletes the job directory.
//   - For done: prints stdout.txt to stdout and auto-deletes the job directory.
//   - Returns exit code 3 with err:not_found if the job does not exist.
func ResultCmd(jobID, subagentsRoot, currentProjectID string, stdout, stderr io.Writer) (*ResultResult, error) {
	// Find the job directory
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return &ResultResult{ExitCode: 3}, fmt.Errorf(`err:not_found "Job not found: %s"`, jobID)
	}

	// Read the status
	status := job.ReadStatus(jobDir)

	// Check if job is still running or queued
	if status == job.StatusRunning {
		return &ResultResult{ExitCode: 1}, fmt.Errorf(`err:user "Job is still running"`)
	}
	if status == job.StatusQueued {
		return &ResultResult{ExitCode: 1}, fmt.Errorf(`err:user "Job is still queued"`)
	}

	// Read stdout.txt
	stdoutData, _ := os.ReadFile(jobDir + "/stdout.txt")
	_, _ = fmt.Fprint(stdout, string(stdoutData))

	// For failed/timeout/permission_error, print stderr.txt as warning
	if status == job.StatusFailed || status == job.StatusTimeout || status == job.StatusPermissionError {
		stderrData, _ := os.ReadFile(jobDir + "/stderr.txt")
		if len(stderrData) > 0 {
			_, _ = fmt.Fprint(stderr, string(stderrData))
		}
	}

	// Auto-delete the job directory
	_ = job.DeleteJob(jobDir)

	return &ResultResult{
		Stdout:   string(stdoutData),
		ExitCode: 0,
		Deleted:  true,
	}, nil
}
