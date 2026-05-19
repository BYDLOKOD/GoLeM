# GoLeM Improvement Roadmap

GoLeM is a stdlib-only Go tool that lets Claude Code (Opus) delegate work to GLM (Z.AI) models as subagents.
Currently CLI-only with file-based job management. This roadmap describes the planned evolution
toward MCP-native operation, streaming progress, DAG pipelines, and smart model routing.

## Phase 0: Foundation

**Goal**: Build shared primitives that unblock all later phases. No external dependencies.

**Dependencies**: None. All three packages are independent and can be built in parallel.

### 0.1 -- Event System

**Package**: `internal/event/`

**Deliverables**:

- `internal/event/event.go` -- event types and the Event struct
- `internal/event/bus.go` -- publish/subscribe bus
- `internal/event/bus_test.go`

**Key types and interfaces**:

```go
// EventType enumerates lifecycle events produced by GoLeM subsystems.
type EventType int

const (
    JobQueued EventType = iota
    JobRunning
    JobProgress
    ToolUse
    JobDone
    JobFailed
    JobTimeout
    JobKilled
    SlotAcquired
    SlotReleased
)

// Event is the unit of information flowing through the bus.
type Event struct {
    Type      EventType
    JobID     string
    Timestamp time.Time
    Data      any // payload varies by EventType
}

// Bus distributes events to subscribers.
// A nil *Bus is safe to call -- Publish and Subscribe are no-ops.
type Bus struct { /* sync.RWMutex + subscriber channels */ }

// Subscribe returns a channel that receives events matching the given filter.
// An empty filter receives all events.
func (b *Bus) Subscribe(filter ...EventType) <-chan Event

// Publish sends an event to every matching subscriber.
// Slow subscribers are dropped (non-blocking send) to avoid back-pressure on producers.
func (b *Bus) Publish(e Event)

// Close drains and closes all subscriber channels.
func (b *Bus) Close()
```

**Tests**: subscribe, publish, filter by type, close idempotency, slow-subscriber drop.

### 0.2 -- Artifact Abstraction

**Package**: `internal/artifact/`

**Deliverables**:

- `internal/artifact/artifact.go`
- `internal/artifact/artifact_test.go`

**Key types**:

```go
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

// NewText creates a text artifact.
func NewText(stepID string, content string) *Artifact

// NewJSON creates a JSON artifact from a marshalable value.
func NewJSON(stepID string, v any) (*Artifact, error)

// NewFileRef creates an artifact that references an external file path.
func NewFileRef(stepID, path string) *Artifact

// Save persists the artifact as artifact-{id}.json inside dir.
func (a *Artifact) Save(dir string) error

// Load reads artifact-{id}.json from dir.
func Load(dir, id string) (*Artifact, error)
```

**Persistence format**: `artifact-{id}.json` in the job directory, JSON-encoded with `encoding/json`.

### 0.3 -- Retry Package

**Package**: `internal/retry/`

**Deliverables**:

- `internal/retry/retry.go`
- `internal/retry/retry_test.go`

**Key interface**:

```go
// Do calls op repeatedly until it succeeds or options are exhausted.
// Backoff: delay = min(base * 2^attempt, maxDelay) + jitter.
func Do(ctx context.Context, op func() error, opts ...Option) error

type Option func(*options)

func WithMaxAttempts(n int) Option          // default: 3
func WithBaseDelay(d time.Duration) Option  // default: 1s
func WithMaxDelay(d time.Duration) Option   // default: 30s
func WithJitter(d time.Duration) Option     // default: 500ms
func WithRetryIf(fn func(error) bool) Option // default: always retry
```

---

## Phase 1: MCP Server (highest priority)

**Goal**: Expose GoLeM as an MCP tool server so Claude Code can call it natively
instead of shelling out via `glm run`. This eliminates CLAUDE.md injection and
enables structured input/output.

**Dependencies**: Phase 0.1 (optional -- bus can be nil, all Publish calls are nil-safe no-ops).

### 1.1 -- MCP Protocol

**Package**: `internal/mcp/`

**Deliverables**:

- `internal/mcp/protocol.go` -- JSON-RPC 2.0 types
- `internal/mcp/transport.go` -- stdio transport
- `internal/mcp/server.go` -- server loop and dispatch
- `internal/mcp/handler.go` -- handler interface
- `internal/mcp/server_test.go`

**Key types**:

```go
// --- protocol.go ---

// Request is a JSON-RPC 2.0 request.
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

// Notification is a JSON-RPC 2.0 notification (no ID).
type Notification struct {
    JSONRPC string          `json:"jsonrpc"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

// --- transport.go ---

// StdioTransport reads JSON-RPC messages from stdin (bufio.Scanner)
// and writes responses via json.Encoder on stdout.
type StdioTransport struct { /* ... */ }

func NewStdioTransport(in io.Reader, out io.Writer) *StdioTransport

// --- handler.go ---

// ToolHandler processes a single MCP tool call.
type ToolHandler interface {
    Handle(ctx context.Context, params json.RawMessage) (json.RawMessage, error)
}

// ToolDefinition describes a tool for tools/list.
type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

// --- server.go ---

// Server handles initialize, tools/list, and tools/call.
// It dispatches tools/call to registered ToolHandlers.
type Server struct { /* ... */ }

func NewServer(transport *StdioTransport) *Server
func (s *Server) RegisterTool(def ToolDefinition, h ToolHandler)
func (s *Server) Serve(ctx context.Context) error
```

### 1.2 -- Tool Handlers

**Package**: `internal/mcp/tools/`

**Deliverables**:

- `internal/mcp/tools/run.go` -- `glm_run` (sync execute, wraps `claude.Execute` + `ParseRawJSON`)
- `internal/mcp/tools/start.go` -- `glm_start` (async launch, returns `{job_id}`)
- `internal/mcp/tools/status.go` -- `glm_status` (returns job status JSON)
- `internal/mcp/tools/result.go` -- `glm_result` (returns job result JSON)
- `internal/mcp/tools/list.go` -- `glm_list` (returns `[]JobListItem` with filters)
- `internal/mcp/tools/kill.go` -- `glm_kill` (returns `{killed: bool}`)
- `internal/mcp/tools/chain.go` -- `glm_chain` (sequential pipeline)
- `internal/mcp/tools/tools_test.go`

**Input/output example** (`glm_run`):

```go
// RunInput is the params schema for glm_run.
type RunInput struct {
    Prompt  string `json:"prompt"`
    Dir     string `json:"dir,omitempty"`     // default: "."
    Timeout int    `json:"timeout,omitempty"` // default: config.DefaultTimeout
    Model   string `json:"model,omitempty"`
    Unsafe  bool   `json:"unsafe,omitempty"`
}

// RunOutput is the result returned by glm_run.
type RunOutput struct {
    Stdout    string `json:"stdout"`
    Changelog string `json:"changelog"`
    ExitCode  int    `json:"exit_code"`
}
```

### 1.3 -- MCP Subcommand and Install

**Deliverables**:

- `cmd/glm/main.go` -- add `glm mcp` subcommand (blocks on stdin, serves JSON-RPC via `Server.Serve`)
- `internal/cmd/install.go` -- update `InstallCmd` to register MCP server in Claude Code settings

The install step writes to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "golem": {
      "command": "glm",
      "args": ["mcp"]
    }
  }
}
```

### 1.4 -- Backward Compatibility

**Deliverables**:

- `internal/cmd/execute.go` -- extract shared execution logic from `cmdRun` in `main.go`

The `ExecuteJob()` function encapsulates: job creation, slot management, `claude.Execute`,
`ParseRawJSON`, status mapping, and cleanup. Both the CLI path (`cmdRun`/`cmdStart`) and
the MCP tool handlers (`glm_run`/`glm_start`) call this same function.

CLI and MCP coexist. CLAUDE.md injection remains available for users who have not migrated to MCP.

---

## Phase 2: Streaming and Progress

**Goal**: Replace poll-based status checking with push notifications. Claude Code
receives progress events in real time through MCP notifications.

**Dependencies**: Phase 0.1 (required -- bus must be functional), Phase 1.1 (required -- MCP transport).

### 2.1 -- Event Producers

**Deliverables**:

- Wire `event.Bus` into `claude.go` (publish `JobRunning`, `JobDone`, `JobFailed`, `JobTimeout`)
- Wire `event.Bus` into `slot.go` (publish `SlotAcquired`, `SlotReleased`)
- Wire `event.Bus` into `job.go` (publish `JobQueued`)

The bus is injected as an optional parameter. Existing code that does not create
a bus continues to work (nil-safe Publish is a no-op).

### 2.2 -- MCP Notifications

**Deliverables**:

- `internal/mcp/notify.go` -- `NotificationSender`
- `internal/mcp/notify_test.go`

```go
// NotificationSender subscribes to the event bus and sends JSON-RPC
// notifications over the MCP transport.
// Format: method = "notifications/tools/progress", progressToken = jobID.
type NotificationSender struct { /* ... */ }

func NewNotificationSender(bus *event.Bus, transport *StdioTransport) *NotificationSender
func (ns *NotificationSender) Start(ctx context.Context)
```

---

## Phase 3: Chain to DAG Pipeline

**Goal**: Generalize the linear `glm chain` into a directed acyclic graph where
independent steps run in parallel and dependent steps wait for their inputs.

**Dependencies**: Phase 0.2 (required -- artifacts carry data between steps).

### 3.1 -- DAG Definition and Scheduler

**Package**: `internal/dag/`

**Deliverables**:

- `internal/dag/dag.go` -- DAG and step definitions
- `internal/dag/scheduler.go` -- concurrent scheduler
- `internal/dag/dag_test.go`
- `internal/dag/scheduler_test.go`

**Key types**:

```go
// Step is a single node in the pipeline graph.
type Step struct {
    ID        string   `json:"id"`
    Prompt    string   `json:"prompt"`
    DependsOn []string `json:"depends_on,omitempty"`
    Condition string   `json:"condition,omitempty"` // CEL-like expression on prior results
    Model     string   `json:"model,omitempty"`
    Timeout   int      `json:"timeout,omitempty"`
}

// DAG is an ordered set of steps with dependency edges.
type DAG struct {
    Steps []Step `json:"steps"`
}

// Validate checks for cycles (topological sort), missing references,
// and duplicate IDs. Returns a descriptive error on failure.
func (d *DAG) Validate() error

// StepExecutor runs a single step and returns its artifacts.
type StepExecutor interface {
    Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error)
}

// Scheduler runs all steps in dependency order, parallelizing independent steps
// via a semaphore of size maxConcurrency.
type Scheduler struct { /* ... */ }

func NewScheduler(exec StepExecutor, maxConcurrency int) *Scheduler
func (s *Scheduler) Run(ctx context.Context, dag *DAG) (map[string][]*artifact.Artifact, error)
```

DAG definitions are JSON (stdlib `encoding/json`). No YAML dependency.

### 3.2 -- DAG CLI

**Deliverables**:

- `cmd/glm/main.go` -- add `glm pipeline` subcommand reading DAG JSON from file or stdin

`glm chain` becomes syntactic sugar: it builds a linear DAG internally and delegates to the scheduler.

### 3.3 -- DAG MCP Tool

**Deliverables**:

- `internal/mcp/tools/pipeline.go` -- `glm_pipeline` tool handler

```go
// PipelineInput is the params schema for glm_pipeline.
type PipelineInput struct {
    DAG     dag.DAG `json:"dag"`
    Dir     string  `json:"dir,omitempty"`
    Timeout int     `json:"timeout,omitempty"`
}
```

---

## Phase 4: Resilience

**Goal**: Automatic retry for transient API errors and low-latency slot notification
to replace the current 2-second polling loop in `WaitForSlot`.

### 4.1 -- Proxy Retry with Backoff

**Dependencies**: Phase 0.3 (required -- retry package).

**Deliverables**:

- `internal/proxy/proxy.go` -- modify `proxyHandler` to buffer the request body and
  retry on 429 and 5xx responses using `retry.Do`

```go
// RetryConfig is embedded in proxy.Config.
type RetryConfig struct {
    MaxRetries int           // default: 3
    BaseDelay  time.Duration // default: 1s
    MaxDelay   time.Duration // default: 30s
}
```

The proxy already sits between every Claude CLI instance and Z.AI. Adding retry
here is transparent to callers.

### 4.2 -- Slot Notification (replace 2s polling)

**Dependencies**: None (standalone).

**Deliverables**:

- `internal/slot/notify.go` -- Unix domain socket listener at `<subagentsDir>/.slot.sock`
- `internal/slot/notify_test.go`

`ReleaseSlot` writes a single byte to the socket. `WaitForSlot` blocks on accept
instead of `time.Sleep(2 * time.Second)`. The polling fallback is kept for portability
(systems where UDS is unavailable or the socket file is stale).

---

## Phase 5: Channels Integration

**Goal**: Forward lifecycle events (job done, failed, timeout) to an external
notification channel (webhook, future Slack/Telegram bridge).

**Dependencies**: Phase 0.1 (required -- event bus), Phase 2 (required -- event producers wired).

### 5.1 -- Channel Client

**Package**: `internal/channel/`

**Deliverables**:

- `internal/channel/client.go`
- `internal/channel/client_test.go`

```go
// Message is the payload sent to the external channel.
type Message struct {
    Type     string            `json:"type"`
    Content  string            `json:"content"`
    JobID    string            `json:"job_id,omitempty"`
    Metadata map[string]string `json:"metadata,omitempty"`
}

// Client sends messages to an external channel endpoint.
// Silent no-op when endpoint is unconfigured.
type Client struct { /* ... */ }

func NewClient(endpoint string) *Client
func (c *Client) Push(ctx context.Context, msg Message) error
```

Endpoint is read from `CLAUDE_CHANNEL_URL` environment variable.
When the variable is empty or unset, `Push` returns nil immediately.

### 5.2 -- Event-to-Channel Bridge

**Deliverables**:

- `internal/channel/bridge.go`
- `internal/channel/bridge_test.go`

```go
// Bridge subscribes to the event bus and forwards selected events
// (JobDone, JobFailed, JobTimeout, JobProgress) to the channel client.
type Bridge struct { /* ... */ }

func NewBridge(bus *event.Bus, client *Client) *Bridge
func (b *Bridge) Start(ctx context.Context)
```

---

## Phase 6: Smart Routing

**Goal**: Automatically select the cheapest model tier that can handle a given prompt,
reducing cost for simple tasks while preserving quality for complex ones.

**Dependencies**: Existing `config/provider.go` (model slot infrastructure already exists).

### 6.1 -- Complexity Estimator

**Package**: `internal/router/`

**Deliverables**:

- `internal/router/router.go`
- `internal/router/router_test.go`

```go
// Tier represents the estimated complexity of a prompt.
type Tier int

const (
    TierLight  Tier = iota // lint, format, simple edits
    TierMedium             // default -- most coding tasks
    TierHeavy              // refactor, debug, architecture
)

// Estimate analyzes the prompt text and optional hints to determine a tier.
// Keyword heuristics:
//   "lint", "format", "rename" -> Light
//   default -> Medium
//   "refactor", "debug", "architecture", "design" -> Heavy
func Estimate(prompt string, hints ...string) Tier
```

### 6.2 -- Integration

**Deliverables**:

- Wire router into `claude.Execute` and MCP tool handlers
- Add routing config to `glm.toml`:

```toml
[routing]
light  = "haiku"
medium = "sonnet"
heavy  = "opus"
```

- Add `--tier light|medium|heavy` CLI flag override to bypass auto-detection

---

## Dependency Graph

```text
Phase 0 (all parallel)
  0.1 Event Bus ──────────┬──> Phase 1.1 MCP ──> 1.2 + 1.3 ──> 1.4
  0.2 Artifact ───────────┼──> Phase 3.1 DAG ──> 3.2 + 3.3
  0.3 Retry ──────────────┼──> Phase 4.1 Proxy Retry
                          |
Phase 2 <─── 0.1 + 1.1   |
Phase 4.2 (standalone)    |
Phase 5 <─── 0.1 + Ph.2  |
Phase 6 (standalone)      |
```

## Items Parallelizable from Day One

The following packages have zero inter-dependencies and can be developed simultaneously:

- **0.1** `internal/event/` -- event bus
- **0.2** `internal/artifact/` -- artifact abstraction
- **0.3** `internal/retry/` -- retry with backoff
- **4.2** `internal/slot/notify.go` -- Unix socket slot notification
- **5.1** `internal/channel/` -- channel client (no bus dependency for the client itself)
- **6.1** `internal/router/` -- complexity estimator
