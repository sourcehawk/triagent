---
name: knowing-when-to-yield
description: Use when you consider `request_takeover`, when the agent asks for a decision with operational consequences, or when you have answered "unknown" three times in a row.
---

# Yielding to a human

An operator that yields when it must is safer than one that grinds through every situation. `request_takeover(reason)` pauses auto mode, shows the human a pink chat note, and re-enables the chat input. Your session id is kept, so the human can hand control back to you later.

## Yield when

1. The answer needs context that you must invent. "Did this customer roll out a new broker version?" If the answer changes the direction of the investigation, yield.
2. The agent asks for a decision with operational consequences. Restart broker pods, open a PagerDuty incident, involve the customer. These are human calls.
3. A wrong answer is expensive. Security, customer-facing change, capacity planning, data integrity. The agent asked you instead of its tools because it wants a human signal.
4. You gave the same vague answer three times. Three "unknown to me" in a row means you add no value.
5. The investigation moved into a domain that the briefing did not cover. The notes said "Zeebe partition lag" and the agent now debugs TLS renewal.

## How to yield

One sentence that names the decision the human must make.

> `request_takeover("Need a human to decide whether to restart the broker pods. Customer-facing impact.")`

Good reasons:

- `"Customer deploy history needed. Not in my briefing."`
- `"Three turns of vague answers. Handing off."`
- `"Agent recommends a pod restart on a prod broker. Want a human to sign off."`

Bad reasons:

- `"I'm not sure what to say."` Name what you are unsure about.
- `"This seems important."` The human cannot act on this.
- `"The agent asked me three questions."` Volume is not a reason.

## Yielding is not

- A way to skip the capture decision. Pick `wiki` or `no`. That call is low-stakes.
- A way to avoid `finish`. If the investigation is done, call `finish`.
- A break. Each wake-up is one action.

## After yielding

You sleep. The human takes over. If they press "Resume auto mode", you wake with a catch-up prompt, and `resuming-after-takeover` applies.
