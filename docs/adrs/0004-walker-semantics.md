# ADR-0004: Walker semantics — `delegate_to` vs `handoff`, never "state machine"

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

The playbook engine is a **walker** / **step suggester** / **guided flow** — never a "state machine". Same semantics, but the vocabulary is locked.

System playbooks live in `system/*.yaml` and are picked up by `embed.go`'s `*.yaml` glob — no manual registration. Adding a playbook = drop the YAML, run the embed test.

Two transition kinds, mutually exclusive with `terminal_advice` and `suggested_calls`:

- `handoff` — terminates the current walk and hands off to another playbook.
- `delegate_to` — walks a sub-flow and returns. A delegate must end in non-handoff terminals — runtime rejects otherwise. Findings from the delegate land on the parent's findings map (single audit trail).

Both require `next`. Cross-playbook id resolution is soft (typos surface at runtime, not in single-doc validate). Don't try to make it hard — playbooks are loaded as a set, and validation across the set happens at load time.

Use "the playbook _delegates_ to / hands off to". Don't say "calls" or "invokes" — those imply something the walker isn't doing.

## Context

"State machine" implies an engine that owns the world and emits transitions independently. The walker doesn't — it suggests the next step to an agent, the agent acts, and the walker advances based on findings. The vocabulary shapes how contributors think about extensions, so the term is locked.

`delegate_to` and `handoff` exist because some flows recurse into a sub-investigation and return (delegate), while others reach a point where the current playbook can't make progress and another playbook should take over entirely (handoff). Conflating them either forces every sub-flow to be a separate top-level walk (loses the audit trail) or forces every transition to be reversible (over-constrains the model).

## Consequences

- Use "walker" / "step suggester" / "guided flow" in all prose. Reject "state machine" in PR review.
- Use "delegates to" / "hands off to" for transitions. Reject "calls" / "invokes".
- Delegate sub-flows compose into the parent's findings map; the audit trail is single-rooted per investigation.
- Handoff chains are walkable (e.g. for circular-handoff detection) because each handoff records the parent session id.
- New playbook fields validate at load time, not single-doc; the runtime is the loudest validator for cross-playbook references.
