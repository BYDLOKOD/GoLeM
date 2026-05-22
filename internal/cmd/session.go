// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/veschin/GoLeM/internal/config"
)

// SessionArgs holds the parsed arguments for the session command.
type SessionArgs struct {
	// Model is the base model flag (-m/--model). When set, it overrides all three
	// Anthropic model slots (opus, sonnet, haiku).
	Model string
	// OpusModel is the --opus flag.
	OpusModel string
	// SonnetModel is the --sonnet flag.
	SonnetModel string
	// HaikuModel is the --haiku flag.
	HaikuModel string
	// PermissionMode is the --mode flag. When "bypassPermissions" or set via
	// --unsafe, the --dangerously-skip-permissions flag is forwarded to claude.
	PermissionMode string
	// WorkDir is the -d flag. When non-empty the process working directory is
	// changed to this path before exec.
	WorkDir string
	// MCPConfig is the --mcp-config flag: golem-scoped MCP servers (path or JSON).
	MCPConfig string
	// Passthrough contains all flags and positional arguments not consumed by
	// GoLeM. They are forwarded verbatim to the claude binary.
	Passthrough []string
}

// SessionResult captures the parameters that SessionCmd would pass to
// syscall.Exec so that tests can inspect them without replacing the process.
type SessionResult struct {
	// Argv is the full argument list for the claude binary (argv[0] == "claude").
	Argv []string
	// Env is the environment slice passed to Exec.
	Env []string
	// WorkDir is the directory that would be chdir'd into before exec.
	WorkDir string
	// DebugMessages contains any debug-level messages that were emitted.
	DebugMessages []string
}

// SessionCmd parses args, builds the environment, and populates a
// SessionResult describing what would be exec'd. The actual exec is
// performed by the caller (main). Using a returned value rather than
// calling syscall.Exec directly keeps the function testable.
//
// cfg is the loaded GoLeM configuration (API key, models, base URL, timeout).
// args are the raw CLI arguments after the "session" sub-command token.
// debugLog receives debug messages; may be nil.
func SessionCmd(cfg *config.Config, args []string, debugLog io.Writer) (*SessionResult, error) {
	// Parse GoLeM-specific flags from args.
	sa := &SessionArgs{}
	var passthroughArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-d":
			if i+1 < len(args) {
				sa.WorkDir = args[i+1]
				i++
			}
		case "-t":
			// Timeout is ignored for session mode; emit debug message.
			if i+1 < len(args) {
				i++ // consume the value
			}
			if debugLog != nil {
				_, _ = fmt.Fprintln(debugLog, "Timeout flag ignored for session mode")
			}
		case "-m", "--model":
			if i+1 < len(args) {
				sa.Model = args[i+1]
				i++
			}
		case "--opus":
			if i+1 < len(args) {
				sa.OpusModel = args[i+1]
				i++
			}
		case "--sonnet":
			if i+1 < len(args) {
				sa.SonnetModel = args[i+1]
				i++
			}
		case "--haiku":
			if i+1 < len(args) {
				sa.HaikuModel = args[i+1]
				i++
			}
		case "--unsafe":
			sa.PermissionMode = "bypassPermissions"
		case "--mode":
			if i+1 < len(args) {
				sa.PermissionMode = args[i+1]
				i++
			}
		case "--mcp-config":
			if i+1 < len(args) {
				sa.MCPConfig = args[i+1]
				i++
			}
		default:
			// Unknown flag/arg - pass through to claude.
			passthroughArgs = append(passthroughArgs, arg)
		}
	}
	sa.Passthrough = passthroughArgs

	// Determine model slots - start from config defaults.
	opusModel := cfg.OpusModel
	sonnetModel := cfg.SonnetModel
	haikuModel := cfg.HaikuModel

	// Per-slot CLI flags override config.
	if sa.OpusModel != "" {
		opusModel = sa.OpusModel
	}
	if sa.SonnetModel != "" {
		sonnetModel = sa.SonnetModel
	}
	if sa.HaikuModel != "" {
		haikuModel = sa.HaikuModel
	}

	// -m/--model overrides all three slots (unless a per-slot flag was given).
	if sa.Model != "" {
		if sa.OpusModel == "" {
			opusModel = sa.Model
		}
		if sa.SonnetModel == "" {
			sonnetModel = sa.Model
		}
		if sa.HaikuModel == "" {
			haikuModel = sa.Model
		}
	}

	// Build environment (filtered copy of os.Environ with blocked vars removed).
	blocked := map[string]bool{
		"CLAUDECODE":                    true,
		"CLAUDE_CODE_ENTRYPOINT":        true,
		"ANTHROPIC_AUTH_TOKEN":          true,
		"ANTHROPIC_BASE_URL":            true,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":  true,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": true,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": true,
		"API_TIMEOUT_MS":                true,
	}
	var env []string
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && blocked[parts[0]] {
			continue
		}
		env = append(env, kv)
	}
	// Inject ZAI-specific env vars.
	env = append(env,
		"ANTHROPIC_AUTH_TOKEN="+cfg.ZaiAPIKey,
		"ANTHROPIC_BASE_URL="+cfg.ZaiBaseURL,
		"ANTHROPIC_DEFAULT_OPUS_MODEL="+opusModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL="+sonnetModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL="+haikuModel,
		"API_TIMEOUT_MS="+cfg.ZaiAPITimeoutMs,
	)

	// Build argv for claude (interactive session - no -p, --output-format, etc.).
	argv := []string{"claude"}

	// Append --effort flag if configured.
	if cfg.Effort != "" {
		argv = append(argv, "--effort", cfg.Effort)
	}

	// Append --exclude-dynamic-system-prompt-sections if configured.
	if cfg.ExcludeDynamicSections {
		argv = append(argv, "--exclude-dynamic-system-prompt-sections")
	}

	// Append permission flags if needed.
	if sa.PermissionMode == "bypassPermissions" {
		argv = append(argv, "--dangerously-skip-permissions")
	} else if sa.PermissionMode != "" {
		argv = append(argv, "--permission-mode", sa.PermissionMode)
	}

	// Golem-scoped MCP servers: flag overrides config default; never touches
	// the host ~/.claude/settings.json.
	mcpConfig := sa.MCPConfig
	if mcpConfig == "" {
		mcpConfig = cfg.MCPConfig
	}
	if mcpConfig != "" {
		argv = append(argv, "--mcp-config", mcpConfig)
		if cfg.MCPStrict {
			argv = append(argv, "--strict-mcp-config")
		}
	}

	// Append passthrough args.
	argv = append(argv, sa.Passthrough...)

	return &SessionResult{
		Argv:    argv,
		Env:     env,
		WorkDir: sa.WorkDir,
	}, nil
}
