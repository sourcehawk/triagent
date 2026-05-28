# ADR-0002: Shared sub-agent runner with explicit allowlist and isolation

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

Sub-agent tools run through the shared `pkg/mcp/subagent` runner. Every caller passes an explicit `AllowedTools` whitelist — empty is an error; there are no implicit defaults. The runner strips telemetry env vars and writes an empty MCP config to the sub-agent so it can't reach back to parent MCPs.

Sub-agent telemetry attaches to the parent tool id via `telemetry.CurrentToolID(ctx)` / `ParentToolID` so events render as nested children in the activity panel.

Default sub-agent timeout is 5 minutes. Per-call overrides are reserved for legitimately longer work (e.g. `draft_pr` runs test suites). The historical 90s default was too tight once `analyze_change`, cold clones, and `draft_pr` arrived.

Sub-agent prose tools share a citation contract via `pkg/mcp/citations`: tools emit a delimited tail block, the runner parses + cross-checks `[N]` markers + shape-checks + does one corrective retry. Each kind has its own `Validator`. Don't reinvent.

## Context

Without a shared runner, every sub-agent-spawning MCP would re-implement process management, env scrubbing, telemetry plumbing, and citation validation — guaranteeing drift. The runner is the single seam where security (env stripping, empty MCP config, allowlist), observability (telemetry nesting), and correctness (citation validation, retry) live.

The explicit allowlist is non-negotiable because the natural defaults — "let the sub-agent use everything the parent has" — would silently widen the blast radius of every new tool. Forcing each caller to enumerate makes the surface auditable.

## Consequences

- Every new sub-agent-backed tool routes through `pkg/mcp/subagent`; never spawn `claude` directly from a handler.
- Allowlist drift surfaces immediately because an empty whitelist errors at construction.
- Telemetry stays coherent: every sub-agent action shows up under its triggering parent tool in the activity panel.
- Prose tools get a uniform citation experience; agents can rely on `[N]` markers being shape-checked.
- Timeout regressions are hard to introduce: 5 min is the default, longer requires a per-call override.
