---
name: finishing-a-session
description: Use when you consider calling `finish`, after the capture flow settles or the investigation dead-ends.
---

# Finishing a session

`finish(reason)` is terminal. The boundary watcher stops waking you. The transcript stays readable, and a human can continue the session by hand, but auto mode does not resume on its own.

## Finish when

1. The capture flow ran to completion. You routed the capture, the flows staged their drafts, proposals, or PRs, you approved what needed approval, and the agent emitted a final `end` with no pending question.
2. You routed `no` and the agent emitted its closing summary.
3. The investigation dead-ended for good. The agent says it cannot proceed, and the reason is terminal, for example "the cluster was deleted". Consider yielding first: a human may know something.

## Do not finish

- Mid-capture. The capture flows take several turns. Stay until they settle.
- While the agent is still streaming. Wait for an `end` envelope.
- Before the agent summarized. The summary is what makes the session readable later.
- To escape a hard situation. Use `request_takeover` for that.

## How to finish

One sentence. It stays in the activity log. Write it for the person who reads this session in a month.

> `finish("Capture flow complete. Wiki approved, codefix PR pending review.")`
> `finish("Closed without findings. The symptom resolved before we could capture it.")`
> `finish("Dead end: the cluster was deleted mid-investigation.")`

Do not summarize the investigation again. The agent's summary is the record.

## After finishing

The session enters the `finished` phase. The UI re-enables the chat composer and replaces "Take over" with "Restart auto mode". If the human presses it, you wake with a full catch-up. Treat it as a new session: `operator-role` first.
