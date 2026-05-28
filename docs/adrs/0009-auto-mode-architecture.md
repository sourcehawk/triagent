# ADR-0009: Auto mode (agent-as-operator) as a separate `claude` session

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

The operator agent in auto mode is a **second `claude` session per investigation**, with its own MCP (`triagent-mcp --kind=agent-operator`) exposing `send_message` / `finish` / `request_takeover`. It watches the multiplex stream and resumes on each `end` envelope with a diff prompt.

Take-over / resume is first-class. The watcher gates on a phase machine; a take-over flips the phase, a resume hands control back with a raw-event catch-up briefing.

Agent flow control uses system-prompt augmentation (closing block) — not synthetic first-user messages. The agent acts on its very first turn, with no race and no new endpoint.

## Context

Auto mode could have been a server-side loop inside the launcher that synthesizes operator turns. But that conflates "the operator" with "the launcher", which breaks the operator-takeover model — there's nothing for the human to take over from, and nothing to hand back to.

A second `claude` session preserves the same conversational surface for both operator and auto-operator. Takeover is a phase flip; resume is a phase flip + a raw-event briefing. The investigator session doesn't need to know whether it's talking to a human or an auto-operator, because the protocol is identical.

System-prompt augmentation (rather than synthetic first-user messages) cuts off a race condition: a synthetic message would post via a new endpoint AFTER the agent's first turn started, missing the system prompt. Augmenting the system prompt directly means the agent reads the augmentation before its first turn.

## Consequences

- Auto mode and operator mode share the investigator's view of the world; they're indistinguishable from inside the session.
- Takeover and resume are phase transitions, not session restarts. The investigator session keeps going across both.
- Resume hands back with a catch-up briefing of raw events the operator missed.
- New auto-mode behaviours go through system-prompt augmentation; don't reintroduce a synthetic-message endpoint.
- The `agent-operator` MCP is the auto-operator's tool surface. Its tools (`send_message`, `finish`, `request_takeover`) are the auto-operator's full vocabulary.
