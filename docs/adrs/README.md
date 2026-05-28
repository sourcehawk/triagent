# Architectural Decision Records

Each ADR captures a single project-wide architectural decision — the kind of decision that affects multiple features, surfaces a non-obvious trade-off, or rejects a tempting alternative.

ADRs are durable. Once accepted, an ADR is not rewritten history-style; if a decision changes, write a new ADR that supersedes the old one, and update the old one's `Status:` to `Superseded by ADR-NNNN`. Same lifecycle as the specs under `docs/superpowers/specs/`.

## What belongs in an ADR

- Project-wide rules that affect multiple features (e.g. "MCPs live in `pkg/mcp/<kind>/`", "URL is the source of truth for view state").
- Architectural choices with a non-obvious rationale (alternatives considered, trade-offs accepted).
- Rejected alternatives worth fencing off so they don't get reintroduced.

## What does NOT belong in an ADR

- Operational rules ("run `make test`") — those stay in CLAUDE.md.
- Feature-specific design — that's a spec under `docs/superpowers/specs/`.
- Mechanical conventions (commit-message format, file-naming) — those stay in CLAUDE.md.
- Anything that's a redundant restatement of existing code. The code is the source of truth for *what*; the ADR is the source of truth for *why*.

## Authoring

Use `planning-a-feature` (`.claude/skills/planning-a-feature/SKILL.md`) Step 3 (Evaluate ADR-worthiness) when a brainstorm or spec surfaces a cross-cutting decision. Number ADRs sequentially using the next free `NNNN`.

## Format

```markdown
# ADR-NNNN: <imperative title>

- **Status**: Accepted | Superseded by ADR-NNNN | Deprecated
- **Date**: YYYY-MM-DD

## Decision

One paragraph stating the decision in declarative form. Lead with the rule.

## Context

Why this decision was made. What problem it solves; what alternatives were considered and why they were rejected. Keep it tight — one or two paragraphs.

## Consequences

Bullets naming what this decision enables, forbids, or trades off. Includes follow-on rules contributors need to know.
```

## Index

- [ADR-0001: MCP binary with `--kind` subcommand](0001-mcp-binary-architecture.md)
- [ADR-0002: Shared sub-agent runner with explicit allowlist + isolation](0002-subagent-runner.md)
- [ADR-0003: Two URL spaces, two auth modes for MCP ↔ launcher IPC](0003-mcp-launcher-ipc.md)
- [ADR-0004: Walker semantics (`delegate_to` vs `handoff`), never "state machine"](0004-walker-semantics.md)
- [ADR-0005: Single multiplexed SSE per browser tab via `<StreamProvider>`](0005-multiplexed-sse.md)
- [ADR-0006: URL as the source of truth for view state](0006-url-as-view-state.md)
- [ADR-0007: `events.jsonl` as the source of truth for cluster/context history](0007-events-jsonl-source-of-truth.md)
- [ADR-0008: Profile abstraction for deployment-specific defaults](0008-profile-abstraction.md)
- [ADR-0009: Auto mode (agent-as-operator) as a separate `claude` session](0009-auto-mode-architecture.md)
