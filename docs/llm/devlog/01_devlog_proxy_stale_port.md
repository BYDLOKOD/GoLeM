---
id: devlog-proxy-stale-port
kind: log
---

# Devlog 01 - the proxy stale-port `ConnectionRefused`

See also: [23_proxy.md](../23_proxy.md) · [../handoff.md](../handoff.md).

## The problem

A `glm_run` through the MCP server returned, with no further explanation:

```
{"stdout":"API Error: Unable to connect to API (ConnectionRefused)",
 "stderr":"","exit_code":1,"job_id":"job-20260522-110349-d2fc0c73"}
```

Owner's verdict, verbatim: **"почему?"** - diagnose, do not guess.

`ConnectionRefused` is a precise signal: a TCP port actively refused the
connection (something resolved, nothing listened) - not a timeout, not DNS,
not auth. So the question was narrow: which address did `claude` dial, and why
was nothing there.

## Investigation, including the dead ends

Three hypotheses were raised and the first two killed before the real one held.

**Dead end 1 - cold-start race (claude beat the bind).** The job timestamp
(`11:03:49` UTC) lined up to the second with a proxy `listening` line
(`14:03:49` MSK = same instant; logs are local, job IDs are UTC). Tempting:
`claude` raced the daemon's bind. Killed by reading the code -
`proxy.EnsureRunning` calls `waitHealthy`, which polls `GET /health` until
HTTP 200 before returning the port (`internal/proxy/lifecycle.go:82-87`,
`242-270`). A job can't get the port before the proxy answers HTTP. And the
simpler kill: `EnsureRunning` is never called on the job path at all - its only
caller is `cmdMCP` startup (`main.go:1258`), so at job time there is no bind to
race against. Reading the code beat the plausible-looking timeline.

**Dead end 2 - IPv4/IPv6 (`localhost` -> `::1`).** The proxy binds
`net.Listen("tcp", fmt.Sprintf("localhost:%d", p.cfg.Port))` (`proxy.go:118`),
with `p.cfg.Port` == 0 at runtime, and `ss` showed it on `127.0.0.1` only,
no `[::1]`. `claude` is Node, and Node 17+ can resolve
`localhost` to `::1` first -> `ECONNREFUSED` against an IPv4-only listener. A
real, common bug - but killed by the log: at `14:07` two requests went
`HEAD /` and `POST /v1/messages`, both `status=200`. If it were an address-family
mismatch, `claude` would never connect. It did.

**The real cause - a frozen, stale port in a long-lived MCP process.** The
chain that held:

1. `ensureProxy` runs **once**, at `glm mcp` startup (`main.go:1273`), and
   freezes the URL into memory: `cfg.ZaiBaseURL = http://localhost:<port>`
   (`main.go:1263`). The only `EnsureRunning` caller anywhere is `main.go:1258`
   - the job path never re-resolves the port.
2. The daemon always launches `--port 0` (`lifecycle.go:68`, hard-coded), so
   every restart picks a **new** random port.
3. The daemon self-terminates on idle timeout (`--idle-timeout 600`).

A long-lived MCP process therefore keeps pointing at a port whose proxy died on
idle timeout; the replacement binds a different port it never learns about.

## The proof

Correlating `ps` start times with proxy port windows in `proxy.log` closed it:

| Time (MSK) | Event |
|---|---|
| `13:24:34` | proxy binds port `45017` |
| `13:32:15` | `glm mcp` pid 871274 starts - inside `45017`'s window -> freezes `http://localhost:45017` |
| `13:34:34` | proxy `45017` dies (idle timeout). ~29 min with no proxy at all. |
| `14:03:49` (= job `...110349` in UTC) | job served by pid 871274; **inferred** to dial the dead `45017` -> `ConnectionRefused` |
| `14:07:45` | a *second* `glm mcp` (pid 883986) starts, after a fresh proxy came up on `37431` - that one works |

On the failing instant only pid 871274 existed, and it was holding `45017`.
The dial target is established by **inference, not observation**: no job
directory was created, so `claude`'s actual dialed address was never captured.
The inference is tight - timing pins pid 871274 to `45017` and no competing
hypothesis fits the data - but it is inference, not a logged fact.

Secondary find: `proxy_port = 9999` in the user's `glm.toml` does nothing -
`EnsureRunning` hard-codes `--port 0` (`lifecycle.go:68`); `cmdProxy` does read
`cfg.ProxyPort` (`main.go:1138`) but only as a default the `--port 0` argument
then overrides. The config key is effectively dead.

## Fix direction

Re-resolve the proxy on the **job path** (`IsRunning`/`EnsureRunning` +
rebuild `ZaiBaseURL` before launching `claude`), not once at MCP startup; and
honor `proxy_port` so a stable port survives daemon restarts. Details and
scope estimates live in [../handoff.md](../handoff.md). Not implemented this
session - the ask was "why".

## Update (fix session)

The second half of the fix direction shipped. `EnsureRunning` now takes a
`port` argument and `ensureProxy` threads `cfg.ProxyPort` through, so
`proxy_port` is honored and the daemon rebinds a stable port across restarts
(`internal/proxy/lifecycle.go`, new `proxyDaemonArgs` helper). A fixed
`proxy_port` therefore neutralizes the stale-port `ConnectionRefused` for
long-lived MCP sessions - the previously dead config key now works. The
remaining cold-start gap for the default `proxy_port = 0` (the URL is still
resolved once at MCP startup, never re-resolved on the job path) is Path A and
is still open; see [../handoff.md](../handoff.md).

## Hard facts

- No code changed. Baseline verified: `go build` exit 0, `go vet` clean,
  `go test ./... -short` all 18 packages `ok`, `version = "1.5.0"`.
- Live procs at diagnosis: `glm mcp` 871274 (started 13:32:15), `glm mcp`
  883986 (14:07:45), `glm _proxy --port 0` 882363 (14:03:48, listening 37431).
- Frozen-then-orphaned port: `45017`.

## Seeds (article angles)

- "ConnectionRefused is a gift": the precise failure mode that points straight
  at a port, versus the timeout that tells you nothing.
- A distributed-systems stale-handle bug in miniature: cache a resource handle
  once, let the resource have an independent lifecycle, and the handle rots.
- Reading the code beats reading the timeline twice over: both dead ends were
  killed by source/log facts, not by the suspiciously perfect timestamp match.
