package cmd_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/veschin/GoLeM/internal/cmd"
)

func TestWriteVisionMCPConfig(t *testing.T) {
	dir := t.TempDir()

	path, err := cmd.WriteVisionMCPConfig(dir, "sk-zai-secret-key")
	if err != nil {
		t.Fatalf("WriteVisionMCPConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600 (the file holds the API key)", info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if err := json.Unmarshal(data, &map[string]any{}); err != nil {
		t.Fatalf("written config is not valid JSON: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "zai-mcp-server") {
		t.Error("config missing the zai-mcp-server entry")
	}
	if !strings.Contains(s, "@z_ai/mcp-server") {
		t.Error("config missing the npx package name")
	}
	if !strings.Contains(s, "sk-zai-secret-key") {
		t.Error("config missing the auto-substituted API key")
	}
}
