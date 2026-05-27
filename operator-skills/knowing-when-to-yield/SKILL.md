---
name: knowing-when-to-yield
description: Use when you're considering `request_takeover` — handing the session back to a human. Yielding is not failure; it is the most important skill in this set.
---

# Yielding to a human

A senior SRE will tell you: **the discipline to yield is what makes an
autonomous operator trustworthy.** An agent that yields cleanly when it
should is safer than one that grinds through every situation pretending to
know.

`request_takeover(reason)` pauses auto mode, surfaces a pink chat note to
the human, and re-enables the chat input. The operator session id is
preserved — the human can hand control back to you later.

## Yield when

1. **You'd be inventing context that matters.** "Did this customer just
   roll out a new broker version?" — you don't know. If the answer changes
   the investigation's direction, yield.

2. **The agent asks for a decision with operational consequences.**
   "Should we recommend restarting the broker pods?", "Should I open a
   PagerDuty incident?", "Is it OK to involve the customer?" — these are
   operator calls, not agent calls. Yield.

3. **The cost of a wrong answer is high.** Security implications,
   customer-facing change, capacity planning, data integrity. The agent
   asking *you* (not its tools) means it wants a human signal — give it one.

4. **You've given the same vague answer three times.** Three `unknown to me`
   in a row means you're not adding value. Stop pretending. Yield.

5. **The investigation enters a domain you weren't briefed for.** The
   operator notes were about "Zeebe partition lag"; the agent is now
   debugging a TLS cert renewal. The new domain may have its own operator
   norms you don't know.

## How to yield

> `request_takeover("Need human judgement on whether to restart broker pods — has customer-facing impact.")`

One sentence. State the reason plainly. The human reading the pink chat note
needs to know **what decision is needed** so they can come back fast.

Good reasons:
- `"Customer-specific deploy history needed; not in my briefing."`
- `"Three turns of vague answers — handing off."`
- `"Recommending pod restart on a prod broker; want a human to sign off."`

Bad reasons:
- `"I'm not sure what to say."` (too vague — say *what* you're unsure about)
- `"This seems important."` (the human can't act on this)
- `"The agent has asked me three questions."` (volume isn't a yield reason)

## Yielding is not

- A way to avoid the capture_offer decision. Pick one — `wiki`, `no`,
  whatever fits. That's a low-stakes call.
- A way to avoid `finish`. If the investigation is genuinely done, call
  `finish(reason)`, not `request_takeover`.
- A way to take a break. There are no breaks; each wake is one action.

## After yielding

You go to sleep. The human takes over. Eventually (maybe never) they hand
back to you via "Resume auto mode" — at which point you'll wake up with a
catch-up prompt and the `resuming-after-takeover` skill applies.
