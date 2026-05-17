package dag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDAGFromFile_JSON(t *testing.T) {
	dagJSON := `{
		"steps": [
			{"id": "s1", "prompt": "analyze code"},
			{"id": "s2", "prompt": "write tests", "depends_on": ["s1"]},
			{"id": "s3", "prompt": "review", "depends_on": ["s1"], "model": "glm-5.1", "timeout": 300}
		]
	}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "pipeline.json")
	if err := os.WriteFile(filePath, []byte(dagJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	d, err := LoadDAGFromFile(filePath)
	if err != nil {
		t.Fatalf("LoadDAGFromFile: %v", err)
	}

	if len(d.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(d.Steps))
	}

	if d.Steps[0].ID != "s1" {
		t.Errorf("step 0 ID = %q, want s1", d.Steps[0].ID)
	}
	if d.Steps[0].Prompt != "analyze code" {
		t.Errorf("step 0 Prompt = %q, want 'analyze code'", d.Steps[0].Prompt)
	}
	if d.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("step 1 DependsOn = %v, want [s1]", d.Steps[1].DependsOn)
	}
	if d.Steps[2].Model != "glm-5.1" {
		t.Errorf("step 2 Model = %q, want glm-5.1", d.Steps[2].Model)
	}
	if d.Steps[2].Timeout != 300 {
		t.Errorf("step 2 Timeout = %d, want 300", d.Steps[2].Timeout)
	}

	// Validate the loaded DAG.
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadDAGFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(filePath, []byte(`{invalid`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadDAGFromFile(filePath)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadDAGFromFile_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "pipeline.txt")
	if err := os.WriteFile(filePath, []byte(`{"steps":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadDAGFromFile(filePath)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

func TestLoadDAGFromFile_MissingFile(t *testing.T) {
	_, err := LoadDAGFromFile("/nonexistent/pipeline.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadDAGFromFile_EmptySteps(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(filePath, []byte(`{"steps":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	d, err := LoadDAGFromFile(filePath)
	if err != nil {
		t.Fatalf("LoadDAGFromFile: %v", err)
	}

	if err := d.Validate(); err == nil {
		t.Fatal("expected validation error for empty steps")
	}
}

func TestLoadDAGFromFile_CaseInsensitiveExtension(t *testing.T) {
	dagJSON := `{"steps": [{"id": "s1", "prompt": "do work"}]}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "pipeline.JSON")
	if err := os.WriteFile(filePath, []byte(dagJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	d, err := LoadDAGFromFile(filePath)
	if err != nil {
		t.Fatalf("LoadDAGFromFile: %v", err)
	}

	if len(d.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(d.Steps))
	}
}

func TestDAG_JSONRoundTrip(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "step one"},
			{ID: "s2", Prompt: "step two", DependsOn: []string{"s1"}, Model: "glm-5.1", Timeout: 60},
		},
	}

	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	// Round-trip.
	var d2 DAG
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(d2.Steps) != 2 {
		t.Fatalf("round-trip: expected 2 steps, got %d", len(d2.Steps))
	}
	if d2.Steps[0].ID != "s1" {
		t.Errorf("round-trip: step 0 ID = %q, want s1", d2.Steps[0].ID)
	}
	if d2.Steps[1].Model != "glm-5.1" {
		t.Errorf("round-trip: step 1 model = %q, want glm-5.1", d2.Steps[1].Model)
	}
	if d2.Steps[1].Timeout != 60 {
		t.Errorf("round-trip: step 1 timeout = %d, want 60", d2.Steps[1].Timeout)
	}
	if len(d2.Steps[1].DependsOn) != 1 || d2.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("round-trip: step 1 DependsOn = %v, want [s1]", d2.Steps[1].DependsOn)
	}
}
