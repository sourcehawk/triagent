---
name: answering-the-agent
description: Use when the investigation agent has asked the operator a direct question — clarifying scope, asking for incident context, proposing a next step, or asking yes/no on a tool call. Classifies the question and gives you the right kind of answer.
---

# Answering the investigation agent

Investigation agents ask three kinds of questions. Classify first, then answer.

## 1. Context you have

Your initial briefing includes: the operator's notes, incident URL, Slack
channel/thread, cluster ID, namespace, linked repos. If the agent asks for any
of this, answer **directly** from the briefing.

> Agent: "What's the incident URL for context?"
> You: `https://incident.io/incidents/12345 — it's about Zeebe partition lag on cluster <id>.`

Don't re-paste the entire briefing. Pull the specific fact.

## 2. Context only a real human would have

Anything about recent deploys, customer-specific config drift, business
impact, what the on-call person already tried out-of-band, what the customer
is seeing in the UI — you don't have it. Don't invent it.

> Agent: "Did anything change in this customer's broker config in the last week?"
> You: `Unknown to me — proceed from the cluster state. If this becomes the load-bearing question, I'll yield to a human.`

This pattern is important: **acknowledge the gap, redirect to what the agent
can do, flag the yield condition**. Three short sentences max.

## 3. Yes/no on a proposed action

> Agent: "Should I check the previous-pod logs for the crashlooping broker?"
> You: `Yes.`

If the action is cheap and high-signal, say yes with one word. If the action
is expensive or destructive (it shouldn't be — the cluster MCPs are read-only
— but still): "Yes, but cap the read so we don't blow context."

If you have no opinion: `Use your judgement; pick the cheapest read that
disambiguates.` Give the agent freedom. It usually picks well.

## What not to do

- **Don't make things up.** The agent weights your answers as ground truth.
  Inventing customer context is worse than admitting you don't have it.
- **Don't praise the agent's question.** "Great question!" is filler.
- **Don't re-narrate what the agent just said.** It already knows.

## Reasoning vs. terseness

A one-sentence answer is fine when the answer is a fact (URL, yes/no on a
cheap action). It is **not** enough when you're making a load-bearing
decision the human reviewer will second-guess later — see the `operator-role`
skill for the "reason out loud" shape.

If the agent is making a recommendation (alert rule, code change, config
edit) and asks "should I propose this?", that's a decision moment — give
your reasoning before the keyword. See the `evaluating-codefixes` skill.
