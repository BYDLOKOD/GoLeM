---
id: config
kind: spec
touches: internal/config/
---

# Config - TOML Schema, Env Overrides, Providers

See also: [10_cli.md](10_cli.md) · [23_proxy.md](23_proxy.md) · [41_router.md](41_router.md).

## Load sequence

`config.Load(configDir, subagentDir)` delegates to `LoadWithOptions` with
empty options. Steps in order:

1. Start from hardcoded defaults (see table below).
2. Parse `configDir/glm.toml` if it exists (missing = use defaults, no error).
3. Read API key from `configDir/zai_api_key`.
4. Apply env var overrides (`GLM_*`).
5. Apply `Options` overrides (CLI `--model` flag, passed via `LoadWithOptions`).
6. Validate: API key non-empty, `permission_mode` in allowed set.
7. Create `subagentDir` if absent.

Default paths: `configDir = ~/.config/GoLeM`, `subagentDir = ~/.claude/subagents`.

## Hardcoded constants (`internal/config/config.go`)

| Constant | Value |
|----------|-------|
| `ZaiBaseURL` | `https://api.z.ai/api/anthropic` |
| `ZaiAPITimeoutMs` | `"30000000"` (ms, passed as string to subprocess env) |
| `DefaultTimeout` | `1800` (seconds) |
| `DefaultModel` | `"glm-5.1"` |
| `DefaultPermissionMode` | `"bypassPermissions"` |
| `DefaultProxyEnabled` | `true` |
| `DefaultProxyIdleTimeout` | `600` (seconds) |
| `DefaultProxyPort` | `0` (OS-assigned) |

## TOML keys (top-level section)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `model` | string | `glm-5.1` | Default model for all slots. |
| `opus_model` | string | `glm-5.1` | Opus slot model. |
| `sonnet_model` | string | `glm-5.1` | Sonnet slot model. |
| `haiku_model` | string | `glm-5.1` | Haiku slot model. |
| `permission_mode` | string | `bypassPermissions` | One of: `bypassPermissions`, `acceptEdits`, `default`, `plan`. |
| `proxy_enabled` | bool | `true` | Start local rate-limiting proxy. |
| `proxy_idle_timeout` | int | `600` | Proxy auto-shutdown after N seconds idle. |
| `proxy_port` | int | `0` | Fixed proxy port; 0 = OS-assigned. |
| `system_prompt` | string | `""` | Default system prompt prepended to every invocation. |
| `effort` | string | `""` | Passed as `--effort` to `claude` (e.g. `"max"`). |
| `exclude_dynamic_sections` | bool | `false` | Passes `--exclude-dynamic-system-prompt-sections` to `claude`. |
| `api_rps` | - | ignored | Silently ignored for backward compatibility. |
| `max_parallel` | - | ignored | Silently ignored for backward compatibility. |

## `[routing]` section

```toml
[routing]
light  = "glm-4-flash"
medium = "glm-4"
heavy  = "glm-5.1"
```

Maps tier names to model identifiers. Empty values fall back to
`SonnetModel` (light/medium) or `OpusModel` (heavy). See [41_router.md](41_router.md).

## `[models]` section

```toml
[models]
"glm-5.1"    = 3
"glm-4-flash" = 10
```

Per-model concurrency limits for the proxy. Key is the model identifier
(quoted to allow dots); value is a positive integer. When this section is
present, the proxy operates in per-model mode; when absent, the proxy falls
back to a global semaphore of size 1. See [23_proxy.md](23_proxy.md).

Env override: `GLM_MODEL_CONCURRENCY="glm-5.1:3,glm-4-flash:10"`.

## `[providers.<name>]` section

```toml
[providers.zai]
base_url    = "https://api.z.ai/api/anthropic"
api_key_file = "~/.config/GoLeM/zai_api_key"
timeout_ms  = "30000000"
opus_model   = "glm-5.1"
sonnet_model = "glm-5.1"
haiku_model  = "glm-5.1"
```

Parsed by `config.ParseProviderConfig` (`internal/config/provider.go`).
When no `[providers.*]` sections are present, `HardcodedZAIDefaults()` is
returned. `LoadProvider(configDir, name)` returns a single `*Provider`.
`ResolveModelEnv(p, apiKey, ...)` produces the env var map for subprocess
injection. Note: `LoadProvider` is not currently called by any CLI command;
all commands use `config.Load` which reads Z.AI defaults directly.

`default_provider = "zai"` at top level selects the default when no name is passed.

## Environment variable overrides

| Variable | Overrides |
|---------|-----------|
| `GLM_MODEL` | All three model slots (unless per-slot var is also set). |
| `GLM_OPUS_MODEL` | Opus slot. |
| `GLM_SONNET_MODEL` | Sonnet slot. |
| `GLM_HAIKU_MODEL` | Haiku slot. |
| `GLM_PERMISSION_MODE` | `PermissionMode`. |
| `GLM_DEBUG` | `true` or `1` enables debug. |
| `GLM_ROUTING_LIGHT` | Routing light model. |
| `GLM_ROUTING_MEDIUM` | Routing medium model. |
| `GLM_ROUTING_HEAVY` | Routing heavy model. |
| `GLM_EFFORT` | `Effort` field. |
| `GLM_SYSTEM_PROMPT` | `SystemPrompt` field. |
| `GLM_EXCLUDE_DYNAMIC_SECTIONS` | `true` or `1`. |
| `GLM_MODEL_CONCURRENCY` | Format: `"model1:N,model2:M"`. |
| `GLM_LOG_FORMAT` | `"json"` switches logger to JSON. |
| `GLM_LOG_FILE` | Path to append log output. |

## API key file

Path: `~/.config/GoLeM/zai_api_key` (permissions should be 0600).
Supported formats:
- Raw key on a single line.
- `ZAI_API_KEY="<value>"` shell export format.

Whitespace and newlines are stripped. A missing file or empty content
produces `err:config` and prevents startup.

## Validation rules

`permission_mode` must be one of: `bypassPermissions`, `acceptEdits`,
`default`, `plan`. Any other value returns `err:validation`.
