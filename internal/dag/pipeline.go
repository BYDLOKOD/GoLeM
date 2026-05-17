package dag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadDAGFromFile reads a DAG definition from a file. Supported formats:
//   - .json: JSON encoding of the DAG struct.
//
// Returns an error for unsupported file extensions.
func LoadDAGFromFile(path string) (*DAG, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dag: read file %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return loadDAGFromJSON(data)
	default:
		return nil, fmt.Errorf("dag: unsupported file format %q (supported: .json)", ext)
	}
}

// loadDAGFromJSON parses a DAG from JSON bytes.
func loadDAGFromJSON(data []byte) (*DAG, error) {
	var d DAG
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("dag: parse JSON: %w", err)
	}
	return &d, nil
}
