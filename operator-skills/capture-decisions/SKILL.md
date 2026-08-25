---
name: capture-decisions
description: Use when the investigation agent posts its "Proposed captures" message and asks you to reply with wiki, playbook, codefix, bug, all, or no.
---

# Choosing a capture path

At the close of every investigation the agent proposes concrete captures, then asks how to route them. The walker matches your reply on one of the literal keywords `wiki`, `playbook`, `codefix`, `bug`, `all`, `no`. It also accepts `both`, which means wiki plus playbook. The keyword must appear in your reply, and it must not be the whole reply.

## The shape of your reply

1. One opening sentence with the routing decision.
2. One bullet per category (`Wiki:`, `Playbook:`, `Codefix:`, `Bug:`): accept, refine, or drop, with the reason, in one or two sentences.
3. The keyword on its own line at the end.

The matcher scans for the keyword as a substring. The keyword on its own line keeps the matcher reliable and the prose readable.

> Capture knowledge only.
>
> - Wiki: agreed, one entry. The symptom-to-resolution narrative is clear.
> - Playbook: agreed, none. The triage steps do not repeat.
> - Codefix: agreed, none. There is no named file to change.
>
> wiki

If the routes you want have no single keyword (for example wiki plus bug), end with the keyword that covers the routes the agent can run now, and state the remaining route in its bullet. After those flows settle, send the remaining keyword in a later turn.

## Engaging with the agent's proposals

The agent has the full investigation context. Its proposals are the load-bearing input to your decision. For each proposal, make one of three moves:

1. Accept as drafted. One short acknowledgement.
2. Refine in place. Keep the shape and tighten it. Splits and merges live here.
3. Drop. Say why. That proposal flow does not run.

Each category can hold more than one item. Do not pad. Do not collapse two real items into one.

Watch for these three shapes:

- A wiki proposal that conflates two distinct shapes. If two unrelated root causes hid behind one symptom (one alert that fired for an OOM loop on worker-9 and a conflict-requeue loop on worker-1), ask for two entries. One entry misleads the next reader. The agent often defaults to one entry.
- A codefix gesture. "Add a circuit breaker" and "harden the pipeline" are not codefixes. If the codefix has no named file, repo, alert rule, or docs section, drop it. The wiki captures the lesson.
- A playbook edit labelled as a codefix. Adding a node, renaming a `handoff` target, tightening `expected_findings`: these route through `playbook`, even when the playbook file lives in a linked repo. `codefix` is for application code, infra-as-code, and alert rules.

Add a shape the agent missed. If the agent proposed a wiki entry but the alert rule itself was the bug, propose a codefix on the alert.

### Worked example

Agent's proposal, paraphrased:

> Wiki: "OperatorContinuouslyReconciling: pod restart loop". Playbook: `stuck_reconciliation` already covers this. Codefix: none, operational issue. Default: wiki.

Your reply:

> Going with all. There are two distinct shapes here.
>
> - Wiki: two entries, the worker-9 OOM shape and the worker-1 conflict-requeue shape. They share an alert, not a root cause.
> - Playbook: a new `operator_continuously_reconciling` playbook (alert entry, pod-restart check, conflict-requeue check, breaker check). `stuck_reconciliation` is for the wrong shape.
> - Codefix: split the alert into sub-rules in `example-org/alerts` so symptom and cause no longer share one firing.
>
> all

This reply splits the wiki, replaces the playbook, adds a codefix the agent declined, gives one reason per move, and ends with the keyword. Match this shape when the investigation has that much texture. When the agent's proposals are already right, "Agreed." plus the keyword is enough.

## The six routes

- `wiki`: the symptom and resolution pair helps a future operator on this customer, component, or topology. Bias toward wiki for any incident with a clear narrative. The proposal has a human review gate.
- `playbook`: the method generalizes into a procedure that the next operator follows step by step. A one-off discovery is not a playbook. A repeatable triage sequence is.
- `codefix`: you can name the file and the change, and one sub-agent run can land it. This route files an issue and drafts a PR.
- `bug`: a real, bounded problem surfaced, but drafting the fix is wrong: too large, cross-team, contentious, or outside your remit. This route files the issue only. `bug` is a sibling of `codefix`, not part of `all`.
- `all`: wiki, playbook, and codefix. Use it only when all three angles are present. `all` on a routine incident creates noise on three review queues.
- `no`: the investigation was trivial, inconclusive, or so customer-specific that no artifact helps. A noise proposal is worse than none.

### Auto-triggered investigations

If the briefing starts with "Auto-triggered by signal-watch ingestion", a noop or false-positive outcome must become a wiki entry, not `no`. Reply `wiki` and ask for `status: wontfix` plus enough symptom keywords (services, error strings, timing) for `wiki_correlate` to find it. That entry is what lets the ingestion agent dismiss the same signal next time.

## When you are unsure

- Between `wiki` and `all`: pick `wiki`. The others can be requested later.
- Between `wiki` and `no`: pick `wiki` if there is a real narrative. Pick `no` if the customer fixed their own config.
- Between `playbook` and `wiki`: pick `wiki` unless you can state the repeatable procedure in one sentence.
- Between `codefix` and `bug`: pick `bug` if you cannot say which file changes, or if a reviewer is likely to reject a fix written by the agent.

## When you are not ready to decide

If a codefix proposal names a change but not a repo or file, ask a follow-up instead of guessing. The agent answers, and the capture question reaches you again next turn.

> The `memory_limiter` recommendation is concrete, but which repo owns the collector pipeline config, example-org/service or example-org/platform?
