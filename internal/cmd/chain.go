// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/dag"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/slot"
	"github.com/veschin/GoLeM/internal/validation"
)

// ChainResult holds the outcome of a ChainCmd call.
type ChainResult struct {
	// FinalStdout is the stdout from the last executed step.
	FinalStdout string
	// ExitCode is 0 if all steps succeeded, 1 if any step failed.
	ExitCode int
	// StepsExecuted is the count of steps that were actually run.
	StepsExecuted int
	// StepsSkipped is the count of steps that were not run (due to failure).
	StepsSkipped int
	// JobDirs is the list of job directory paths for all executed steps.
	JobDirs []string
}

// ChainStep is a single step in a chain, with optional validation and retry.
type ChainStep struct {
	// Prompt is the instruction to execute.
	Prompt string
	// Validate is an optional output validation rule checked after successful execution.
	Validate *validation.ValidationRule
	// Retry configures automatic retries on failure or validation error.
	Retry *dag.RetryConfig
}

// ChainStepsFromPrompts converts a plain prompt list into ChainStep entries
// with no validation or retry configuration.
func ChainStepsFromPrompts(prompts []string) []ChainStep {
	steps := make([]ChainStep, len(prompts))
	for i, p := range prompts {
		steps[i] = ChainStep{Prompt: p}
	}
	return steps
}

// ChainFlags holds options specific to the chain subcommand.
type ChainFlags struct {
	// Flags embeds the common run flags (Dir, Timeout, Model, etc.).
	Flags *Flags
	// ContinueOnError instructs the chain to keep running even when a step fails.
	ContinueOnError bool
	// Prompts is the ordered list of prompts to execute.
	Prompts []string
	// Steps is the ordered list of chain steps (takes precedence over Prompts).
	Steps []ChainStep
}

// ChainCmd executes a sequence of prompts as separate jobs, injecting the
// previous job's stdout into the next prompt using the format:
//
//	"Previous agent result:\n{stdout}\n\nYour task:\n{prompt}"
//
// Progress is written to stderr as "[N/M] Running step N...".
// By default the chain stops at the first failure. With ContinueOnError set
// it continues and still injects stdout from the failed step.
// The final exit code is 0 only when all steps succeed; 1 if any step failed.
//
// Each step acquires and releases its own concurrency slot via a freshly
// initialised SlotManager. The cfg parameter provides ZAI API credentials
// and routing settings required by ExecuteJob.
func ChainCmd(cf *ChainFlags, cfg *config.Config, subagentsRoot, projectID string, stdout, stderr io.Writer) (*ChainResult, error) {
	steps := cf.Steps
	if len(steps) == 0 {
		steps = ChainStepsFromPrompts(cf.Prompts)
	}
	total := len(steps)

	result := &ChainResult{
		JobDirs: make([]string, 0, total),
	}

	prevStdout := ""
	anyFailed := false
	// anyValidationFailed tracks whether any step that had a Validate rule
	// produced a validation failure.  anyValidationConfigured is set whenever
	// at least one step carries a non-nil Validate rule.  Together they allow
	// the final exit code to be determined by validation outcomes alone when
	// validation is in use: a chain whose validated steps all pass succeeds
	// even if unvalidated steps exit non-zero.
	anyValidationFailed := false
	anyValidationConfigured := false

	// Reconcile stale jobs once at the start of the chain.
	if err := job.Reconcile(subagentsRoot, time.Now()); err != nil {
		// Non-fatal: log to stderr and continue.
		_, _ = fmt.Fprintf(stderr, "warn: reconcile: %v\n", err)
	}

	// One SlotManager per step: Init runs once, then each attempt only does
	// WaitForSlot (claim) and ExecuteJob's defer does ReleaseSlot. This avoids
	// the structurally confusing "new manager per attempt" pattern that would
	// be a real double-claim bug the moment slot limits start being enforced.
	for i, step := range steps {
		stepNum := i + 1

		maxAttempts := 1
		if step.Retry != nil && step.Retry.MaxAttempts > 1 {
			maxAttempts = step.Retry.MaxAttempts
		}

		currentPromptText := step.Prompt
		var stepResult *ExecuteJobResult

		stepSlotMgr := slot.NewSlotManager(subagentsRoot, 0) // 0 = unlimited
		if err := stepSlotMgr.Init(); err != nil {
			return nil, fmt.Errorf("chain step %d: slot init: %w", stepNum, err)
		}

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			// Print progress to stderr.
			if attempt > 1 {
				_, _ = fmt.Fprintf(stderr, "[%d/%d] Retrying step %d (attempt %d/%d)...\n", stepNum, total, stepNum, attempt, maxAttempts)
			} else {
				_, _ = fmt.Fprintf(stderr, "[%d/%d] Running step %d...\n", stepNum, total, stepNum)
			}

			// Build the prompt for this step, injecting previous output when present.
			var prompt string
			if i == 0 {
				prompt = currentPromptText
			} else {
				prompt = BuildChainPrompt(prevStdout, currentPromptText)
			}

			// Build per-step flags with the resolved prompt.
			stepFlags := &Flags{
				Dir:            cf.Flags.Dir,
				Timeout:        cf.Flags.Timeout,
				Model:          cf.Flags.Model,
				OpusModel:      cf.Flags.OpusModel,
				SonnetModel:    cf.Flags.SonnetModel,
				HaikuModel:     cf.Flags.HaikuModel,
				PermissionMode: cf.Flags.PermissionMode,
				SystemPrompt:   cf.Flags.SystemPrompt,
				Constraints:    cf.Flags.Constraints,
				Prompt:         prompt,
			}

			// Acquire the slot for this attempt. ExecuteJob's defer releases
			// it before returning, so the next attempt can claim cleanly.
			if err := stepSlotMgr.WaitForSlot(); err != nil {
				return nil, fmt.Errorf("chain step %d: slot wait: %w", stepNum, err)
			}

			// ExecuteJob runs the claude subprocess and releases the slot via defer.
			var err error
			stepResult, err = ExecuteJob(context.Background(), ExecuteJobParams{
				Cfg:           cfg,
				Flags:         stepFlags,
				SubagentsRoot: subagentsRoot,
				ProjectID:     projectID,
				AutoDelete:    false,
				SlotManager:   stepSlotMgr,
			})
			if err != nil {
				return nil, fmt.Errorf("chain step %d: execute: %w", stepNum, err)
			}

			result.JobDirs = append(result.JobDirs, stepResult.JobDir)

			// When a Validate rule is configured it is the authoritative success
			// criterion for this step:
			//   - Validation passes → step is successful (clear any non-zero exit code).
			//   - Validation fails  → step is failed (retry if attempts remain).
			// When no Validate rule is configured, fall back to the claude exit code.
			if step.Validate != nil {
				anyValidationConfigured = true
				if verr := step.Validate.Check(stepResult.Stdout); verr != nil {
					_, _ = fmt.Fprintf(stderr, "[%d/%d] Validation failed for step %d: %v\n", stepNum, total, stepNum, verr)
					stepResult.ExitCode = 1
					anyValidationFailed = true
					if attempt < maxAttempts && step.Retry != nil {
						anyValidationFailed = false // will retry
						currentPromptText = step.Prompt + "\n\n" + step.Retry.Feedback
						continue
					}
					break
				}
				// Validation passed — treat step as successful regardless of exit code.
				stepResult.ExitCode = 0
				break
			}

			// No validation rule: use the claude exit code directly.
			if stepResult.ExitCode != 0 {
				if attempt < maxAttempts && step.Retry != nil {
					currentPromptText = step.Prompt + "\n\n" + step.Retry.Feedback
					continue
				}
				break
			}

			break
		}

		result.StepsExecuted++

		// Use this step's stdout as input for the next step.
		prevStdout = stepResult.Stdout

		if stepResult.ExitCode != 0 {
			anyFailed = true
			if !cf.ContinueOnError {
				// Stop chain; count remaining steps as skipped.
				result.StepsSkipped = total - stepNum
				break
			}
		}
	}

	// Capture final stdout from the last executed step.
	result.FinalStdout = prevStdout

	// Determine final exit code.
	// When at least one step carried a Validate rule the overall success is
	// decided by validation outcomes: only validation failures produce a
	// non-zero exit.  When no step used validation the raw execution exit
	// codes determine the result.
	if anyValidationConfigured {
		if anyValidationFailed {
			result.ExitCode = 1
		}
	} else if anyFailed {
		result.ExitCode = 1
	}

	return result, nil
}

// BuildChainPrompt formats the injected prompt for step N+1 given the previous
// step's stdout and the raw user prompt for step N+1.
//
// Format:
//
//	Previous agent result:
//	{prevStdout}
//
//	Your task:
//	{prompt}
func BuildChainPrompt(prevStdout, prompt string) string {
	return fmt.Sprintf("Previous agent result:\n%s\n\nYour task:\n%s", prevStdout, prompt)
}
