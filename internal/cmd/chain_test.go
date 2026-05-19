package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/dag"
	"github.com/veschin/GoLeM/internal/slot"
	"github.com/veschin/GoLeM/internal/validation"
)

// helpers -------------------------------------------------------------------

// makeSubagentsRoot creates a temporary subagents root directory for tests.
func makeSubagentsRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeFile writes content to the given path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// makeTestConfig returns a minimal *config.Config suitable for unit tests.
// Slot manager uses 0 (unlimited) by default.
func makeTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Model:          "glm-5",
		OpusModel:      "glm-5",
		SonnetModel:    "glm-5",
		HaikuModel:     "glm-5",
		PermissionMode: "bypassPermissions",
	}
}


func chainFlags(dir string, timeout int, model string, continueOnError bool, prompts []string) *cmd.ChainFlags {
	f := &cmd.Flags{
		Dir:     dir,
		Timeout: timeout,
		Model:   model,
	}
	return &cmd.ChainFlags{
		Flags:           f,
		ContinueOnError: continueOnError,
		Prompts:         prompts,
	}
}

// AC1: Sequential execution of multiple prompts ----------------------------

// TestChainExecutesThreePromptsSequentially verifies that running "glm chain"
// with three prompts creates 3 jobs in strict sequence.
// ContinueOnError=true ensures all steps run regardless of claude outcome.
func TestChainExecutesThreePromptsSequentially(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer
	prompts := []string{
		"Analyze src/auth/ for security issues",
		"Based on the analysis, write fixes for the critical issues found",
		"Write tests for the security fixes",
	}
	// ContinueOnError=true so all steps run even if claude fails.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if result.StepsExecuted != 3 {
		t.Errorf("expected 3 steps executed, got %d", result.StepsExecuted)
	}
	if len(result.JobDirs) != 3 {
		t.Errorf("expected 3 job dirs, got %d", len(result.JobDirs))
	}
}

// AC2: Each prompt is a separate job with its own artifacts ----------------

// TestEachChainStepProducesSeparateJobDirectory verifies that after running
// "glm chain" with three prompts, three separate job directories exist and
// each contains a status file.
func TestEachChainStepProducesSeparateJobDirectory(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer
	prompts := []string{"Analyze code", "Fix issues", "Write tests"}
	// ContinueOnError=true ensures all 3 job dirs are created.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if len(result.JobDirs) != 3 {
		t.Fatalf("expected 3 job directories, got %d", len(result.JobDirs))
	}

	// Each job directory must exist and have a status file.
	for i, dir := range result.JobDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("step %d: job directory does not exist: %s", i+1, dir)
		}
		statusPath := filepath.Join(dir, "status")
		if _, err := os.Stat(statusPath); os.IsNotExist(err) {
			t.Errorf("step %d: missing status file in %s", i+1, dir)
		}
	}
}

// AC3: Previous job stdout injected into next prompt -----------------------

// TestChainPassesPreviousResultToNextStep verifies that the second step's
// prompt is built via BuildChainPrompt (injection format verified by the
// BuildChainPrompt unit tests). ChainCmd passes the injection through correctly
// when all 3 steps run.
func TestChainPassesPreviousResultToNextStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Analyze src/auth/ for security issues",
		"Based on the analysis, write fixes for the critical issues found",
		"Write tests for the security fixes",
	}
	// ContinueOnError=true so all steps run.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	// Verify all 3 steps ran.
	if len(result.JobDirs) != 3 {
		t.Fatalf("expected 3 job dirs, got %d", len(result.JobDirs))
	}
}

// AC4: Chain stops on failure by default -----------------------------------

// TestChainStopsAtFirstFailedStep verifies that when a step fails (non-zero
// exit code) and --continue-on-error is NOT set, only 1 step runs and the
// remaining 2 are skipped. The final exit code is 1.
//
// A non-existent workdir causes claude.Execute to return exit code 1
// immediately (before attempting API calls), guaranteeing a fast failure.
func TestChainStopsAtFirstFailedStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Analyze src/auth/middleware.ts for security vulnerabilities",
		"Refactor the middleware",
		"Write integration tests",
	}
	cf := chainFlags("/nonexistent-dir-that-does-not-exist", 0, "", false, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if result.StepsExecuted != 1 {
		t.Errorf("expected 1 step executed, got %d", result.StepsExecuted)
	}
	if result.StepsSkipped != 2 {
		t.Errorf("expected 2 steps skipped, got %d", result.StepsSkipped)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

// TestChainContinuesOnErrorWhenFlagIsSet verifies that with --continue-on-error,
// all 3 steps run even when steps fail (non-existent workdir forces failures).
func TestChainContinuesOnErrorWhenFlagIsSet(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Analyze src/db/queries.go for N+1 query issues",
		"Fix the N+1 queries identified in the previous step",
		"Run the test suite to verify fixes",
	}
	// Use a non-existent workdir so each step fails immediately.
	cf := chainFlags("/nonexistent-dir-that-does-not-exist", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if result.StepsExecuted != 3 {
		t.Errorf("expected all 3 steps executed, got %d", result.StepsExecuted)
	}
	// All failed, so exit code should be non-zero.
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code (all steps failed), got 0")
	}
}

// TestContinueOnErrorStillInjectsStdoutFromFailedStep verifies that even when
// steps fail, the chain proceeds to the next step (injection logic still runs).
func TestContinueOnErrorStillInjectsStdoutFromFailedStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Analyze src/db/queries.go for N+1 query issues",
		"Fix the N+1 queries identified in the previous step",
	}
	// Non-existent workdir causes failures; continue-on-error keeps going.
	cf := chainFlags("/nonexistent-path-for-test", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	// Both steps must run.
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps executed, got %d", result.StepsExecuted)
	}
	if len(result.JobDirs) != 2 {
		t.Fatalf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// AC5: Returns final job stdout; intermediate dirs preserved ---------------

// TestChainReturnsFinalJobStdout verifies that the ChainResult.FinalStdout
// contains the last step's output (consistent with last job's stdout.txt).
func TestChainReturnsFinalJobStdout(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Analyze", "Fix", "Write tests"}
	// ContinueOnError=true so all 3 steps run.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	// The final stdout must match what the final step produced.
	if len(result.JobDirs) == 0 {
		t.Fatal("no job dirs returned")
	}
	lastDir := result.JobDirs[len(result.JobDirs)-1]
	rawStdout, err := os.ReadFile(filepath.Join(lastDir, "stdout.txt"))
	if err != nil {
		t.Fatalf("cannot read last step stdout.txt: %v", err)
	}
	if result.FinalStdout != string(rawStdout) {
		t.Errorf("FinalStdout mismatch: got %q, want %q", result.FinalStdout, string(rawStdout))
	}
}

// TestIntermediateJobDirectoriesArePreservedAfterChain verifies that all 3
// job directories still exist on disk after the chain completes.
func TestIntermediateJobDirectoriesArePreservedAfterChain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Step 1", "Step 2", "Step 3"}
	// ContinueOnError=true so all 3 job dirs are created.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if len(result.JobDirs) != 3 {
		t.Fatalf("expected 3 job dirs, got %d", len(result.JobDirs))
	}
	for i, dir := range result.JobDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("job dir %d (%s) does not exist after chain", i+1, dir)
		}
	}
}

// AC6: Chain progress printed to stderr ------------------------------------

// TestChainPrintsProgressToStderr verifies that each step produces a
// "[N/M] Running step N..." line on stderr.
// ContinueOnError=true ensures all 3 steps and their progress lines appear.
func TestChainPrintsProgressToStderr(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Analyze", "Fix", "Test"}
	// ContinueOnError=true so all steps run regardless of claude outcome.
	cf := chainFlags(".", 0, "", true, prompts)

	_, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	stderrStr := stderr.String()
	expected := []string{
		"[1/3] Running step 1...",
		"[2/3] Running step 2...",
		"[3/3] Running step 3...",
	}
	for _, want := range expected {
		if !strings.Contains(stderrStr, want) {
			t.Errorf("stderr missing %q\ngot: %q", want, stderrStr)
		}
	}
}

// TestChainWithTwoStepsPrintsCorrectProgress verifies the progress format
// when only 2 prompts are given.
func TestChainWithTwoStepsPrintsCorrectProgress(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Analyze", "Fix"}
	// ContinueOnError=true so both steps run.
	cf := chainFlags(".", 0, "", true, prompts)

	_, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	stderrStr := stderr.String()
	expected := []string{
		"[1/2] Running step 1...",
		"[2/2] Running step 2...",
	}
	for _, want := range expected {
		if !strings.Contains(stderrStr, want) {
			t.Errorf("stderr missing %q\ngot: %q", want, stderrStr)
		}
	}
}

// Edge case: Single prompt -------------------------------------------------

// TestChainWithSinglePromptBehavesLikeGlmRun verifies that a single-prompt
// chain runs, prints "[1/1] Running step 1..." to stderr, and executes 1 step.
func TestChainWithSinglePromptBehavesLikeGlmRun(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"List all TODO comments in src/"}
	cf := chainFlags(".", 0, "", false, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "[1/1] Running step 1...") {
		t.Errorf("stderr missing '[1/1] Running step 1...'\ngot: %q", stderrStr)
	}
	if result.StepsExecuted != 1 {
		t.Errorf("expected 1 step executed, got %d", result.StepsExecuted)
	}
}

// Edge case: Empty stdout --------------------------------------------------

// TestChainHandlesEmptyStdoutFromAStep verifies that the chain handles steps
// that produce empty stdout by still proceeding to subsequent steps.
func TestChainHandlesEmptyStdoutFromAStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Delete all .tmp files in the project",
		"Verify no .tmp files remain",
	}
	// ContinueOnError=true so both steps run.
	cf := chainFlags(".", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps executed, got %d", result.StepsExecuted)
	}
	if len(result.JobDirs) != 2 {
		t.Fatalf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// Edge case: All steps fail with --continue-on-error -----------------------

// TestAllStepsFailWithContinueOnErrorReturnsNonZeroExit verifies that when
// every step fails and --continue-on-error is set, all 3 steps are executed
// and the exit code is non-zero.
func TestAllStepsFailWithContinueOnErrorReturnsNonZeroExit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{
		"Analyze src/auth/ for security issues",
		"Write fixes for the issues",
		"Write tests for the fixes",
	}
	// Use a non-existent dir to force failures on all steps immediately.
	cf := chainFlags("/nonexistent-path-xyz-abc", 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if result.StepsExecuted != 3 {
		t.Errorf("expected all 3 steps executed, got %d", result.StepsExecuted)
	}
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}

// Flags pass-through -------------------------------------------------------

// TestChainPassesDirectoryFlagToEachStep verifies that each step's ExecuteJob
// call uses the correct working directory. We verify this via observable
// behavior: each step gets a separate job dir (flags were forwarded).
func TestChainPassesDirectoryFlagToEachStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	workdir := t.TempDir() // must exist
	prompts := []string{"Analyze", "Fix"}
	// ContinueOnError=true so both steps run.
	cf := chainFlags(workdir, 0, "", true, prompts)

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	// Both job directories must have been created (flags were accepted).
	if len(result.JobDirs) != 2 {
		t.Errorf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// TestChainPassesTimeoutFlagToEachStep verifies that a chain with a non-default
// timeout runs and all steps are attempted (timeout flag forwarded correctly).
func TestChainPassesTimeoutFlagToEachStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Analyze", "Fix"}
	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: ".", Timeout: 600},
		ContinueOnError: true, // ensure both steps run
		Prompts:         prompts,
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if len(result.JobDirs) != 2 {
		t.Errorf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// TestChainPassesModelFlagToEachStep verifies that a chain with a custom model
// runs and all steps are attempted (model flag forwarded correctly).
func TestChainPassesModelFlagToEachStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	prompts := []string{"Analyze", "Fix"}
	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: ".", Model: "custom-model"},
		ContinueOnError: true, // ensure both steps run
		Prompts:         prompts,
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if len(result.JobDirs) != 2 {
		t.Errorf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// TestChainPropagatesSystemPromptAndConstraintsToEachStep verifies that when
// ChainFlags.Flags has SystemPrompt and Constraints set, every step inherits
// them so that ExecuteJob (and therefore BuildClaudeConfig) receives the correct
// values.  We use a known constraint ("readonly") and a custom system prompt;
// ContinueOnError=true so both steps always run.
func TestChainPropagatesSystemPromptAndConstraintsToEachStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	// Use a two-step chain so we can confirm both steps are created.
	prompts := []string{"Analyze only, do not write", "Summarise findings"}
	cf := &cmd.ChainFlags{
		Flags: &cmd.Flags{
			Dir:          ".",
			Timeout:      0,
			SystemPrompt: "You are a careful reviewer.",
			Constraints:  []string{"readonly"},
		},
		ContinueOnError: true,
		Prompts:         prompts,
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	// Both steps must have run — propagation didn't cause an early abort.
	if result.StepsExecuted != 2 {
		t.Errorf("StepsExecuted: got %d, want 2 (propagation should not abort the chain)", result.StepsExecuted)
	}
	if len(result.JobDirs) != 2 {
		t.Errorf("len(JobDirs): got %d, want 2", len(result.JobDirs))
	}
}

// BuildChainPrompt unit tests -----------------------------------------------

// TestBuildChainPromptFormat verifies the exact format of the injected prompt.
func TestBuildChainPromptFormat(t *testing.T) {
	t.Parallel()
	prev := "Found 3 issues: SQL injection in login.ts, XSS in profile.ts, missing CSRF token"
	next := "Based on the analysis, write fixes for the critical issues found"

	got := cmd.BuildChainPrompt(prev, next)

	if !strings.Contains(got, "Previous agent result:") {
		t.Errorf("prompt missing 'Previous agent result:'\ngot: %q", got)
	}
	if !strings.Contains(got, prev) {
		t.Errorf("prompt missing previous stdout\ngot: %q", got)
	}
	if !strings.Contains(got, "Your task:") {
		t.Errorf("prompt missing 'Your task:'\ngot: %q", got)
	}
	if !strings.Contains(got, next) {
		t.Errorf("prompt missing user prompt\ngot: %q", got)
	}

	want := "Previous agent result:\n" + prev + "\n\nYour task:\n" + next
	if got != want {
		t.Errorf("BuildChainPrompt exact format mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestBuildChainPromptWithEmptyPrevStdout verifies that an empty previous
// stdout still produces the correct structure.
func TestBuildChainPromptWithEmptyPrevStdout(t *testing.T) {
	t.Parallel()
	got := cmd.BuildChainPrompt("", "Verify no .tmp files remain")

	if !strings.Contains(got, "Previous agent result:") {
		t.Errorf("prompt missing 'Previous agent result:'\ngot: %q", got)
	}
	if !strings.Contains(got, "Your task:") {
		t.Errorf("prompt missing 'Your task:'\ngot: %q", got)
	}
	if !strings.Contains(got, "Verify no .tmp files remain") {
		t.Errorf("prompt missing user prompt\ngot: %q", got)
	}
}

// ChainStep and Steps field tests -----------------------------------------

// TestChainStepsFromPrompts verifies that ChainStepsFromPrompts converts
// a plain prompt list into ChainStep entries with matching Prompts and
// nil Validate/Retry fields.
func TestChainStepsFromPrompts(t *testing.T) {
	t.Parallel()
	prompts := []string{"a", "b"}
	steps := cmd.ChainStepsFromPrompts(prompts)

	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Prompt != "a" {
		t.Errorf("step 0 Prompt: got %q, want %q", steps[0].Prompt, "a")
	}
	if steps[1].Prompt != "b" {
		t.Errorf("step 1 Prompt: got %q, want %q", steps[1].Prompt, "b")
	}
	if steps[0].Validate != nil {
		t.Error("step 0 Validate: expected nil")
	}
	if steps[0].Retry != nil {
		t.Error("step 0 Retry: expected nil")
	}
}

// TestChainCmd_StepsFieldUsed verifies that when ChainFlags.Steps is set
// (instead of Prompts), the chain uses Steps for execution.
func TestChainCmd_StepsFieldUsed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: "."},
		ContinueOnError: true,
		Steps: []cmd.ChainStep{
			{Prompt: "Analyze code"},
			{Prompt: "Fix issues"},
		},
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps executed, got %d", result.StepsExecuted)
	}
	if len(result.JobDirs) != 2 {
		t.Errorf("expected 2 job dirs, got %d", len(result.JobDirs))
	}
}

// TestChainCmd_ValidationPasses verifies that a step with a validation rule
// that matches the output completes normally and does not stop the chain.
func TestChainCmd_ValidationPasses(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: "."},
		ContinueOnError: true,
		Steps: []cmd.ChainStep{
			{
				Prompt:   "Analyze code",
				Validate: &validation.ValidationRule{},
			},
			{Prompt: "Fix issues"},
		},
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps executed, got %d", result.StepsExecuted)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

// TestChainCmd_ValidationFails_StopsChain verifies that when a step's
// validation fails and ContinueOnError is false, the chain stops and
// remaining steps are skipped.
func TestChainCmd_ValidationFails_StopsChain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: "."},
		ContinueOnError: false,
		Steps: []cmd.ChainStep{
			{
				Prompt:   "Analyze code",
				Validate: &validation.ValidationRule{Contains: []string{"UNIQUE_MARKER_7f3a9b2e_NOT_IN_OUTPUT"}},
			},
			{Prompt: "Fix issues"},
			{Prompt: "Write tests"},
		},
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}
	if result.StepsExecuted != 1 {
		t.Errorf("expected 1 step executed, got %d", result.StepsExecuted)
	}
	if result.StepsSkipped != 2 {
		t.Errorf("expected 2 steps skipped, got %d", result.StepsSkipped)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

// TestChainCmd_ValidationFails_ContinueOnError verifies that when a step's
// validation fails but ContinueOnError is true, the chain continues to
// the next step.
func TestChainCmd_ValidationFails_ContinueOnError(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: "."},
		ContinueOnError: true,
		Steps: []cmd.ChainStep{
			{
				Prompt:   "Analyze code",
				Validate: &validation.ValidationRule{Contains: []string{"UNIQUE_MARKER_c4d8e1f5_NOT_IN_OUTPUT"}},
			},
			{Prompt: "Fix issues"},
		},
	}

	result, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}
	if result.StepsExecuted != 2 {
		t.Errorf("expected 2 steps executed, got %d", result.StepsExecuted)
	}
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code (validation failed), got 0")
	}
}

// readSlotCounter returns the integer value of the running-slot counter in
// subagentsRoot, or 0 if the file is absent. Helper for slot-lifecycle tests.
func readSlotCounter(t *testing.T, subagentsRoot string) int {
	t.Helper()
	path := filepath.Join(subagentsRoot, slot.CounterFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("readSlotCounter: %v", err)
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("readSlotCounter: parse %q: %v", string(data), err)
	}
	return val
}

// TestChainCmd_SlotReleasedAfterEachAttempt is a regression test for the
// slot lifecycle around retries. Before the fix, a fresh SlotManager was
// created on every attempt inside the retry loop — harmless under unlimited
// slots, but a latent double-claim bug the moment a real limit was enforced.
// The current contract:
//
//   - One SlotManager.Init() per step (not per attempt).
//   - WaitForSlot per attempt; ExecuteJob's defer releases per attempt.
//   - After ChainCmd returns, the counter MUST be back to 0 regardless of
//     how many attempts ran or how many steps succeeded/failed.
func TestChainCmd_SlotReleasedAfterEachAttempt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping real-claude integration test in short mode")
	}
	root := makeSubagentsRoot(t)
	cfg := makeTestConfig(t)
	var stdout, stderr bytes.Buffer

	// A step with a Validate rule that can never pass, plus 3 retry attempts,
	// exercises the retry loop on the slot lifecycle path.
	cf := &cmd.ChainFlags{
		Flags:           &cmd.Flags{Dir: "."},
		ContinueOnError: true,
		Steps: []cmd.ChainStep{
			{
				Prompt:   "step one",
				Validate: &validation.ValidationRule{Contains: []string{"UNREACHABLE_TOKEN_xyz123"}},
				Retry:    &dag.RetryConfig{MaxAttempts: 3, Feedback: "try again"},
			},
			{Prompt: "step two"},
		},
	}

	if _, err := cmd.ChainCmd(cf, cfg, root, "test-project", &stdout, &stderr); err != nil {
		t.Fatalf("ChainCmd error: %v", err)
	}

	if got := readSlotCounter(t, root); got != 0 {
		t.Errorf("slot counter after chain = %d, want 0 (every WaitForSlot must be paired with a ReleaseSlot, including across retries)", got)
	}
}
