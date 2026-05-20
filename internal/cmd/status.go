package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"

	"github.com/veschin/GoLeM/internal/job"
)

// StatusResult holds the outcome of a StatusCmd call.
type StatusResult struct {
	// Status is the single-word status string printed to stdout.
	Status string
	// ExitCode is 0 on success, 3 if the job is not found.
	ExitCode int
}

// StatusCmd prints the current status of the job identified by jobID:
//   - Looks up the job directory under subagentsRoot.
//   - If the status file reads "running", checks whether the PID in pid.txt
//     is still alive. If the PID is dead it updates the status to "failed".
//   - Prints the final status word followed by a newline to stdout.
//   - Returns exit code 3 with an err:not_found error if the job does not exist.
func StatusCmd(jobID, subagentsRoot, currentProjectID string, stdout io.Writer) (*StatusResult, error) {
	// Find the job directory
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return &StatusResult{ExitCode: 3}, fmt.Errorf(`err:not_found "Job not found: %s"`, jobID)
	}

	// Read the status
	status := job.ReadStatus(jobDir)

	// If status is "running", check if PID is still alive
	if status == job.StatusRunning {
		pidPath := jobDir + "/pid.txt"
		pidData, err := os.ReadFile(pidPath)
		if err == nil {
			pid, _ := strconv.Atoi(string(pidData))
			if pid > 0 {
				// Check if process is alive
				process, err := os.FindProcess(pid)
				if err == nil {
					err := process.Signal(syscall.Signal(0))
					if err != nil {
						// PID is dead, update status to failed
						_ = job.AtomicWrite(jobDir+"/status", []byte(job.StatusFailed))
						status = job.StatusFailed
					}
				}
			}
		}
	}

	// Print status to stdout
	_, _ = fmt.Fprintln(stdout, status)

	return &StatusResult{
		Status:   string(status),
		ExitCode: 0,
	}, nil
}
