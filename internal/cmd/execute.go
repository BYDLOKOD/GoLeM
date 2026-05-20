package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/veschin/GoLeM/internal/claude"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/event"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/prompt"
	"github.com/veschin/GoLeM/internal/slot"
)

// ExecuteJobParams holds all parameters needed to execute a subagent job.
type ExecuteJobParams struct {
	// Cfg is the loaded GoLeM configuration.
	Cfg *config.Config
	// Flags are the parsed command-line flags (prompt, timeout, dir, models).
	Flags *Flags
	// SubagentsRoot is the base directory for job storage.
	SubagentsRoot string
	// ProjectID is the resolved project identifier.
	ProjectID string
	// AutoDelete controls whether the job directory is removed after execution.
	AutoDelete bool
	// SlotManager is required. The caller must create it via reconcileAndInitSlots.
	// ExecuteJob releases the slot in a defer after execution.
	SlotManager *slot.SlotManager
	// JobID is optional. If set and the job directory already exists,
	// ExecuteJob skips job.NewJob() and uses the existing directory.
	// If empty, ExecuteJob generates a new job ID and creates the directory.
	JobID string
	// Bus is an optional event bus. When set, lifecycle events (JobQueued,
	// JobRunning, JobDone, JobFailed, JobTimeout, SlotAcquired, SlotReleased)
	// are published at appropriate points. Nil means no events.
	Bus *event.Bus
}

// ExecuteJobResult holds the outcome of a job execution.
type ExecuteJobResult struct {
	// JobID is the unique identifier of the created job.
	JobID string
	// ExitCode is the claude subprocess exit code (0 = success, 124 = timeout, etc).
	ExitCode int
	// Stdout is the parsed text output from claude.
	Stdout string
	// Stderr is the raw stderr output from claude.
	Stderr string
	// Status is the mapped job status (done, failed, timeout, permission_error).
	Status string
	// JobDir is the absolute path to the job directory (may be deleted if AutoDelete).
	JobDir string
	// Changelog is the content of changelog.txt (read before auto-delete).
	Changelog string
}

// ExecuteJob creates a job, runs the claude subprocess, parses results,
// releases the slot, and returns the outcome.
//
// The caller is responsible for:
//   - Reconciliation and slot manager initialization (reconcileAndInitSlots).
//   - Calling WaitForSlot on the SlotManager before calling ExecuteJob.
//
// ExecuteJob releases the slot via defer when done.
//
// The function is synchronous: it blocks until the claude subprocess exits or
// the context is cancelled. The context is propagated to claude.Execute, so
// cancelling it terminates the subprocess and yields exit code 124 (timeout).
// For async usage, call it in a goroutine.
func ExecuteJob(ctx context.Context, params ExecuteJobParams) (*ExecuteJobResult, error) {
	// Always release the slot when done (single defer, no manual release).
	defer func() {
		if releaseErr := params.SlotManager.ReleaseSlot(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warn: release slot: %v\n", releaseErr)
		}
	}()

	// Resolve or create the job.
	var j *job.Job
	jobID := params.JobID
	if jobID != "" {
		// Check if the job directory already exists (pre-created by cmdStart).
		jobDir := filepath.Join(params.SubagentsRoot, params.ProjectID, jobID)
		if info, err := os.Stat(jobDir); err == nil && info.IsDir() {
			// Reuse existing job directory.
			j = &job.Job{ID: jobID, ProjectID: params.ProjectID, Dir: jobDir}
		}
	}
	if j == nil {
		// No pre-created job -- generate a new one.
		if jobID == "" {
			jobID = job.GenerateJobID()
		}
		var err error
		j, err = job.NewJob(params.SubagentsRoot, params.ProjectID, jobID)
		if err != nil {
			return nil, fmt.Errorf("create job: %w", err)
		}
	}

	// Wire event bus into the job and emit queued event.
	j.SetBus(params.Bus)
	j.EmitQueued()

	// Write PID only for new jobs (not pre-created by caller).
	if params.JobID == "" {
		pid := os.Getpid()
		_ = os.WriteFile(filepath.Join(j.Dir, "pid.txt"), []byte(strconv.Itoa(pid)), 0o644)
	}

	// Transition to running.
	_ = j.StatusTransition(job.StatusRunning)

	// Build claude config and wire event bus.
	claudeCfg, err := BuildClaudeConfig(params.Cfg, params.Flags, j.Dir)
	if err != nil {
		return nil, fmt.Errorf("build claude config: %w", err)
	}
	claudeCfg.Bus = params.Bus
	claudeCfg.JobID = jobID
	claudeCfg.ProjectID = params.ProjectID

	// Execute claude subprocess. The caller's context is propagated so that
	// cancellation kills the subprocess (mapped to exit code 124).
	exitCode, _ := claude.Execute(ctx, claudeCfg)

	// Parse raw.json into stdout.txt + changelog.txt.
	_ = claude.ParseRawJSON(j.Dir)

	// Determine final status.
	stderrData, _ := os.ReadFile(filepath.Join(j.Dir, "stderr.txt"))
	finalStatus := claude.MapStatus(exitCode, string(stderrData))
	_ = os.WriteFile(filepath.Join(j.Dir, "status"), []byte(finalStatus), 0o644)

	// Read stdout and changelog before potential auto-delete.
	stdoutData, _ := os.ReadFile(filepath.Join(j.Dir, "stdout.txt"))
	changelogData, _ := os.ReadFile(filepath.Join(j.Dir, "changelog.txt"))

	result := &ExecuteJobResult{
		JobID:     jobID,
		ExitCode:  exitCode,
		Stdout:    string(stdoutData),
		Stderr:    string(stderrData),
		Status:    finalStatus,
		JobDir:    j.Dir,
		Changelog: string(changelogData),
	}

	// Auto-delete if requested.
	if params.AutoDelete {
		_ = job.DeleteJob(j.Dir)
	}

	return result, nil
}

// BuildClaudeConfig creates a claude.Config from the loaded config and parsed flags.
func BuildClaudeConfig(cfg *config.Config, flags *Flags, jobDir string) (claude.Config, error) {
	opusModel := cfg.OpusModel
	sonnetModel := cfg.SonnetModel
	haikuModel := cfg.HaikuModel

	if flags.Model != "" {
		opusModel = flags.Model
		sonnetModel = flags.Model
		haikuModel = flags.Model
	}
	if flags.OpusModel != "" {
		opusModel = flags.OpusModel
	}
	if flags.SonnetModel != "" {
		sonnetModel = flags.SonnetModel
	}
	if flags.HaikuModel != "" {
		haikuModel = flags.HaikuModel
	}

	// Smart routing: select execution model based on prompt complexity.
	execModel := SelectModel(cfg, flags, flags.Prompt)

	permMode := cfg.PermissionMode
	if flags.PermissionMode != "" {
		permMode = flags.PermissionMode
	}

	// Determine base system prompt: flags override config default.
	baseSystemPrompt := flags.SystemPrompt
	if baseSystemPrompt == "" {
		baseSystemPrompt = cfg.SystemPrompt
	}

	// Assemble final system prompt from constraints + base text.
	finalSystemPrompt, err := prompt.AssembleSystemPrompt(flags.Constraints, baseSystemPrompt)
	if err != nil {
		return claude.Config{}, err
	}

	return claude.Config{
		ZAIAPIKey:              cfg.ZaiAPIKey,
		ZAIBaseURL:             cfg.ZaiBaseURL,
		ZAIAPITimeoutMS:        cfg.ZaiAPITimeoutMs,
		OpusModel:              opusModel,
		SonnetModel:            sonnetModel,
		HaikuModel:             haikuModel,
		PermissionMode:         permMode,
		Model:                  execModel,
		Prompt:                 flags.Prompt,
		WorkDir:                flags.Dir,
		TimeoutSecs:            flags.Timeout,
		JobDir:                 jobDir,
		Effort:                 cfg.Effort,
		ExcludeDynamicSections: cfg.ExcludeDynamicSections,
		SystemPrompt:           finalSystemPrompt,
	}, nil
}
