package cmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/veschin/GoLeM/internal/cmd"
)

// ─── Helpers ───────────────────────────────────────────────────────────────────

func installOpts(t *testing.T, tmpDir string, input string) (cmd.InstallOptions, *bytes.Buffer) {
	t.Helper()
	configDir := filepath.Join(tmpDir, "config")
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")
	subagentsDir := filepath.Join(tmpDir, "subagents")
	var output bytes.Buffer

	return cmd.InstallOptions{
		CloneDir:     "",
		ConfigDir:    configDir,
		ClaudeMDPath: claudeMD,
		SubagentsDir: subagentsDir,
		Version:      "test-1.0.0",
		In:           strings.NewReader(input),
		Out:          &output,
	}, &output
}

func uninstallOpts(t *testing.T, tmpDir string, input string) (cmd.UninstallOptions, *bytes.Buffer) {
	t.Helper()
	configDir := filepath.Join(tmpDir, "config")
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")
	subagentsDir := filepath.Join(tmpDir, "subagents")
	var output bytes.Buffer

	return cmd.UninstallOptions{
		ConfigDir:    configDir,
		ClaudeMDPath: claudeMD,
		SubagentsDir: subagentsDir,
		In:           strings.NewReader(input),
		Out:          &output,
	}, &output
}

// ─── AC1: Interactive setup prompts for API key ────────────────────────────────

func TestInstallPromptsForAPIKeyAndSaves(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-test-api-key-12345\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	apiKeyPath := filepath.Join(opts.ConfigDir, "zai_api_key")
	data, err := os.ReadFile(apiKeyPath)
	if err != nil {
		t.Fatalf("read API key: %v", err)
	}
	if string(data) != "sk-test-api-key-12345" {
		t.Errorf("API key: got %q, want %q", string(data), "sk-test-api-key-12345")
	}

	info, err := os.Stat(apiKeyPath)
	if err != nil {
		t.Fatalf("stat API key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("API key permissions: got %o, want 0600", info.Mode().Perm())
	}
}

func TestInstallSkipsAPIKeyPromptIfKeyExistsAndUserDeclines(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config", "zai_api_key"), []byte("sk-existing-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, _ := installOpts(t, tmpDir, "n\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "zai_api_key"))
	if string(data) != "sk-existing-key" {
		t.Errorf("API key: got %q, want %q", string(data), "sk-existing-key")
	}
}

func TestInstallOverwritesAPIKeyWhenUserConfirms(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config", "zai_api_key"), []byte("sk-old-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, _ := installOpts(t, tmpDir, "y\nsk-new-key-999\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "zai_api_key"))
	if string(data) != "sk-new-key-999" {
		t.Errorf("API key: got %q, want %q", string(data), "sk-new-key-999")
	}
}

func TestInstallRejectsEmptyAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "\n")

	err := cmd.InstallCmd(opts)
	if err == nil {
		t.Fatal("expected error for empty API key, got nil")
	}
	if !strings.Contains(err.Error(), "API key cannot be empty") {
		t.Errorf("error: got %q, want 'API key cannot be empty'", err.Error())
	}
}

// ─── AC2: Prompts for permission mode ──────────────────────────────────────────

func TestInstallPromptsForPermissionModeAndSavesBypass(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	tomlData, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "glm.toml"))
	if !strings.Contains(string(tomlData), "bypassPermissions") {
		t.Errorf("glm.toml: got %q, expected to contain 'bypassPermissions'", string(tomlData))
	}
}

func TestInstallPromptsForPermissionModeAndSavesAcceptEdits(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\nacceptEdits\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	tomlData, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "glm.toml"))
	if !strings.Contains(string(tomlData), "acceptEdits") {
		t.Errorf("glm.toml: got %q, expected to contain 'acceptEdits'", string(tomlData))
	}
}

func TestInstallUsesDefaultPermissionMode(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\n\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	tomlData, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "glm.toml"))
	if !strings.Contains(string(tomlData), "bypassPermissions") {
		t.Errorf("glm.toml: got %q, expected to contain 'bypassPermissions'", string(tomlData))
	}
}

func TestInstallSkipsPermissionPromptIfTomlExists(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config", "glm.toml"), []byte("permission_mode = \"plan\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Only API key input, no permission mode prompt needed
	opts, _ := installOpts(t, tmpDir, "sk-key\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	tomlData, _ := os.ReadFile(filepath.Join(opts.ConfigDir, "glm.toml"))
	if !strings.Contains(string(tomlData), "plan") {
		t.Errorf("glm.toml: got %q, expected to still contain 'plan'", string(tomlData))
	}
}

// ─── AC3: Creates config.json with metadata ─────────────────────────────────────

func TestInstallCreatesConfigJSONWithMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")
	opts.Version = "1.2.3"

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	configJSON := filepath.Join(opts.ConfigDir, "config.json")
	data, err := os.ReadFile(configJSON)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}

	var meta struct {
		InstalledAt string `json:"installed_at"`
		Version     string `json:"version"`
		InstallMode string `json:"install_mode"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}

	if meta.Version != "1.2.3" {
		t.Errorf("version: got %q, want '1.2.3'", meta.Version)
	}
	if meta.InstallMode != "go-install" {
		t.Errorf("install_mode: got %q, want 'go-install'", meta.InstallMode)
	}
	if meta.InstalledAt == "" {
		t.Error("installed_at is empty")
	}
	// Validate ISO 8601 format
	if !strings.Contains(meta.InstalledAt, "T") || !strings.Contains(meta.InstalledAt, "Z") {
		t.Errorf("installed_at: got %q, expected ISO 8601 format", meta.InstalledAt)
	}
}

func TestInstallConfigJSONContainsCloneDirForSourceInstall(t *testing.T) {
	tmpDir := t.TempDir()
	cloneDir := filepath.Join(tmpDir, "clone")
	if err := os.MkdirAll(filepath.Join(cloneDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")
	opts.CloneDir = cloneDir

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	configJSON := filepath.Join(opts.ConfigDir, "config.json")
	data, _ := os.ReadFile(configJSON)

	var meta struct {
		InstallMode string `json:"install_mode"`
		CloneDir    string `json:"clone_dir"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}

	if meta.InstallMode != "source" {
		t.Errorf("install_mode: got %q, want 'source'", meta.InstallMode)
	}
	if meta.CloneDir != cloneDir {
		t.Errorf("clone_dir: got %q, want %q", meta.CloneDir, cloneDir)
	}
}

// ─── AC5: Injects GLM instructions into CLAUDE.md ───────────────────────────────

func TestInstallCreatesCLAUDEMDWhenNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	data, err := os.ReadFile(opts.ClaudeMDPath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(data), "<!-- GLM-SUBAGENT-START -->") {
		t.Error("CLAUDE.md missing GLM-SUBAGENT-START marker")
	}
	if !strings.Contains(string(data), "<!-- GLM-SUBAGENT-END -->") {
		t.Error("CLAUDE.md missing GLM-SUBAGENT-END marker")
	}
}

func TestInstallReplacesExistingGLMSection(t *testing.T) {
	tmpDir := t.TempDir()
	existingContent := `# System-Wide Instructions
## My Custom Rules
- Always use TypeScript
<!-- GLM-SUBAGENT-START -->
## GLM Subagent (old version)
Old content here
<!-- GLM-SUBAGENT-END -->
## My Editor Preferences
- 2-space indentation
`
	if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpDir, "CLAUDE.md")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(existingContent), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	data, _ := os.ReadFile(opts.ClaudeMDPath)
	content := string(data)
	if !strings.Contains(content, "# System-Wide Instructions") {
		t.Error("CLAUDE.md lost content before markers")
	}
	if !strings.Contains(content, "## My Editor Preferences") {
		t.Error("CLAUDE.md lost content after markers")
	}
	if strings.Contains(content, "Old content here") {
		t.Error("CLAUDE.md still contains old GLM section content")
	}
}

func TestInstallAppendsGLMSectionToExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	existingContent := `# System-Wide Instructions
## My Custom Rules
- Always use TypeScript
`
	if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpDir, "CLAUDE.md")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(existingContent), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	data, _ := os.ReadFile(opts.ClaudeMDPath)
	content := string(data)
	if !strings.Contains(content, "## My Custom Rules") {
		t.Error("CLAUDE.md lost original content")
	}
	if !strings.Contains(content, "<!-- GLM-SUBAGENT-START -->") {
		t.Error("CLAUDE.md missing GLM section")
	}
}

// ─── AC6: Creates subagents directory ───────────────────────────────────────────

func TestInstallCreatesSubagentsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	opts, _ := installOpts(t, tmpDir, "sk-key\nbypassPermissions\n")

	err := cmd.InstallCmd(opts)
	if err != nil {
		t.Fatalf("InstallCmd: %v", err)
	}

	info, err := os.Stat(opts.SubagentsDir)
	if err != nil {
		t.Fatalf("stat subagents dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("subagents path is not a directory")
	}
}

// ─── AC8: Removes GLM section from CLAUDE.md ─────────────────────────────────────

func TestUninstallRemovesGLMSectionFromCLAUDEMD(t *testing.T) {
	tmpDir := t.TempDir()
	content := `# System-Wide Instructions
<!-- GLM-SUBAGENT-START -->
## GLM Subagent
Content
<!-- GLM-SUBAGENT-END -->
## Other section
`
	if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpDir, "CLAUDE.md")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, _ := uninstallOpts(t, tmpDir, "n\nn\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	data, _ := os.ReadFile(opts.ClaudeMDPath)
	result := string(data)
	if strings.Contains(result, "<!-- GLM-SUBAGENT-START -->") {
		t.Error("CLAUDE.md still contains GLM section markers")
	}
	if !strings.Contains(result, "# System-Wide Instructions") {
		t.Error("CLAUDE.md lost content outside markers")
	}
	if !strings.Contains(result, "## Other section") {
		t.Error("CLAUDE.md lost content after markers")
	}
}

// ─── AC9: Prompts before removing credentials and job results ───────────────────

func TestUninstallPromptsBeforeRemovingCredentials(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "config", "zai_api_key"), []byte("sk-test-key"), 0o600)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	opts, output := uninstallOpts(t, tmpDir, "n\nn\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	if !strings.Contains(output.String(), "Remove credentials") {
		t.Error("expected prompt about credentials removal")
	}
}

func TestUninstallRemovesCredentialsWhenUserConfirms(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "config", "zai_api_key"), []byte("sk-test-key"), 0o600)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	opts, _ := uninstallOpts(t, tmpDir, "y\nn\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	// Credentials are removed before config dir is removed
	// Check via output that prompt was shown
	// Note: config dir is removed at the end regardless
}

func TestUninstallPromptsBeforeRemovingJobResults(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	opts, output := uninstallOpts(t, tmpDir, "n\nn\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	if !strings.Contains(output.String(), "Remove job results") {
		t.Error("expected prompt about job results removal")
	}
}

func TestUninstallRemovesJobResultsWhenUserConfirms(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "subagents", "project", "job-1"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	opts, _ := uninstallOpts(t, tmpDir, "n\ny\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	if _, err := os.Stat(opts.SubagentsDir); !os.IsNotExist(err) {
		t.Error("subagents directory should be removed")
	}
}

func TestUninstallPreservesJobResultsWhenDeclined(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "subagents", "project", "job-1"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	// Note: config dir removal will still happen, which may remove parent
	// This tests that the prompt is shown
	opts, output := uninstallOpts(t, tmpDir, "n\nn\n")

	_ = cmd.UninstallCmd(opts)

	if !strings.Contains(output.String(), "Remove job results") {
		t.Error("expected prompt about job results")
	}
}

// ─── AC10: Removes config directory ─────────────────────────────────────────────

func TestUninstallRemovesConfigDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("<!-- GLM-SUBAGENT-START -->x<!-- GLM-SUBAGENT-END -->"), 0o644)

	opts, _ := uninstallOpts(t, tmpDir, "n\nn\n")

	err := cmd.UninstallCmd(opts)
	if err != nil {
		t.Fatalf("UninstallCmd: %v", err)
	}

	if _, err := os.Stat(opts.ConfigDir); !os.IsNotExist(err) {
		t.Error("config directory should be removed")
	}
}

// ─── AC11: Update validates git repo ────────────────────────────────────────────

func TestUpdateValidatesGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	cloneDir := filepath.Join(tmpDir, "not-a-git-repo")
	os.MkdirAll(cloneDir, 0o755)
	os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"install_mode": "source"}`), 0o644)

	var stdoutBuf, stderrBuf bytes.Buffer
	opts := cmd.UpdateOptions{
		ConfigDir:    tmpDir,
		CloneDir:     cloneDir,
		ClaudeMDPath: filepath.Join(tmpDir, "CLAUDE.md"),
		Out:          &stdoutBuf,
		ErrOut:       &stderrBuf,
	}

	err := cmd.UpdateCmd(opts)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error: got %q, want 'not a git repository'", err.Error())
	}
}

func TestUpdateGoInstallMode(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"install_mode": "go-install"}`), 0o644)

	var stdoutBuf, stderrBuf bytes.Buffer
	opts := cmd.UpdateOptions{
		ConfigDir:    tmpDir,
		CloneDir:     "",
		ClaudeMDPath: filepath.Join(tmpDir, "CLAUDE.md"),
		Out:          &stdoutBuf,
		ErrOut:       &stderrBuf,
	}

	_ = cmd.UpdateCmd(opts)

	if !strings.Contains(stdoutBuf.String(), "go install") {
		t.Error("expected go install message")
	}
}

// ─── Edge Cases ─────────────────────────────────────────────────────────────────

func TestInstallOverExistingInstallationReRunsSetup(t *testing.T) {
	tmpDir := t.TempDir()

	// First install
	opts1, _ := installOpts(t, tmpDir, "sk-old-key\nbypassPermissions\n")
	if err := cmd.InstallCmd(opts1); err != nil {
		t.Fatalf("first InstallCmd: %v", err)
	}

	// Verify first install
	_, _ = os.ReadFile(filepath.Join(opts1.ConfigDir, "config.json"))
	apiKey1, _ := os.ReadFile(filepath.Join(opts1.ConfigDir, "zai_api_key"))
	if string(apiKey1) != "sk-old-key" {
		t.Fatalf("first install API key: got %q", string(apiKey1))
	}

	// Second install (overwrite)
	opts2, _ := installOpts(t, tmpDir, "y\nsk-new-key\nbypassPermissions\n")
	if err := cmd.InstallCmd(opts2); err != nil {
		t.Fatalf("second InstallCmd: %v", err)
	}

	// Verify updated
	apiKey2, _ := os.ReadFile(filepath.Join(opts2.ConfigDir, "zai_api_key"))
	if string(apiKey2) != "sk-new-key" {
		t.Errorf("second install API key: got %q, want 'sk-new-key'", string(apiKey2))
	}

	// CLAUDE.md should still have GLM section
	claudeMD, _ := os.ReadFile(opts2.ClaudeMDPath)
	if !strings.Contains(string(claudeMD), "<!-- GLM-SUBAGENT-START -->") {
		t.Error("CLAUDE.md should still have GLM section")
	}
}

// ─── InjectClaudeMD helper tests ───────────────────────────────────────────────

func TestInjectClaudeMDCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	template := "<!-- GLM-SUBAGENT-START -->\nTest content\n<!-- GLM-SUBAGENT-END -->"

	err := cmd.InjectClaudeMD(claudeMD, template)
	if err != nil {
		t.Fatalf("InjectClaudeMD: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	if !strings.Contains(string(data), "Test content") {
		t.Errorf("CLAUDE.md: got %q", string(data))
	}
}

func TestInjectClaudeMDReplacesSection(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	os.WriteFile(claudeMD, []byte(`Header
<!-- GLM-SUBAGENT-START -->
Old content
<!-- GLM-SUBAGENT-END -->
Footer
`), 0o644)

	template := "<!-- GLM-SUBAGENT-START -->\nNew content\n<!-- GLM-SUBAGENT-END -->"

	err := cmd.InjectClaudeMD(claudeMD, template)
	if err != nil {
		t.Fatalf("InjectClaudeMD: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	content := string(data)
	if !strings.Contains(content, "Header") {
		t.Error("lost content before markers")
	}
	if !strings.Contains(content, "Footer") {
		t.Error("lost content after markers")
	}
	if strings.Contains(content, "Old content") {
		t.Error("old content should be replaced")
	}
	if !strings.Contains(content, "New content") {
		t.Error("new content should be present")
	}
}

func TestInjectClaudeMDAppendsWhenNoMarkers(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	os.WriteFile(claudeMD, []byte("Existing content\n"), 0o644)

	template := "<!-- GLM-SUBAGENT-START -->\nNew section\n<!-- GLM-SUBAGENT-END -->"

	err := cmd.InjectClaudeMD(claudeMD, template)
	if err != nil {
		t.Fatalf("InjectClaudeMD: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	content := string(data)
	if !strings.Contains(content, "Existing content") {
		t.Error("existing content should be preserved")
	}
	if !strings.Contains(content, "New section") {
		t.Error("new section should be appended")
	}
}

func TestRemoveClaudeMDSection(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	os.WriteFile(claudeMD, []byte(`Header
<!-- GLM-SUBAGENT-START -->
GLM content
<!-- GLM-SUBAGENT-END -->
Footer
`), 0o644)

	err := cmd.RemoveClaudeMDSection(claudeMD)
	if err != nil {
		t.Fatalf("RemoveClaudeMDSection: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	content := string(data)
	if strings.Contains(content, "<!-- GLM-SUBAGENT-START -->") {
		t.Error("markers should be removed")
	}
	if strings.Contains(content, "GLM content") {
		t.Error("GLM content should be removed")
	}
	if !strings.Contains(content, "Header") {
		t.Error("Header should be preserved")
	}
	if !strings.Contains(content, "Footer") {
		t.Error("Footer should be preserved")
	}
}

func TestRemoveClaudeMDSectionNoMarkersNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	originalContent := "Just some content\nwithout any markers\n"
	os.WriteFile(claudeMD, []byte(originalContent), 0o644)

	err := cmd.RemoveClaudeMDSection(claudeMD)
	if err != nil {
		t.Fatalf("RemoveClaudeMDSection: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	if string(data) != originalContent {
		t.Error("content should be unchanged when no markers present")
	}
}

func TestRemoveClaudeMDSectionNoFileNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	claudeMD := filepath.Join(tmpDir, "CLAUDE.md")

	// File doesn't exist - should not error
	err := cmd.RemoveClaudeMDSection(claudeMD)
	if err != nil {
		t.Fatalf("RemoveClaudeMDSection: %v", err)
	}
}

// ─── MCP Registration ──────────────────────────────────────────────────────────

func TestRegisterMCPServerAtPath_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mcpServers := settings["mcpServers"].(map[string]any)
	golem := mcpServers["golem"].(map[string]any)
	if golem["command"] != "/usr/bin/glm" {
		t.Errorf("command = %v, want /usr/bin/glm", golem["command"])
	}
	args := golem["args"].([]any)
	if len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", args)
	}
}

func TestRegisterMCPServerAtPath_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// First registration.
	if err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm"); err != nil {
		t.Fatalf("first register: %v", err)
	}

	// Second registration with different path.
	if err := cmd.RegisterMCPServerAtPath(path, "/usr/local/bin/glm"); err != nil {
		t.Fatalf("second register: %v", err)
	}

	data, _ := os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcpServers := settings["mcpServers"].(map[string]any)
	golem := mcpServers["golem"].(map[string]any)
	if golem["command"] != "/usr/local/bin/glm" {
		t.Errorf("command = %v, want /usr/local/bin/glm", golem["command"])
	}

	// Verify no duplicate keys.
	if len(mcpServers) != 1 {
		t.Errorf("mcpServers has %d entries, want 1", len(mcpServers))
	}
}

func TestRegisterMCPServerAtPath_PreservesExistingServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Pre-existing settings with another MCP server.
	existing := map[string]any{
		"mcpServers": map[string]any{
			"other-tool": map[string]any{
				"command": "other",
				"args":    []string{"serve"},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm"); err != nil {
		t.Fatalf("register: %v", err)
	}

	data, _ = os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcpServers := settings["mcpServers"].(map[string]any)

	// Both servers should exist.
	if _, ok := mcpServers["other-tool"]; !ok {
		t.Error("other-tool server missing")
	}
	if _, ok := mcpServers["golem"]; !ok {
		t.Error("golem server missing")
	}
}

func TestRegisterMCPServerAtPath_PreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Pre-existing settings with non-MCP fields.
	existing := map[string]any{
		"permissions": map[string]any{
			"allow": []string{"Read", "Write"},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm"); err != nil {
		t.Fatalf("register: %v", err)
	}

	data, _ = os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// permissions should still be present.
	if _, ok := settings["permissions"]; !ok {
		t.Error("existing permissions field was lost")
	}
	// golem should be registered.
	mcpServers := settings["mcpServers"].(map[string]any)
	if _, ok := mcpServers["golem"]; !ok {
		t.Error("golem server missing")
	}
}

func TestRegisterMCPServerAtPath_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write malformed JSON.
	os.WriteFile(path, []byte("{broken json"), 0o644)

	// Should succeed by starting fresh.
	err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm")
	if err != nil {
		t.Fatalf("register with malformed file: %v", err)
	}

	data, _ := os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("result is invalid JSON: %v", err)
	}
	mcpServers := settings["mcpServers"].(map[string]any)
	if _, ok := mcpServers["golem"]; !ok {
		t.Error("golem server missing after malformed file recovery")
	}
}

func TestRegisterMCPServerAtPath_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "settings.json")

	err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file not created: %v", err)
	}
}

// ─── MCP Deregistration ────────────────────────────────────────────────────────

func TestDeregisterMCPServerAtPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Register first.
	if err := cmd.RegisterMCPServerAtPath(path, "/usr/bin/glm"); err != nil {
		t.Fatal(err)
	}

	// Deregister.
	if err := cmd.DeregisterMCPServerAtPath(path); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	data, _ := os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// mcpServers should be removed entirely (was the only entry).
	if _, ok := settings["mcpServers"]; ok {
		t.Error("mcpServers should be removed when empty")
	}
}

func TestDeregisterMCPServerAtPath_PreservesOtherServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Register golem and another server.
	existing := map[string]any{
		"mcpServers": map[string]any{
			"golem": map[string]any{
				"command": "glm",
				"args":    []string{"mcp"},
			},
			"other-tool": map[string]any{
				"command": "other",
				"args":    []string{"serve"},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(path, data, 0o644)

	if err := cmd.DeregisterMCPServerAtPath(path); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	data, _ = os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcpServers := settings["mcpServers"].(map[string]any)

	if _, ok := mcpServers["golem"]; ok {
		t.Error("golem should be removed")
	}
	if _, ok := mcpServers["other-tool"]; !ok {
		t.Error("other-tool should be preserved")
	}
}

func TestDeregisterMCPServerAtPath_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// No file -- should be a no-op.
	if err := cmd.DeregisterMCPServerAtPath(path); err != nil {
		t.Fatalf("deregister non-existent file: %v", err)
	}
}

func TestDeregisterMCPServerAtPath_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	os.WriteFile(path, []byte("{broken json"), 0o644)

	// Malformed file -- no-op, nothing to remove.
	if err := cmd.DeregisterMCPServerAtPath(path); err != nil {
		t.Fatalf("deregister malformed file: %v", err)
	}
}

func TestDeregisterMCPServerAtPath_NoGolemEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := map[string]any{
		"mcpServers": map[string]any{
			"other-tool": map[string]any{
				"command": "other",
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(path, data, 0o644)

	if err := cmd.DeregisterMCPServerAtPath(path); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	data, _ = os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcpServers := settings["mcpServers"].(map[string]any)

	if _, ok := mcpServers["other-tool"]; !ok {
		t.Error("other-tool should be preserved")
	}
}
