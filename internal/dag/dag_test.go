package dag

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/veschin/GoLeM/internal/validation"
)

func TestValidate_ValidLinearDAG(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "step one"},
			{ID: "s2", Prompt: "step two", DependsOn: []string{"s1"}},
			{ID: "s3", Prompt: "step three", DependsOn: []string{"s2"}},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ValidParallelDAG(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "independent 1"},
			{ID: "s2", Prompt: "independent 2"},
			{ID: "s3", Prompt: "join", DependsOn: []string{"s1", "s2"}},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ValidDiamondDAG(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "root"},
			{ID: "s2", Prompt: "left", DependsOn: []string{"s1"}},
			{ID: "s3", Prompt: "right", DependsOn: []string{"s1"}},
			{ID: "s4", Prompt: "join", DependsOn: []string{"s2", "s3"}},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_EmptyDAG(t *testing.T) {
	d := &DAG{Steps: []Step{}}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for empty DAG")
	}
}

func TestValidate_NilSteps(t *testing.T) {
	d := &DAG{}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for nil steps")
	}
}

func TestValidate_Cycle(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "step one", DependsOn: []string{"s2"}},
			{ID: "s2", Prompt: "step two", DependsOn: []string{"s1"}},
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatal("expected error for cycle")
	}
	if err.Error() != "err:dag cycle detected" {
		t.Errorf("error = %v, want 'err:dag cycle detected'", err)
	}
}

func TestValidate_SelfCycle(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "self-loop", DependsOn: []string{"s1"}},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for self-cycle")
	}
}

func TestValidate_DuplicateID(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "first"},
			{ID: "s1", Prompt: "second"},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestValidate_MissingDependency(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "step one", DependsOn: []string{"nonexistent"}},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

func TestValidate_EmptyID(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "", Prompt: "no id"},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for empty step ID")
	}
}

func TestValidate_EmptyPrompt(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: ""},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestValidate_SingleStep(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "only step"},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate single step: %v", err)
	}
}

func TestValidate_CachesResult(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "step one"},
			{ID: "s2", Prompt: "step two", DependsOn: []string{"s1"}},
		},
	}

	// First call computes and caches.
	if err := d.Validate(); err != nil {
		t.Fatalf("first Validate: %v", err)
	}

	// Second call should return immediately from cache.
	if err := d.Validate(); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
}

func TestValidate_GateStep_ValidConfig(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "step1", Prompt: "do work"},
			{ID: "gate1", Type: "gate", DependsOn: []string{"step1"}, Validate: &validation.ValidationRule{Contains: []string{"ok"}}},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_GateStep_MissingDependsOn(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "gate1", Type: "gate", Validate: &validation.ValidationRule{Contains: []string{"ok"}}},
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatal("expected error for gate step without depends_on")
	}
	if !strings.Contains(err.Error(), "must have at least one dependency") {
		t.Errorf("error = %v, want containing 'must have at least one dependency'", err)
	}
}

func TestValidate_GateStep_NilValidate(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "step1", Prompt: "do work"},
			{ID: "gate1", Type: "gate", DependsOn: []string{"step1"}},
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatal("expected error for gate step without validate rule")
	}
	if !strings.Contains(err.Error(), "no validate rule") {
		t.Errorf("error = %v, want containing 'no validate rule'", err)
	}
}

func TestValidate_GateStep_PromptOptional(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "step1", Prompt: "do work"},
			{ID: "gate1", Type: "gate", DependsOn: []string{"step1"}, Validate: &validation.ValidationRule{Contains: []string{"ok"}}},
		},
	}
	// gate1 has Prompt="" (zero value) — should not trigger empty prompt error
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestStep_JSONRoundTrip_WithValidateAndRetry(t *testing.T) {
	original := Step{
		ID:       "step1",
		Prompt:   "test",
		Type:     "gate",
		Validate: &validation.ValidationRule{Contains: []string{"test"}},
		Retry:    &RetryConfig{MaxAttempts: 3, Feedback: "try again"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Step
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
	}
	if decoded.Validate == nil {
		t.Fatal("Validate is nil after round-trip")
	}
	if len(decoded.Validate.Contains) != 1 || decoded.Validate.Contains[0] != "test" {
		t.Errorf("Validate.Contains = %v, want [test]", decoded.Validate.Contains)
	}
	if decoded.Retry == nil {
		t.Fatal("Retry is nil after round-trip")
	}
	if decoded.Retry.MaxAttempts != 3 {
		t.Errorf("Retry.MaxAttempts = %d, want 3", decoded.Retry.MaxAttempts)
	}
	if decoded.Retry.Feedback != "try again" {
		t.Errorf("Retry.Feedback = %q, want 'try again'", decoded.Retry.Feedback)
	}
}
