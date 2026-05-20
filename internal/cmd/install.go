// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InstallOptions configures the install command.
type InstallOptions struct {
	// CloneDir is the directory where GoLeM source lives (used for re-reading the
	// CLAUDE.md template on update). Empty for go-install mode.
	CloneDir string
	// ConfigDir is the GoLeM config directory (default: ~/.config/GoLeM).
	ConfigDir string
	// ClaudeMDPath is the target CLAUDE.md file (default: ~/.claude/CLAUDE.md).
	ClaudeMDPath string
	// SubagentsDir is the subagents directory (default: ~/.claude/subagents).
	SubagentsDir string
	// Version is the current glm version string (e.g. "1.0.0").
	Version string
	// In is the reader used for interactive prompts (defaults to os.Stdin).
	In io.Reader
	// Out is the writer used for prompt output (defaults to os.Stdout).
	Out io.Writer
}

// prompter wraps a reader for line-by-line prompting without losing buffered data.
type prompter struct {
	scanner *bufio.Scanner
	out     io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{
		scanner: bufio.NewScanner(in),
		out:     out,
	}
}

func (p *prompter) prompt(message string) (string, error) {
	_, _ = fmt.Fprint(p.out, message)
	if p.scanner.Scan() {
		return strings.TrimSpace(p.scanner.Text()), nil
	}
	if err := p.scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func (p *prompter) promptYN(message string) (bool, error) {
	resp, err := p.prompt(message)
	if err != nil {
		return false, err
	}
	return strings.ToLower(resp) == "y", nil
}

// glmSubagentTemplate is the GLM section content to inject into CLAUDE.md.
// The actual template content is loaded from CloneDir/CLAUDE.md if available.
const glmSubagentTemplate = `<!-- GLM-SUBAGENT-START -->
## GLM Subagent (GLM-5 via Z.AI) — MANDATORY

You have access to ` + "`glm`" + ` — a tool that spawns parallel Claude Code agents powered by GLM-5 via Z.AI.
` + "GLM agents are free, full Claude Code instances — they read/write files, run tests, use MCP servers and tools." + `

### When to delegate to GLM
- **Implementation work**: coding, refactoring, file edits, test writing
- **Parallel independent tasks**: launch multiple agents with ` + "`glm start`" + `
- **Sequential pipelines**: dependent steps with ` + "`glm chain`" + `
- **NOT for**: tasks requiring user interaction or approval

### Commands
` + "```" + `
glm run -d <dir> -t <sec> "prompt"    # sync: blocks until done, prints result
glm start -d <dir> -t <sec> "prompt"  # async: prints job ID, runs in background
glm chain -d <dir> "step1" "step2"    # sequential: stdout of step N → prompt of step N+1
glm status <JOB_ID>                   # check: queued/running/done/failed/timeout
glm result <JOB_ID>                   # read job stdout
glm log    <JOB_ID>                   # read file changelog (edits, writes, deletes)
glm list                              # list all jobs with proxy stats
glm kill   <JOB_ID>                   # stop a running job
` + "```" + `

### Rules
- **Always set -t (timeout)**: agents can hang. Use ` + "`-t 300`" + ` (5 min) or ` + "`-t 600`" + ` (10 min).
- **Always set -d (directory)**: agents work in that directory. Use absolute paths.
- **Flags before prompt**: ` + "`glm start -d /path -t 300 \"your prompt\"`" + ` — prompt is positional, must come last.
- **Check results**: after ` + "`glm start`" + `, poll with ` + "`glm list`" + ` or ` + "`glm status <ID>`" + `, then read with ` + "`glm result <ID>`" + `.
- **Rate limiting**: per-model concurrency limits are configured via the `+"`[models]`"+` section in glm.toml. No global limit is enforced.
- **No mocks, no stubs**: GLM agents write real code in real directories.

### Multi-agent pattern
` + "```bash" + `
# Launch parallel agents
JOB1=$(glm start -d /path -t 300 "task 1")
JOB2=$(glm start -d /path -t 300 "task 2")
# Monitor
glm list
# Read results when done
glm result $JOB1
glm result $JOB2
` + "```" + `
<!-- GLM-SUBAGENT-END -->`

// glmSectionStart is the start marker for the GLM section in CLAUDE.md.
const glmSectionStart = "<!-- GLM-SUBAGENT-START -->"

// glmSectionEnd is the end marker for the GLM section in CLAUDE.md.
const glmSectionEnd = "<!-- GLM-SUBAGENT-END -->"

// InstallCmd runs the interactive glm _install flow:
//  1. Prompts for Z.AI API key (saves to ConfigDir/zai_api_key, mode 0600).
//  2. Prompts for permission mode (saves to ConfigDir/glm.toml).
//  3. Writes ConfigDir/config.json with metadata.
//  4. Prints binary path info.
//  5. Injects the GLM subagent section into ClaudeMDPath (idempotent).
//  6. Creates SubagentsDir.
//  7. Registers the MCP server in ~/.claude/settings.json.
func InstallCmd(opts InstallOptions) error {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	// Create a shared prompter to avoid losing buffered input.
	p := newPrompter(in, out)

	// Ensure config directory exists.
	if err := os.MkdirAll(opts.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Step 1: API key — check existing, then prompt.
	apiKeyPath := filepath.Join(opts.ConfigDir, "zai_api_key")
	apiKeyExists := false
	if _, err := os.Stat(apiKeyPath); err == nil {
		apiKeyExists = true
	}

	writeKey := true
	if apiKeyExists {
		overwrite, err := p.promptYN("Z.AI API key already exists. Overwrite? [y/N]: ")
		if err != nil {
			return fmt.Errorf("read overwrite prompt: %w", err)
		}
		writeKey = overwrite
	}

	if writeKey {
		apiKey, err := p.prompt("Enter Z.AI API key: ")
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			return fmt.Errorf(`err:user "API key cannot be empty"`)
		}
		if err := os.WriteFile(apiKeyPath, []byte(apiKey), 0o600); err != nil {
			return fmt.Errorf("write API key: %w", err)
		}
	}

	// Step 2: Permission mode (only if glm.toml does not exist).
	tomlPath := filepath.Join(opts.ConfigDir, "glm.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		permMode, err := p.prompt("Permission mode [bypassPermissions/acceptEdits] (default: bypassPermissions): ")
		if err != nil {
			return fmt.Errorf("read permission mode: %w", err)
		}
		if permMode == "" {
			permMode = "bypassPermissions"
		}
		tomlContent := fmt.Sprintf("permission_mode = %q\n", permMode)
		if err := os.WriteFile(tomlPath, []byte(tomlContent), 0o644); err != nil {
			return fmt.Errorf("write glm.toml: %w", err)
		}
	}

	// Step 3: Write config.json with metadata.
	type configMeta struct {
		InstalledAt string `json:"installed_at"`
		Version     string `json:"version"`
		InstallMode string `json:"install_mode"`
		CloneDir    string `json:"clone_dir,omitempty"`
	}
	installMode := "go-install"
	if opts.CloneDir != "" {
		if _, err := os.Stat(filepath.Join(opts.CloneDir, ".git")); err == nil {
			installMode = "source"
		}
	}
	meta := configMeta{
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Version:     opts.Version,
		InstallMode: installMode,
	}
	if installMode == "source" {
		meta.CloneDir = opts.CloneDir
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config.json: %w", err)
	}
	configJSONPath := filepath.Join(opts.ConfigDir, "config.json")
	if err := os.WriteFile(configJSONPath, append(metaJSON, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}

	// Step 4: Print binary path info.
	_, _ = fmt.Fprintf(out, "Binary: %s\n", glmExecutablePath())

	// Step 5: Inject GLM section into CLAUDE.md.
	template := loadGLMTemplate(opts.CloneDir)
	if err := InjectClaudeMD(opts.ClaudeMDPath, template); err != nil {
		return fmt.Errorf("inject CLAUDE.md: %w", err)
	}

	// Step 6: Create subagents directory.
	if err := os.MkdirAll(opts.SubagentsDir, 0o755); err != nil {
		return fmt.Errorf("create subagents dir: %w", err)
	}

	// Step 7: Register MCP server in ~/.claude/settings.json.
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		glmBin := glmExecutablePath()
		if err := RegisterMCPServerAtPath(settingsPath, glmBin); err != nil {
			return fmt.Errorf("register MCP server: %w", err)
		}
	}

	_, _ = fmt.Fprintln(out, "GoLeM installed successfully.")
	return nil
}

// glmExecutablePath returns the path to the currently running glm binary.
func glmExecutablePath() string {
	p, err := os.Executable()
	if err != nil {
		return "glm"
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return real
}

// loadGLMTemplate reads the GLM section from CloneDir's CLAUDE.md global
// file if available, otherwise returns a minimal default template.
func loadGLMTemplate(cloneDir string) string {
	if cloneDir == "" {
		return glmSubagentTemplate
	}
	// Try to read the template from ~/.claude/CLAUDE.md or from the repo's template file.
	candidates := []string{
		filepath.Join(cloneDir, "CLAUDE.md"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Extract the GLM section.
		content := string(data)
		startIdx := strings.Index(content, glmSectionStart)
		endIdx := strings.Index(content, glmSectionEnd)
		if startIdx >= 0 && endIdx > startIdx {
			return content[startIdx : endIdx+len(glmSectionEnd)]
		}
	}
	return glmSubagentTemplate
}

// UninstallOptions configures the uninstall command.
type UninstallOptions struct {
	// ConfigDir is the GoLeM config directory.
	ConfigDir string
	// ClaudeMDPath is the CLAUDE.md file containing the GLM section.
	ClaudeMDPath string
	// SubagentsDir is the subagents directory.
	SubagentsDir string
	// In is the reader for interactive prompts.
	In io.Reader
	// Out is the writer for prompt output.
	Out io.Writer
}

// UninstallCmd runs the interactive glm _uninstall flow:
//  1. Removes the GLM section from ClaudeMDPath (leaves other content).
//  2. Deregisters the MCP server from ~/.claude/settings.json.
//  3. Prompts before removing ConfigDir/zai_api_key.
//  4. Prompts before removing SubagentsDir.
//  5. Removes ConfigDir.
func UninstallCmd(opts UninstallOptions) error {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	// Create a shared prompter to avoid losing buffered input.
	p := newPrompter(in, out)

	// Step 1: Remove GLM section from CLAUDE.md.
	if err := RemoveClaudeMDSection(opts.ClaudeMDPath); err != nil {
		return fmt.Errorf("remove CLAUDE.md section: %w", err)
	}

	// Step 2: Deregister MCP server from ~/.claude/settings.json.
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		if err := DeregisterMCPServerAtPath(settingsPath); err != nil {
			return fmt.Errorf("deregister MCP server: %w", err)
		}
	}

	// Step 3: Prompt before removing API key.
	apiKeyPath := filepath.Join(opts.ConfigDir, "zai_api_key")
	removeKey, err := p.promptYN(fmt.Sprintf("Remove credentials (%s)? [y/N]: ", apiKeyPath))
	if err != nil {
		return fmt.Errorf("read credentials prompt: %w", err)
	}
	if removeKey {
		if err := os.Remove(apiKeyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove API key: %w", err)
		}
	}

	// Step 4: Prompt before removing subagents directory.
	removeSubagents, err := p.promptYN(fmt.Sprintf("Remove job results (%s)? [y/N]: ", opts.SubagentsDir))
	if err != nil {
		return fmt.Errorf("read subagents prompt: %w", err)
	}
	if removeSubagents {
		if err := os.RemoveAll(opts.SubagentsDir); err != nil {
			return fmt.Errorf("remove subagents dir: %w", err)
		}
	}

	// Step 5: Remove config directory.
	if err := os.RemoveAll(opts.ConfigDir); err != nil {
		return fmt.Errorf("remove config dir: %w", err)
	}

	_, _ = fmt.Fprintln(out, "GoLeM uninstalled.")
	return nil
}

// UpdateOptions configures the update command.
type UpdateOptions struct {
	// ConfigDir is the GoLeM config directory (for reading config.json install_mode).
	ConfigDir string
	// CloneDir is the git repository to update (only used for source installs).
	CloneDir string
	// ClaudeMDPath is the CLAUDE.md to re-inject after pulling.
	ClaudeMDPath string
	// Out is the writer for progress output.
	Out io.Writer
	// ErrOut is the writer for error output.
	ErrOut io.Writer
}

// UpdateCmd implements glm update:
//
// For source installs:
//  1. Validates CloneDir is a git repository.
//  2. Records the current HEAD revision.
//  3. Runs "git pull --ff-only".
//  4. Displays old→new revisions and the commit log between them.
//  5. Re-injects the GLM section into ClaudeMDPath.
//
// For go-install:
//  1. Runs "go install github.com/veschin/GoLeM/cmd/glm@latest".
//  2. Re-injects the GLM section into ClaudeMDPath.
func UpdateCmd(opts UpdateOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}

	installMode := readInstallMode(opts.ConfigDir)

	if installMode == "go-install" {
		return updateGoInstall(opts.ClaudeMDPath, out, errOut)
	}

	return updateSource(opts.CloneDir, opts.ClaudeMDPath, out)
}

// updateSource handles update for clone-based installs via git pull.
func updateSource(cloneDir, claudeMDPath string, out io.Writer) error {
	// Validate CloneDir is a git repository.
	gitDir := filepath.Join(cloneDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return fmt.Errorf("err:user %q is not a git repository", cloneDir)
	}

	// Record the current HEAD revision.
	oldRev, err := gitRevParse(cloneDir, "HEAD")
	if err != nil {
		return fmt.Errorf("get current HEAD: %w", err)
	}

	// Run "git pull --ff-only".
	pullCmd := exec.Command("git", "pull", "--ff-only")
	pullCmd.Dir = cloneDir
	pullOutput, pullErr := pullCmd.CombinedOutput()
	if pullErr != nil {
		if strings.Contains(string(pullOutput), "Not possible to fast-forward") ||
			strings.Contains(string(pullOutput), "diverged") {
			return fmt.Errorf(`err:user "Cannot fast-forward, repository has diverged"`)
		}
		return fmt.Errorf("git pull: %s", strings.TrimSpace(string(pullOutput)))
	}

	// Get new HEAD revision.
	newRev, err := gitRevParse(cloneDir, "HEAD")
	if err != nil {
		return fmt.Errorf("get new HEAD: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Updated: %s → %s\n", oldRev, newRev)

	// Show commit log between old and new revisions if they differ.
	if oldRev != newRev {
		logCmd := exec.Command("git", "log", "--oneline", oldRev+".."+newRev)
		logCmd.Dir = cloneDir
		logOutput, _ := logCmd.Output()
		if len(logOutput) > 0 {
			_, _ = fmt.Fprintf(out, "%s\n", strings.TrimSpace(string(logOutput)))
		}
	}

	// Re-inject the GLM section into CLAUDE.md.
	template := loadGLMTemplate(cloneDir)
	if err := InjectClaudeMD(claudeMDPath, template); err != nil {
		return fmt.Errorf("inject CLAUDE.md: %w", err)
	}

	_, _ = fmt.Fprintln(out, "Update complete.")
	return nil
}

// updateGoInstall handles update for go-install-based installs.
func updateGoInstall(claudeMDPath string, out, errOut io.Writer) error {
	_, _ = fmt.Fprintln(out, "Updating via go install...")
	goCmd := exec.Command("go", "install", "github.com/veschin/GoLeM/cmd/glm@latest")
	goCmd.Stdout = out
	goCmd.Stderr = errOut
	if err := goCmd.Run(); err != nil {
		return fmt.Errorf("go install: %w", err)
	}

	// Re-inject CLAUDE.md with default template (no clone dir for go-install).
	if err := InjectClaudeMD(claudeMDPath, glmSubagentTemplate); err != nil {
		return fmt.Errorf("inject CLAUDE.md: %w", err)
	}

	_, _ = fmt.Fprintln(out, "Update complete.")
	return nil
}

// readInstallMode reads the install_mode from config.json in configDir.
// Returns "source" as default if config.json is missing or unreadable.
func readInstallMode(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return "source"
	}
	var meta struct {
		InstallMode string `json:"install_mode"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "source"
	}
	if meta.InstallMode == "" {
		return "source"
	}
	return meta.InstallMode
}

// gitRevParse runs "git rev-parse --short <ref>" in dir and returns the output.
func gitRevParse(dir, ref string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// InjectClaudeMD injects or replaces the GLM subagent section (bounded by
// <!-- GLM-SUBAGENT-START --> and <!-- GLM-SUBAGENT-END --> markers) in the
// file at claudeMDPath using content from template.
//
//   - If the file does not exist it is created containing only the section.
//   - If the file exists with both markers the section between them is replaced.
//   - If the file exists without markers the section is appended at the end.
func InjectClaudeMD(claudeMDPath, template string) error {
	// Ensure the template itself contains the markers.
	// If it doesn't already have them, wrap it.
	templateContent := template
	if !strings.Contains(templateContent, glmSectionStart) {
		templateContent = glmSectionStart + "\n" + template + "\n" + glmSectionEnd
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(claudeMDPath), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	// Check if file exists.
	existing, err := os.ReadFile(claudeMDPath)
	if os.IsNotExist(err) {
		// File does not exist — create it with only the section.
		return os.WriteFile(claudeMDPath, []byte(templateContent+"\n"), 0o644)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", claudeMDPath, err)
	}

	content := string(existing)
	startIdx := strings.Index(content, glmSectionStart)
	endIdx := strings.Index(content, glmSectionEnd)

	if startIdx >= 0 && endIdx > startIdx {
		// Both markers found — replace the section between them (inclusive).
		before := content[:startIdx]
		after := content[endIdx+len(glmSectionEnd):]
		newContent := before + templateContent + after
		return os.WriteFile(claudeMDPath, []byte(newContent), 0o644)
	}

	// No markers — append the section at the end.
	// Add a newline separator if the file doesn't end with one.
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	newContent := content + templateContent + "\n"
	return os.WriteFile(claudeMDPath, []byte(newContent), 0o644)
}

// RegisterMCPServerAtPath reads the JSON file at path, adds or updates the
// "golem" entry under "mcpServers", and writes it back atomically.
// If the file does not exist, it is created with the minimal structure.
func RegisterMCPServerAtPath(path string, glmBinPath string) error {
	// Read existing settings or start with empty object.
	var settings map[string]any
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			// File exists but is malformed -- start fresh.
			settings = make(map[string]any)
		}
	} else {
		settings = make(map[string]any)
	}

	// Get or create mcpServers map.
	mcpServers, ok := settings["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	// Set golem entry.
	mcpServers["golem"] = map[string]any{
		"command": glmBinPath,
		"args":    []string{"mcp"},
	}
	settings["mcpServers"] = mcpServers

	// Write atomically.
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}

	// Atomic write via tmp+rename.
	tmp := path + ".tmp." + fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write settings tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename settings: %w", err)
	}
	return nil
}

// DeregisterMCPServerAtPath removes the "golem" entry from mcpServers in
// the JSON file at path. No-ops if the file or entry does not exist.
func DeregisterMCPServerAtPath(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil // malformed file, nothing to remove
	}

	mcpServers, ok := settings["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}

	delete(mcpServers, "golem")

	// If mcpServers is now empty, remove it entirely.
	if len(mcpServers) == 0 {
		delete(settings, "mcpServers")
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	tmp := path + ".tmp." + fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write settings tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename settings: %w", err)
	}
	return nil
}

// RemoveClaudeMDSection removes the GLM subagent section (including the marker
// lines themselves) from the file at claudeMDPath. Content outside the markers
// is preserved. No-ops when the file does not exist or contains no markers.
func RemoveClaudeMDSection(claudeMDPath string) error {
	data, err := os.ReadFile(claudeMDPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", claudeMDPath, err)
	}

	content := string(data)
	startIdx := strings.Index(content, glmSectionStart)
	endIdx := strings.Index(content, glmSectionEnd)

	if startIdx < 0 || endIdx <= startIdx {
		// No markers found — no-op.
		return nil
	}

	// Remove from start marker to end of end marker (inclusive).
	before := content[:startIdx]
	after := content[endIdx+len(glmSectionEnd):]

	// Trim any trailing newline from "before" and leading newline from "after"
	// to avoid leaving a blank line where the section was.
	before = strings.TrimRight(before, "\n")
	after = strings.TrimLeft(after, "\n")

	var newContent string
	if before != "" && after != "" {
		newContent = before + "\n" + after
	} else if before != "" {
		newContent = before + "\n"
	} else if after != "" {
		newContent = after
	}
	// If both are empty, newContent is ""

	return os.WriteFile(claudeMDPath, []byte(newContent), 0o644)
}
