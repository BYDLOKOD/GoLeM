package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/artifact"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/dag"
)

// --- PipelineInput / PipelineOutput serialization tests ---

func TestPipelineInputUnmarshal(t *testing.T) {
	raw := `{
		"dag": {
			"steps": [
				{"id": "s1", "prompt": "write tests", "depends_on": []},
				{"id": "s2", "prompt": "implement", "depends_on": ["s1"]}
			]
		},
		"dir": "/tmp/work",
		"timeout": 600
	}`
	var input PipelineInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(input.DAG.Steps) != 2 {
		t.Errorf("steps count = %d, want 2", len(input.DAG.Steps))
	}
	if input.DAG.Steps[0].ID != "s1" {
		t.Errorf("step[0].ID = %q, want s1", input.DAG.Steps[0].ID)
	}
	if input.DAG.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("step[1].DependsOn[0] = %q, want s1", input.DAG.Steps[1].DependsOn[0])
	}
	if input.Dir != "/tmp/work" {
		t.Errorf("dir = %q, want /tmp/work", input.Dir)
	}
	if input.Timeout != 600 {
		t.Errorf("timeout = %d, want 600", input.Timeout)
	}
}

func TestPipelineInputUnmarshal_MinimalDAG(t *testing.T) {
	raw := `{"dag": {"steps": [{"id": "s1", "prompt": "do it"}]}}`
	var input PipelineInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(input.DAG.Steps) != 1 {
		t.Errorf("steps count = %d, want 1", len(input.DAG.Steps))
	}
	if input.Dir != "" {
		t.Errorf("dir = %q, want empty", input.Dir)
	}
	if input.Timeout != 0 {
		t.Errorf("timeout = %d, want 0", input.Timeout)
	}
}

func TestPipelineOutputMarshal(t *testing.T) {
	out := PipelineOutput{
		Results: map[string]StepResult{
			"s1": {Status: "completed", ExitCode: 0, Stdout: "done"},
		},
		Status: "completed",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if decoded["status"] != "completed" {
		t.Errorf("status = %v, want completed", decoded["status"])
	}
	results, ok := decoded["results"].(map[string]any)
	if !ok {
		t.Fatal("results not a map")
	}
	s1, ok := results["s1"].(map[string]any)
	if !ok {
		t.Fatal("s1 result not a map")
	}
	if s1["status"] != "completed" {
		t.Errorf("s1 status = %v, want completed", s1["status"])
	}
}

func TestPipelineOutputMarshal_AllStatuses(t *testing.T) {
	out := PipelineOutput{
		Results: map[string]StepResult{
			"s1": {Status: "completed", ExitCode: 0, Stdout: "ok"},
			"s2": {Status: "failed", ExitCode: 1, Error: "step error"},
			"s3": {Status: "skipped", Error: "dependency failed"},
		},
		Status: "partial",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded PipelineOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if decoded.Status != "partial" {
		t.Errorf("status = %q, want partial", decoded.Status)
	}
	if decoded.Results["s2"].ExitCode != 1 {
		t.Errorf("s2 exit_code = %d, want 1", decoded.Results["s2"].ExitCode)
	}
	if decoded.Results["s3"].Error != "dependency failed" {
		t.Errorf("s3 error = %q, want 'dependency failed'", decoded.Results["s3"].Error)
	}
}

// --- pipelineStubExecutor for handler tests ---

// pipelineStubExecutor implements dag.StepExecutor for pipeline handler tests.
// It avoids any external dependency (no claude CLI, no filesystem beyond t.TempDir).
type pipelineStubExecutor struct {
	results  map[string][]*artifact.Artifact
	errForID map[string]error
	delay    time.Duration
}

func newPipelineStubExecutor() *pipelineStubExecutor {
	return &pipelineStubExecutor{
		results:  make(map[string][]*artifact.Artifact),
		errForID: make(map[string]error),
	}
}

func (e *pipelineStubExecutor) Execute(ctx context.Context, step dag.Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if err, ok := e.errForID[step.ID]; ok {
		return nil, err
	}

	if arts, ok := e.results[step.ID]; ok {
		return arts, nil
	}

	// Default: return a text artifact with the step ID.
	return []*artifact.Artifact{artifact.NewText(step.ID, "output of "+step.ID)}, nil
}

// newTestPipelineConfig creates a minimal config for pipeline handler tests.
func newTestPipelineConfig() *config.Config {
	return &config.Config{
		Model:          "glm-5",
		PermissionMode: "bypassPermissions",
	}
}

// --- PipelineHandler.Handle tests ---

func TestPipelineHandler_ValidDAG_AllComplete(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "write tests"},
				{ID: "s2", Prompt: "implement", DependsOn: []string{"s1"}},
			},
		},
	})

	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if output.Status != "completed" {
		t.Errorf("status = %q, want completed", output.Status)
	}
	if len(output.Results) != 2 {
		t.Fatalf("results count = %d, want 2", len(output.Results))
	}
	if output.Results["s1"].Status != "completed" {
		t.Errorf("s1 status = %q, want completed", output.Results["s1"].Status)
	}
	if output.Results["s2"].Status != "completed" {
		t.Errorf("s2 status = %q, want completed", output.Results["s2"].Status)
	}
	if output.Results["s1"].Stdout == "" {
		t.Error("s1 stdout should not be empty")
	}
}

func TestPipelineHandler_ParallelSteps(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.delay = 30 * time.Millisecond
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "parallel 1"},
				{ID: "s2", Prompt: "parallel 2"},
				{ID: "s3", Prompt: "parallel 3"},
				{ID: "s4", Prompt: "join", DependsOn: []string{"s1", "s2", "s3"}},
			},
		},
	})

	start := time.Now()
	result, err := h.Handle(context.Background(), params)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if output.Status != "completed" {
		t.Errorf("status = %q, want completed", output.Status)
	}
	if len(output.Results) != 4 {
		t.Errorf("results count = %d, want 4", len(output.Results))
	}

	// If s1-s3 ran sequentially (3 * 30ms = 90ms), plus s4 (30ms) = 120ms.
	// In parallel: ~30ms + 30ms = ~60ms. Allow generous margin.
	if elapsed > 200*time.Millisecond {
		t.Errorf("steps did not run in parallel: elapsed %v", elapsed)
	}
}

func TestPipelineHandler_InvalidDAG_Cycle(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "first", DependsOn: []string{"s2"}},
				{ID: "s2", Prompt: "second", DependsOn: []string{"s1"}},
			},
		},
	})

	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for cyclic DAG")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want to contain 'cycle'", err.Error())
	}
}

func TestPipelineHandler_InvalidDAG_MissingDependency(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "first", DependsOn: []string{"nonexistent"}},
			},
		},
	})

	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
	if !strings.Contains(err.Error(), "unknown step") {
		t.Errorf("error = %q, want to contain 'unknown step'", err.Error())
	}
}

func TestPipelineHandler_InvalidDAG_DuplicateID(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "first"},
				{ID: "s1", Prompt: "duplicate"},
			},
		},
	})

	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for duplicate step ID")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want to contain 'duplicate'", err.Error())
	}
}

func TestPipelineHandler_EmptyDAG(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{},
		},
	})

	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty DAG")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want to contain 'empty'", err.Error())
	}
}

func TestPipelineHandler_StepFailure_PartialResults(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.errForID["s2"] = fmt.Errorf("step s2 failed: compilation error")
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "ok step"},
				{ID: "s2", Prompt: "failing step", DependsOn: []string{"s1"}},
				{ID: "s3", Prompt: "should skip", DependsOn: []string{"s2"}},
			},
		},
	})

	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// s1 completed, s2 failed, s3 skipped.
	if output.Results["s1"].Status != "completed" {
		t.Errorf("s1 status = %q, want completed", output.Results["s1"].Status)
	}
	if output.Results["s2"].Status != "failed" {
		t.Errorf("s2 status = %q, want failed", output.Results["s2"].Status)
	}
	if output.Results["s2"].ExitCode != 1 {
		t.Errorf("s2 exit_code = %d, want 1", output.Results["s2"].ExitCode)
	}
	if output.Results["s3"].Status != "skipped" {
		t.Errorf("s3 status = %q, want skipped", output.Results["s3"].Status)
	}

	// Overall status should be "partial" (s1 completed, others failed/skipped).
	if output.Status != "partial" {
		t.Errorf("status = %q, want partial", output.Status)
	}
}

func TestPipelineHandler_AllStepsFail(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.errForID["s1"] = fmt.Errorf("s1 exploded")
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "will fail"},
				{ID: "s2", Prompt: "depends on s1", DependsOn: []string{"s1"}},
			},
		},
	})

	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if output.Status != "failed" {
		t.Errorf("status = %q, want failed", output.Status)
	}
	if output.Results["s1"].Status != "failed" {
		t.Errorf("s1 status = %q, want failed", output.Results["s1"].Status)
	}
	if output.Results["s2"].Status != "skipped" {
		t.Errorf("s2 status = %q, want skipped", output.Results["s2"].Status)
	}
}

func TestPipelineHandler_ContextCancellation(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.delay = 5 * time.Second // very slow, will be cancelled
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "slow step"},
				{ID: "s2", Prompt: "never reached", DependsOn: []string{"s1"}},
			},
		},
	})

	result, err := h.Handle(ctx, params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should be failed due to context cancellation.
	if output.Status != "failed" {
		t.Errorf("status = %q, want failed", output.Status)
	}
}

func TestPipelineHandler_TimeoutField(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.delay = 5 * time.Second // very slow
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "slow step"},
			},
		},
		Timeout: 1, // 1 second timeout from PipelineInput
	})

	start := time.Now()
	result, err := h.Handle(context.Background(), params)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if output.Status != "failed" {
		t.Errorf("status = %q, want failed", output.Status)
	}

	// Should complete in ~1s, not 5s.
	if elapsed > 3*time.Second {
		t.Errorf("timeout was not applied: elapsed %v", elapsed)
	}
}

func TestPipelineHandler_NilInput(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	_, err := h.Handle(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestPipelineHandler_InvalidJSON(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	_, err := h.Handle(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPipelineHandler_DefaultMaxConcurrency(t *testing.T) {
	cfg := newTestPipelineConfig()

	// Pass 0 for maxConcurrency -- means unlimited (stored as 0).
	h := NewPipelineHandler(cfg, nil, 0)
	if h.maxConcurrency != 0 {
		t.Errorf("maxConcurrency = %d, want 0 (unlimited)", h.maxConcurrency)
	}
}

func TestPipelineHandler_ExplicitMaxConcurrency(t *testing.T) {
	cfg := newTestPipelineConfig()

	h := NewPipelineHandler(cfg, nil, 2)
	if h.maxConcurrency != 2 {
		t.Errorf("maxConcurrency = %d, want 2 (explicit)", h.maxConcurrency)
	}
}

func TestPipelineHandler_IndependentStepFailureDoesNotAffectOthers(t *testing.T) {
	exec := newPipelineStubExecutor()
	exec.errForID["s1"] = fmt.Errorf("s1 failed")
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "will fail"},
				{ID: "s2", Prompt: "independent"},
				{ID: "s3", Prompt: "depends on s1", DependsOn: []string{"s1"}},
			},
		},
	})

	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if output.Results["s1"].Status != "failed" {
		t.Errorf("s1 status = %q, want failed", output.Results["s1"].Status)
	}
	if output.Results["s2"].Status != "completed" {
		t.Errorf("s2 status = %q, want completed", output.Results["s2"].Status)
	}
	if output.Results["s3"].Status != "skipped" {
		t.Errorf("s3 status = %q, want skipped", output.Results["s3"].Status)
	}
	if output.Status != "partial" {
		t.Errorf("status = %q, want partial", output.Status)
	}
}

// --- PipelineInput system_prompt / constraints tests ---

// TestPipelineInputUnmarshal_SystemPromptAndConstraints verifies that
// PipelineInput accepts system_prompt and constraints from JSON.
func TestPipelineInputUnmarshal_SystemPromptAndConstraints(t *testing.T) {
	raw := `{
		"dag": {"steps": [{"id": "s1", "prompt": "do work"}]},
		"system_prompt": "be cautious",
		"constraints": ["readonly", "plan-first"]
	}`
	var input PipelineInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.SystemPrompt != "be cautious" {
		t.Errorf("SystemPrompt = %q, want be cautious", input.SystemPrompt)
	}
	if len(input.Constraints) != 2 {
		t.Fatalf("Constraints len = %d, want 2", len(input.Constraints))
	}
	if input.Constraints[0] != "readonly" {
		t.Errorf("Constraints[0] = %q, want readonly", input.Constraints[0])
	}
}

// TestPipelineDefinition_HasSystemPromptAndConstraints verifies that
// PipelineDefinition exposes system_prompt and constraints in its schema.
func TestPipelineDefinition_HasSystemPromptAndConstraints(t *testing.T) {
	schema := PipelineDefinition()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing or wrong type")
	}
	if _, exists := props["system_prompt"]; !exists {
		t.Error("system_prompt property missing from PipelineDefinition")
	}
	if _, exists := props["constraints"]; !exists {
		t.Error("constraints property missing from PipelineDefinition")
	}
}

// TestPipelineHandler_SystemPromptPassedToExecutor verifies that when a
// system_prompt is provided in PipelineInput, the handler creates a
// ClaudeStepExecutor with the assembled system prompt. We verify this by
// passing system_prompt and constraints through a real PipelineHandler with a
// stub executor (which ignores the system prompt, but the handler must
// assemble and pass it without error).
func TestPipelineHandler_SystemPromptFromInput(t *testing.T) {
	exec := newPipelineStubExecutor()
	cfg := newTestPipelineConfig()
	h := NewPipelineHandler(cfg, exec, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "task"},
			},
		},
		SystemPrompt: "You are a helpful assistant",
		Constraints:  []string{"readonly"},
	})

	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var output PipelineOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if output.Status != "completed" {
		t.Errorf("status = %q, want completed", output.Status)
	}
}

// TestPipelineHandler_InvalidConstraintReturnsError verifies that an unknown
// constraint key causes the handler to return an error (via AssembleSystemPrompt).
func TestPipelineHandler_InvalidConstraintReturnsError(t *testing.T) {
	cfg := newTestPipelineConfig()
	// Pass nil executor so the handler calls prompt.AssembleSystemPrompt
	// when building the real ClaudeStepExecutor.
	h := NewPipelineHandler(cfg, nil, 4)

	params := mustMarshal(t, PipelineInput{
		DAG: dag.DAG{
			Steps: []dag.Step{
				{ID: "s1", Prompt: "task"},
			},
		},
		Constraints: []string{"nonexistent-constraint-xyz"},
	})

	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown constraint")
	}
	if !strings.Contains(err.Error(), "unknown constraint") {
		t.Errorf("error = %q, want to contain 'unknown constraint'", err.Error())
	}
}

// --- PipelineDefinition tests ---

func TestPipelineDefinition_HasRequiredDAG(t *testing.T) {
	schema := PipelineDefinition()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required field missing or wrong type")
	}
	if len(required) != 1 || required[0] != "dag" {
		t.Errorf("required = %v, want [dag]", required)
	}
}

func TestPipelineDefinition_HasProperties(t *testing.T) {
	schema := PipelineDefinition()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing or wrong type")
	}
	for _, key := range []string{"dag", "dir", "timeout"} {
		if _, exists := props[key]; !exists {
			t.Errorf("property %q is missing", key)
		}
	}
}

// TestPipelineInput_StepWithValidateAndGate verifies that a DAG with a gate step
// and validate/retry fields unmarshals correctly through PipelineInput.
func TestPipelineInput_StepWithValidateAndGate(t *testing.T) {
	raw := `{
		"dag": {
			"steps": [
				{"id": "s1", "prompt": "do work"},
				{
					"id": "gate1",
					"type": "gate",
					"depends_on": ["s1"],
					"validate": {"contains": ["SUCCESS"], "not_contains": ["ERROR"], "matches": "^\\w+$"},
					"retry": {"max_attempts": 3, "feedback": "Retry gate"}
				}
			]
		}
	}`
	var input PipelineInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(input.DAG.Steps) != 2 {
		t.Fatalf("steps count = %d, want 2", len(input.DAG.Steps))
	}

	gate := input.DAG.Steps[1]
	if gate.Type != "gate" {
		t.Errorf("step[1].Type = %q, want 'gate'", gate.Type)
	}
	if gate.Validate == nil {
		t.Fatal("gate step Validate is nil")
	}
	if len(gate.Validate.Contains) != 1 || gate.Validate.Contains[0] != "SUCCESS" {
		t.Errorf("gate.Validate.Contains = %v, want [SUCCESS]", gate.Validate.Contains)
	}
	if len(gate.Validate.NotContains) != 1 || gate.Validate.NotContains[0] != "ERROR" {
		t.Errorf("gate.Validate.NotContains = %v, want [ERROR]", gate.Validate.NotContains)
	}
	if gate.Validate.Matches != `^\w+$` {
		t.Errorf("gate.Validate.Matches = %q, want '^\\w+$'", gate.Validate.Matches)
	}
	if gate.Retry == nil {
		t.Fatal("gate step Retry is nil")
	}
	if gate.Retry.MaxAttempts != 3 {
		t.Errorf("gate.Retry.MaxAttempts = %d, want 3", gate.Retry.MaxAttempts)
	}
	if gate.Retry.Feedback != "Retry gate" {
		t.Errorf("gate.Retry.Feedback = %q, want 'Retry gate'", gate.Retry.Feedback)
	}
}

// TestPipelineDefinition_StepItemsHaveValidateTypeRetry verifies that the
// PipelineDefinition schema includes validate, type, and retry in step items.
func TestPipelineDefinition_StepItemsHaveValidateTypeRetry(t *testing.T) {
	schema := PipelineDefinition()
	dagProp, ok := schema["properties"].(map[string]any)["dag"].(map[string]any)
	if !ok {
		t.Fatal("dag property missing")
	}
	stepsProp, ok := dagProp["properties"].(map[string]any)["steps"].(map[string]any)
	if !ok {
		t.Fatal("steps property missing")
	}
	items, ok := stepsProp["items"].(map[string]any)
	if !ok {
		t.Fatal("steps items missing")
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("step items properties missing")
	}
	for _, key := range []string{"validate", "type", "retry"} {
		if _, exists := itemProps[key]; !exists {
			t.Errorf("property %q missing from pipeline step items schema", key)
		}
	}
}
