package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
)

// ─── DoctorCmd tests ─────────────────────────────────────────────────────────

// TestDoctorCmdOutputFormat verifies that DoctorCmd writes all expected check
// names to the output writer.
func TestDoctorCmdOutputFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		// Use a non-existent endpoint and very short timeout to avoid real network calls.
		ZAIEndpoint: "http://127.0.0.1:1",
		HTTPTimeout: 1 * time.Millisecond,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatalf("DoctorCmd unexpected error: %v", err)
	}
	output := buf.String()

	expectedNames := []string{"claude_cli", "api_key", "models", "slots", "platform"}
	for _, name := range expectedNames {
		if !strings.Contains(output, name) {
			t.Errorf("output missing check name %q; got:\n%s", name, output)
		}
	}
}

// TestDoctorCmdWithAPIKey creates a temp file with an API key and passes its
// path in DoctorOptions.APIKeyPath, verifying that the api_key check shows "OK".
func TestDoctorCmdWithAPIKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api_key")
	if err := os.WriteFile(keyPath, []byte("sk-test-key"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		APIKeyPath:  keyPath,
		ZAIEndpoint: "http://127.0.0.1:1",
		HTTPTimeout: 1 * time.Millisecond,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatalf("DoctorCmd unexpected error: %v", err)
	}
	output := buf.String()

	// Find the api_key line and verify it shows OK.
	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "api_key") {
			if !strings.Contains(line, "OK") {
				t.Errorf("api_key line expected OK; got: %q", line)
			}
			return
		}
	}
	t.Errorf("api_key line not found in output:\n%s", output)
}

// TestDoctorCmdWithoutAPIKey passes a non-existent path and verifies that
// the api_key check shows "FAIL".
func TestDoctorCmdWithoutAPIKey(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		APIKeyPath:  "/nonexistent/path/to/api_key_file",
		ZAIEndpoint: "http://127.0.0.1:1",
		HTTPTimeout: 1 * time.Millisecond,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatalf("DoctorCmd unexpected error: %v", err)
	}
	output := buf.String()

	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "api_key") {
			if !strings.Contains(line, "FAIL") {
				t.Errorf("api_key line expected FAIL; got: %q", line)
			}
			return
		}
	}
	t.Errorf("api_key line not found in output:\n%s", output)
}

// TestDoctorCmdModelsDisplay passes custom model names and verifies they
// appear in the output.
func TestDoctorCmdModelsDisplay(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		OpusModel:   "my-opus-model",
		SonnetModel: "my-sonnet-model",
		HaikuModel:  "my-haiku-model",
		ZAIEndpoint: "http://127.0.0.1:1",
		HTTPTimeout: 1 * time.Millisecond,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatalf("DoctorCmd unexpected error: %v", err)
	}
	output := buf.String()

	for _, model := range []string{"my-opus-model", "my-sonnet-model", "my-haiku-model"} {
		if !strings.Contains(output, model) {
			t.Errorf("output missing model name %q; got:\n%s", model, output)
		}
	}
}

// TestDoctorCmdSlotsDisplay creates a temp subagents dir with a running job
// and verifies the slot count appears in output.
func TestDoctorCmdSlotsDisplay(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create a running job directory.
	jobDir := filepath.Join(root, "job-running-001")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("mkdir job: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "status"), []byte("running"), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	var buf bytes.Buffer
	opts := cmd.DoctorOptions{
		SubagentsRoot: root,
		ZAIEndpoint:   "http://127.0.0.1:1",
		HTTPTimeout:   1 * time.Millisecond,
	}
	if err := cmd.DoctorCmd(opts, &buf); err != nil {
		t.Fatalf("DoctorCmd unexpected error: %v", err)
	}
	output := buf.String()

	// The slots line should show "1 running" (no max since global limit is removed).
	if !strings.Contains(output, "1 running") {
		t.Errorf("output missing slot count '1 running'; got:\n%s", output)
	}
}

// ─── ConfigShowCmd tests ──────────────────────────────────────────────────────

// TestConfigShowCmdDefaults verifies that ConfigShowCmd with no TOML and no
// env vars displays all expected config keys.
func TestConfigShowCmdDefaults(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opts := cmd.ConfigShowOptions{
		EnvGetenv: func(string) string { return "" }, // no env vars
	}
	if err := cmd.ConfigShowCmd(opts, &buf); err != nil {
		t.Fatalf("ConfigShowCmd unexpected error: %v", err)
	}
	output := buf.String()

	expectedKeys := []string{
		"model", "opus_model", "sonnet_model", "haiku_model",
		"permission_mode", "debug",
		"zai_base_url", "zai_api_timeout_ms",
	}
	for _, key := range expectedKeys {
		if !strings.Contains(output, key) {
			t.Errorf("output missing key %q; got:\n%s", key, output)
		}
	}
}

// TestConfigShowCmdWithTOML creates a temp dir with glm.toml and verifies
// that config values from the file show "(config)" as the source annotation.
func TestConfigShowCmdWithTOML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	toml := `model = "glm-4.9"
`
	if err := os.WriteFile(filepath.Join(dir, "glm.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write glm.toml: %v", err)
	}

	var buf bytes.Buffer
	opts := cmd.ConfigShowOptions{
		ConfigDir: dir,
		EnvGetenv: func(string) string { return "" },
	}
	if err := cmd.ConfigShowCmd(opts, &buf); err != nil {
		t.Fatalf("ConfigShowCmd unexpected error: %v", err)
	}
	output := buf.String()

	if !strings.Contains(output, "(config)") {
		t.Errorf("output should contain '(config)' annotation for TOML values; got:\n%s", output)
	}
	if !strings.Contains(output, "glm-4.9") {
		t.Errorf("output should contain TOML model value 'glm-4.9'; got:\n%s", output)
	}
}

// TestConfigShowCmdWithEnv passes a custom EnvGetenv returning a GLM_MODEL
// value and verifies that the model shows "(env)" as the source annotation.
func TestConfigShowCmdWithEnv(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opts := cmd.ConfigShowOptions{
		EnvGetenv: func(key string) string {
			if key == "GLM_MODEL" {
				return "env-override-model"
			}
			return ""
		},
	}
	if err := cmd.ConfigShowCmd(opts, &buf); err != nil {
		t.Fatalf("ConfigShowCmd unexpected error: %v", err)
	}
	output := buf.String()

	if !strings.Contains(output, "(env)") {
		t.Errorf("output should contain '(env)' annotation; got:\n%s", output)
	}
	if !strings.Contains(output, "env-override-model") {
		t.Errorf("output should contain env model value 'env-override-model'; got:\n%s", output)
	}
}

// ─── ConfigSetCmd tests ───────────────────────────────────────────────────────

// TestConfigSetCmdHappyPath sets a known config key and verifies glm.toml
// is updated correctly.
func TestConfigSetCmdHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := cmd.ConfigSetOptions{
		ConfigDir: dir,
		Key:       "model",
		Value:     "glm-5.0",
	}
	if err := cmd.ConfigSetCmd(opts); err != nil {
		t.Fatalf("ConfigSetCmd unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "glm.toml"))
	if err != nil {
		t.Fatalf("read glm.toml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "glm-5.0") {
		t.Errorf("glm.toml should contain 'glm-5.0'; got:\n%s", content)
	}
	if !strings.Contains(content, "model") {
		t.Errorf("glm.toml should contain key 'model'; got:\n%s", content)
	}
}

// TestConfigSetCmdUnknownKey verifies that setting an unknown key returns
// an err:user error.
func TestConfigSetCmdUnknownKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := cmd.ConfigSetOptions{
		ConfigDir: dir,
		Key:       "unknown_key_xyz",
		Value:     "somevalue",
	}
	err := cmd.ConfigSetCmd(opts)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.HasPrefix(err.Error(), "err:user") {
		t.Errorf("error should start with err:user; got: %s", err.Error())
	}
}

// TestConfigSetCmdInvalidPermissionMode verifies that setting an invalid
// permission_mode value returns an error.
func TestConfigSetCmdInvalidPermissionMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := cmd.ConfigSetOptions{
		ConfigDir: dir,
		Key:       "permission_mode",
		Value:     "invalid_mode",
	}
	err := cmd.ConfigSetCmd(opts)
	if err == nil {
		t.Fatal("expected error for invalid permission_mode, got nil")
	}
	if !strings.Contains(err.Error(), "permission_mode") {
		t.Errorf("error should mention 'permission_mode'; got: %s", err.Error())
	}
}

// TestConfigSetCmdAPIRPSIsUnknown verifies that api_rps is no longer a
// recognized config key (the global concurrency limit was removed).
func TestConfigSetCmdAPIRPSIsUnknown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := cmd.ConfigSetOptions{
		ConfigDir: dir,
		Key:       "api_rps",
		Value:     "3",
	}
	err := cmd.ConfigSetCmd(opts)
	if err == nil {
		t.Fatal("expected error for removed api_rps key, got nil")
	}
	if !strings.HasPrefix(err.Error(), "err:user") {
		t.Errorf("error should start with err:user; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "api_rps") {
		t.Errorf("error should mention 'api_rps'; got: %s", err.Error())
	}
}

// TestConfigSetCmdCreatesDirectory verifies that ConfigSetCmd creates the
// config directory if it does not exist.
func TestConfigSetCmdCreatesDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Use a nested directory that doesn't exist yet.
	dir := filepath.Join(base, "nested", "config")

	opts := cmd.ConfigSetOptions{
		ConfigDir: dir,
		Key:       "model",
		Value:     "glm-5",
	}
	if err := cmd.ConfigSetCmd(opts); err != nil {
		t.Fatalf("ConfigSetCmd unexpected error: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("ConfigSetCmd should have created config directory %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.toml")); os.IsNotExist(err) {
		t.Errorf("ConfigSetCmd should have created glm.toml in %s", dir)
	}
}
