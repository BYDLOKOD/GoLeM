package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/slot"
)

// helperCreateSlotManager creates a slot manager for testing.
func helperCreateSlotManager(t *testing.T, subagentsRoot string, apiRPS int) *slot.SlotManager {
	t.Helper()
	sm := slot.NewSlotManager(subagentsRoot, apiRPS)
	if err := sm.Init(); err != nil {
		t.Fatalf("slot init: %v", err)
	}
	return sm
}

func TestExecuteJob_UsesPreCreatedJob(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)

	// Pre-create the job (as cmdStart would).
	preJobID := job.GenerateJobID()
	j, err := job.NewJob(subagentsRoot, projectID, preJobID)
	if err != nil {
		t.Fatalf("pre-create job: %v", err)
	}

	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	// ExecuteJob should reuse the pre-created job directory.
	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
		JobID:         preJobID,
	})

	// Result should use the pre-created job ID and directory.
	if result.JobID != preJobID {
		t.Errorf("JobID: got %q, want %q", result.JobID, preJobID)
	}
	if result.JobDir != j.Dir {
		t.Errorf("JobDir: got %q, want %q", result.JobDir, j.Dir)
	}
}

func TestExecuteJob_GeneratesNewJobIDWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)
	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	// ExecuteJob with empty JobID should generate a new one.
	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
	})

	if result.JobID == "" {
		t.Error("JobID should not be empty when auto-generated")
	}
	if result.JobDir == "" {
		t.Error("JobDir should not be empty")
	}
	// Job directory should exist since AutoDelete is false.
	if _, err := os.Stat(result.JobDir); os.IsNotExist(err) {
		t.Errorf("JobDir %s should exist when AutoDelete is false", result.JobDir)
	}
}

func TestExecuteJob_AutoDeleteRemovesJobDir(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)
	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    true,
		SlotManager:   sm,
	})

	// Job directory should be deleted when AutoDelete is true.
	if _, err := os.Stat(result.JobDir); !os.IsNotExist(err) {
		t.Errorf("job dir %s should have been deleted with AutoDelete=true", result.JobDir)
	}

	// Data should still be available in the result struct.
	if result.JobID == "" {
		t.Error("JobID should be set even after auto-delete")
	}
	if result.Status == "" {
		t.Error("Status should be set even after auto-delete")
	}
}

func TestExecuteJob_ReleasesSlotOnReturn(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)
	sm := helperCreateSlotManager(t, subagentsRoot, 10)

	// Claim a slot manually.
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	// Run ExecuteJob which should release the slot via defer.
	_, _ = ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
	})

	// Verify the slot was released by reading the counter file.
	counterData, err := os.ReadFile(filepath.Join(subagentsRoot, slot.CounterFile))
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if string(counterData) != "0" {
		t.Errorf("counter: got %q, want %q (slot should be released)", string(counterData), "0")
	}
}

func TestExecuteJob_WritesPIDForNewJob(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)
	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
		// JobID is empty => new job => PID should be written.
	})

	pidData, err := os.ReadFile(filepath.Join(result.JobDir, "pid.txt"))
	if err != nil {
		t.Fatalf("read pid.txt: %v", err)
	}
	if len(pidData) == 0 {
		t.Error("pid.txt should not be empty for new jobs")
	}
}

func TestExecuteJob_SkipsPIDForPreCreatedJob(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)

	// Pre-create the job.
	preJobID := job.GenerateJobID()
	_, err := job.NewJob(subagentsRoot, projectID, preJobID)
	if err != nil {
		t.Fatalf("pre-create job: %v", err)
	}

	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
		JobID:         preJobID,
	})

	// PID should NOT be written by ExecuteJob for pre-created jobs (caller handles it).
	pidPath := filepath.Join(result.JobDir, "pid.txt")
	if _, err := os.Stat(pidPath); err == nil {
		t.Error("pid.txt should not be written by ExecuteJob for pre-created jobs (caller's responsibility)")
	}
}

func TestExecuteJob_StatusSetOnCompletion(t *testing.T) {
	dir := t.TempDir()
	subagentsRoot := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubagentDir: subagentsRoot,
		OpusModel:   "test-model",
		SonnetModel: "test-model",
		HaikuModel:  "test-model",
	}

	flags := &Flags{
		Dir:     dir,
		Timeout: 10,
		Prompt:  "test prompt",
	}

	projectID := job.ResolveProjectID(dir)
	sm := helperCreateSlotManager(t, subagentsRoot, 10)
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("wait for slot: %v", err)
	}

	ctx := context.Background()

	result, _ := ExecuteJob(ctx, ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
		AutoDelete:    false,
		SlotManager:   sm,
	})

	// Status should be non-empty (set by MapStatus).
	if result.Status == "" {
		t.Error("Status should be set after execution")
	}

	// Status file should exist on disk.
	statusData, err := os.ReadFile(filepath.Join(result.JobDir, "status"))
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	if string(statusData) != result.Status {
		t.Errorf("status file: got %q, want %q", string(statusData), result.Status)
	}
}

func TestBuildClaudeConfig_DefaultModels(t *testing.T) {
	cfg := &config.Config{
		OpusModel:       "opus-1",
		SonnetModel:     "sonnet-1",
		HaikuModel:      "haiku-1",
		ZaiAPIKey:       "test-key",
		ZaiBaseURL:      "https://example.com",
		ZaiAPITimeoutMs: "3000",
		PermissionMode:  "default",
	}

	flags := &Flags{
		Prompt:  "test prompt",
		Dir:     "/tmp",
		Timeout: 60,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.OpusModel != "opus-1" {
		t.Errorf("OpusModel: got %q, want %q", result.OpusModel, "opus-1")
	}
	if result.SonnetModel != "sonnet-1" {
		t.Errorf("SonnetModel: got %q, want %q", result.SonnetModel, "sonnet-1")
	}
	if result.HaikuModel != "haiku-1" {
		t.Errorf("HaikuModel: got %q, want %q", result.HaikuModel, "haiku-1")
	}
	if result.Model != "sonnet-1" {
		t.Errorf("Model (default execution): got %q, want %q", result.Model, "sonnet-1")
	}
	if result.PermissionMode != "default" {
		t.Errorf("PermissionMode: got %q, want %q", result.PermissionMode, "default")
	}
}

func TestBuildClaudeConfig_FlagModelOverridesAll(t *testing.T) {
	cfg := &config.Config{
		OpusModel:   "opus-1",
		SonnetModel: "sonnet-1",
		HaikuModel:  "haiku-1",
	}

	flags := &Flags{
		Model:   "override-model",
		Prompt:  "test",
		Dir:     "/tmp",
		Timeout: 30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.OpusModel != "override-model" {
		t.Errorf("OpusModel: got %q, want %q", result.OpusModel, "override-model")
	}
	if result.SonnetModel != "override-model" {
		t.Errorf("SonnetModel: got %q, want %q", result.SonnetModel, "override-model")
	}
	if result.HaikuModel != "override-model" {
		t.Errorf("HaikuModel: got %q, want %q", result.HaikuModel, "override-model")
	}
}

func TestBuildClaudeConfig_PerSlotOverrideTakesPriority(t *testing.T) {
	cfg := &config.Config{
		OpusModel:   "opus-1",
		SonnetModel: "sonnet-1",
		HaikuModel:  "haiku-1",
	}

	flags := &Flags{
		Model:      "override-all",
		OpusModel:  "opus-specific",
		HaikuModel: "haiku-specific",
		Prompt:     "test",
		Dir:        "/tmp",
		Timeout:    30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.OpusModel != "opus-specific" {
		t.Errorf("OpusModel: got %q, want %q (per-slot flag should take priority)", result.OpusModel, "opus-specific")
	}
	if result.SonnetModel != "override-all" {
		t.Errorf("SonnetModel: got %q, want %q (generic flag when no per-slot override)", result.SonnetModel, "override-all")
	}
	if result.HaikuModel != "haiku-specific" {
		t.Errorf("HaikuModel: got %q, want %q (per-slot flag should take priority)", result.HaikuModel, "haiku-specific")
	}
}

func TestBuildClaudeConfig_FlagPermissionModeOverride(t *testing.T) {
	cfg := &config.Config{
		PermissionMode: "default",
		OpusModel:      "m",
		SonnetModel:    "m",
		HaikuModel:     "m",
	}

	flags := &Flags{
		PermissionMode: "bypassPermissions",
		Prompt:         "test",
		Dir:            "/tmp",
		Timeout:        30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode: got %q, want %q", result.PermissionMode, "bypassPermissions")
	}
}

func TestBuildClaudeConfig_EffortPassthrough(t *testing.T) {
	cfg := &config.Config{
		OpusModel:   "m",
		SonnetModel: "m",
		HaikuModel:  "m",
		Effort:      "max",
	}

	flags := &Flags{
		Prompt:  "test",
		Dir:     "/tmp",
		Timeout: 30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.Effort != "max" {
		t.Errorf("Effort: got %q, want %q", result.Effort, "max")
	}
}

func TestBuildClaudeConfig_ExcludeDynamicSectionsPassthrough(t *testing.T) {
	cfg := &config.Config{
		OpusModel:              "m",
		SonnetModel:            "m",
		HaikuModel:             "m",
		ExcludeDynamicSections: true,
	}

	flags := &Flags{
		Prompt:  "test",
		Dir:     "/tmp",
		Timeout: 30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if !result.ExcludeDynamicSections {
		t.Error("ExcludeDynamicSections: got false, want true")
	}
}

func TestBuildClaudeConfig_ExcludeDynamicSectionsFalsePassthrough(t *testing.T) {
	cfg := &config.Config{
		OpusModel:              "m",
		SonnetModel:            "m",
		HaikuModel:             "m",
		ExcludeDynamicSections: false,
	}

	flags := &Flags{
		Prompt:  "test",
		Dir:     "/tmp",
		Timeout: 30,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.ExcludeDynamicSections {
		t.Error("ExcludeDynamicSections: got true, want false")
	}
}

func TestBuildClaudeConfig_FieldMapping(t *testing.T) {
	cfg := &config.Config{
		ZaiAPIKey:       "key-123",
		ZaiBaseURL:      "https://api.example.com",
		ZaiAPITimeoutMs: "5000",
		OpusModel:       "opus",
		SonnetModel:     "sonnet",
		HaikuModel:      "haiku",
		PermissionMode:  "default",
	}

	flags := &Flags{
		Prompt:  "do stuff",
		Dir:     "/work",
		Timeout: 120,
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/123")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}

	if result.ZAIAPIKey != "key-123" {
		t.Errorf("ZAIAPIKey: got %q, want %q", result.ZAIAPIKey, "key-123")
	}
	if result.ZAIBaseURL != "https://api.example.com" {
		t.Errorf("ZAIBaseURL: got %q, want %q", result.ZAIBaseURL, "https://api.example.com")
	}
	if result.ZAIAPITimeoutMS != "5000" {
		t.Errorf("ZAIAPITimeoutMS: got %q, want %q", result.ZAIAPITimeoutMS, "5000")
	}
	if result.Prompt != "do stuff" {
		t.Errorf("Prompt: got %q, want %q", result.Prompt, "do stuff")
	}
	if result.WorkDir != "/work" {
		t.Errorf("WorkDir: got %q, want %q", result.WorkDir, "/work")
	}
	if result.TimeoutSecs != 120 {
		t.Errorf("TimeoutSecs: got %d, want %d", result.TimeoutSecs, 120)
	}
	if result.JobDir != "/job/123" {
		t.Errorf("JobDir: got %q, want %q", result.JobDir, "/job/123")
	}
}

// System prompt tests ---------------------------------------------------------

// TestBuildClaudeConfig_SystemPromptFromFlags verifies that when flags.SystemPrompt
// is set it appears verbatim in the returned claude.Config.SystemPrompt.
func TestBuildClaudeConfig_SystemPromptFromFlags(t *testing.T) {
	cfg := &config.Config{OpusModel: "m", SonnetModel: "m", HaikuModel: "m"}
	flags := &Flags{
		Prompt:       "do work",
		Dir:          "/tmp",
		Timeout:      30,
		SystemPrompt: "You are a Go expert.",
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	if result.SystemPrompt != "You are a Go expert." {
		t.Errorf("SystemPrompt: got %q, want %q", result.SystemPrompt, "You are a Go expert.")
	}
}

// TestBuildClaudeConfig_SystemPromptFromConfig verifies that when flags.SystemPrompt
// is empty, cfg.SystemPrompt is used as the base system prompt.
func TestBuildClaudeConfig_SystemPromptFromConfig(t *testing.T) {
	cfg := &config.Config{
		OpusModel:    "m",
		SonnetModel:  "m",
		HaikuModel:   "m",
		SystemPrompt: "Default from config.",
	}
	flags := &Flags{Prompt: "do work", Dir: "/tmp", Timeout: 30}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	if result.SystemPrompt != "Default from config." {
		t.Errorf("SystemPrompt: got %q, want %q", result.SystemPrompt, "Default from config.")
	}
}

// TestBuildClaudeConfig_FlagSystemPromptOverridesConfig verifies that when both
// flags.SystemPrompt and cfg.SystemPrompt are set, the flag value wins.
func TestBuildClaudeConfig_FlagSystemPromptOverridesConfig(t *testing.T) {
	cfg := &config.Config{
		OpusModel:    "m",
		SonnetModel:  "m",
		HaikuModel:   "m",
		SystemPrompt: "From config.",
	}
	flags := &Flags{
		Prompt:       "do work",
		Dir:          "/tmp",
		Timeout:      30,
		SystemPrompt: "From flags.",
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	if result.SystemPrompt != "From flags." {
		t.Errorf("SystemPrompt: got %q, want %q (flag should override config)", result.SystemPrompt, "From flags.")
	}
}

// TestBuildClaudeConfig_ConstraintsExpanded verifies that a known constraint key
// in flags.Constraints is expanded to its full instruction text in SystemPrompt.
func TestBuildClaudeConfig_ConstraintsExpanded(t *testing.T) {
	cfg := &config.Config{OpusModel: "m", SonnetModel: "m", HaikuModel: "m"}
	flags := &Flags{
		Prompt:      "analyze code",
		Dir:         "/tmp",
		Timeout:     30,
		Constraints: []string{"readonly"},
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	want := "You MUST NOT create, modify, or delete any files. You may only read files and report findings."
	if result.SystemPrompt != want {
		t.Errorf("SystemPrompt: got %q, want %q", result.SystemPrompt, want)
	}
}

// TestBuildClaudeConfig_ConstraintsPlusSystemPrompt verifies that when both
// flags.Constraints and flags.SystemPrompt are set, the assembled result places
// expanded constraints first, then a blank line, then the system prompt.
func TestBuildClaudeConfig_ConstraintsPlusSystemPrompt(t *testing.T) {
	cfg := &config.Config{OpusModel: "m", SonnetModel: "m", HaikuModel: "m"}
	flags := &Flags{
		Prompt:       "analyze code",
		Dir:          "/tmp",
		Timeout:      30,
		Constraints:  []string{"no-create"},
		SystemPrompt: "Focus on the proxy package.",
	}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	noCreate := "You MUST NOT create any new files. You may only read or modify existing files."
	want := noCreate + "\n\n" + "Focus on the proxy package."
	if result.SystemPrompt != want {
		t.Errorf("SystemPrompt: got %q, want %q", result.SystemPrompt, want)
	}
}

// TestBuildClaudeConfig_UnknownConstraintReturnsError verifies that an unknown
// constraint key in flags.Constraints causes BuildClaudeConfig to return an error.
func TestBuildClaudeConfig_UnknownConstraintReturnsError(t *testing.T) {
	cfg := &config.Config{OpusModel: "m", SonnetModel: "m", HaikuModel: "m"}
	flags := &Flags{
		Prompt:      "analyze code",
		Dir:         "/tmp",
		Timeout:     30,
		Constraints: []string{"bogus"},
	}

	_, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err == nil {
		t.Fatal("BuildClaudeConfig: expected error for unknown constraint, got nil")
	}
}

// TestBuildClaudeConfig_NoSystemPrompt verifies that when neither flags nor config
// carry a system prompt, the returned claude.Config.SystemPrompt is empty and no
// error is returned.
func TestBuildClaudeConfig_NoSystemPrompt(t *testing.T) {
	cfg := &config.Config{OpusModel: "m", SonnetModel: "m", HaikuModel: "m"}
	flags := &Flags{Prompt: "do work", Dir: "/tmp", Timeout: 30}

	result, err := BuildClaudeConfig(cfg, flags, "/job/dir")
	if err != nil {
		t.Fatalf("BuildClaudeConfig error: %v", err)
	}
	if result.SystemPrompt != "" {
		t.Errorf("SystemPrompt: got %q, want empty string", result.SystemPrompt)
	}
}
