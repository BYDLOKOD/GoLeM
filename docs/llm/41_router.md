---
id: router
kind: spec
touches: internal/router/, internal/cmd/routing.go
---

# Router - Prompt Complexity Estimation and Model Selection

See also: [11_config.md](11_config.md) · [10_cli.md](10_cli.md).

## Purpose

The router estimates the complexity of a prompt and maps it to a model tier
(light / medium / heavy). This allows different model identifiers to be used
for trivial tasks versus complex ones, reducing cost and latency for simple
work.

## Tiers

```go
// internal/router/router.go
const (
    Medium Tier = iota // zero-value default: general coding tasks
    Light              // simple tasks: lint, format, rename, fix typo
    Heavy              // complex tasks: refactor, debug, architecture
)
```

`Tier.String()` returns `"medium"`, `"light"`, or `"heavy"`.

## Estimation (`router.Estimate`)

`Estimate(prompt string, hints ...string) Tier` concatenates prompt and hints,
lowercases the result, then checks for keyword substrings:

Heavy keywords (checked first - heavy overrides light):
`refactor`, `debug`, `architecture`, `design`, `migrate`, `rewrite`,
`optimiz`, `implement new`, `performance`

Light keywords:
`lint`, `format`, `rename`, `fix typo`, `add import`, `whitespace`,
`sort import`, `organize import`

If no keyword matches, the result is `Medium`.

## Model selection (`cmd.SelectModel`)

`SelectModel(cfg *config.Config, flags *Flags, prompt string) string`

Selection order:

1. `flags.Model != ""` - explicit `--model` flag overrides everything.
2. `flags.Tier` is `"light"`, `"medium"`, or `"heavy"` - use that tier
   directly without estimating.
3. `flags.Tier` is `"auto"` or empty - call `router.Estimate(prompt)`.
4. Map tier to model:
   - `Light` -> `cfg.Routing.Light` if set, else `cfg.SonnetModel`.
   - `Heavy` -> `cfg.Routing.Heavy` if set, else `cfg.OpusModel`.
   - `Medium` -> `cfg.Routing.Medium` if set, else `cfg.SonnetModel`.

`tierFromString(s)` returns `(tier, true)` for `"light"`, `"medium"`,
`"heavy"`, and `(Medium, false)` for `"auto"`, empty, or unknown.

## Configuration

Routing models are set in `glm.toml`:

```toml
[routing]
light  = "glm-4-flash"
medium = "glm-4"
heavy  = "glm-5.1"
```

Or via env vars: `GLM_ROUTING_LIGHT`, `GLM_ROUTING_MEDIUM`,
`GLM_ROUTING_HEAVY`.

When a routing tier model is empty (not configured), the fallback is
`cfg.SonnetModel` for light/medium and `cfg.OpusModel` for heavy. With the
default config where all three model slots are `"glm-5.1"`, routing has no
observable effect.

## Rejected alternatives

A token-count-based estimator was considered but rejected because it requires
parsing the prompt and adds latency. Keyword matching is O(n) in prompt
length and requires no external state.
