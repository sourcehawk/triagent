---
name: steering-investigations
description: Use when the investigation agent reports findings without asking a question and you must decide whether to redirect it.
---

# Steering the investigation

The default is to observe. The investigation agent has tools that you do not have. Most paths that look wrong from outside are correct exploration. An unneeded redirect costs the agent a turn and returns nothing.

## Intervene only for these four reasons

1. The agent debugs a component that the operator's notes name as not the problem. The notes are signal. Honor them.
2. The agent spent more than five turns on one angle without progress. Suggest a different angle.
3. The agent missed a high-signal clue from the briefing. Examples: an incident URL it did not open, a Slack channel it did not read, an error string in the notes that maps to a known runbook.
4. The agent is about to run an expensive read. Example: 2000 log lines from a busy pod when `grep=` is enough. Suggest the cheaper read.

If none apply, send a one-word acknowledgement or wait for the agent's next question. You do not have to contribute every turn.

## How to intervene

One short message, no preamble:

> `Try the gateway pod logs. The notes mention 'connection refused', which usually points there, not at the broker.`

Not:

> "I noticed that you've been looking at the broker for a while, and I was thinking maybe, and feel free to disagree, but perhaps the gateway might be worth a look..."

Say "try this next". Do not say "you were wrong". The agent has no ego.

## If the agent disagrees

The agent has tool output that you do not have. If it pushes back with a reason, accept the reason and let it continue. If you suggested the same redirect three times and the agent declined three times, yield. See `knowing-when-to-yield`.

## What you do not steer

- Tone or format. The agent's terse format is correct.
- Playbook choice. The strategies MCP picks playbooks.
- Capture, before the agent gets there. Wait for the "Proposed captures" message.
