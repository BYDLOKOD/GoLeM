package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewText(t *testing.T) {
	a := NewText("step-1", "hello world")

	if a.StepID != "step-1" {
		t.Errorf("expected step-1, got %s", a.StepID)
	}
	if a.Type != TypeText {
		t.Errorf("expected %q, got %q", TypeText, a.Type)
	}
	if string(a.Content) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(a.Content))
	}
	if a.ID == "" {
		t.Error("expected non-empty ID")
	}
	if a.Metadata != nil {
		t.Error("expected nil Metadata for text artifact")
	}
}

func TestNewTextEmptyContent(t *testing.T) {
	a := NewText("step-1", "")

	if a.Type != TypeText {
		t.Errorf("expected %q, got %q", TypeText, a.Type)
	}
	if len(a.Content) != 0 {
		t.Errorf("expected empty content, got %q", string(a.Content))
	}
}

func TestNewJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	p := payload{Name: "test", Age: 42}
	a, err := NewJSON("step-2", p)
	if err != nil {
		t.Fatalf("NewJSON: %v", err)
	}

	if a.StepID != "step-2" {
		t.Errorf("expected step-2, got %s", a.StepID)
	}
	if a.Type != TypeJSON {
		t.Errorf("expected %q, got %q", TypeJSON, a.Type)
	}

	// Verify the content is valid JSON matching the original payload.
	var got payload
	if err := json.Unmarshal(a.Content, &got); err != nil {
		t.Fatalf("artifact content is not valid JSON: %v", err)
	}
	if got.Name != "test" || got.Age != 42 {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestNewJSONMarshalError(t *testing.T) {
	// Channels cannot be marshaled to JSON.
	ch := make(chan int)
	_, err := NewJSON("step-err", ch)
	if err == nil {
		t.Fatal("expected error for unmarshalable value")
	}
}

func TestNewFileRef(t *testing.T) {
	a := NewFileRef("step-3", "/tmp/output.txt")

	if a.StepID != "step-3" {
		t.Errorf("expected step-3, got %s", a.StepID)
	}
	if a.Type != TypeFileRef {
		t.Errorf("expected %q, got %q", TypeFileRef, a.Type)
	}
	if string(a.Content) != "/tmp/output.txt" {
		t.Errorf("expected '/tmp/output.txt', got %q", string(a.Content))
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()

	original := NewText("step-1", "roundtrip test content")
	if err := original.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir, original.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID mismatch: expected %q, got %q", original.ID, loaded.ID)
	}
	if loaded.StepID != original.StepID {
		t.Errorf("StepID mismatch: expected %q, got %q", original.StepID, loaded.StepID)
	}
	if loaded.Type != original.Type {
		t.Errorf("Type mismatch: expected %q, got %q", original.Type, loaded.Type)
	}
	if string(loaded.Content) != string(original.Content) {
		t.Errorf("Content mismatch: expected %q, got %q", string(original.Content), string(loaded.Content))
	}
}

func TestSaveJSONArtifact(t *testing.T) {
	dir := t.TempDir()

	type data struct {
		Result string `json:"result"`
	}
	a, err := NewJSON("step-2", data{Result: "success"})
	if err != nil {
		t.Fatalf("NewJSON: %v", err)
	}

	if err := a.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir, a.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Type != TypeJSON {
		t.Errorf("expected %q, got %q", TypeJSON, loaded.Type)
	}

	var d data
	if err := json.Unmarshal(loaded.Content, &d); err != nil {
		t.Fatalf("unmarshal loaded content: %v", err)
	}
	if d.Result != "success" {
		t.Errorf("expected 'success', got %q", d.Result)
	}
}

func TestSaveCreatesFile(t *testing.T) {
	dir := t.TempDir()

	a := NewText("step-1", "file existence check")
	if err := a.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	expectedPath := filepath.Join(dir, "artifact-"+a.ID+".json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", expectedPath)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(dir, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist error, got: %v", err)
	}
}

func TestLoadCorruptJSON(t *testing.T) {
	dir := t.TempDir()

	// Write a file with the correct name but invalid JSON content.
	path := filepath.Join(dir, "artifact-corrupt-id.json")
	if err := os.WriteFile(path, []byte("this is not json{{{"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := Load(dir, "corrupt-id")
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
	// The error should mention JSON parsing failure.
	if !strings.Contains(err.Error(), "json") {
		t.Errorf("error should mention JSON parsing, got: %v", err)
	}
}

func TestLoadWrongID(t *testing.T) {
	dir := t.TempDir()

	// Save an artifact, then try to load with a different ID.
	a := NewText("step-1", "data")
	if err := a.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := Load(dir, "wrong-id")
	if err == nil {
		t.Fatal("expected error for wrong ID")
	}
}

func TestSaveWithMetadata(t *testing.T) {
	dir := t.TempDir()

	a := NewText("step-1", "content with metadata")
	a.Metadata = map[string]string{
		"source": "glm-5-turbo",
		"model":  "glm-5-turbo",
	}

	if err := a.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir, a.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Metadata["source"] != "glm-5-turbo" {
		t.Errorf("metadata source mismatch: expected 'glm-5-turbo', got %q", loaded.Metadata["source"])
	}
}

func TestSaveOverwrite(t *testing.T) {
	dir := t.TempDir()

	a1 := NewText("step-1", "first version")
	if err := a1.Save(dir); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	// Create a new artifact with the same ID but different content.
	a2 := NewText("step-1", "second version")
	a2.ID = a1.ID // force same ID
	if err := a2.Save(dir); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	loaded, err := Load(dir, a1.ID)
	if err != nil {
		t.Fatalf("Load after overwrite: %v", err)
	}

	if string(loaded.Content) != "second version" {
		t.Errorf("expected 'second version', got %q", string(loaded.Content))
	}
}

func TestArtifactJSONSerialization(t *testing.T) {
	a := NewText("step-1", "serialize me")
	a.Metadata = map[string]string{"key": "value"}

	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify it's valid JSON with expected fields.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}

	if m["id"] != a.ID {
		t.Errorf("JSON id mismatch")
	}
	if m["step_id"] != "step-1" {
		t.Errorf("JSON step_id mismatch")
	}
	if m["type"] != "text" {
		t.Errorf("JSON type mismatch")
	}
	if m["content"] == nil {
		t.Error("JSON content is nil")
	}
}

func TestNewTextWithMetadata(t *testing.T) {
	a := NewText("step-1", "content")
	if a.Metadata != nil {
		// Constructors should NOT initialize Metadata.
		t.Error("expected nil Metadata from NewText")
	}
}

func TestNewJSONWithMetadata(t *testing.T) {
	a, err := NewJSON("step-1", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("NewJSON: %v", err)
	}
	if a.Metadata != nil {
		t.Error("expected nil Metadata from NewJSON")
	}
}

func TestNewFileRefWithMetadata(t *testing.T) {
	a := NewFileRef("step-1", "/path")
	if a.Metadata != nil {
		t.Error("expected nil Metadata from NewFileRef")
	}
}

func TestSaveRejectsEmptyFields(t *testing.T) {
	tests := []struct {
		name string
		art  Artifact
	}{
		{"empty ID", Artifact{ID: "", StepID: "s1", Type: "text"}},
		{"empty StepID", Artifact{ID: "a1", StepID: "", Type: "text"}},
		{"empty Type", Artifact{ID: "a1", StepID: "s1", Type: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.art.Save(t.TempDir())
			if err == nil {
				t.Fatal("expected error for empty required field")
			}
			if !strings.Contains(err.Error(), "err:validation") {
				t.Fatalf("expected err:validation prefix, got %v", err)
			}
		})
	}
}
