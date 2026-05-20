package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/veschin/GoLeM/internal/job"
)

// LogCmd prints the changelog.txt from the job directory identified by jobID.
// It searches subagentsRoot using the same lookup strategy as FindJobDir.
// If changelog.txt is absent it prints "(no changelog)".
// If the job directory cannot be found it returns an exitcode.Error with
// category not_found (exit code 3).
func LogCmd(subagentsRoot, currentProjectID, jobID string, w io.Writer) error {
	jobDir, err := job.FindJobDir(subagentsRoot, currentProjectID, jobID)
	if err != nil {
		return fmt.Errorf("err:not_found")
	}

	data, err := os.ReadFile(filepath.Join(jobDir, "changelog.txt"))
	if err != nil {
		// changelog.txt does not exist: print fallback message.
		_, _ = fmt.Fprint(w, "(no changelog)")
		return nil
	}

	_, err = fmt.Fprint(w, string(data))
	return err
}
