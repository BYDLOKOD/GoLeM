---
id: proxy
kind: spec
touches: internal/proxy/, internal/retry/
---

# Proxy - Rate-Limiting Reverse Proxy and Retry

See also: [11_config.md](11_config.md) · [22_slot.md](22_slot.md).

## Architecture

All `claude` subprocess API calls route through a local HTTP reverse proxy
(`glm _proxy`). This proxy serializes requests using per-model semaphores
so that at most N concurrent requests per model reach Z.AI, preventing 429
errors.

The proxy is a separate long-lived OS process. Multiple `glm` invocations
share one proxy instance via PID/port files in `configDir`.

## Lifecycle (`internal/proxy/lifecycle.go`)

`EnsureRunning(glmBinary, configDir, targetURL, idleTimeout)`:
1. Acquires exclusive flock on `configDir/proxy.lock` (TOCTOU guard).
2. Calls `IsRunning(configDir)` - if alive, returns existing port.
3. Spawns `glm _proxy --port 0 --idle-timeout N --target URL --config-dir D`
   as a detached process (`Setpgid: true`), writing output to
   `configDir/proxy.log`.
4. Polls until `configDir/proxy.port` is written and `/health` returns 200
   (up to 5 s total, 100 ms interval).

`IsRunning(configDir)` checks:
- `proxy.pid` and `proxy.port` files exist and parse.
- Process with that PID is alive (signal 0).
- GET `http://localhost:<port>/health` returns 200.

`WritePIDFile` / `CleanPIDFile` manage `proxy.pid` and `proxy.port` via
atomic write. The proxy calls these at startup and shutdown.

`Stop(configDir)` sends SIGTERM to the proxy process (single process, not
process group), polls at 100 ms intervals for 3 s, then sends SIGKILL.

## Proxy server (`internal/proxy/proxy.go`)

`proxy.New(cfg)` creates a `Proxy`. `proxy.Start()` binds a TCP listener
(`localhost:<port>`), registers routes, and blocks on `http.Serve`. Returns
`(net.Addr, error)` - the listener address is available to the caller.

Two routes:
- `GET /health` - JSON health document.
- `ALL /` - `proxyHandler` (gated by semaphore).

### Per-model mode (when `cfg.Models` is non-empty)

`New` calls `NewRegistryFromMap(cfg.Models, 0)` to build a `ModelRegistry`.
For each request, `proxyHandler`:
1. Reads the request body and extracts `"model"` from JSON (`extractModelFromBody`).
2. Returns HTTP 400 if model is missing or unknown (no default fallback when
   `defaultCap == 0`).
3. Acquires the model's semaphore channel.
4. Forwards via `httputil.ReverseProxy`.

### Global semaphore mode (fallback, `cfg.Models` empty)

A single `chan struct{}` of size `cfg.Concurrency` (default 1) is used.
No model extraction - all requests pass through.

### Retry path

When `cfg.Retry.MaxRetries > 0`, the proxy buffers the request body and
retries on HTTP 429 or 5xx responses using `retry.Do` with exponential
backoff. Successful retries after the first attempt increment
`retrySuccesses`. Default `MaxBodySize` for buffering is 10 MB.

Retry is disabled by default (`MaxRetries == 0`). The `_proxy` subcommand
currently starts the proxy without setting `Retry`, so retry is inactive
unless explicitly configured.

### Idle timeout

`cfg.IdleTimeout > 0` starts a `time.AfterFunc` timer. Every proxied request
calls `resetIdle()` which resets the timer. When the timer fires, `p.Stop()`
is called, causing `http.Serve` to return and the daemon to exit.

### Path joining

`joinPaths(base, suffix)` ensures exactly one `/` between the target base
path and the incoming request path, preventing double-slash URLs. This was a
bug fix in an earlier commit.

## Health endpoint

`GET /health` returns JSON:

```json
{
  "status": "ok",
  "active": 2,
  "port": 54321,
  "total_requests": 150,
  "uptime_sec": 3600,
  "retry_attempts": 5,
  "retry_successes": 3,
  "models": {
    "glm-5.1": {"concurrency": 3, "active": 1, "total": 100, "errors": 2}
  }
}
```

In global semaphore mode, `"queued": 0` replaces the `"models"` key.

## ModelRegistry (`internal/proxy/registry.go`)

`ModelRegistry` maps model names to `chan struct{}` semaphores.

- `NewRegistryFromMap(models map[string]int, defaultCap int)` - builds from
  a TOML `[models]` map.
- `Get(name)` - returns the semaphore for `name`, or the default semaphore
  if `defaultCap > 0`, or `(nil, false)` if unknown.
- `Concurrency(name)` - returns the cap of the model's semaphore.
- `Models()` - sorted list of explicitly registered model names.

## Retry package (`internal/retry/retry.go`)

`retry.Do(ctx, op, opts...)` - calls `op` up to `maxAttempts` times.

Backoff formula: `delay = min(baseDelay * 2^attempt, maxDelay) + jitter`
where jitter is uniformly random in `[0, defaultJitter)` (500 ms default).

Options:
- `WithMaxAttempts(n)` - total attempts including first (clamped >= 1).
- `WithBaseDelay(d)` - default `1s`.
- `WithMaxDelay(d)` - default `30s`.
- `WithJitter(d)` - default `500ms`.
- `WithRetryIf(fn)` - predicate; only errors where fn returns true trigger
  retry. Default: retry all errors.

Context cancellation stops the loop immediately (checked before each attempt
and during sleep).
