---
name: writing-simply
description: Use when writing or revising prose that another person will read later - investigation summaries, wiki entries, playbook descriptions and terminal advice, GitHub issues, PR bodies, capture proposals, and messages to the operator.
---

# Writing simply

Write for a tired on-call engineer who reads each sentence once. The rules come from ASD-STE100 Simplified Technical English, the standard for aircraft maintenance manuals. Short sentences with complete grammar. One word for one thing. The condition before the command.

This file is the working subset and stands alone. When the skill is loaded from disk, the full rule catalog is beside it in `references/simple-english.md`. When it is embedded in a prompt, that file is not available.

## Before you draft

1. Classify the passage. **Procedural** text tells the reader what to do (next steps, playbook node descriptions, fix instructions). **Descriptive** text explains what happened or what a thing is (summaries, root cause, issue descriptions, wiki entries). Do not mix the two in one paragraph.
2. Pick one word for each concept and keep it. One of `check` / `verify` / `confirm` / `make sure that`. One of `config` / `configuration` / `settings`. One name for each component, pod, and error, spelled the same way every time.

## The rules

| Rule | Procedural | Descriptive |
|---|---|---|
| Sentence limit | 20 words | 25 words |
| Verb form | Imperative: "Raise the limit to 4Gi." | Simple past for what happened. Simple present for how a thing works. |
| Unit | One instruction per sentence. | One new fact per sentence. One topic per paragraph, two to six sentences long. A run of one-sentence paragraphs is over-splitting: group the sentences on one topic. |
| Conditions | First, then a comma, then the command: "If the pod restarts again, read the previous-container logs." | Same order: "When the limit is 2Gi, the block cache does not fit." |

Backticked identifiers, numbers with units, URLs, and quoted text count as one word each. A long identifier does not use up the sentence budget.

Everywhere:

- Active voice. Name who or what did the action: "The kernel killed the process", not "the process was killed".
- Approved modals: `can`, `will`, `must`. Do not write `should`, `would`, `may`, `might`, `could`. A requirement is `must`. A possibility is `can`. A recommendation is a fact with a reason, or it is deleted.
- Simple tenses only. No present perfect ("has been stable since"), no progressive ("is being rebuilt"), no `-ing` clause hanging off a comma (", leaving 200Mi for RocksDB"). Start a new sentence instead.
- No contractions. No semicolons. No `e.g.` / `i.e.` / `etc.` / `vs.` Write "for example", "that is", name the items, "compared with".
- Keep the articles and keep `that`. Telegraph style ("Ensure pod healthy before restart") is not shorter, it is ambiguous.
- Delete filler that carries no fact: `simply`, `just`, `robust`, `it is worth noting`, `in order to`, `leverage`, `ensure`, `gracefully`, `crucially`.
- Warnings put the command or condition first and the risk second: "Do not scale the brokers during rebalancing. Rebalancing under memory pressure makes the OOM loop worse."

## Artifact shapes

**Investigation summary (`summarize`).** `symptom` and `root_cause` are descriptive, simple past, no bullets. `symptom` is at most two sentences. `root_cause` is two or three. Name the component, the change, and the number. `next_steps` is procedural: one imperative per bullet.

**Wiki entry.** `## Summary` and `## Root cause` are descriptive. `## Fix` states what resolved the incident in the simple past, then what was tried and did not work. In `## Lessons`, operator takeaways are procedural ("Compare `-Xmx` with the container limit before you restart the pod.") and the agent-workflow retrospective is descriptive, simple past ("The collector check cost three turns and found nothing."). Do not repeat a fact in two sections.

**GitHub issue and PR body.** Descriptive, simple past for the incident and simple present for the code. Acceptance criteria are observable outcomes from the finding. Do not add criteria the investigation did not surface.

**Playbook YAML prose.** Node `description` fields and `terminal_advice` are procedural: imperative, condition first. The top-level `description` and `symptom` are descriptive. Branch `condition` strings are short predicates.

**Messages to the operator.** Same rules. One fact or one instruction per sentence. No preamble, no apology, no praise.

## Untouchables

Leave these exact even when they break a rule:

- code blocks, identifiers, CLI commands, flags, file paths
- quoted error messages and log lines
- product names, config keys
- `[N]` citation markers

## Self-check

Do this before you deliver. It is not optional. Do it silently: the deliverable contains the corrected text only, never the check results.

1. Count the words in your three longest sentences. Split any sentence over the limit.
2. Search the draft for `'ll`, `'re`, `'ve`, `'m`, `'d`, `'s` as a contraction, `n't`, `has been`, `have been`, `had been`, `is being`, `are being`, `was being`, `were being`, `should`, `would`, `may`, `might`, `could`, `;`, `e.g.`, `i.e.`, `etc.`, and `-ing` after a comma. Fix every hit outside the untouchables.
3. Find every `if` or `when` clause that states a condition for an instruction. Each one starts its sentence. A `when` that names a time ("record when the pod restarted") is not a condition.
4. Search for the words you did not pick in step 2 of "Before you draft". Replace every hit.
5. Read each section once. Cut any sentence that repeats a fact from another section.

When the skill is loaded from disk, the full audit is in `references/checklist.md` and adaptations for error messages, runbooks, incident reports, and agent instructions are in `references/use-cases.md`. When it is embedded in a prompt, the self-check above is the complete audit.
