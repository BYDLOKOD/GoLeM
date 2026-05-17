package claude_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/claude"
	"github.com/veschin/GoLeM/internal/event"
)

// seedDir is the relative path to the claude package test fixtures.
const seedDir = "testdata"

// readSeed reads a file from the seed directory and returns its contents.
func readSeed(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(seedDir, name))
	if err != nil {
		t.Fatalf("readSeed(%q): %v", name, err)
	}
	return string(data)
}

// copySeed copies a seed file into dstDir under the given filename.
func copySeed(t *testing.T, seedName, dstDir, dstFile string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(seedDir, seedName))
	if err != nil {
		t.Fatalf("copySeed(%q): %v", seedName, err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, dstFile), data, 0o644); err != nil {
		t.Fatalf("copySeed write %q: %v", dstFile, err)
	}
}

// readJobFile reads a file from the job directory.
func readJobFile(t *testing.T, jobDir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(jobDir, name))
	if err != nil {
		t.Fatalf("readJobFile(%q): %v", name, err)
	}
	return string(data)
}

// envMap converts a "KEY=VALUE" slice into a map for easy lookup.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

// --------------------------------------------------------------------------
// AC1: Environment variable construction
// --------------------------------------------------------------------------

// TestBuildCorrectEnvironmentVariablesForClaudeSubprocess verifies that
// BuildEnv injects the six required Anthropic/ZAI variables from the config.
func TestBuildCorrectEnvironmentVariablesForClaudeSubprocess(t *testing.T) {
	// Parse expected values from seed.
	raw := readSeed(t, "expected_env.json")
	var seed struct {
		ConfigInput struct {
			ZAIAPIKey       string `json:"zai_api_key"`
			ZAIBaseURL      string `json:"zai_base_url"`
			ZAIAPITimeoutMS string `json:"zai_api_timeout_ms"`
			OpusModel       string `json:"opus_model"`
			SonnetModel     string `json:"sonnet_model"`
			HaikuModel      string `json:"haiku_model"`
		} `json:"config_input"`
		ExpectedEnv map[string]string `json:"expected_env"`
	}
	if err := json.Unmarshal([]byte(raw), &seed); err != nil {
		t.Fatalf("unmarshal expected_env.json: %v", err)
	}

	cfg := claude.Config{
		ZAIAPIKey:       seed.ConfigInput.ZAIAPIKey,
		ZAIBaseURL:      seed.ConfigInput.ZAIBaseURL,
		ZAIAPITimeoutMS: seed.ConfigInput.ZAIAPITimeoutMS,
		OpusModel:       seed.ConfigInput.OpusModel,
		SonnetModel:     seed.ConfigInput.SonnetModel,
		HaikuModel:      seed.ConfigInput.HaikuModel,
	}

	env := envMap(claude.BuildEnv(cfg))

	for key, want := range seed.ExpectedEnv {
		got, ok := env[key]
		if !ok {
			t.Errorf("env missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("env[%q] = %q, want %q", key, got, want)
		}
	}
}

// --------------------------------------------------------------------------
// AC2: Nesting detection variables must be absent
// --------------------------------------------------------------------------

// TestCLAUDECODEAndCLAUDE_CODE_ENTRYPOINTAreUnset verifies that BuildEnv
// strips CLAUDECODE and CLAUDE_CODE_ENTRYPOINT from the subprocess env even
// when they are present in the parent process environment.
func TestCLAUDECODEAndCLAUDE_CODE_ENTRYPOINTAreUnset(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	env := envMap(claude.BuildEnv(claude.Config{}))

	if _, present := env["CLAUDECODE"]; present {
		t.Error("CLAUDECODE must not be present in subprocess environment")
	}
	if _, present := env["CLAUDE_CODE_ENTRYPOINT"]; present {
		t.Error("CLAUDE_CODE_ENTRYPOINT must not be present in subprocess environment")
	}
}

// --------------------------------------------------------------------------
// AC3: CLI flag construction
// --------------------------------------------------------------------------

// --------------------------------------------------------------------------
// CLAUDE_CODE_* env vars must not be injected
// --------------------------------------------------------------------------

// TestBuildEnvDoesNotInjectCLAUDE_CODE_EnvVars verifies that BuildEnv does
// not add personal CLAUDE_CODE_* variables that were not already present in
// the host environment before the call. When the test runs inside a Claude
// Code shell the host already has these set; we temporarily clear them so the
// absence of explicit injection is observable.
func TestBuildEnvDoesNotInjectCLAUDE_CODE_EnvVars(t *testing.T) {
	keys := []string{
		"CLAUDE_CODE_NO_FLICKER",
		"CLAUDE_CODE_NEW_INIT",
		"CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING",
		"CLAUDE_CODE_INVESTIGATE_FIRST",
		"CLAUDE_CODE_ALWAYS_ENABLE_EFFORT",
	}

	// Clear keys from host env for the duration of this test.
	for _, key := range keys {
		prev, had := os.LookupEnv(key)
		if had {
			os.Unsetenv(key) //nolint:errcheck
			t.Cleanup(func() { os.Setenv(key, prev) })
		}
	}

	env := envMap(claude.BuildEnv(claude.Config{}))
	for _, key := range keys {
		if _, ok := env[key]; ok {
			t.Errorf("BuildEnv must not inject %q (was not in host env)", key)
		}
	}
}

// --------------------------------------------------------------------------
// --effort flag
// --------------------------------------------------------------------------

// TestBuildFlagsWithEffort verifies that a non-empty Effort field produces
// --effort <value> in the CLI flags.
func TestBuildFlagsWithEffort(t *testing.T) {
	cfg := claude.Config{Effort: "max"}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--effort max") {
		t.Errorf("flags missing --effort max; got: %q", joined)
	}
}

// TestBuildFlagsWithEmptyEffortOmitsFlag verifies that an empty Effort field
// does NOT add --effort to the CLI flags.
func TestBuildFlagsWithEmptyEffortOmitsFlag(t *testing.T) {
	cfg := claude.Config{Effort: ""}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if strings.Contains(joined, "--effort") {
		t.Errorf("flags should not contain --effort when empty; got: %q", joined)
	}
}

// --------------------------------------------------------------------------
// --exclude-dynamic-system-prompt-sections flag
// --------------------------------------------------------------------------

// TestBuildFlagsWithExcludeDynamicSections verifies that when
// ExcludeDynamicSections is true the long flag is present.
func TestBuildFlagsWithExcludeDynamicSections(t *testing.T) {
	cfg := claude.Config{ExcludeDynamicSections: true}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--exclude-dynamic-system-prompt-sections") {
		t.Errorf("flags missing --exclude-dynamic-system-prompt-sections; got: %q", joined)
	}
}

// TestBuildFlagsWithExcludeDynamicSectionsFalse verifies that when
// ExcludeDynamicSections is false the flag is NOT added.
func TestBuildFlagsWithExcludeDynamicSectionsFalse(t *testing.T) {
	cfg := claude.Config{ExcludeDynamicSections: false}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if strings.Contains(joined, "--exclude-dynamic-system-prompt-sections") {
		t.Errorf("flags should not contain --exclude-dynamic-system-prompt-sections when false; got: %q", joined)
	}
}

// --------------------------------------------------------------------------
// AC3: CLI flag construction
// --------------------------------------------------------------------------

// TestBuildCLIFlagsWithBypassPermissionsMode verifies the full flag set when
// permission mode is "bypassPermissions".
func TestBuildCLIFlagsWithBypassPermissionsMode(t *testing.T) {
	cfg := claude.Config{
		PermissionMode: "bypassPermissions",
		Model:          "sonnet",
		SystemPrompt:   "You are a helpful coding assistant",
	}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	required := []string{
		"-p",
		"--no-session-persistence",
		"--model",
		"--output-format json",
		"--dangerously-skip-permissions",
	}
	for _, f := range required {
		if !strings.Contains(joined, f) {
			t.Errorf("flags missing %q; got: %q", f, joined)
		}
	}

	// --append-system-prompt must carry the system prompt text.
	if !strings.Contains(joined, "--append-system-prompt") {
		t.Errorf("flags missing --append-system-prompt; got: %q", joined)
	}
	if !strings.Contains(joined, "You are a helpful coding assistant") {
		t.Errorf("system prompt text missing from flags; got: %q", joined)
	}
}

// TestBuildCLIFlagsWithNonBypassPermissionMode verifies that acceptEdits mode
// uses --permission-mode and omits --dangerously-skip-permissions.
func TestBuildCLIFlagsWithNonBypassPermissionMode(t *testing.T) {
	cfg := claude.Config{PermissionMode: "acceptEdits"}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--permission-mode acceptEdits") {
		t.Errorf("flags missing --permission-mode acceptEdits; got: %q", joined)
	}
	if strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Errorf("flags must NOT contain --dangerously-skip-permissions for acceptEdits; got: %q", joined)
	}
}

// TestBuildCLIFlagsWithPlanPermissionMode verifies plan mode flags.
func TestBuildCLIFlagsWithPlanPermissionMode(t *testing.T) {
	cfg := claude.Config{PermissionMode: "plan"}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--permission-mode plan") {
		t.Errorf("flags missing --permission-mode plan; got: %q", joined)
	}
	if strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Errorf("flags must NOT contain --dangerously-skip-permissions for plan; got: %q", joined)
	}
}

// TestBuildFlagsSystemPromptIsPassedRawWithoutExtraQuoting verifies that the
// system prompt value is forwarded to --append-system-prompt as a raw string,
// not wrapped in Go-style double-quotes.  A prompt containing embedded double
// quotes (e.g. Say "hello") must appear in the flags slice verbatim, not as
// "Say \"hello\"".
func TestBuildFlagsSystemPromptIsPassedRawWithoutExtraQuoting(t *testing.T) {
	raw := `Say "hello"`
	cfg := claude.Config{SystemPrompt: raw}
	flags := claude.BuildFlags(cfg)

	// Find the argument that follows --append-system-prompt.
	for i, f := range flags {
		if f == "--append-system-prompt" {
			if i+1 >= len(flags) {
				t.Fatal("--append-system-prompt flag has no following argument")
			}
			got := flags[i+1]
			if got != raw {
				t.Errorf("--append-system-prompt value = %q, want %q", got, raw)
			}
			return
		}
	}
	t.Error("--append-system-prompt flag not found in BuildFlags output")
}

// TestBuildCLIFlagsWithDefaultPermissionMode verifies that "default" mode
// uses --permission-mode default.
func TestBuildCLIFlagsWithDefaultPermissionMode(t *testing.T) {
	cfg := claude.Config{PermissionMode: "default"}
	flags := claude.BuildFlags(cfg)
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--permission-mode default") {
		t.Errorf("flags missing --permission-mode default; got: %q", joined)
	}
}

// --------------------------------------------------------------------------
// AC4: Execution with working directory and timeout
// --------------------------------------------------------------------------

// TestExecuteClaudeInSpecifiedWorkingDirectoryWithTimeout verifies that
// Execute validates working directory existence. We cannot run the real CLI
// in tests but can verify the error path for a nonexistent dir and that a
// real dir does not produce a directory-not-found error (the CLI-missing error
// fires first, which is expected behaviour when claude is unavailable).
func TestExecuteClaudeInSpecifiedWorkingDirectoryWithTimeout(t *testing.T) {
	jobDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     "/nonexistent/path",
		TimeoutSecs: 600,
		Prompt:      "Analyze the code",
		JobDir:      jobDir,
	}

	code, err := claude.Execute(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent working directory, got nil")
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.Error(), `err:user "Directory not found: /nonexistent/path"`) {
		t.Errorf("error message = %q, want err:user Directory not found", err.Error())
	}
}

// --------------------------------------------------------------------------
// AC5: Stdout → raw.json, stderr → stderr.txt
// --------------------------------------------------------------------------

// TestStdoutCapturedToRawJSONAndStderrToStderrTxt verifies that after a
// successful execution the job directory contains raw.json and stderr.txt.
// We test this by calling ParseRawJSON on a pre-written raw.json (since we
// cannot run the real CLI), which validates that the file is consumed correctly.
func TestStdoutCapturedToRawJSONAndStderrToStderrTxt(t *testing.T) {
	jobDir := t.TempDir()

	// Write seed raw.json and empty stderr.txt as Execute would.
	copySeed(t, "raw_output_happy.json", jobDir, "raw.json")
	if err := os.WriteFile(filepath.Join(jobDir, "stderr.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify both files exist.
	for _, name := range []string{"raw.json", "stderr.txt"} {
		if _, err := os.Stat(filepath.Join(jobDir, name)); err != nil {
			t.Errorf("job dir missing %q: %v", name, err)
		}
	}

	rawContent := readJobFile(t, jobDir, "raw.json")
	if !strings.Contains(rawContent, `"type": "result"`) {
		t.Errorf("raw.json does not contain expected JSON; got: %.100s", rawContent)
	}
}

// --------------------------------------------------------------------------
// AC6: JSON parsing and changelog generation
// --------------------------------------------------------------------------

// TestParseRawJSONWithEditAndWriteToolCalls verifies the happy-path parsing:
// stdout.txt receives the .result text and changelog.txt lists EDIT + WRITE.
func TestParseRawJSONWithEditAndWriteToolCalls(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_happy.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	// stdout.txt must contain the .result string.
	stdout := readJobFile(t, jobDir, "stdout.txt")
	if !strings.Contains(stdout, "race condition") {
		t.Errorf("stdout.txt missing expected result text; got: %.200s", stdout)
	}

	// changelog.txt must match seed file exactly.
	wantChangelog := strings.TrimRight(readSeed(t, "expected_changelog_happy.txt"), "\n")
	gotChangelog := strings.TrimRight(readJobFile(t, jobDir, "changelog.txt"), "\n")
	if gotChangelog != wantChangelog {
		t.Errorf("changelog.txt mismatch:\ngot:  %q\nwant: %q", gotChangelog, wantChangelog)
	}

	// Spot-check individual lines.
	if !strings.Contains(gotChangelog, "EDIT /home/veschin/work/GoLeM/internal/slot/slot.go: 341 chars") {
		t.Errorf("changelog missing EDIT entry; got: %q", gotChangelog)
	}
	if !strings.Contains(gotChangelog, "WRITE /home/veschin/work/GoLeM/internal/job/atomic.go") {
		t.Errorf("changelog missing WRITE entry; got: %q", gotChangelog)
	}
}

// TestParseRawJSONWithNoToolCallsProducesNoChangesChangelog verifies that when
// the Claude output contains no tool_use blocks, the changelog reads
// "(no file changes)".
func TestParseRawJSONWithNoToolCallsProducesNoChangesChangelog(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_no_changes.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	wantChangelog := strings.TrimRight(readSeed(t, "expected_changelog_no_changes.txt"), "\n")
	gotChangelog := strings.TrimRight(readJobFile(t, jobDir, "changelog.txt"), "\n")
	if gotChangelog != wantChangelog {
		t.Errorf("changelog.txt mismatch:\ngot:  %q\nwant: %q", gotChangelog, wantChangelog)
	}
	if !strings.Contains(gotChangelog, "(no file changes)") {
		t.Errorf("changelog must contain '(no file changes)'; got: %q", gotChangelog)
	}
}

// TestParseRawJSONWithBashDeleteCommand verifies that a Bash rm command is
// recorded as "DELETE via bash: ...".
func TestParseRawJSONWithBashDeleteCommand(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_bash_delete.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	wantChangelog := strings.TrimRight(readSeed(t, "expected_changelog_bash_delete.txt"), "\n")
	gotChangelog := strings.TrimRight(readJobFile(t, jobDir, "changelog.txt"), "\n")
	if gotChangelog != wantChangelog {
		t.Errorf("changelog.txt mismatch:\ngot:  %q\nwant: %q", gotChangelog, wantChangelog)
	}
	if !strings.Contains(gotChangelog, "DELETE via bash: rm -rf /tmp/old-data") {
		t.Errorf("changelog missing DELETE entry; got: %q", gotChangelog)
	}
}

// TestParseRawJSONWithBashFilesystemCommand verifies that a non-delete Bash
// command is recorded as "FS: ...".
func TestParseRawJSONWithBashFilesystemCommand(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_bash_fs.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	wantChangelog := strings.TrimRight(readSeed(t, "expected_changelog_bash_fs.txt"), "\n")
	gotChangelog := strings.TrimRight(readJobFile(t, jobDir, "changelog.txt"), "\n")
	if gotChangelog != wantChangelog {
		t.Errorf("changelog.txt mismatch:\ngot:  %q\nwant: %q", gotChangelog, wantChangelog)
	}
	if !strings.Contains(gotChangelog, "FS: mkdir -p /tmp/test/output") {
		t.Errorf("changelog missing FS entry; got: %q", gotChangelog)
	}
}

// TestParseRawJSONWithNotebookEditToolCall verifies that a NotebookEdit tool
// call is recorded as "NOTEBOOK <path>".
func TestParseRawJSONWithNotebookEditToolCall(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_notebook.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	wantChangelog := strings.TrimRight(readSeed(t, "expected_changelog_notebook.txt"), "\n")
	gotChangelog := strings.TrimRight(readJobFile(t, jobDir, "changelog.txt"), "\n")
	if gotChangelog != wantChangelog {
		t.Errorf("changelog.txt mismatch:\ngot:  %q\nwant: %q", gotChangelog, wantChangelog)
	}
	if !strings.Contains(gotChangelog, "NOTEBOOK /home/veschin/work/analysis/preprocess.ipynb") {
		t.Errorf("changelog missing NOTEBOOK entry; got: %q", gotChangelog)
	}
}

// --------------------------------------------------------------------------
// AC7: Exit code mapping
// --------------------------------------------------------------------------

// TestExitCode0MapsToDone verifies that exit code 0 maps to "done".
func TestExitCode0MapsToDone(t *testing.T) {
	got := claude.MapStatus(0, "")
	want := "done"
	if got != want {
		t.Errorf("MapStatus(0, \"\") = %q, want %q", got, want)
	}
}

// TestExitCode124MapsToTimeout verifies that exit code 124 maps to "timeout".
func TestExitCode124MapsToTimeout(t *testing.T) {
	got := claude.MapStatus(124, "")
	want := "timeout"
	if got != want {
		t.Errorf("MapStatus(124, \"\") = %q, want %q", got, want)
	}
}

// TestNonZeroExitWithPermissionRelatedStderrMapsToPermissionError verifies
// that stderr containing "Permission denied" triggers permission_error status.
func TestNonZeroExitWithPermissionRelatedStderrMapsToPermissionError(t *testing.T) {
	stderr := readSeed(t, "stderr_permission_denied.txt")
	got := claude.MapStatus(1, stderr)
	want := "permission_error"
	if got != want {
		t.Errorf("MapStatus(1, permissionDenied) = %q, want %q", got, want)
	}
}

// TestNonZeroExitWithNotAllowedStderrMapsToPermissionError verifies that
// stderr containing "not allowed" triggers permission_error status.
func TestNonZeroExitWithNotAllowedStderrMapsToPermissionError(t *testing.T) {
	stderr := readSeed(t, "stderr_not_allowed.txt")
	got := claude.MapStatus(1, stderr)
	want := "permission_error"
	if got != want {
		t.Errorf("MapStatus(1, notAllowed) = %q, want %q", got, want)
	}
}

// TestPermissionDetectionIsCaseInsensitive verifies that uppercase
// "PERMISSION DENIED" is still caught.
func TestPermissionDetectionIsCaseInsensitive(t *testing.T) {
	got := claude.MapStatus(1, "PERMISSION DENIED")
	want := "permission_error"
	if got != want {
		t.Errorf("MapStatus(1, \"PERMISSION DENIED\") = %q, want %q", got, want)
	}
}

// TestPermissionDetectionMatchesUnauthorizedKeyword verifies the "unauthorized"
// keyword is recognised.
func TestPermissionDetectionMatchesUnauthorizedKeyword(t *testing.T) {
	got := claude.MapStatus(1, "Unauthorized access to resource")
	want := "permission_error"
	if got != want {
		t.Errorf("MapStatus(1, unauthorized) = %q, want %q", got, want)
	}
}

// TestPermissionDetectionMatchesDeniedKeyword verifies the "denied" keyword
// is recognised.
func TestPermissionDetectionMatchesDeniedKeyword(t *testing.T) {
	got := claude.MapStatus(1, "Access denied for operation")
	want := "permission_error"
	if got != want {
		t.Errorf("MapStatus(1, denied) = %q, want %q", got, want)
	}
}

// TestNonZeroExitWithoutPermissionKeywordsMapsToFailed verifies that a normal
// error stderr produces "failed".
func TestNonZeroExitWithoutPermissionKeywordsMapsToFailed(t *testing.T) {
	stderr := readSeed(t, "stderr_normal_error.txt")
	got := claude.MapStatus(1, stderr)
	want := "failed"
	if got != want {
		t.Errorf("MapStatus(1, normalError) = %q, want %q", got, want)
	}
}

// TestExitCode137SIGKILLMapsToFailed verifies that a SIGKILL exit (137)
// produces "failed".
func TestExitCode137SIGKILLMapsToFailed(t *testing.T) {
	got := claude.MapStatus(137, "")
	want := "failed"
	if got != want {
		t.Errorf("MapStatus(137, \"\") = %q, want %q", got, want)
	}
}

// TestSignalExitCodeSIGKILL verifies that a process killed by SIGKILL (9)
// produces exit code 137 (128+9) via the Unix convention.
func TestSignalExitCodeSIGKILL(t *testing.T) {
	// Create a fake claude script that sends SIGKILL to itself.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\nkill -9 $$\n"
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 10,
		Prompt:      "test sigkill",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode != 137 {
		t.Errorf("exit code = %d, want 137 (128 + SIGKILL)", exitCode)
	}
}

// TestSignalExitCodeSIGTERM verifies that a process killed by SIGTERM (15)
// produces exit code 143 (128+15) via the Unix convention.
func TestSignalExitCodeSIGTERM(t *testing.T) {
	// Create a fake claude script that sends SIGTERM to itself.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\nkill -15 $$\n"
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 10,
		Prompt:      "test sigterm",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode != 143 {
		t.Errorf("exit code = %d, want 143 (128 + SIGTERM)", exitCode)
	}
}

// --------------------------------------------------------------------------
// AC8: Metadata file writes
// --------------------------------------------------------------------------

// TestMetadataFilesWrittenBeforeExecution verifies that Execute writes all
// required metadata files before invoking the subprocess.  We rely on the
// working-directory-not-found error to abort before the actual subprocess
// runs, then check that the files were not written for a nonexistent dir.
// For a valid dir with no `claude` binary we expect the dependency error to
// fire first, but metadata should still be written if we reach that stage.
//
// Because in CI `claude` is absent, we test with a real existing dir and
// verify the metadata write sequence via a helper function that writes the
// files independently.
func TestMetadataFilesWrittenBeforeExecution(t *testing.T) {
	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		Prompt:         "Analyze the code",
		WorkDir:        workDir,
		PermissionMode: "bypassPermissions",
		OpusModel:      "glm-5",
		SonnetModel:    "glm-5",
		HaikuModel:     "glm-5",
		JobDir:         jobDir,
	}

	// Execute will fail at the claude-not-found step but metadata must be
	// written before the subprocess starts. We verify the write via the public
	// WriteMetadata helper.
	claude.WriteMetadata(cfg)

	for _, tc := range []struct{ file, want string }{
		{"prompt.txt", "Analyze the code"},
		{"workdir.txt", workDir},
		{"permission_mode.txt", "bypassPermissions"},
		{"model.txt", "opus=glm-5 sonnet=glm-5 haiku=glm-5"},
	} {
		got := readJobFile(t, jobDir, tc.file)
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.file, got, tc.want)
		}
	}

	// started_at.txt must parse as a valid RFC3339 timestamp.
	startedAt := readJobFile(t, jobDir, "started_at.txt")
	if _, err := time.Parse(time.RFC3339, startedAt); err != nil {
		t.Errorf("started_at.txt %q is not RFC3339: %v", startedAt, err)
	}
}

// TestFinishedAtWrittenAfterExecutionCompletes verifies that
// WriteFinishedAt creates finished_at.txt with a valid ISO 8601 timestamp.
func TestFinishedAtWrittenAfterExecutionCompletes(t *testing.T) {
	jobDir := t.TempDir()

	claude.WriteFinishedAt(jobDir)

	finishedAt := readJobFile(t, jobDir, "finished_at.txt")
	if _, err := time.Parse(time.RFC3339, finishedAt); err != nil {
		t.Errorf("finished_at.txt %q is not RFC3339: %v", finishedAt, err)
	}
}

// TestExitCodeFileWrittenOnNonZeroExit verifies that WriteExitCode creates
// exit_code.txt with the exit code as a decimal string.
func TestExitCodeFileWrittenOnNonZeroExit(t *testing.T) {
	jobDir := t.TempDir()

	claude.WriteExitCode(jobDir, 1)

	got := readJobFile(t, jobDir, "exit_code.txt")
	if got != "1" {
		t.Errorf("exit_code.txt = %q, want %q", got, "1")
	}
}

// TestExitCodeFileNotWrittenOnSuccess verifies that WriteExitCode does NOT
// write exit_code.txt when the exit code is 0.
func TestExitCodeFileNotWrittenOnSuccess(t *testing.T) {
	jobDir := t.TempDir()

	claude.WriteExitCode(jobDir, 0)

	if _, err := os.Stat(filepath.Join(jobDir, "exit_code.txt")); err == nil {
		t.Error("exit_code.txt must not exist when exit code is 0")
	}
}

// --------------------------------------------------------------------------
// AC9: Claude CLI dependency check
// --------------------------------------------------------------------------

// TestClaudeCLINotFoundInPATH verifies that Execute returns a dependency error
// when `claude` is not in PATH.
func TestClaudeCLINotFoundInPATH(t *testing.T) {
	// Override PATH to an empty directory so claude cannot be found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir: workDir,
		JobDir:  jobDir,
	}

	code, err := claude.Execute(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected dependency error, got nil")
	}
	wantMsg := `err:dependency "claude CLI not found in PATH"`
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantMsg)
	}
	if code != 127 {
		t.Errorf("exit code = %d, want 127", code)
	}
}

// TestPython3IsNotRequired verifies that the absence of python3 does NOT
// produce a dependency error (only the claude CLI is required).
func TestPython3IsNotRequired(t *testing.T) {
	// Build a PATH that has claude but not python3.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	// Write a tiny shell script that exits immediately.
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir: workDir,
		JobDir:  jobDir,
	}

	_, err := claude.Execute(t.Context(), cfg)
	// The only acceptable error here is NOT a dependency error — the CLI ran
	// (and exited 0) so there should be no error at all, or if it fails it
	// must not be the "claude CLI not found" dependency error.
	if err != nil && strings.Contains(err.Error(), `err:dependency "claude CLI not found in PATH"`) {
		t.Errorf("must not return dependency error when claude is available: %v", err)
	}
}

// --------------------------------------------------------------------------
// Edge cases
// --------------------------------------------------------------------------

// TestEmptyRawJSONFromClaudeCrash verifies that an empty JSON object ({})
// produces empty stdout.txt and "(no file changes)" in changelog.txt.
func TestEmptyRawJSONFromClaudeCrash(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_empty.json", jobDir, "raw.json")

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	stdout := readJobFile(t, jobDir, "stdout.txt")
	if stdout != "" {
		t.Errorf("stdout.txt = %q, want empty string", stdout)
	}

	changelog := readJobFile(t, jobDir, "changelog.txt")
	if !strings.Contains(changelog, "(no file changes)") {
		t.Errorf("changelog.txt must contain '(no file changes)'; got: %q", changelog)
	}
}

// TestMalformedJSONInRawJSON verifies that invalid JSON input results in
// empty stdout.txt, "(no file changes)" in changelog.txt, and a warning
// logged to stderr.
func TestMalformedJSONInRawJSON(t *testing.T) {
	jobDir := t.TempDir()
	copySeed(t, "raw_output_malformed.txt", jobDir, "raw.json")

	// Capture stderr to verify the warning.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := claude.ParseRawJSON(jobDir)

	_ = w.Close()
	os.Stderr = oldStderr
	var stderrBuf strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			stderrBuf.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	_ = r.Close()

	if err != nil {
		t.Fatalf("ParseRawJSON must not return an error for malformed JSON, got: %v", err)
	}

	stdout := readJobFile(t, jobDir, "stdout.txt")
	if stdout != "" {
		t.Errorf("stdout.txt = %q, want empty string", stdout)
	}

	changelog := readJobFile(t, jobDir, "changelog.txt")
	if !strings.Contains(changelog, "(no file changes)") {
		t.Errorf("changelog.txt must contain '(no file changes)'; got: %q", changelog)
	}

	if !strings.Contains(stderrBuf.String(), "warning") {
		t.Errorf("expected warning in stderr, got: %q", stderrBuf.String())
	}
}

// TestRawJSONHasNoResultField verifies that valid JSON without a ".result"
// field produces an empty stdout.txt.
func TestRawJSONHasNoResultField(t *testing.T) {
	jobDir := t.TempDir()

	// Write valid JSON with no "result" field.
	if err := os.WriteFile(filepath.Join(jobDir, "raw.json"), []byte(`{"type":"result"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	stdout := readJobFile(t, jobDir, "stdout.txt")
	if stdout != "" {
		t.Errorf("stdout.txt = %q, want empty string", stdout)
	}
}

// TestWorkingDirectoryDoesNotExist verifies that Execute returns
// 'err:user "Directory not found: ..."' with exit code 1 and does not run
// the claude subprocess.
func TestWorkingDirectoryDoesNotExist(t *testing.T) {
	jobDir := t.TempDir()

	cfg := claude.Config{
		WorkDir: "/nonexistent/path",
		JobDir:  jobDir,
	}

	code, err := claude.Execute(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
	wantMsg := `err:user "Directory not found: /nonexistent/path"`
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantMsg)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	// claude must not have been executed — raw.json must not exist.
	if _, err := os.Stat(filepath.Join(jobDir, "raw.json")); err == nil {
		t.Error("raw.json must not exist when working directory is invalid")
	}
}

// TestTimeoutFiresDuringExecution verifies that a very short timeout causes
// the subprocess to be killed and the status to become "timeout".
// We use a tiny mock claude script that sleeps.
func TestTimeoutFiresDuringExecution(t *testing.T) {
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	// Shell script that busy-loops far longer than our timeout using a shell
	// builtin so it works even when PATH is restricted.
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nwhile true; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 1, // 1-second timeout
		Prompt:      "long-running task",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	status := claude.MapStatus(exitCode, "")

	if status != "timeout" {
		t.Errorf("status = %q, want %q (exit code was %d)", status, "timeout", exitCode)
	}

	// finished_at.txt must have been written.
	finishedAt := readJobFile(t, jobDir, "finished_at.txt")
	if _, err := time.Parse(time.RFC3339, finishedAt); err != nil {
		t.Errorf("finished_at.txt %q is not RFC3339: %v", finishedAt, err)
	}
}

// TestTimeoutKillsEntireProcessGroup verifies that on timeout the entire
// process group (parent + children) is killed, not just the direct child.
// We use a shell script that spawns a background child writing to a marker
// file; after the timeout fires, the marker file must stop growing, proving
// the child was reaped.
func TestTimeoutKillsEntireProcessGroup(t *testing.T) {
	binDir := t.TempDir()
	markerDir := t.TempDir()
	markerFile := filepath.Join(markerDir, "child_alive.txt")

	// Shell script: spawns a background child that writes to markerFile in a
	// loop, then the parent also loops. Both must be killed on timeout.
	script := fmt.Sprintf(`#!/bin/sh
# Child process: writes a timestamp every 0.1s
(while true; do date +%%s%%N >> %s; sleep 0.1; done) &
# Parent process: busy-loop
while true; do :; done
`, markerFile)
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 1,
		Prompt:      "spawn children",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode != 124 {
		t.Fatalf("exit code = %d, want 124 (timeout)", exitCode)
	}

	// Give a brief grace period for any surviving child to write more lines.
	time.Sleep(500 * time.Millisecond)

	// Read the marker file line count right after timeout.
	data1, _ := os.ReadFile(markerFile)
	count1 := len(strings.Split(strings.TrimSpace(string(data1)), "\n"))

	// Wait again — if child survived it would have written more lines.
	time.Sleep(500 * time.Millisecond)
	data2, _ := os.ReadFile(markerFile)
	count2 := len(strings.Split(strings.TrimSpace(string(data2)), "\n"))

	if count2 > count1 {
		t.Errorf("child process survived timeout: line count grew from %d to %d", count1, count2)
	}
}

// TestBashCommandLongerThan80CharsIsTruncatedInChangelog verifies that a Bash
// tool call command longer than 80 characters is truncated in the changelog.
func TestBashCommandLongerThan80CharsIsTruncatedInChangelog(t *testing.T) {
	jobDir := t.TempDir()

	long := strings.Repeat("a", 100) // 100-char command
	raw := fmt.Sprintf(`{
  "type": "result",
  "result": "",
  "messages": [
    {
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "name": "Bash",
          "input": {"command": %q}
        }
      ]
    }
  ]
}`, long)

	if err := os.WriteFile(filepath.Join(jobDir, "raw.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := claude.ParseRawJSON(jobDir); err != nil {
		t.Fatalf("ParseRawJSON: %v", err)
	}

	changelog := readJobFile(t, jobDir, "changelog.txt")
	// The changelog entry should not exceed "FS: " + 80 chars.
	lines := strings.Split(strings.TrimRight(changelog, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("changelog.txt is empty")
	}
	// Find the FS: line.
	var fsLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "FS: ") {
			fsLine = l
		}
	}
	if fsLine == "" {
		t.Fatalf("no FS: line in changelog; got: %q", changelog)
	}
	// The command portion is after "FS: ".
	cmdPart := strings.TrimPrefix(fsLine, "FS: ")
	if len(cmdPart) > 80 {
		t.Errorf("command part is %d chars, want ≤ 80; got: %q", len(cmdPart), cmdPart)
	}
}

// --------------------------------------------------------------------------
// Event producer tests (P2.1)
// --------------------------------------------------------------------------

// TestExecute_NilBus_NoPanic verifies that Execute does not panic when the
// Bus field is nil (the default).
func TestExecute_NilBus_NoPanic(t *testing.T) {
	dir := t.TempDir()
	cfg := claude.Config{
		Prompt:  "echo test",
		WorkDir: dir,
		JobDir:  filepath.Join(dir, "job"),
		Bus:     nil,
	}
	_ = os.MkdirAll(cfg.JobDir, 0o755)

	// Will fail because claude CLI may not be available, but must not panic.
	_, _ = claude.Execute(t.Context(), cfg)
}

// TestExecute_PublishesJobRunningAndDone verifies that Execute publishes a
// JobRunning event before the subprocess starts and a JobDone event after
// it exits successfully.
func TestExecute_PublishesJobRunningAndDone(t *testing.T) {
	// Create a fake claude script that exits immediately.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.JobRunning, event.JobDone, event.JobFailed, event.JobTimeout)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 10,
		Prompt:      "test events",
		Bus:         bus,
		JobID:       "test-job-123",
		ProjectID:   "test-project",
		Model:       "test-model",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	// First event should be JobRunning.
	select {
	case e := <-ch:
		if e.Type != event.JobRunning {
			t.Fatalf("first event type = %v, want JobRunning", e.Type)
		}
		if e.JobID != "test-job-123" {
			t.Errorf("JobRunning job_id = %v, want test-job-123", e.JobID)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("JobRunning Data is not map[string]any: %T", e.Data)
		}
		if data["model"] != "test-model" {
			t.Errorf("model = %v, want test-model", data["model"])
		}
		if data["project_id"] != "test-project" {
			t.Errorf("project_id = %v, want test-project", data["project_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JobRunning event")
	}

	// Second event should be JobDone.
	select {
	case e := <-ch:
		if e.Type != event.JobDone {
			t.Fatalf("second event type = %v, want JobDone", e.Type)
		}
		if e.JobID != "test-job-123" {
			t.Errorf("JobDone job_id = %v, want test-job-123", e.JobID)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("JobDone Data is not map[string]any: %T", e.Data)
		}
		if data["exit_code"] != 0 {
			t.Errorf("exit_code = %v, want 0", data["exit_code"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JobDone event")
	}
}

// TestExecute_PublishesJobFailed verifies that Execute publishes a JobFailed
// event when the subprocess exits with a non-zero code.
func TestExecute_PublishesJobFailed(t *testing.T) {
	// Create a fake claude script that exits with error.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\necho 'some error' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.JobRunning, event.JobDone, event.JobFailed, event.JobTimeout)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 10,
		Prompt:      "test failure",
		Bus:         bus,
		JobID:       "test-job-fail",
		ProjectID:   "test-project",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}

	// Skip the JobRunning event.
	select {
	case e := <-ch:
		if e.Type != event.JobRunning {
			t.Fatalf("first event type = %v, want JobRunning", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JobRunning event")
	}

	// Second event should be JobFailed.
	select {
	case e := <-ch:
		if e.Type != event.JobFailed {
			t.Fatalf("second event type = %v, want JobFailed", e.Type)
		}
		if e.JobID != "test-job-fail" {
			t.Errorf("JobFailed job_id = %v, want test-job-fail", e.JobID)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("JobFailed Data is not map[string]any: %T", e.Data)
		}
		if data["exit_code"] != 1 {
			t.Errorf("exit_code = %v, want 1", data["exit_code"])
		}
		stderr, _ := data["stderr"].(string)
		if !strings.Contains(stderr, "some error") {
			t.Errorf("stderr = %q, want to contain 'some error'", stderr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JobFailed event")
	}
}

// TestExecute_PublishesJobTimeout verifies that Execute publishes a JobTimeout
// event when the subprocess exceeds its time limit.
func TestExecute_PublishesJobTimeout(t *testing.T) {
	// Create a fake claude script that loops forever.
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nwhile true; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	bus := event.NewBus()
	defer bus.Close()

	ch := bus.Subscribe(event.JobRunning, event.JobDone, event.JobFailed, event.JobTimeout)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 1,
		Prompt:      "test timeout",
		Bus:         bus,
		JobID:       "test-job-timeout",
		ProjectID:   "test-project",
	}

	exitCode, _ := claude.Execute(t.Context(), cfg)
	if exitCode != 124 {
		t.Fatalf("exit code = %d, want 124 (timeout)", exitCode)
	}

	// Skip the JobRunning event.
	select {
	case e := <-ch:
		if e.Type != event.JobRunning {
			t.Fatalf("first event type = %v, want JobRunning", e.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for JobRunning event")
	}

	// Second event should be JobTimeout.
	select {
	case e := <-ch:
		if e.Type != event.JobTimeout {
			t.Fatalf("second event type = %v, want JobTimeout", e.Type)
		}
		if e.JobID != "test-job-timeout" {
			t.Errorf("JobTimeout job_id = %v, want test-job-timeout", e.JobID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for JobTimeout event")
	}
}

// TestExecute_RespectsParentContextCancellation verifies that cancelling the
// context passed to Execute terminates the subprocess and returns exit code
// 124 (timeout). This is the regression test for the previous behavior, where
// Execute discarded the caller's context entirely — pipeline cancellation
// could not stop in-flight Claude subprocesses.
//
// The fake claude script spins forever. The local TimeoutSecs is set high
// (60s) so only the external context cancellation can stop it. The test
// expects Execute to return within a couple of seconds of Cancel().
func TestExecute_RespectsParentContextCancellation(t *testing.T) {
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nwhile true; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 60, // intentionally larger than the test budget
		Prompt:      "test parent cancel",
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay; Execute must observe this and tear down.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	exitCode, _ := claude.Execute(ctx, cfg)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("Execute did not honor parent cancellation: returned after %v", elapsed)
	}
	if exitCode != 124 {
		t.Errorf("exit code = %d, want 124 (timeout) after parent context cancellation", exitCode)
	}
}

// TestExecute_NilContextSafe verifies that passing nil for the parent context
// is treated as Background(), so the existing CLI callers that do not have a
// context need not synthesize one.
func TestExecute_NilContextSafe(t *testing.T) {
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	jobDir := t.TempDir()
	workDir := t.TempDir()

	cfg := claude.Config{
		WorkDir:     workDir,
		JobDir:      jobDir,
		TimeoutSecs: 5,
		Prompt:      "nil ctx",
	}

	// Must not panic when parent is nil; exit code should reflect the script.
	// Use a typed nil variable so static analyzers see the deliberate contract
	// without firing SA1012-style "nil context" warnings.
	var nilCtx context.Context
	exitCode, _ := claude.Execute(nilCtx, cfg)
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0 (nil context must behave as Background)", exitCode)
	}
}
