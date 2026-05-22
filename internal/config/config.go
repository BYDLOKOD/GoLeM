// Package config loads, validates, and exposes GoLeM configuration.
// Config is read from TOML files and environment variables with strict
// priority ordering and validation at load time.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Hardcoded constants exposed for inspection.
const (
	ZaiBaseURL              = "https://api.z.ai/api/anthropic"
	ZaiAPITimeoutMs         = "30000000"
	DefaultTimeout          = 1800
	DefaultModel            = "glm-5.1"
	DefaultPermissionMode   = "acceptEdits"
	DefaultProxyEnabled     = true
	DefaultProxyIdleTimeout = 600 // seconds
	DefaultProxyPort        = 0   // 0 = auto-select
)

// RoutingConfig holds per-tier model names for smart routing.
// If a tier field is empty, the fallback model is used instead.
type RoutingConfig struct {
	Light  string
	Medium string
	Heavy  string
}

// Config holds all configuration values for GoLeM operations.
type Config struct {
	Model                  string
	OpusModel              string
	SonnetModel            string
	HaikuModel             string
	PermissionMode         string
	SubagentDir            string
	ConfigDir              string
	ZaiBaseURL             string
	ZaiAPIKey              string
	ZaiAPITimeoutMs        string
	Debug                  bool
	ProxyEnabled           bool
	ProxyIdleTimeout       int // seconds
	ProxyPort              int
	Routing                RoutingConfig
	Effort                 string         // --effort flag value (e.g. "max")
	ExcludeDynamicSections bool           // --exclude-dynamic-system-prompt-sections
	Models                 map[string]int // per-model concurrency limits from [models] section
	SystemPrompt           string         // optional default system prompt for all invocations
	MCPConfig              string         // path/JSON of MCP servers attached to golems only (claude --mcp-config)
	MCPStrict              bool           // golems use only MCPConfig servers, ignoring global (claude --strict-mcp-config)
}

// Options allows CLI flags to override config values after load.
type Options struct {
	Model string
}

// Load reads configuration from configDir/glm.toml, API key from configDir/zai_api_key,
// applies environment variable overrides, validates the result, and creates the
// subagent directory.
func Load(configDir, subagentDir string) (*Config, error) {
	return LoadWithOptions(configDir, subagentDir, Options{})
}

// LoadWithOptions is the internal implementation that Load delegates to.
func LoadWithOptions(configDir, subagentDir string, opts Options) (*Config, error) {
	// Start with defaults
	cfg := &Config{
		Model:                  DefaultModel,
		OpusModel:              DefaultModel,
		SonnetModel:            DefaultModel,
		HaikuModel:             DefaultModel,
		PermissionMode:         DefaultPermissionMode,
		SubagentDir:            subagentDir,
		ConfigDir:              configDir,
		ZaiBaseURL:             ZaiBaseURL,
		ZaiAPITimeoutMs:        ZaiAPITimeoutMs,
		Debug:                  false,
		ProxyEnabled:           DefaultProxyEnabled,
		ProxyIdleTimeout:       DefaultProxyIdleTimeout,
		ProxyPort:              DefaultProxyPort,
		Effort:                 "",
		ExcludeDynamicSections: false,
	}

	// 1. Read TOML from configDir/glm.toml
	tomlPath := filepath.Join(configDir, "glm.toml")
	if tomlData, err := os.ReadFile(tomlPath); err == nil {
		if err := parseTOML(string(tomlData), cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("err:config \"Cannot read glm.toml: %s\"", err.Error())
	}
	// Missing file = use defaults, no error

	// 2. Read API key from configDir/zai_api_key
	apiKey, err := readAPIKey(configDir)
	if err != nil {
		return nil, err
	}
	cfg.ZaiAPIKey = apiKey

	// 3. Apply env var overrides
	applyEnvOverrides(cfg)

	// 4. Apply LoadOption overrides (CLI flags)
	if opts.Model != "" {
		cfg.Model = opts.Model
		cfg.OpusModel = opts.Model
		cfg.SonnetModel = opts.Model
		cfg.HaikuModel = opts.Model
	}

	// 5. Validate
	if err := validate(cfg); err != nil {
		return nil, err
	}

	// 6. Create subagent directory if not exists
	if err := createSubagentDir(subagentDir); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseTOML manually parses simple key = value TOML format.
// Supports section headers ([routing]) for grouped configuration.
// Ignores unknown keys and unknown sections.
func parseTOML(data string, cfg *Config) error {
	lines := strings.Split(data, "\n")
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Track current section.
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		// Parse key = value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("err:config \"Failed to parse glm.toml: invalid line '%s'\"", line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Trim quotes from value (both single and double)
		value = strings.Trim(value, `"'`)

		switch section {
		case "routing":
			if err := parseRoutingKey(key, value, cfg); err != nil {
				return err
			}
		case "models":
			if err := parseModelsKey(key, value, cfg); err != nil {
				return err
			}
		default:
			if err := parseGlobalKey(key, value, cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseRoutingKey handles keys within the [routing] TOML section.
func parseRoutingKey(key, value string, cfg *Config) error {
	if value == "" {
		return fmt.Errorf("err:config \"Failed to parse glm.toml: routing.%s value is empty\"", key)
	}
	switch key {
	case "light":
		cfg.Routing.Light = value
	case "medium":
		cfg.Routing.Medium = value
	case "heavy":
		cfg.Routing.Heavy = value
	default:
		// Unknown routing keys are ignored (consistent with global behavior).
	}
	return nil
}

// parseModelsKey handles keys within the [models] TOML section.
// Each key is a model name (optionally quoted) and value is an integer
// concurrency limit.
func parseModelsKey(key, value string, cfg *Config) error {
	// Strip quotes from model name key (TOML allows "model.name" = N).
	key = strings.Trim(key, `"'`)
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("err:config \"Failed to parse glm.toml: invalid concurrency value '%s' for model '%s'\"", value, key)
	}
	if cfg.Models == nil {
		cfg.Models = make(map[string]int)
	}
	cfg.Models[key] = n
	return nil
}

// parseGlobalKey handles top-level (non-sectioned) TOML keys.
func parseGlobalKey(key, value string, cfg *Config) error {
	switch key {
	case "model":
		if value == "" {
			return fmt.Errorf(`err:config "Failed to parse glm.toml: model value is empty"`)
		}
		cfg.Model = value
	case "opus_model":
		if value == "" {
			return fmt.Errorf(`err:config "Failed to parse glm.toml: opus_model value is empty"`)
		}
		cfg.OpusModel = value
	case "sonnet_model":
		if value == "" {
			return fmt.Errorf(`err:config "Failed to parse glm.toml: sonnet_model value is empty"`)
		}
		cfg.SonnetModel = value
	case "haiku_model":
		if value == "" {
			return fmt.Errorf(`err:config "Failed to parse glm.toml: haiku_model value is empty"`)
		}
		cfg.HaikuModel = value
	case "permission_mode":
		if value == "" {
			return fmt.Errorf(`err:config "Failed to parse glm.toml: permission_mode value is empty"`)
		}
		cfg.PermissionMode = value
	case "api_rps", "max_parallel":
		// Removed: global concurrency limit is no longer supported.
		// These keys are silently ignored for backward compatibility.
	case "proxy_enabled":
		cfg.ProxyEnabled = value == "true"
	case "proxy_idle_timeout":
		if n, err := strconv.Atoi(value); err == nil {
			cfg.ProxyIdleTimeout = n
		} else {
			return fmt.Errorf("err:config \"Failed to parse glm.toml: invalid proxy_idle_timeout value '%s'\"", value)
		}
	case "proxy_port":
		if n, err := strconv.Atoi(value); err == nil {
			cfg.ProxyPort = n
		} else {
			return fmt.Errorf("err:config \"Failed to parse glm.toml: invalid proxy_port value '%s'\"", value)
		}
	case "effort":
		cfg.Effort = value
	case "exclude_dynamic_sections":
		cfg.ExcludeDynamicSections = value == "true"
	case "system_prompt":
		cfg.SystemPrompt = value
	case "mcp_config":
		cfg.MCPConfig = value
	case "mcp_strict":
		cfg.MCPStrict = value == "true"
	}
	// Unknown keys are ignored
	return nil
}

// readAPIKey reads the API key from configDir/zai_api_key.
func readAPIKey(configDir string) (string, error) {
	primaryPath := filepath.Join(configDir, "zai_api_key")
	if data, err := os.ReadFile(primaryPath); err == nil {
		return parseAPIKey(string(data)), nil
	} else if !os.IsNotExist(err) {
		errMsg := err.Error()
		if strings.Contains(errMsg, ": permission denied") {
			return "", fmt.Errorf("err:config \"Cannot read API key file: permission denied\"")
		}
		return "", fmt.Errorf("err:config \"Cannot read API key file: %s\"", errMsg)
	}

	return "", fmt.Errorf("err:config API key file not found: %s", primaryPath)
}

// parseAPIKey parses raw key or ZAI_API_KEY="value" format, stripping whitespace/newlines
func parseAPIKey(data string) string {
	data = strings.TrimSpace(data)
	// Check for ZAI_API_KEY="value" format
	if rest, ok := strings.CutPrefix(data, "ZAI_API_KEY="); ok {
		data = strings.Trim(rest, `"`)
	}
	return strings.TrimSpace(data)
}

// applyEnvOverrides applies environment variable overrides to the config
func applyEnvOverrides(cfg *Config) {
	if v := getenv("GLM_MODEL"); v != "" {
		cfg.Model = v
		// GLM_MODEL applies to all slots unless per-slot override is set
		if getenv("GLM_OPUS_MODEL") == "" {
			cfg.OpusModel = v
		}
		if getenv("GLM_SONNET_MODEL") == "" {
			cfg.SonnetModel = v
		}
		if getenv("GLM_HAIKU_MODEL") == "" {
			cfg.HaikuModel = v
		}
	}
	if v := getenv("GLM_OPUS_MODEL"); v != "" {
		cfg.OpusModel = v
	}
	if v := getenv("GLM_SONNET_MODEL"); v != "" {
		cfg.SonnetModel = v
	}
	if v := getenv("GLM_HAIKU_MODEL"); v != "" {
		cfg.HaikuModel = v
	}
	if v := getenv("GLM_PERMISSION_MODE"); v != "" {
		cfg.PermissionMode = v
	}
	if v := getenv("GLM_DEBUG"); v != "" {
		lower := strings.ToLower(v)
		cfg.Debug = lower == "true" || lower == "1"
	}
	if v := getenv("GLM_ROUTING_LIGHT"); v != "" {
		cfg.Routing.Light = v
	}
	if v := getenv("GLM_ROUTING_MEDIUM"); v != "" {
		cfg.Routing.Medium = v
	}
	if v := getenv("GLM_ROUTING_HEAVY"); v != "" {
		cfg.Routing.Heavy = v
	}
	if v := getenv("GLM_EFFORT"); v != "" {
		cfg.Effort = v
	}
	if v := getenv("GLM_SYSTEM_PROMPT"); v != "" {
		cfg.SystemPrompt = v
	}
	if v := getenv("GLM_MCP_CONFIG"); v != "" {
		cfg.MCPConfig = v
	}
	if v := getenv("GLM_MCP_STRICT"); v != "" {
		lower := strings.ToLower(v)
		cfg.MCPStrict = lower == "true" || lower == "1"
	}
	if v := getenv("GLM_EXCLUDE_DYNAMIC_SECTIONS"); v != "" {
		lower := strings.ToLower(v)
		cfg.ExcludeDynamicSections = lower == "true" || lower == "1"
	}
	// GLM_MODEL_CONCURRENCY format: "model1:N,model2:M"
	if v := getenv("GLM_MODEL_CONCURRENCY"); v != "" {
		for entry := range strings.SplitSeq(v, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
			if len(parts) != 2 {
				continue
			}
			modelName := strings.TrimSpace(parts[0])
			if modelName == "" {
				continue
			}
			if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				if cfg.Models == nil {
					cfg.Models = make(map[string]int)
				}
				cfg.Models[modelName] = n
			}
		}
	}
}

// validate validates the config and returns an error if invalid
func validate(cfg *Config) error {
	// Check API key non-empty
	if cfg.ZaiAPIKey == "" {
		return fmt.Errorf("err:validation zai_api_key: API key is empty")
	}

	// Check permission_mode in valid set
	validModes := map[string]bool{
		"bypassPermissions": true,
		"acceptEdits":       true,
		"default":           true,
		"plan":              true,
	}
	if !validModes[cfg.PermissionMode] {
		return fmt.Errorf("err:validation permission_mode: must be one of: bypassPermissions, acceptEdits, default, plan (got %q)", cfg.PermissionMode)
	}

	return nil
}

// createSubagentDir creates the subagent directory if it doesn't exist
func createSubagentDir(subagentDir string) error {
	if _, err := os.Stat(subagentDir); err == nil {
		// Directory already exists
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("err:config \"Cannot create subagent directory: %s\"", err.Error())
	}

	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		// Strip the "mkdir <path>: " prefix from the error
		errMsg := err.Error()
		if strings.Contains(errMsg, ": permission denied") {
			return fmt.Errorf("err:config \"Cannot create subagent directory: permission denied\"")
		}
		return fmt.Errorf("err:config \"Cannot create subagent directory: %s\"", errMsg)
	}
	return nil
}

// getenv wraps os.Getenv for testability.
var getenv = os.Getenv
