package tools

import (
	"os"
	"syscall"
	"time"

	"github.com/veschin/GoLeM/internal/config"
)

// ToolContext holds shared dependencies for all tool handlers.
// It is created once at server startup and passed to handler constructors.
type ToolContext struct {
	// Cfg is the loaded GoLeM configuration.
	Cfg *config.Config
	// SubagentsRoot is the root directory for job storage
	// (typically ~/.claude/subagents).
	SubagentsRoot string
	// ProjectID is the resolved project identifier for job scoping.
	ProjectID string
}

// NewToolContext creates a ToolContext from the loaded config.
// subagentsRoot defaults to cfg.SubagentDir if empty.
// projectID defaults to "mcp" if empty (MCP tools run outside a project directory).
func NewToolContext(cfg *config.Config, subagentsRoot, projectID string) *ToolContext {
	if subagentsRoot == "" {
		subagentsRoot = cfg.SubagentDir
	}
	if projectID == "" {
		projectID = "mcp"
	}
	return &ToolContext{
		Cfg:           cfg,
		SubagentsRoot: subagentsRoot,
		ProjectID:     projectID,
	}
}

// productionSignalFn sends a signal to a process group using syscall.Kill.
func productionSignalFn(pid int, sig os.Signal) error {
	return syscall.Kill(-pid, sig.(syscall.Signal))
}

// productionSleepFn sleeps for 1 second.
func productionSleepFn() {
	time.Sleep(1 * time.Second)
}
