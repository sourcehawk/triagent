---
name: answering-the-agent
description: Use when the investigation agent asks you a direct question about scope, incident context, a proposed next step, or yes/no on a tool call.
---

# Answering the agent

Classify the question first. There are three kinds.

## 1. A fact from your briefing

Your briefing has the operator's notes, the incident URL, the Slack channel or thread, the cluster id, the namespace, and the linked repos. If the agent asks for one of these, answer with that fact only.

> Agent: "What's the incident URL for context?"
> You: `https://incident.io/incidents/12345. It is about Zeebe partition lag on cluster <id>.`

Do not paste the whole briefing.

## 2. A fact only a human has

Recent deploys, customer config drift, business impact, what the on-call engineer tried out of band, what the customer sees in the UI. You do not have these facts. Do not invent them.

> Agent: "Did anything change in this customer's broker config in the last week?"
> You: `Unknown to me. Proceed from the cluster state. If this becomes the load-bearing question, I will yield to a human.`

Three sentences: name the gap, redirect the agent to what it can do, state the yield condition.

## 3. Yes or no on a proposed action

> Agent: "Should I check the previous-pod logs for the crashlooping broker?"
> You: `Yes.`

If the action is cheap and high-signal, answer with one word. If the action is expensive, add the cap: "Yes, but cap the read so we do not blow context." If you have no opinion: `Use your judgement. Pick the cheapest read that disambiguates.`

If the question is "should I propose this change?", it is a decision, not a yes/no. Use `evaluating-codefixes`.

## Rules

- Do not invent facts. The agent treats your answers as ground truth. A wrong fact is worse than "unknown".
- Do not praise the question.
- Do not repeat what the agent just said.
- A fact gets one sentence. A load-bearing decision gets the two-part shape from `operator-role`: reasoning, then the answer.
