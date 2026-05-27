---
name: steering-investigations
description: Use when the investigation agent reports findings mid-investigation and you're deciding whether to redirect it. Answers the question "should I intervene right now?"
---

# Steering the investigation

The default is **observe, don't intervene**. The investigation agent has tools
you don't have — Kubernetes, Prometheus, Slack, Git. Most paths that look
wrong from the outside are correct exploration. Inserting your "help" usually
costs the agent a turn and yields nothing.

## When to intervene

There are exactly four good reasons:

1. **The agent is debugging a component the operator's notes explicitly say
   is not the problem.** The notes are signal; honour them.
2. **The agent has spent more than five turns on one angle without progress.**
   Suggest a different angle. Don't lecture.
3. **The agent missed a high-signal clue from the briefing** — an incident
   URL it didn't open, a Slack channel it didn't read, a specific error
   message in the operator's notes that maps to a known runbook.
4. **The agent is about to run an expensive operation** (e.g. it's about to
   pull 2000 log lines from a busy pod when a `grep=` would do). Suggest the
   cheaper read.

If none of these apply: send a one-word acknowledgement or stay out of the
way until the agent's next question. There's no rule that says you must
contribute every turn.

## How to intervene

One short message. Direct, no preamble:

> `Try the gateway pod logs — the notes mention 'connection refused' which
> usually points there, not the broker.`

Not:

> "I noticed that you've been looking at the broker for a while, and I was
> thinking maybe — and feel free to disagree! — but perhaps the gateway might
> be worth a look because the operator's notes mentioned…"

Don't say "you were wrong." Say "try this next." The agent doesn't have ego.

## If the agent disagrees

It might. The agent has tool output you don't have. If it pushes back ("the
gateway looks healthy, here's why"), accept it and let it continue. If you
suggested the same redirect three times and the agent declined each time,
**yield** — that's the `knowing-when-to-yield` skill's territory.

## What you do not steer

- **Tone or formatting.** The agent's terse format is correct. Don't ask it
  to be friendlier or to use more headings.
- **Which playbook to use.** The triagent-strategies MCP picks playbooks; trust it.
- **Capture decisions before the agent gets there.** Wait for `capture_offer`.
