// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// visionMCPFileName is the generated MCP config that attaches Z.AI's
// image-vision MCP server to golems. It lives in the GoLeM config dir.
const visionMCPFileName = "golem-vision-mcp.json"

// WriteVisionMCPConfig writes the Z.AI vision MCP server config, with the API
// key filled in, to configDir/golem-vision-mcp.json (mode 0600, written
// atomically) and returns its path. The key goes into a 0600 file rather than
// onto the command line so it never appears in process arguments.
//
// The server is spawned by each golem's claude subprocess via --mcp-config;
// it is never registered in the host ~/.claude/settings.json.
func WriteVisionMCPConfig(configDir, apiKey string) (string, error) {
	doc := map[string]any{
		"mcpServers": map[string]any{
			"zai-mcp-server": map[string]any{
				"type":    "stdio",
				"command": "npx",
				"args":    []string{"-y", "@z_ai/mcp-server"},
				"env": map[string]string{
					"Z_AI_API_KEY": apiKey,
					"Z_AI_MODE":    "ZAI",
				},
			},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal vision mcp config: %w", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	path := filepath.Join(configDir, visionMCPFileName)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write vision mcp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename vision mcp config: %w", err)
	}
	return path, nil
}
