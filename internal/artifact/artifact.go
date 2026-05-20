// Package artifact provides a typed, persistable container for pipeline step
// outputs. Artifacts carry typed content (text, JSON, file references) and
// optional metadata, and can be saved to and loaded from the filesystem as
// individual JSON files.
package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ArtifactType distinguishes payload kinds.
type ArtifactType string

const (
	TypeText    ArtifactType = "text"
	TypeJSON    ArtifactType = "json"
	TypeFileRef ArtifactType = "file_ref"
)

// Artifact is a typed blob produced by a pipeline step.
type Artifact struct {
	ID       string            `json:"id"`
	StepID   string            `json:"step_id"`
	Type     ArtifactType      `json:"type"`
	Content  []byte            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// generateID returns a new unique artifact ID using crypto/rand.
// Produces 16 hex characters (8 random bytes).
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is extremely rare but catastrophic --
		// returning non-unique IDs would corrupt data.
		panic(fmt.Sprintf("crypto/rand failure: %v", err))
	}
	return hex.EncodeToString(b)
}

// NewText creates a text artifact with the given step ID and content.
func NewText(stepID string, content string) *Artifact {
	return &Artifact{
		ID:      generateID(),
		StepID:  stepID,
		Type:    TypeText,
		Content: []byte(content),
	}
}

// NewJSON creates a JSON artifact from a marshalable value.
// Returns an error if v cannot be marshaled to JSON.
func NewJSON(stepID string, v any) (*Artifact, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("err:validation marshal JSON: %w", err)
	}
	return &Artifact{
		ID:      generateID(),
		StepID:  stepID,
		Type:    TypeJSON,
		Content: data,
	}, nil
}

// NewFileRef creates an artifact that references an external file path.
// The path is stored as UTF-8 bytes in Content.
func NewFileRef(stepID, path string) *Artifact {
	return &Artifact{
		ID:      generateID(),
		StepID:  stepID,
		Type:    TypeFileRef,
		Content: []byte(path),
	}
}

// artifactFilename returns the filename for the artifact: "artifact-{id}.json".
func artifactFilename(id string) string {
	return "artifact-" + id + ".json"
}

// Save persists the artifact as artifact-{id}.json inside dir.
// Uses atomic write (write to temp file, then rename) to prevent partial writes.
// The directory dir must exist.
func (a *Artifact) Save(dir string) error {
	if a.ID == "" || a.StepID == "" || a.Type == "" {
		return fmt.Errorf("err:validation: artifact ID, StepID, and Type are required")
	}

	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("err:internal marshal artifact: %w", err)
	}

	target := filepath.Join(dir, artifactFilename(a.ID))

	// Atomic write: write to temp file, then rename.
	tmp, err := os.CreateTemp(dir, ".artifact-tmp-*")
	if err != nil {
		return fmt.Errorf("err:internal create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("err:internal write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("err:internal close temp file: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("err:internal rename temp file: %w", err)
	}

	return nil
}

// Load reads artifact-{id}.json from dir and returns the parsed Artifact.
// Returns an error if the file does not exist or contains invalid JSON.
// Callers can use os.IsNotExist to distinguish missing files from corrupt data.
func Load(dir, id string) (*Artifact, error) {
	path := filepath.Join(dir, artifactFilename(id))

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var a Artifact
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("err:validation parse artifact %s json: %w", id, err)
	}

	return &a, nil
}
