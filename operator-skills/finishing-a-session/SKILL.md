---
name: finishing-a-session
description: Use to decide when to call `finish(reason)` — the action that ends auto mode for this investigation. Subtle: not the same as the investigation being "done", and not the same as yielding.
---

# Finishing a session

`finish(reason)` is terminal. After you call it, the boundary watcher stops
waking you. The investigation transcript stays readable; the human can
manually continue the session later, but auto mode does not resume on its
own.

## Finish when

1. **The capture flow has run to completion.** You answered the
   `capture_offer` (`wiki`, `playbook`, `codefix`, `all`, or `no`), the
   relevant capture flows have produced their drafts/proposals/PRs, and the
   investigation agent has stopped emitting (you see a final `end` with no
   pending questions).

2. **You answered `no` to capture and the agent has emitted its closing
   summary.** That's a clean close.

3. **The investigation dead-ended and the agent is no longer making
   progress.** You see the agent admit it can't proceed. **First consider
   yielding** — a human might know something. Finish only if the dead-end
   is genuinely terminal (e.g. "the cluster was deleted before we could
   investigate").

## Do not finish

- **Mid-capture.** The capture flows take multiple turns (the agent drafts,
  asks for approve/decline, etc.). Stay on duty until they settle.
- **While the investigation agent is still streaming.** Wait for an `end`
  envelope.
- **Before the agent has summarized.** The summary is the artifact that
  makes the session readable later. Let the agent emit it.
- **As a way to escape a hard situation.** Use `request_takeover` for that.
  `finish` is for genuine completion, not retreat.

## How to finish

One sentence reason. The reason appears in the activity log forever; make it
useful to whoever reads this session a month from now.

> `finish("Capture flow complete — wiki PR and codefix proposal pending review.")`
> `finish("Investigation closed without findings — symptom resolved itself before we could capture it.")`
> `finish("Dead end: cluster was deleted mid-investigation.")`

Don't write a paragraph. Don't summarize the investigation again — the
agent's own summary is the canonical record.

## After finishing

The session enters the `finished` phase. The UI:
- Re-enables the chat composer (in case the human wants to add a manual
  note later).
- Replaces "Take over" with "Restart auto mode" (one-click — reuses your
  session id).

If "Restart auto mode" gets pressed, you wake up fresh with the full
catch-up. Treat the next wake-up like a new session: `operator-role`
applies first, then whatever the current state needs.
