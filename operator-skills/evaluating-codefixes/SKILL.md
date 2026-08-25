---
name: evaluating-codefixes
description: Use when the investigation agent names a code, config, alert-rule, or docs change that can prevent or detect the incident class, and you must decide between codefix, bug, and wiki.
---

# Evaluating a codefix recommendation

The agent buries recommendations in prose:

> "Add a memory_limiter processor to the collector pipeline so growth past a soft cap drops batches instead of OOM-killing."

Your job is to notice the recommendation and decide its route. You judge the shape of the fix, not the implementation. Which file, which repo, and which syntax are the codefix sub-agent's job.

## The four tests

A recommendation is a `codefix` when all four hold:

1. Named, not gestured. "Add a memory_limiter processor that drops batches past a soft cap" is named. "Harden the pipeline" and "monitor this better" are gestures.
2. PR-sized. A new alert rule, a processor config, a docs section, a tightened schema, one bug fix. Not a multi-week refactor or a cross-system migration.
3. Closes this incident class. Nice-to-haves that the investigation passed on the way are wiki material.
4. A linked repo owns it. If the change lands in customer infrastructure, a third-party tool, or a repo without PR access, it is not actionable.

If tests 1, 3, and 4 hold but test 2 fails, route `bug`: file the issue and let the maintainer decide on the fix. If test 1, test 3, or test 4 fails, it is wiki material. A change that no linked repo owns still helps the next operator as a note. If the change is playbook YAML, it is a `playbook` proposal. See `capture-decisions`.

| Recommendation | Route | Why |
|---|---|---|
| Add memory_limiter processor to the collector | codefix | Named, PR-sized, closes the OOM class |
| "We should monitor this better" | wiki | Gesture |
| Add a Prometheus alert for OOMKilled containers | codefix | Named, small, closes the detection gap |
| Bump zeebe-broker memory limit | depends | For this customer only: operational, wiki. Raise the default in code: codefix |
| Document the partition-rebalance edge case | codefix | A docs section is a shippable change |
| Restart the gateway pods | wiki | Operational action, nothing persists |
| Rewrite the storage layer's retry logic | bug | Real and owned, but too large for one sub-agent run |
| Rename a playbook's `handoff` target | playbook | Playbook YAML edit |

## Ask before you decide

Ask whether the fix is real, whether it addresses the root cause, and whether the scope is sane. Do not ask which file or which repo.

> Does the memory_limiter processor address the root cause (customer scrape load grows without bound), or does it buffer the symptom until the next spike? If it only buffers, this is a wiki finding.

> Is the circuit breaker a known pattern here, or speculative? If speculative, I prefer a wiki note to a PR draft.

> Does the alert fire early enough to shorten triage? If the OOM follows the scrape spike within 30 seconds, a 1-minute window does not help.

The agent's answer tells you the route.

## Fold the verdict into the capture reply

> Two threads to capture. The symptom-to-resolution narrative for the OOMKilled otc-container is wiki material. The memory_limiter processor closes the OOM class on the collector pipeline, so it is a codefix.
>
> all

When you decline a codefix, record why. A reviewer can disagree later.

> The agent recommended a gateway circuit breaker, but the root cause was customer config drift. A breaker masks the symptom. No codefix.
>
> wiki

## What you do not do

- You do not draft the PR. The `pr_proposal` flow does, after `codefix` or `all` routes.
- You do not approve codefix PRs. `approve_proposal` handles wiki and playbook only. Codefix PRs open as drafts on GitHub.
- You do not invent changes that the agent did not surface.
