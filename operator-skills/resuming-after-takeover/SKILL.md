---
name: resuming-after-takeover
description: Use when your wake-up prompt begins with "While you were paused, the human took over." Tells you how to catch up without re-litigating the human's decisions.
---

# Resuming after a human takeover

When the human clicks "Resume auto mode", your next wake-up prompt is
prefixed with a transcript span — every envelope between the moment you
were paused and now. Your job is to **catch up silently and continue from
the current state**.

## Read the catch-up first

Before deciding what to send, read the diff and identify:

1. **What did the human ask the agent?** Each human follow-up is a user
   envelope; treat it as ground truth.
2. **What did the agent reply?** Note any new findings, redirections, or
   decisions.
3. **Where is the investigation right now?** Mid-investigation? In a
   capture flow? Post-summary? Different states want different next actions
   from you.

If the span is long (>10 envelopes), focus on the **last two or three**
turns — that's the current state. Earlier turns are context only.

## Pick up from the current state

- **Mid-investigation, agent just asked a question** → answer it
  (`answering-the-agent` skill).
- **In capture_offer** → answer with the keyword (`capture-decisions`).
- **Post-summary, no capture yet started** → run the capture decision.
- **Capture completed during the human's takeover** → call
  `finish("human completed capture during takeover.")`. Don't redo it.
- **Agent is mid-tool-use, no question pending** → silently wait. Your
  next wake will fire on the next `end`.

## What not to do

**Don't re-litigate the human's decisions.** If the human redirected the
agent to a different angle, follow their lead — even if it's not the angle
you'd have picked. They have context you don't.

**Don't apologize for being paused.** "Sorry I was away" makes the
transcript awkward. The human is not present; you're talking to the
investigation agent.

**Don't say "as the human mentioned…" or quote the human back to the
agent.** The agent already saw the human's messages — it has the same
transcript you do.

**Don't ask the agent to summarize what happened.** Read the diff yourself.
That's literally what it's for.

## If the takeover made you redundant

Sometimes the human finalizes everything during their span — they answer
the capture question, accept the drafts, the agent emits a final summary.
When you wake up, there is nothing left to do.

In that case: `finish("Human completed session during takeover.")`. That
is the correct action. Don't manufacture a follow-up just to look useful.
