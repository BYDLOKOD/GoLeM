package dag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/veschin/GoLeM/internal/artifact"
	"github.com/veschin/GoLeM/internal/claude"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/validation"
)

// ClaudeStepExecutor implements StepExecutor by running each step as a
// Claude subprocess invocation. Each step gets its own job directory
// under baseDir.
type ClaudeStepExecutor struct {
	cfg          *config.Config
	baseDir      string
	workDir      string
	model        string
	timeout      int
	systemPrompt string
}

// NewClaudeStepExecutor creates an executor that runs steps via the
// Claude CLI. baseDir is the parent directory for step job directories.
// workDir is passed to claude.Config.WorkDir. model and timeout are
// defaults (steps can override). systemPrompt is the assembled system prompt
// string to pass to each step invocation (empty means no system prompt override).
func NewClaudeStepExecutor(cfg *config.Config, baseDir, workDir, model string, timeout int, systemPrompt string) *ClaudeStepExecutor {
	return &ClaudeStepExecutor{
		cfg:          cfg,
		baseDir:      baseDir,
		workDir:      workDir,
		model:        model,
		timeout:      timeout,
		systemPrompt: systemPrompt,
	}
}

// Execute runs a single DAG step by invoking the Claude CLI subprocess.
// For gate steps, it validates upstream output. For normal steps, it
// creates a job directory, builds the prompt with injected input artifacts,
// runs the Claude CLI, validates output, and retries on validation failure
// when retry is configured.
func (e *ClaudeStepExecutor) Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	// Gate steps validate upstream output instead of running a prompt.
	if step.Type == "gate" {
		return e.executeGate(step, inputs)
	}

	maxAttempts := 1
	var feedback string
	if step.Retry != nil && step.Retry.MaxAttempts > 1 {
		maxAttempts = step.Retry.MaxAttempts
		feedback = step.Retry.Feedback
	}

	basePrompt := buildInjectedPrompt(inputs, step.Prompt)

	stdout, err := retryExecute(maxAttempts, feedback, func(p string) (string, error) {
		return e.runStep(ctx, step, p)
	}, basePrompt)
	if err != nil {
		return nil, err
	}

	return []*artifact.Artifact{
		artifact.NewText(step.ID, stdout),
	}, nil
}

// runStep executes a single attempt of a non-gate step: creates a job directory,
// runs the Claude CLI, reads stdout, and validates output. The context is
// propagated to claude.Execute so pipeline cancellation aborts in-flight steps.
func (e *ClaudeStepExecutor) runStep(ctx context.Context, step Step, prompt string) (string, error) {
	jobDir, err := os.MkdirTemp(e.baseDir, fmt.Sprintf("step-%s-*", step.ID))
	if err != nil {
		return "", fmt.Errorf("dag: create job dir for step %s: %w", step.ID, err)
	}

	model := step.Model
	if model == "" {
		model = e.model
	}
	if model == "" && e.cfg != nil {
		model = e.cfg.Model
	}

	timeout := step.Timeout
	if timeout <= 0 {
		timeout = e.timeout
	}
	if timeout <= 0 {
		timeout = config.DefaultTimeout
	}

	claudeCfg := claude.Config{
		Model:        model,
		Prompt:       prompt,
		WorkDir:      e.workDir,
		TimeoutSecs:  timeout,
		JobDir:       jobDir,
		SystemPrompt: e.systemPrompt,
	}

	if e.cfg != nil {
		claudeCfg.ZAIAPIKey = e.cfg.ZaiAPIKey
		claudeCfg.ZAIBaseURL = e.cfg.ZaiBaseURL
		claudeCfg.ZAIAPITimeoutMS = e.cfg.ZaiAPITimeoutMs
		claudeCfg.OpusModel = e.cfg.OpusModel
		claudeCfg.SonnetModel = e.cfg.SonnetModel
		claudeCfg.HaikuModel = e.cfg.HaikuModel
		claudeCfg.PermissionMode = e.cfg.PermissionMode
	}

	exitCode, execErr := claude.Execute(ctx, claudeCfg)

	_ = claude.ParseRawJSON(jobDir)

	stdoutData, _ := os.ReadFile(filepath.Join(jobDir, "stdout.txt"))
	stdout := string(stdoutData)

	if execErr != nil {
		return "", fmt.Errorf("dag: step %q failed (exit %d): %w", step.ID, exitCode, execErr)
	}

	if exitCode != 0 {
		stderrData, _ := os.ReadFile(filepath.Join(jobDir, "stderr.txt"))
		// Preserve jobDir on subprocess failure so the failure can be inspected.
		return "", fmt.Errorf("dag: step %q failed (exit %d): %s", step.ID, exitCode, string(stderrData))
	}

	if err := applyValidation(step, stdout); err != nil {
		// Preserve jobDir on validation failure too: raw.json, stderr.txt and
		// other artifacts are essential for diagnosing why output didn't match
		// the rule. Deleting first would destroy the only forensic trail.
		return "", err
	}

	// Step succeeded AND validation passed -- safe to discard scratch dir.
	_ = os.RemoveAll(jobDir)

	return stdout, nil
}

// applyValidation checks step output against the step's validation rule.
// Returns nil if the step has no validation rule.
func applyValidation(step Step, stdout string) error {
	if step.Validate == nil {
		return nil
	}
	return step.Validate.Check(stdout)
}

// executeGate handles gate steps by combining upstream artifact content and
// running validation against it. Gate steps pass through their input artifacts
// unchanged when validation succeeds.
func (e *ClaudeStepExecutor) executeGate(step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	var combined string
	for _, inp := range inputs {
		combined += string(inp.Content)
	}
	if err := step.Validate.Check(combined); err != nil {
		return nil, fmt.Errorf("gate step %q validation failed: %w", step.ID, err)
	}
	return inputs, nil
}

// isValidationError checks whether an error wraps a *validation.ValidationError.
func isValidationError(err error) bool {
	var ve *validation.ValidationError
	return errors.As(err, &ve)
}

// retryExecute runs fn up to maxAttempts times. On each retry after the first,
// feedback is appended to the prompt. Only validation errors trigger retries;
// other errors are returned immediately.
func retryExecute(maxAttempts int, feedback string, fn func(prompt string) (string, error), basePrompt string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		currentPrompt := basePrompt
		if attempt > 0 && feedback != "" {
			currentPrompt = basePrompt + "\n\n" + feedback
		}
		result, err := fn(currentPrompt)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isValidationError(err) {
			return "", err
		}
	}
	return "", lastErr
}

// buildInjectedPrompt constructs the effective prompt for a step by
// prepending dependency artifacts. If there are no inputs, the original
// prompt is returned unchanged.
func buildInjectedPrompt(inputs []*artifact.Artifact, prompt string) string {
	if len(inputs) == 0 {
		return prompt
	}

	var injected string
	for _, inp := range inputs {
		injected += fmt.Sprintf("Previous agent result (from step %q):\n%s\n\n", inp.StepID, string(inp.Content))
	}

	return fmt.Sprintf("%sYour task:\n%s", injected, prompt)
}

// BuildLinearDAG creates a DAG from an ordered list of prompts where
// each step depends on the previous one. Step IDs are "step-0",
// "step-1", etc.
func BuildLinearDAG(prompts []string) *DAG {
	steps := make([]Step, len(prompts))
	for i, p := range prompts {
		steps[i] = Step{
			ID:     fmt.Sprintf("step-%d", i),
			Prompt: p,
		}
		if i > 0 {
			steps[i].DependsOn = []string{steps[i-1].ID}
		}
	}
	return &DAG{Steps: steps}
}
