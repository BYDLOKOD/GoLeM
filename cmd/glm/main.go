// Binary glm - GoLeM CLI tool for spawning parallel Claude Code subagents.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/veschin/GoLeM/internal/cmd"
	"github.com/veschin/GoLeM/internal/config"
	"github.com/veschin/GoLeM/internal/dag"
	"github.com/veschin/GoLeM/internal/exitcode"
	"github.com/veschin/GoLeM/internal/job"
	"github.com/veschin/GoLeM/internal/log"
	"github.com/veschin/GoLeM/internal/mcp"
	"github.com/veschin/GoLeM/internal/mcp/tools"
	"github.com/veschin/GoLeM/internal/prompt"
	"github.com/veschin/GoLeM/internal/proxy"
	"github.com/veschin/GoLeM/internal/slot"
)

const version = "2.0.0"

// logger is the global structured logger, initialized in run().
var logger *log.Logger

func main() {
	code := run(os.Args[1:])
	os.Exit(code)
}

// initLogger creates the global logger from environment variables.
func initLogger() *log.Logger {
	opts := []log.Option{log.WithWriter(os.Stderr)}

	if os.Getenv("GLM_DEBUG") == "1" {
		opts = append(opts, log.WithLevel(log.LevelDebug))
	}

	if os.Getenv("GLM_LOG_FORMAT") == "json" {
		opts = append(opts, log.WithFormat(log.FormatJSON))
	}

	fi, _ := os.Stderr.Stat()
	if fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		opts = append(opts, log.WithIsTTY(true))
	}

	if logFile := os.Getenv("GLM_LOG_FILE"); logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			opts = append(opts, log.WithFile(f))
		}
	}

	return log.New(opts...)
}

func run(args []string) int {
	logger = initLogger()

	if len(args) == 0 {
		usage(os.Stderr)
		return 1
	}

	subcmd := args[0]
	rest := args[1:]

	logger.Debug("command=" + subcmd)

	switch subcmd {
	case "run":
		return cmdRun(rest)
	case "start":
		return cmdStart(rest)
	case "status":
		return cmdStatus(rest)
	case "result":
		return cmdResult(rest)
	case "log":
		return cmdLog(rest)
	case "list":
		return cmdList(rest)
	case "clean":
		return cmdClean(rest)
	case "kill":
		return cmdKill(rest)
	case "chain":
		return cmdChain(rest)
	case "pipeline":
		return cmdPipeline(rest)
	case "session":
		return cmdSession(rest)
	case "doctor":
		return cmdDoctor()
	case "update":
		return cmdUpdate()
	case "config":
		return cmdConfig(rest)
	case "_install":
		return cmdInstall()
	case "_uninstall":
		return cmdUninstall()
	case "_proxy":
		return cmdProxy(rest)
	case "mcp":
		return cmdMCP()
	case "version", "--version", "-v":
		fmt.Println("glm " + version)
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", subcmd)
		usage(os.Stderr)
		return 1
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: glm {session|run|start|status|result|log|list|clean|kill|chain|pipeline|update|doctor|config|mcp} [options]

Commands:
  session [flags] [claude flags]     Interactive Claude Code
  run   [flags] "prompt"             Sync execution
  start [flags] "prompt"             Async execution
  chain [flags] "p1" "p2" ...        Chained execution
  pipeline FILE                      Execute DAG pipeline from JSON file
  status  JOB_ID                     Check job status
  result  JOB_ID                     Get text output
  log     JOB_ID                     Show file changes
  list    [--status S] [--since D]   List all jobs
  clean   [--days N]                 Remove old jobs
  kill    JOB_ID                     Terminate job
  update                             Self-update from GitHub
  doctor                             Check system health
  config  {show|set KEY VAL}         Manage configuration
  mcp                                MCP server (JSON-RPC over stdio)

Flags:
  -d, --dir DIR       Working directory
  -t, --timeout SEC   Timeout in seconds
  -m, --model MODEL   Set all three model slots to MODEL
  --opus MODEL        Set opus model
  --sonnet MODEL      Set sonnet model
  --haiku MODEL       Set haiku model
  --tier TIER         Model selection tier {light|medium|heavy|auto} (default: auto)
  --unsafe            Bypass all permission checks
  --mode MODE         Set permission mode
  --system-prompt TEXT  System prompt appended to constrain the golem
  --constraint KEY      Predefined constraint (repeatable): readonly, no-create, plan-first, scope:<path>
  --json              JSON output format
`)
}

// loadConfig loads the GoLeM configuration from standard paths.
func loadConfig() (*config.Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	configDir := filepath.Join(home, ".config", "GoLeM")
	subagentDir := filepath.Join(home, ".claude", "subagents")
	logger.Debug("config_dir=" + configDir)
	cfg, err := config.Load(configDir, subagentDir)
	if err != nil {
		return nil, err
	}
	logger.Debug(fmt.Sprintf("model=%s", cfg.Model))
	return cfg, nil
}

// resolveProjectID determines the project ID from the working directory.
func resolveProjectID(workdir string) string {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	return job.ResolveProjectID(abs)
}

// reconcileAndInitSlots runs startup reconciliation and creates a SlotManager.
func reconcileAndInitSlots(cfg *config.Config) (*slot.SlotManager, error) {
	if err := job.Reconcile(cfg.SubagentDir, time.Now()); err != nil {
		logger.Warn("reconcile: " + err.Error())
	}
	sm := slot.NewSlotManager(cfg.SubagentDir, 0) // 0 = unlimited
	if err := sm.Init(); err != nil {
		return nil, fmt.Errorf("slot init: %w", err)
	}
	return sm, nil
}

// die prints an error message to stderr and returns the appropriate exit code.
func die(err error) int {
	msg := err.Error()
	fmt.Fprintln(os.Stderr, msg)

	if strings.Contains(msg, "err:not_found") {
		return exitcode.NotFound
	}
	if strings.Contains(msg, "err:dependency") {
		return exitcode.DependencyMissing
	}
	if strings.Contains(msg, "err:timeout") {
		return exitcode.Timeout
	}
	return exitcode.UserError
}

// hasFlag checks if a specific flag is present in args.
func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// hasHelpFlag is true when -h or --help appears in args.
func hasHelpFlag(args []string) bool {
	return hasFlag(args, "-h") || hasFlag(args, "--help")
}

// stripFlag removes a boolean flag from args and returns the cleaned slice.
func stripFlag(args []string, flag string) []string {
	result := make([]string, 0, len(args))
	for _, a := range args {
		if a != flag {
			result = append(result, a)
		}
	}
	return result
}

// getFlagValue returns the value of a flag and remaining args, or empty string.
func getFlagValue(args []string, flag string) (string, []string) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			remaining := make([]string, 0, len(args)-2)
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+2:]...)
			return args[i+1], remaining
		}
	}
	return "", args
}

func cmdRun(args []string) int {
	if hasHelpFlag(args) {
		usage(os.Stdout)
		return 0
	}
	jsonMode := hasFlag(args, "--json")

	flags, err := cmd.ParseFlags(args)
	if err != nil {
		return die(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	ensureProxy(cfg)

	// Apply config defaults.
	if flags.Timeout <= 0 {
		flags.Timeout = config.DefaultTimeout
	}

	if err := cmd.Validate(flags); err != nil {
		return die(err)
	}

	// Caller handles reconciliation and slot wait.
	sm, err := reconcileAndInitSlots(cfg)
	if err != nil {
		return die(err)
	}
	if err := sm.WaitForSlot(); err != nil {
		return die(err)
	}

	projectID := resolveProjectID(flags.Dir)

	// ExecuteJob releases the slot via defer when done.
	result, err := cmd.ExecuteJob(context.Background(), cmd.ExecuteJobParams{
		Cfg:           cfg,
		Flags:         flags,
		SubagentsRoot: cfg.SubagentDir,
		ProjectID:     projectID,
		AutoDelete:    true,
		SlotManager:   sm,
	})
	if err != nil {
		return die(err)
	}

	if jsonMode {
		_ = cmd.ResultJSON(cfg.SubagentDir, projectID, result.JobID, os.Stdout)
	} else {
		if len(result.Stdout) > 0 {
			_, _ = fmt.Fprint(os.Stdout, result.Stdout)
		}
		if len(result.Changelog) > 0 {
			_, _ = fmt.Fprint(os.Stderr, result.Changelog)
		}
		if len(result.Stderr) > 0 {
			_, _ = fmt.Fprint(os.Stderr, result.Stderr)
		}
	}

	return result.ExitCode
}

func cmdStart(args []string) int {
	if hasHelpFlag(args) {
		usage(os.Stdout)
		return 0
	}
	flags, err := cmd.ParseFlags(args)
	if err != nil {
		return die(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	ensureProxy(cfg)

	if flags.Timeout <= 0 {
		flags.Timeout = config.DefaultTimeout
	}

	if err := cmd.Validate(flags); err != nil {
		return die(err)
	}

	projectID := resolveProjectID(flags.Dir)

	// Pre-create job so the user gets a valid job ID immediately.
	jobID := job.GenerateJobID()
	j, err := job.NewJob(cfg.SubagentDir, projectID, jobID)
	if err != nil {
		return die(err)
	}

	// Write PID before printing job ID (preserves original timing).
	pid := os.Getpid()
	_ = os.WriteFile(filepath.Join(j.Dir, "pid.txt"), []byte(strconv.Itoa(pid)), 0o644)

	// Print job ID immediately.
	_, _ = fmt.Fprintln(os.Stdout, jobID)

	// Run in background goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				_ = os.WriteFile(filepath.Join(j.Dir, "status"), []byte("failed"), 0o644)
				_ = os.WriteFile(filepath.Join(j.Dir, "stderr.txt"),
					fmt.Appendf(nil, "panic: %v", r), 0o644)
			}
		}()

		// Caller handles reconciliation and slot wait inside the goroutine.
		sm, slotErr := reconcileAndInitSlots(cfg)
		if slotErr != nil {
			_ = os.WriteFile(filepath.Join(j.Dir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(j.Dir, "stderr.txt"),
				fmt.Appendf(nil, "slot init: %v", slotErr), 0o644)
			return
		}
		if waitErr := sm.WaitForSlot(); waitErr != nil {
			_ = os.WriteFile(filepath.Join(j.Dir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(j.Dir, "stderr.txt"),
				fmt.Appendf(nil, "slot wait: %v", waitErr), 0o644)
			return
		}

		// ExecuteJob releases the slot via defer when done.
		_, execErr := cmd.ExecuteJob(context.Background(), cmd.ExecuteJobParams{
			Cfg:           cfg,
			Flags:         flags,
			SubagentsRoot: cfg.SubagentDir,
			ProjectID:     projectID,
			AutoDelete:    false,
			SlotManager:   sm,
			JobID:         jobID,
		})
		if execErr != nil {
			_ = os.WriteFile(filepath.Join(j.Dir, "status"), []byte("failed"), 0o644)
			_ = os.WriteFile(filepath.Join(j.Dir, "stderr.txt"),
				[]byte(execErr.Error()), 0o644)
		}
	}()

	// Wait for completion or signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sig:
		return 0
	case <-done:
		return 0
	}
}

func cmdStatus(args []string) int {
	jsonMode := hasFlag(args, "--json")
	args = stripFlag(args, "--json")

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No job ID provided"`)
		return exitcode.UserError
	}

	jobID := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	cwd, _ := os.Getwd()
	projectID := resolveProjectID(cwd)

	if jsonMode {
		if err := cmd.StatusJSON(cfg.SubagentDir, projectID, jobID, os.Stdout); err != nil {
			return die(err)
		}
		return 0
	}

	result, err := cmd.StatusCmd(jobID, cfg.SubagentDir, projectID, os.Stdout)
	if err != nil {
		return die(err)
	}
	return result.ExitCode
}

func cmdResult(args []string) int {
	jsonMode := hasFlag(args, "--json")
	args = stripFlag(args, "--json")

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No job ID provided"`)
		return exitcode.UserError
	}

	jobID := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	cwd, _ := os.Getwd()
	projectID := resolveProjectID(cwd)

	if jsonMode {
		if err := cmd.ResultJSON(cfg.SubagentDir, projectID, jobID, os.Stdout); err != nil {
			return die(err)
		}
		return 0
	}

	result, err := cmd.ResultCmd(jobID, cfg.SubagentDir, projectID, os.Stdout, os.Stderr)
	if err != nil {
		return die(err)
	}
	return result.ExitCode
}

func cmdLog(args []string) int {
	jsonMode := hasFlag(args, "--json")
	args = stripFlag(args, "--json")

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No job ID provided"`)
		return exitcode.UserError
	}

	jobID := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	cwd, _ := os.Getwd()
	projectID := resolveProjectID(cwd)

	if jsonMode {
		if err := cmd.LogJSON(cfg.SubagentDir, projectID, jobID, os.Stdout); err != nil {
			return die(err)
		}
		return 0
	}

	if err := cmd.LogCmd(cfg.SubagentDir, projectID, jobID, os.Stdout); err != nil {
		return die(err)
	}
	return 0
}

func cmdList(args []string) int {
	jsonMode := hasFlag(args, "--json")

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	// Parse filter options (shared between JSON and text modes).
	var filter cmd.FilterOptions
	statusRaw, args := getFlagValue(args, "--status")
	if statusRaw != "" {
		statuses, parseErr := cmd.ParseStatusFilter(statusRaw)
		if parseErr != nil {
			return die(parseErr)
		}
		filter.Statuses = statuses
	}

	sinceRaw, _ := getFlagValue(args, "--since")
	if sinceRaw != "" {
		since, parseErr := cmd.ParseSinceFilter(sinceRaw, time.Now)
		if parseErr != nil {
			return die(parseErr)
		}
		filter.Since = since
	}

	if jsonMode {
		if err := cmd.ListJSON(cfg.SubagentDir, &filter, os.Stdout); err != nil {
			return die(err)
		}
		return 0
	}

	// Print proxy stats header if the proxy is running.
	if port, alive := proxy.IsRunning(cfg.ConfigDir); alive {
		proxyClient := &http.Client{Timeout: 2 * time.Second}
		proxyURL := fmt.Sprintf("http://localhost:%d/health", port)
		if resp, err := proxyClient.Get(proxyURL); err == nil {
			var hr struct {
				Active        int64 `json:"active"`
				Queued        int64 `json:"queued"`
				TotalRequests int64 `json:"total_requests"`
				UptimeSec     int64 `json:"uptime_sec"`
			}
			if jsonErr := json.NewDecoder(resp.Body).Decode(&hr); jsonErr == nil {
				uptime := time.Duration(hr.UptimeSec) * time.Second
				uptimeStr := uptime.Round(time.Second).String()
				_, _ = fmt.Fprintf(os.Stdout, "Proxy: active=%d queued=%d total=%d | uptime=%s\n",
					hr.Active, hr.Queued, hr.TotalRequests, uptimeStr)
			}
			_ = resp.Body.Close()
		}
	}

	if err := cmd.ListCmd(cfg.SubagentDir, os.Stdout, &filter); err != nil {
		return die(err)
	}
	return 0
}

func cmdClean(args []string) int {
	days := -1 // default: remove only terminal status

	daysRaw, _ := getFlagValue(args, "--days")
	if daysRaw != "" {
		d, err := strconv.Atoi(daysRaw)
		if err != nil || d < 0 {
			fmt.Fprintf(os.Stderr, `err:user "Invalid --days value: %s"`+"\n", daysRaw)
			return exitcode.UserError
		}
		days = d
	}

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	if err := cmd.CleanCmd(cfg.SubagentDir, days, time.Now(), os.Stdout); err != nil {
		return die(err)
	}
	return 0
}

func cmdKill(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No job ID provided"`)
		return exitcode.UserError
	}

	jobID := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	cwd, _ := os.Getwd()
	projectID := resolveProjectID(cwd)

	signalFn := func(pid int, sig os.Signal) error {
		return syscall.Kill(-pid, sig.(syscall.Signal))
	}
	sleepFn := func() {
		time.Sleep(1 * time.Second)
	}

	if err := cmd.KillCmd(cfg.SubagentDir, projectID, jobID, signalFn, sleepFn); err != nil {
		return die(err)
	}
	return 0
}

func cmdChain(args []string) int {
	if hasHelpFlag(args) {
		usage(os.Stdout)
		return 0
	}
	// Parse chain-specific flags.
	continueOnError := hasFlag(args, "--continue-on-error")

	// Remove --continue-on-error from args for flag parsing.
	var cleanArgs []string
	for _, a := range args {
		if a != "--continue-on-error" {
			cleanArgs = append(cleanArgs, a)
		}
	}

	// Split prompts (each quoted argument is a prompt).
	flags, err := cmd.ParseFlags(cleanArgs)
	if err != nil {
		return die(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	ensureProxy(cfg)

	if flags.Timeout <= 0 {
		flags.Timeout = config.DefaultTimeout
	}

	// For chain, the "prompt" is actually multiple prompts joined.
	// Re-parse args to extract individual prompts.
	prompts := extractPrompts(cleanArgs)
	if len(prompts) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No prompts provided"`)
		return exitcode.UserError
	}

	projectID := resolveProjectID(flags.Dir)

	cf := &cmd.ChainFlags{
		Flags:           flags,
		ContinueOnError: continueOnError,
		Prompts:         prompts,
	}

	// ChainCmd manages per-step slot acquisition internally; no pre-acquisition here.
	result, err := cmd.ChainCmd(cf, cfg, cfg.SubagentDir, projectID, os.Stdout, os.Stderr)
	if err != nil {
		return die(err)
	}
	return result.ExitCode
}

// extractPrompts extracts individual prompts from chain arguments.
// Flags (-d, -t, -m, etc.) and their values are skipped.
func extractPrompts(args []string) []string {
	flagsWithValue := map[string]bool{
		"-d": true, "--dir": true,
		"-t": true, "--timeout": true,
		"-m": true, "--model": true,
		"--opus": true, "--sonnet": true, "--haiku": true,
		"--mode": true, "--tier": true,
		"--system-prompt": true, "--constraint": true,
	}

	var prompts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if flagsWithValue[a] {
			i++ // skip value
			continue
		}
		if a == "--unsafe" || a == "--continue-on-error" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		prompts = append(prompts, a)
	}
	return prompts
}

func cmdPipeline(args []string) int {
	if hasHelpFlag(args) {
		usage(os.Stdout)
		return 0
	}
	// Parse --system-prompt and --constraint flags from args.
	// The pipeline file path is the first non-flag argument.
	var systemPromptFlag string
	var constraintFlags []string
	var remainingArgs []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--system-prompt":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, `err:user "Missing value for --system-prompt flag"`)
				return exitcode.UserError
			}
			systemPromptFlag = args[i+1]
			i++
		case "--constraint":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, `err:user "Missing value for --constraint flag"`)
				return exitcode.UserError
			}
			constraintFlags = append(constraintFlags, args[i+1])
			i++
		default:
			remainingArgs = append(remainingArgs, args[i])
		}
	}

	if len(remainingArgs) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "No pipeline file provided"`)
		return exitcode.UserError
	}

	filePath := remainingArgs[0]

	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	ensureProxy(cfg)

	// Load the DAG definition.
	d, err := dag.LoadDAGFromFile(filePath)
	if err != nil {
		return die(err)
	}

	// Validate.
	if err := d.Validate(); err != nil {
		return die(err)
	}

	// Resolve working directory.
	cwd, _ := os.Getwd()
	projectID := resolveProjectID(cwd)

	// Create a base directory for step job directories.
	pipelineDir := filepath.Join(cfg.SubagentDir, projectID, "pipeline-"+job.GenerateJobID())
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		return die(fmt.Errorf("create pipeline dir: %w", err))
	}
	defer func() { _ = os.RemoveAll(pipelineDir) }()

	// Slot management: one slot for the entire pipeline.
	sm, err := reconcileAndInitSlots(cfg)
	if err != nil {
		return die(err)
	}
	if err := sm.WaitForSlot(); err != nil {
		return die(err)
	}
	defer func() {
		if releaseErr := sm.ReleaseSlot(); releaseErr != nil {
			logger.Warn("release slot: " + releaseErr.Error())
		}
	}()

	// Determine model and timeout defaults.
	model := cfg.Model
	timeout := config.DefaultTimeout

	// Assemble system prompt: CLI flags take precedence over config default.
	baseSystemPrompt := systemPromptFlag
	if baseSystemPrompt == "" {
		baseSystemPrompt = cfg.SystemPrompt
	}
	finalSystemPrompt, err := prompt.AssembleSystemPrompt(constraintFlags, baseSystemPrompt)
	if err != nil {
		return die(err)
	}

	// Create executor with assembled system prompt.
	executor := dag.NewClaudeStepExecutor(cfg, pipelineDir, cwd, model, timeout, finalSystemPrompt)

	// Create scheduler with unlimited concurrency (0 = unlimited).
	scheduler := dag.NewScheduler(executor, 0)

	// Run the pipeline.
	results, _, err := scheduler.Run(context.Background(), d)
	if err != nil {
		return die(err)
	}

	// Print results.
	anyFailed := false
	for _, step := range d.Steps {
		arts := results[step.ID]
		if len(arts) == 0 {
			fmt.Fprintf(os.Stderr, "[FAIL] step %q: failed or skipped\n", step.ID)
			anyFailed = true
			continue
		}
		_, _ = fmt.Fprintf(os.Stdout, "[OK]   step %q:\n", step.ID)
		if len(arts[0].Content) > 0 {
			_, _ = fmt.Fprintln(os.Stdout, string(arts[0].Content))
		}
	}

	if anyFailed {
		return 1
	}
	return 0
}

func cmdSession(args []string) int {
	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	var debugLog *log.Logger
	if os.Getenv("GLM_DEBUG") == "1" {
		debugLog = log.New(log.WithLevel(log.LevelDebug), log.WithWriter(os.Stderr))
	}

	var debugWriter *os.File
	if debugLog != nil {
		debugWriter = os.Stderr
	}

	result, err := cmd.SessionCmd(cfg, args, debugWriter)
	if err != nil {
		return die(err)
	}

	// Change working directory if specified.
	if result.WorkDir != "" {
		if err := os.Chdir(result.WorkDir); err != nil {
			fmt.Fprintf(os.Stderr, `err:user "Directory not found: %s"`+"\n", result.WorkDir)
			return exitcode.UserError
		}
	}

	// Exec the claude binary, replacing the current process.
	claudePath, err := findClaude()
	if err != nil {
		return die(err)
	}

	if err := syscall.Exec(claudePath, result.Argv, result.Env); err != nil {
		fmt.Fprintf(os.Stderr, "exec claude: %v\n", err)
		return 1
	}
	return 0 // unreachable after exec
}

func cmdDoctor() int {
	cfg, err := loadConfig()
	if err != nil {
		// Doctor should work even without full config.
		home, _ := os.UserHomeDir()
		cfg = &config.Config{
			SubagentDir: filepath.Join(home, ".claude", "subagents"),
			ConfigDir:   filepath.Join(home, ".config", "GoLeM"),
			OpusModel:   config.DefaultModel,
			SonnetModel: config.DefaultModel,
			HaikuModel:  config.DefaultModel,
		}
	}

	opts := cmd.DoctorOptions{
		ClaudeBinaryName: "claude",
		APIKeyPath:       filepath.Join(cfg.ConfigDir, "zai_api_key"),
		ZAIEndpoint:      config.ZaiBaseURL,
		HTTPTimeout:      5 * time.Second,
		SubagentsRoot:    cfg.SubagentDir,
		OpusModel:        cfg.OpusModel,
		SonnetModel:      cfg.SonnetModel,
		HaikuModel:       cfg.HaikuModel,
		ConfigDir:        cfg.ConfigDir,
	}

	if err := cmd.DoctorCmd(opts, os.Stdout); err != nil {
		return die(err)
	}
	return 0
}

func cmdUpdate() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return die(err)
	}

	configDir := filepath.Join(home, ".config", "GoLeM")

	// Determine clone directory (where GoLeM source lives).
	execPath, err := os.Executable()
	if err != nil {
		return die(fmt.Errorf(`err:user "Cannot determine executable path"`))
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}
	cloneDir := filepath.Dir(filepath.Dir(realPath))

	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")

	opts := cmd.UpdateOptions{
		ConfigDir:    configDir,
		CloneDir:     cloneDir,
		ClaudeMDPath: claudeMDPath,
		Out:          os.Stdout,
		ErrOut:       os.Stderr,
	}

	if err := cmd.UpdateCmd(opts); err != nil {
		return die(err)
	}
	return 0
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `err:user "Usage: glm config {show|set KEY VALUE}"`)
		return exitcode.UserError
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return die(err)
	}
	configDir := filepath.Join(home, ".config", "GoLeM")
	subagentDir := filepath.Join(home, ".claude", "subagents")

	switch args[0] {
	case "show":
		opts := cmd.ConfigShowOptions{
			ConfigDir:   configDir,
			SubagentDir: subagentDir,
			EnvGetenv:   os.Getenv,
		}
		if err := cmd.ConfigShowCmd(opts, os.Stdout); err != nil {
			return die(err)
		}
		return 0

	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, `err:user "Usage: glm config set KEY VALUE"`)
			return exitcode.UserError
		}
		opts := cmd.ConfigSetOptions{
			ConfigDir: configDir,
			Key:       args[1],
			Value:     args[2],
		}
		if err := cmd.ConfigSetCmd(opts); err != nil {
			return die(err)
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", args[0])
		return exitcode.UserError
	}
}

func cmdInstall() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return die(err)
	}

	// Determine clone directory. For source installs the binary lives inside
	// the repo (e.g. ~/GoLeM/glm). For go-install the binary is in
	// $GOPATH/bin and cloneDir will not contain .git - InstallCmd detects this.
	execPath, _ := os.Executable()
	realPath, _ := filepath.EvalSymlinks(execPath)
	cloneDir := filepath.Dir(filepath.Dir(realPath))

	// If cloneDir doesn't contain .git, it's a go-install - pass empty.
	if _, err := os.Stat(filepath.Join(cloneDir, ".git")); err != nil {
		cloneDir = ""
	}

	opts := cmd.InstallOptions{
		CloneDir:     cloneDir,
		ConfigDir:    filepath.Join(home, ".config", "GoLeM"),
		ClaudeMDPath: filepath.Join(home, ".claude", "CLAUDE.md"),
		SubagentsDir: filepath.Join(home, ".claude", "subagents"),
		Version:      version,
		In:           os.Stdin,
		Out:          os.Stdout,
	}

	if err := cmd.InstallCmd(opts); err != nil {
		return die(err)
	}
	return 0
}

func cmdUninstall() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return die(err)
	}

	opts := cmd.UninstallOptions{
		ConfigDir:    filepath.Join(home, ".config", "GoLeM"),
		ClaudeMDPath: filepath.Join(home, ".claude", "CLAUDE.md"),
		SubagentsDir: filepath.Join(home, ".claude", "subagents"),
		In:           os.Stdin,
		Out:          os.Stdout,
	}

	if err := cmd.UninstallCmd(opts); err != nil {
		return die(err)
	}
	return 0
}

func cmdProxy(args []string) int {
	cfg, err := loadConfig()
	if err != nil {
		return die(err)
	}

	port := cfg.ProxyPort
	idleTimeout := cfg.ProxyIdleTimeout
	target := cfg.ZaiBaseURL
	configDir := cfg.ConfigDir

	// Parse flags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil {
					port = n
				}
			}
		case "--concurrency":
			// Removed: global concurrency limit no longer used.
			// Accept and discard the flag for backward compatibility with running proxy daemons.
			if i+1 < len(args) {
				i++
				_ = args[i] // discard value
			}
		case "--idle-timeout":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil {
					idleTimeout = n
				}
			}
		case "--target":
			if i+1 < len(args) {
				i++
				target = args[i]
			}
		case "--config-dir":
			if i+1 < len(args) {
				i++
				configDir = args[i]
			}
		}
	}

	// Build per-model config from glm.toml [models] section.
	// If no [models] section exists, fall back to global concurrency.
	var modelsCfg map[string]int
	if len(cfg.Models) > 0 {
		modelsCfg = cfg.Models
	}

	p := proxy.New(proxy.Config{
		TargetURL:   target,
		Concurrency: 1, // fallback for global-semaphore mode when no [models] section
		IdleTimeout: time.Duration(idleTimeout) * time.Second,
		Port:        port,
		LogFile:     filepath.Join(configDir, "proxy.log"),
		Models:      modelsCfg,
	})

	// Start the proxy in a goroutine so we can write the PID file
	// between binding the listener and blocking on Serve.
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Start()
		errCh <- err
	}()

	// Wait until the proxy has bound the listener and the port is known.
	deadline := time.Now().Add(5 * time.Second)
	boundPort := 0
	for time.Now().Before(deadline) {
		if bp := p.Port(); bp > 0 {
			boundPort = bp
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if boundPort == 0 {
		// Start returned an error before binding.
		select {
		case startErr := <-errCh:
			if startErr != nil {
				return die(startErr)
			}
		default:
		}
		return die(fmt.Errorf("proxy: failed to bind listener within 5 seconds"))
	}

	// Write PID and port files so other glm instances can discover this proxy.
	if err := proxy.WritePIDFile(configDir, os.Getpid(), boundPort); err != nil {
		logger.Warn("proxy: " + err.Error())
	}
	defer func() {
		if cleanErr := proxy.CleanPIDFile(configDir); cleanErr != nil {
			logger.Warn("proxy: " + cleanErr.Error())
		}
	}()

	// Block until the proxy shuts down (idle timeout or signal).
	if err := <-errCh; err != nil {
		return die(err)
	}
	return 0
}

// ensureProxy starts the rate-limiting proxy if enabled and updates cfg.ZaiBaseURL
// to point at the local proxy port.
func ensureProxy(cfg *config.Config) {
	if !cfg.ProxyEnabled {
		return
	}
	glmBin, err := os.Executable()
	if err != nil {
		logger.Warn("proxy: cannot find executable: " + err.Error())
		return
	}
	if realBin, evalErr := filepath.EvalSymlinks(glmBin); evalErr == nil {
		glmBin = realBin
	}
	proxyPort, err := proxy.EnsureRunning(glmBin, cfg.ConfigDir, cfg.ZaiBaseURL, time.Duration(cfg.ProxyIdleTimeout)*time.Second)
	if err != nil {
		logger.Warn("proxy: " + err.Error())
		return
	}
	cfg.ZaiBaseURL = fmt.Sprintf("http://localhost:%d", proxyPort)
}

func cmdMCP() int {
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("load config: " + err.Error())
		return 1
	}

	ensureProxy(cfg)

	transport := mcp.NewStdioTransport(os.Stdin, os.Stdout)
	srv := mcp.NewServer(transport)
	// TODO: wire event bus for MCP progress notifications (glm_start progress events)

	// Register all tool handlers.
	tc := tools.NewToolContext(cfg, cfg.SubagentDir, "")
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_run",
		Description: "Execute a GLM subagent task synchronously",
		InputSchema: tools.RunDefinition(),
	}, tools.RunHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_start",
		Description: "Start a GLM subagent task asynchronously",
		InputSchema: tools.StartDefinition(),
	}, tools.StartHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_status",
		Description: "Check the status of a GLM subagent job",
		InputSchema: tools.StatusDefinition(),
	}, tools.StatusHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_result",
		Description: "Retrieve the output of a completed GLM subagent job",
		InputSchema: tools.ResultDefinition(),
	}, tools.ResultHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_list",
		Description: "List all GLM subagent jobs with optional filters",
		InputSchema: tools.ListDefinition(),
	}, tools.ListHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_kill",
		Description: "Terminate a running GLM subagent job",
		InputSchema: tools.KillDefinition(),
	}, tools.KillHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_chain",
		Description: "Execute a chain of GLM subagent tasks sequentially",
		InputSchema: tools.ChainDefinition(),
	}, tools.ChainHandler(tc))
	srv.RegisterTool(mcp.ToolDefinition{
		Name:        "glm_pipeline",
		Description: "Execute a DAG pipeline of GLM subagent steps with dependency ordering and parallel execution",
		InputSchema: tools.PipelineDefinition(),
	}, tools.NewPipelineHandler(cfg, nil, 0))

	// Block until stdin closes or signal received.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Serve(ctx); err != nil {
		logger.Error("mcp serve: " + err.Error())
		return 1
	}
	return 0
}

// findClaude locates the claude binary in PATH.
func findClaude() (string, error) {
	path, err := filepath.Abs("claude")
	if err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}
	}

	// Search PATH.
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		candidate := filepath.Join(dir, "claude")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf(`err:dependency "claude CLI not found in PATH"`)
}
