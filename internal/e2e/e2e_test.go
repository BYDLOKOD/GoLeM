//go:build e2e

package e2e_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/claude"
	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/job"
)

func setupE2E(t *testing.T) (*config.Config, string) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	realKeyPath := filepath.Join(home, ".config", "GoLeM", "zai_api_key")
	key, err := os.ReadFile(realKeyPath)
	if err != nil {
		t.Skip("API key not found at " + realKeyPath)
	}
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	subagentDir := filepath.Join(tmpDir, "subagents")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "zai_api_key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configDir, subagentDir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, tmpDir
}

func buildTestClaudeConfig(t *testing.T, cfg *config.Config, tmpDir, prompt string) claude.Config {
	t.Helper()
	subagentDir := filepath.Join(tmpDir, "subagents")
	projectID := "e2e-test"
	jobID := job.GenerateJobID()
	j, err := job.NewJob(subagentDir, projectID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	return claude.Config{
		ZAIAPIKey:       cfg.ZaiAPIKey,
		ZAIBaseURL:      cfg.ZaiBaseURL,
		ZAIAPITimeoutMS: cfg.ZaiAPITimeoutMs,
		OpusModel:       cfg.OpusModel,
		SonnetModel:     cfg.SonnetModel,
		HaikuModel:      cfg.HaikuModel,
		PermissionMode:  "bypassPermissions",
		Model:           cfg.SonnetModel,
		Prompt:          prompt,
		WorkDir:         tmpDir,
		TimeoutSecs:     120,
		JobDir:          j.Dir,
	}
}

func TestE2EConfigLoadAndDoctor(t *testing.T) {
	cfg, _ := setupE2E(t)

	// Verify config loaded correctly
	if cfg.ZaiAPIKey == "" {
		t.Fatal("API key is empty after loading")
	}
	if cfg.ZaiBaseURL == "" {
		t.Fatal("ZAI base URL is empty")
	}

	// Run doctor
	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		ClaudeBinaryName: "claude",
		APIKeyPath:       filepath.Join(cfg.ConfigDir, "zai_api_key"),
		ZAIEndpoint:      config.ZaiBaseURL,
		HTTPTimeout:      5 * time.Second,
		SubagentsRoot:    cfg.SubagentDir,
		OpusModel:        cfg.OpusModel,
		SonnetModel:      cfg.SonnetModel,
		HaikuModel:       cfg.HaikuModel,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "claude_cli") {
		t.Error("doctor output missing claude_cli check")
	}
	if !strings.Contains(output, "api_key") || !strings.Contains(output, "OK") {
		t.Error("doctor output missing api_key OK")
	}
	if !strings.Contains(output, "platform") {
		t.Error("doctor output missing platform check")
	}
	t.Log("Doctor output:\n" + output)
}

func TestE2ERunSimplePrompt(t *testing.T) {
	cfg, tmpDir := setupE2E(t)

	cc := buildTestClaudeConfig(t, cfg, tmpDir, `respond with exactly this text and nothing else: GOLEM_E2E_OK`)

	exitCode, err := claude.Execute(cc)
	if err != nil {
		t.Logf("Execute error (may be non-fatal): %v", err)
	}

	// Parse raw.json
	if parseErr := claude.ParseRawJSON(cc.JobDir); parseErr != nil {
		t.Fatalf("ParseRawJSON: %v", parseErr)
	}

	stdout, err := os.ReadFile(filepath.Join(cc.JobDir, "stdout.txt"))
	if err != nil {
		t.Fatalf("read stdout.txt: %v", err)
	}

	t.Logf("Exit code: %d", exitCode)
	t.Logf("Stdout: %s", string(stdout))

	if exitCode != 0 {
		stderr, _ := os.ReadFile(filepath.Join(cc.JobDir, "stderr.txt"))
		t.Fatalf("claude exited with %d, stderr: %s", exitCode, string(stderr))
	}

	if !strings.Contains(string(stdout), "GOLEM_E2E_OK") {
		t.Errorf("stdout does not contain GOLEM_E2E_OK; got: %q", string(stdout))
	}
}

func TestE2ESessionBuildVerify(t *testing.T) {
	cfg, _ := setupE2E(t)

	var dbg bytes.Buffer
	result, err := cmd.SessionCmd(cfg, []string{"--verbose"}, &dbg)
	if err != nil {
		t.Fatal(err)
	}

	// Verify argv
	if len(result.Argv) == 0 || result.Argv[0] != "claude" {
		t.Errorf("argv[0] = %q; want claude", result.Argv[0])
	}

	// Verify env has real API key
	found := false
	for _, e := range result.Env {
		if strings.HasPrefix(e, "ANTHROPIC_AUTH_TOKEN=") {
			val := strings.TrimPrefix(e, "ANTHROPIC_AUTH_TOKEN=")
			if val == "" {
				t.Error("ANTHROPIC_AUTH_TOKEN is empty")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("ANTHROPIC_AUTH_TOKEN not found in env")
	}

	// Verify passthrough
	passthroughFound := false
	for _, a := range result.Argv {
		if a == "--verbose" {
			passthroughFound = true
		}
	}
	if !passthroughFound {
		t.Error("--verbose not passed through to argv")
	}
}

func TestE2EConfigOverrideModel(t *testing.T) {
	_, tmpDir := setupE2E(t)

	// Write custom TOML with different model
	configDir := filepath.Join(tmpDir, "config")
	tomlContent := `model = "custom-test-model"
opus_model = "custom-opus"
sonnet_model = "custom-sonnet"
haiku_model = "custom-haiku"
`
	if err := os.WriteFile(filepath.Join(configDir, "glm.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reload config
	subagentDir := filepath.Join(tmpDir, "subagents")
	newCfg, err := config.Load(configDir, subagentDir)
	if err != nil {
		t.Fatal(err)
	}

	if newCfg.OpusModel != "custom-opus" {
		t.Errorf("OpusModel = %q; want custom-opus", newCfg.OpusModel)
	}
	if newCfg.SonnetModel != "custom-sonnet" {
		t.Errorf("SonnetModel = %q; want custom-sonnet", newCfg.SonnetModel)
	}
	if newCfg.HaikuModel != "custom-haiku" {
		t.Errorf("HaikuModel = %q; want custom-haiku", newCfg.HaikuModel)
	}

	// Verify BuildFlags uses the model
	cc := claude.Config{
		Model:          newCfg.SonnetModel,
		PermissionMode: "bypassPermissions",
	}
	flags := claude.BuildFlags(cc)
	modelFound := false
	for i, f := range flags {
		if f == "--model" && i+1 < len(flags) && flags[i+1] == "custom-sonnet" {
			modelFound = true
		}
	}
	if !modelFound {
		t.Errorf("BuildFlags did not include --model custom-sonnet; got %v", flags)
	}

	// Also verify session picks it up
	var dbg bytes.Buffer
	sessResult, err := cmd.SessionCmd(newCfg, nil, &dbg)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range sessResult.Env {
		if e == "ANTHROPIC_DEFAULT_SONNET_MODEL=custom-sonnet" {
			return // success
		}
	}
	t.Error("session env missing ANTHROPIC_DEFAULT_SONNET_MODEL=custom-sonnet")
}

func TestE2EWriteCodeAndVerify(t *testing.T) {
	cfg, tmpDir := setupE2E(t)

	// Copy testdata/main.go to tmpdir
	testFile, err := os.ReadFile("testdata/main.go")
	if err != nil {
		t.Fatal("cannot read testdata/main.go:", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), testFile, 0644); err != nil {
		t.Fatal(err)
	}

	prompt := `Read the file main.go in the current directory. Respond with ONLY the name of the function that adds two numbers. No explanation, just the function name.`
	cc := buildTestClaudeConfig(t, cfg, tmpDir, prompt)

	exitCode, err := claude.Execute(cc)
	if err != nil {
		t.Logf("Execute error: %v", err)
	}

	if parseErr := claude.ParseRawJSON(cc.JobDir); parseErr != nil {
		t.Fatalf("ParseRawJSON: %v", parseErr)
	}

	stdout, _ := os.ReadFile(filepath.Join(cc.JobDir, "stdout.txt"))
	t.Logf("Exit code: %d, stdout: %s", exitCode, string(stdout))

	if exitCode != 0 {
		stderr, _ := os.ReadFile(filepath.Join(cc.JobDir, "stderr.txt"))
		t.Fatalf("claude exited %d, stderr: %s", exitCode, string(stderr))
	}

	if !strings.Contains(strings.ToLower(string(stdout)), "calculatesum") {
		t.Errorf("stdout should contain 'calculateSum'; got: %q", string(stdout))
	}
}

func TestE2EAPIKeyPermissions(t *testing.T) {
	_, tmpDir := setupE2E(t)
	keyPath := filepath.Join(tmpDir, "config", "zai_api_key")

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("API key file permissions = %o; want 0600", perm)
	}
}

// ensure fmt and time imports are used (they are used above; this is a compile guard)
var _ = fmt.Sprintf
var _ = time.Second
