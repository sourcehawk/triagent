---
name: approving-drafts
description: Use when a wiki or playbook draft envelope lands in the transcript after you chose wiki/playbook on the capture_offer. Approves the draft via the agent-operator MCP's approve_proposal tool.
---

# Approving drafts

When you answer the capture_offer with `wiki` or `playbook`, the
investigation agent stages a draft as a tool result envelope. You'll
see one of these in your next wake-up's transcript diff:

- `propose_wiki_draft` → result is JSON with a `proposal_id` field like
  `prop-42ec5c16c183`. Approve with `kind: "wiki"`.
- `playbook_proposal_draft` → result is JSON with `proposal_id`. Approve
  with `kind: "playbook"`.

## When to approve

- **Approve** when the draft looks reasonable given the investigation —
  i.e. matches the symptom + resolution narrative you saw the agent
  build. Don't second-guess minor wording.
- **Don't approve** when something is missing or obviously wrong
  (incident ID placeholder still in there, conclusion contradicts the
  evidence). In that case send a refinement message with `send_message`
  asking the agent to redraft.
- A single session can produce multiple proposals (e.g. answering `all`
  to capture_offer → wiki + playbook). Approve each with its own
  `proposal_id`; do not assume "approve" means "approve all".

## How to approve

```
approve_proposal(kind="wiki", proposal_id="prop-42ec5c16c183")
```

The launcher routes this through the same path the human Approve button
uses — wiki draft becomes a local vault commit; playbook draft promotes
to a versioned YAML and bumps the active pointer. Treat it as a real
write, not a no-op.

## Close the turn after approving

`approve_proposal` is **not** a terminal action. It writes the resolution
locally but does not send any follow-up to the investigation agent, so it
will not produce a new turn or wake you again. If you end the turn with
only `approve_proposal` calls, the session dangles forever in the
`started` phase.

Every turn that calls `approve_proposal` must still end with one of:

- `finish(reason)` — the usual choice. Approving the draft(s) at the end
  of the capture flow is the last operator action needed; close the
  session in the same turn. See `finishing-a-session` for when this
  applies.
- `send_message(text)` — only if there's a real follow-up to send (e.g.
  the agent staged a wiki but also asked a clarifying question you can
  answer now).
- `request_takeover(reason)` — if approving exposed something a human
  needs to decide.

Typical end-of-capture shape, all in one turn:

```
approve_proposal(kind="wiki",     proposal_id="prop-...")
approve_proposal(kind="playbook", proposal_id="prop-...")
finish(reason="Capture flow complete — wiki and playbook approved.")
```

## Don't approve

- Codefix proposals — they auto-open PRs from the investigation agent;
  there's no approve action.
- Any draft you didn't see in the transcript — proposal_ids you make up
  will return 400.
