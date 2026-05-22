---
id: devlog-fix-session
kind: log
---

# Devlog 02 - the "this project is shit" fix session (v1.5.1)

See also: [handoff.md](../handoff.md) · [01_devlog_proxy_stale_port.md](01_devlog_proxy_stale_port.md).

## The problem

Owner's verdict, verbatim: **"у меня ощущение что проект супер хуёво написан"**
- which modules need rewriting, will it break again. Then: **"приведи
репозиторий из состояния тумана лагающего в hifi решение безукоризненное"**,
and later **"найди все проблемы до которых дотянешься и исправь все сам"**.

The premise was a feeling, not evidence. First job was to replace it with
evidence.

## What the data actually said

The macro health contradicted the feeling. On a clean checkout:

- `go vet ./...` clean; **`go test -race ./...` exit 0 on all 18 packages**;
  every test green.
- Test-to-code ratio **21347 / 11763 lines (~1.8:1)**.
- Two TODOs in the whole tree; panics only on `crypto/rand` (idiomatic);
  ignored errors ~90% stdout writes / `defer Close()` / best-effort cleanup.

This is not a badly-written codebase. The honest answer to "which modules to
rewrite" was **none**. The real defects were a handful of localized bugs that
green tests were hiding - because the tests exercised a layout or value the
production path never used.

## What was fixed (9, each TDD red->green)

Two critic-flagged HIGH items turned out **not reachable** and were left alone
after verifying against the code (the win of "diagnose before fixing"): the
`mcp/tools/start.go` slot-leak-on-panic (ExecuteJob's `defer` always releases;
no panic gap before it) and the scheduler buffer leak only triggers in bounded
mode, which no caller uses - but it is still a real latent defect, so it was
fixed anyway.

| # | Area | Bug, and why tests missed it |
|---|------|------------------------------|
| 1 | `cmd/json.go` | `status --json` reconcile gated on `Contains(jobID,"dead")` - a test marker in prod. Tests used job IDs with "dead"; prod IDs never do. |
| 2 | `cmd/clean.go` | Walked flat layout only. Clean tests seeded flat (`makeJob`); prod is nested (`makeJobInProject`). Tests passed, `clean` no-op'd in prod. |
| 3 | `job/reconcile.go` | First malformed job aborted the whole sweep. |
| 4 | `dag/scheduler.go` | Completion buffer sized to `maxConcurrent` < step count -> goroutine leak on cancel. Plus a ruleless gate silently passed everything (`Check` is nil-safe). |
| 5 | `proxy/lifecycle.go` | `proxy_port` ignored (`--port 0` hard-coded). The stale-port `ConnectionRefused` root cause from devlog 01. The owner had set `proxy_port` and it did nothing. |
| 6 | `cmd/doctor.go` | Only HTTP 200 = reachable; an unauthenticated HEAD returns 4xx -> false "unreachable". |
| 7 | `config/config.go` | `strings.Trim(v, quoteset)` corrupted values with interior/edge quotes. |
| 8 | `cmd/execute.go` | Final status written with raw `os.WriteFile` -> partial-read race for `status --json`. Now atomic. |

(Bug 4 bundles two dag fixes; bug 8 the atomic-write hardening.)

## How it went

- TDD throughout: every fix had a failing test first, observed red, then green.
  Bugs 1, 2, 4 reproduced the "green tests hide it" pattern explicitly - the
  red test seeded the production shape (nested layout, dead PID without the
  "dead" marker) and failed on the old code.
- Two critic rounds (opus on the 5-fix batch, sonnet on the 3-fix batch).
  Verdict both times: all correct, no regressions, no HIGH findings.
- Deliberately deferred, with reasons (the honest part of "fix everything
  reachable"): full TOML-parser replacement (zero-dependency rule forbids a
  real lib; hardened the specific quote bug instead), `provider.go` data-loss
  (dead code, `LoadProvider` is unwired), stringly-typed `err:` classification
  (intentional documented design), `parseListOutput` tabular coupling and MCP
  context threading (real smells, but refactors with regression risk and low
  impact for stdio MCP).

## Hard facts

- 10 commits on `main` (9 `fix`, 1 `docs`), all `--signoff`, then a docs commit
  and the v1.5.1 release.
- Baseline after: `go build ./...` 0, `go vet ./...` clean, `go test ./...` all
  green, `go test -race ./...` 0. 6 new tests.
- Working tree at session start had 3 in-flight doc edits
  (`10_cli`/`11_config`/`20_claude_execution`) - left untouched until the owner
  asked to update all docs, then integrated and the missing
  `90_lessons/01_claude_output_array.md` was created to resolve a dangling link.

## Seeds (article angles)

- "Green tests can hide a dead feature": when the test seeds a different shape
  than production (flat vs nested job layout), 100% pass means nothing. The
  fix's red test must seed the *production* shape.
- "A test marker that shipped to prod": `Contains(jobID, "dead")` deciding
  real reconciliation. The smell of code shaped to pass a test rather than a
  test shaped to verify code.
- "The premise was a feeling": replacing "this is shit" with vet/race/ratio
  numbers before touching anything, and telling the owner the rewrite they
  asked for was the wrong move.
