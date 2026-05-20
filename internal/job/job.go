// Package job manages the lifecycle of subagent jobs stored on the filesystem.
// Jobs are stored under ~/.claude/subagents/<project-id>/<job-id>/ with atomic
// writes and a strict status state machine.
package job

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/veschin/GoLeM/internal/event"
)

// Status represents the lifecycle state of a job.
type Status string

const (
	StatusQueued          Status = "queued"
	StatusRunning         Status = "running"
	StatusDone            Status = "done"
	StatusFailed          Status = "failed"
	StatusTimeout         Status = "timeout"
	StatusKilled          Status = "killed"
	StatusPermissionError Status = "permission_error"
)

// validStatuses is the set of all recognised status values.
var validStatuses = map[Status]bool{
	StatusQueued:          true,
	StatusRunning:         true,
	StatusDone:            true,
	StatusFailed:          true,
	StatusTimeout:         true,
	StatusKilled:          true,
	StatusPermissionError: true,
}

// allowedTransitions maps each status to the set of statuses it may legally
// transition into.
var allowedTransitions = map[Status][]Status{
	StatusQueued:  {StatusRunning},
	StatusRunning: {StatusDone, StatusFailed, StatusTimeout, StatusKilled, StatusPermissionError},
}

// ErrNotFound is returned by FindJobDir when the job directory cannot be
// located under any search path.
var ErrNotFound = errors.New("err:not_found")

// ValidateJobID checks that jobID contains only safe characters:
// lowercase alphanumeric, hyphens, and underscores.
// Returns an error if the ID is empty or contains path separators, dots,
// or other unsafe chars.
func ValidateJobID(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("err:validation job ID is empty")
	}
	for _, r := range jobID {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return fmt.Errorf("err:validation job ID contains invalid character: %q", r)
		}
	}
	return nil
}

// Job holds the metadata for a single subagent job.
type Job struct {
	ID        string
	ProjectID string
	Dir       string     // absolute path to the job directory
	Bus       *event.Bus // optional event bus
}

// SetBus sets the event bus on the Job. Returns the receiver for chaining.
func (j *Job) SetBus(bus *event.Bus) *Job {
	j.Bus = bus
	return j
}

// EmitQueued publishes a JobQueued event if the bus is set.
// Call this after SetBus if you want queued events.
func (j *Job) EmitQueued() {
	event.Publish(j.Bus, event.Event{
		Type:      event.JobQueued,
		JobID:     j.ID,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"project_id": j.ProjectID,
			"job_id":     j.ID,
		},
	})
}

// NewJob creates a new job directory under subagentsRoot/<projectID>/<jobID>/,
// writes the initial "queued" status file atomically, and returns the Job.
func NewJob(subagentsRoot, projectID, jobID string) (*Job, error) {
	if err := ValidateJobID(jobID); err != nil {
		return nil, err
	}
	dir := filepath.Join(subagentsRoot, projectID, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create job dir: %w", err)
	}

	j := &Job{ID: jobID, ProjectID: projectID, Dir: dir}
	if err := j.SetStatus(StatusQueued); err != nil {
		return nil, err
	}
	return j, nil
}

// GenerateJobID returns a new job ID in the format
// "job-YYYYMMDD-HHMMSS-XXXXXXXX" where XXXXXXXX is 4 random bytes encoded as
// lowercase hex.  The timestamp is taken from the current wall clock.
func GenerateJobID() string {
	now := time.Now().UTC()
	date := now.Format("20060102")
	timeOfDay := now.Format("150405")

	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		// In the extremely unlikely case of crypto/rand failure, panic.
		// This is preferable to returning non-unique IDs.
		panic(fmt.Sprintf("crypto/rand failure: %v", err))
	}

	return fmt.Sprintf("job-%s-%s-%s", date, timeOfDay, hex.EncodeToString(randomBytes))
}

// ResolveProjectID derives the project identifier from an absolute directory
// path using the format "{basename}-{cksum}" where cksum is the CRC32 IEEE
// checksum of the full path expressed as a decimal integer.
func ResolveProjectID(absPath string) string {
	base := filepath.Base(absPath)
	sum := crc32.ChecksumIEEE([]byte(absPath))
	return fmt.Sprintf("%s-%d", base, sum)
}

// FindJobDir searches for jobID in the following order:
//  1. subagentsRoot/<currentProjectID>/<jobID>   (current project scope)
//  2. subagentsRoot/<jobID>                       (legacy flat layout)
//  3. subagentsRoot/*/<jobID>                     (any other project directory)
//
// Returns the absolute path to the job directory, or ErrNotFound.
func FindJobDir(subagentsRoot, currentProjectID, jobID string) (string, error) {
	if err := ValidateJobID(jobID); err != nil {
		return "", err
	}
	// 1. Current project scope.
	candidate := filepath.Join(subagentsRoot, currentProjectID, jobID)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}

	// 2. Legacy flat layout.
	candidate = filepath.Join(subagentsRoot, jobID)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}

	// 3. Walk all sub-directories.
	entries, err := os.ReadDir(subagentsRoot)
	if err != nil {
		return "", ErrNotFound
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate = filepath.Join(subagentsRoot, e.Name(), jobID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}

	return "", ErrNotFound
}

// DeleteJob removes the entire job directory and all of its contents.
func DeleteJob(dir string) error {
	return os.RemoveAll(dir)
}

// ReadStatus reads the "status" file inside dir and returns the parsed Status.
// If the file is missing or contains an unrecognised value it returns
// StatusFailed and logs a warning (warning is printed to stderr as a stub).
func ReadStatus(dir string) Status {
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: job %s: cannot read status file: %v\n", dir, err)
		return StatusFailed
	}
	s := Status(data)
	if !validStatuses[s] {
		fmt.Fprintf(os.Stderr, "warning: job %s: unknown status %q, treating as failed\n", dir, s)
		return StatusFailed
	}
	return s
}

// SetStatus atomically writes newStatus to the "status" file inside j.Dir.
// It uses a temp file and os.Rename to guarantee atomicity.
func (j *Job) SetStatus(newStatus Status) error {
	return AtomicWrite(filepath.Join(j.Dir, "status"), []byte(newStatus))
}

// StatusTransition validates and performs a status transition on j.
// It returns an error if the transition is not permitted by the state machine.
// The entire read-check-write sequence is protected by an exclusive flock to
// prevent TOCTOU races when multiple processes transition the same job.
func (j *Job) StatusTransition(newStatus Status) error {
	lockPath := filepath.Join(j.Dir, "status.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("status lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("status flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	current := ReadStatus(j.Dir)
	allowed := allowedTransitions[current]
	for _, a := range allowed {
		if a == newStatus {
			return j.SetStatus(newStatus)
		}
	}
	return fmt.Errorf("invalid transition %s -> %s", current, newStatus)
}

// AtomicWrite writes data to path using a write-then-rename strategy so that
// readers never observe a partial write.  The temporary file is placed at
// path + ".tmp." + pid.
func AtomicWrite(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("atomic write (temp): %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write (rename): %w", err)
	}
	return nil
}
