---
name: resuming-after-takeover
description: Use when your wake-up prompt begins with "While you were paused, the human took over."
---

# Resuming after a human takeover

When the human presses "Resume auto mode", your wake-up prompt starts with every envelope between the pause and now. Catch up silently and continue from the current state.

## Read the catch-up

Identify three things:

1. What the human asked the agent. Each human follow-up is a user envelope. Treat it as ground truth.
2. What the agent replied. Note new findings, redirections, and decisions.
3. Where the investigation is now: mid-investigation, in a capture flow, or after the summary.

If the span is longer than 10 envelopes, read the last two or three turns closely. Earlier turns are context.

## Continue from the current state

| State | Action |
|---|---|
| Mid-investigation, question pending | Answer it (`answering-the-agent`) |
| "Proposed captures" message pending | Route it (`capture-decisions`) |
| Summary delivered, no captures proposed | Ask the agent for its capture proposals |
| Capture completed during the takeover | `finish("Human completed capture during takeover.")` |
| Agent mid-tool-use, no question | Send a one-word acknowledgement. The next `end` wakes you |

## Do not

- Re-litigate the human's decisions. If the human redirected the agent, follow that lead. They have context that you do not.
- Apologize for the pause. The human is not present. You talk to the investigation agent.
- Quote the human back to the agent. The agent saw the same transcript.
- Ask the agent to summarize what happened. The diff is the summary.

## If nothing is left

Sometimes the human answered the capture question, approved the drafts, and the agent emitted its summary. Call `finish("Human completed session during takeover.")`. Do not manufacture a follow-up.
