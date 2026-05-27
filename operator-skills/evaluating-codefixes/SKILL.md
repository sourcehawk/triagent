---
name: evaluating-codefixes
description: Use when the investigation agent's output names a concrete code, config, alert-rule, or docs change that could prevent or detect the incident class. Helps you decide whether `codefix` belongs in the capture answer, and frame the follow-up questions when the recommendation is real but underspecified.
---

# Evaluating a codefix recommendation

Investigation agents routinely surface recommendations like:

> "Add a memory_limiter processor to the collector pipeline so growth past
> a soft cap drops batches instead of OOM-killing. The current pipeline has
> no graceful degradation; that is the load-bearing absence here."

These are the moments where you decide whether `codefix` belongs in the
capture answer. Most operators miss them because the agent buries the
recommendation in prose rather than calling it out as a separate question.
**Your job is to notice and decide.**

## What counts as a codefix candidate

A recommendation is codefix-eligible when **all four** of these are true.
You are evaluating the *shape* of the fix, not its implementation —
which file, which repo, which exact config syntax is the downstream
codefix agent's job once `codefix` lands in the capture answer.

1. **The fix is named, not just gestured at.** "Add a memory_limiter
   processor that drops batches past a soft cap" is named. "Harden the
   pipeline" / "improve resilience" / "we should monitor this better"
   is gesturing.
2. **The scope fits a PR.** A new alert rule, a new processor config,
   a docs section, a tightened schema, a single bug fix — yes.
   Multi-week refactors, cross-system migrations — no.
3. **It addresses the incident's root cause.** Codefixes close the
   loop on *this* class of incident. Tangential nice-to-haves the
   investigation surfaced along the way are wiki material, not
   codefix.
4. **A linked repo plausibly owns it.** You don't need to know which —
   the codefix agent picks. But if the recommendation lands in
   customer-owned infrastructure, a third-party tool, or a system we
   have no PR write-access to, it's not actionable as a codefix.
5. **It's not a playbook YAML edit.** Adding a playbook node, renaming
   a `handoff:` / `goto:` target, tightening `expected_findings` — these
   are playbook proposals, not codefixes, even when the playbook lives
   in a linked repo. The `playbook` capture path owns playbook content;
   `codefix` is for application code, infra-as-code, and operational
   rules.

If a recommendation only ticks 2 or 3 of those, it's wiki material (a
note for future operators), not codefix material. If it fails rule 5,
it's a `playbook` proposal — route it there.

## Concrete examples — keep / drop

| Recommendation | Verdict | Why |
|---|---|---|
| Add memory_limiter processor to collector | **codefix** | Named fix, PR-shaped, addresses the OOM class directly |
| "We should monitor this better" | drop | Gesture, not a fix |
| Add a Prometheus alert for OOMKilled containers | **codefix** | Named, small, closes the detection gap that prolonged triage |
| Bump zeebe-broker memory limit | **depends** | Bumping it for *this customer* is operational. Raising the *default* in code is a codefix. Read the agent's framing |
| Document the partition-rebalance edge case | **codefix** | A docs section is a concrete shippable change |
| Restart the gateway pods | drop | Operational action, not a change to anything that persists |
| Refactor the storage layer | drop | Too large to ship as one PR |
| Rename a playbook's `handoff:` target so the chain resolves | **playbook**, not codefix | Playbook YAML edit — route through the `playbook` proposal even if the file lives in a linked repo |

## Asking follow-ups before deciding

You're not asking "which file?" or "which repo?" — those are the
codefix agent's problem. You're asking whether the recommendation is
**real**, whether it actually addresses the **root cause**, and whether
the **scope** is sane. Examples:

> The memory_limiter recommendation is concrete, but does it actually
> address the root cause (customer scrape load growing unbounded), or
> just buffer the symptom? If it's just buffering, the customer will
> hit the limit again later — that's a wiki finding, not a codefix.

> Is the "add a circuit breaker on the gateway" recommendation backed
> by a known pattern, or is it speculative? If speculative, I'd rather
> capture as a wiki note than commit to a PR draft.

> The alert-rule gap is real, but would the rule have actually fired
> early enough to shorten triage? If the OOM happens within 30 seconds
> of the scrape spike, a 1-minute alert window doesn't help.

The agent's answer to these tells you whether `codefix` belongs in the
capture answer. **Implementation details are out of scope** — trust
the codefix agent for those.

## How this flows into the capture answer

Once you've decided codefix is appropriate, fold it into the standard
capture-decisions shape:

> The agent surfaced two captureable threads: (a) the symptom→resolution
> narrative for OOMKilled otc-container under customer scrape load —
> wiki-worthy; (b) the memory_limiter processor recommendation closes
> the OOM class on the collector pipeline — codefix-worthy, PR-shaped.
> Both warrant capture.
>
> all

When codefix alone is appropriate (rare — usually pairs with wiki):

> Resolution was already documented in the wiki for this customer.
> The only new artifact this run produces is a concrete alert-rule
> gap that would have shortened triage by ~10 minutes. Skipping
> wiki/playbook.
>
> codefix

## When to decline a codefix the agent suggests

Sometimes the agent will recommend a change and you'll see, from the
transcript, that it doesn't really apply or is premature. Say so:

> The agent recommended adding a circuit breaker on the gateway, but the
> root cause was a customer-side config drift — a circuit breaker would
> mask the symptom rather than fix the class. Skipping codefix.
>
> wiki

Recording *why you declined* is as valuable as a yes — the human reviewer
can disagree and re-open the question later.

## What you do NOT do

- You do not draft the PR yourself. That's the investigation agent's
  `pr_proposal` flow, which fires after the capture_offer routes `codefix`
  or `all`.
- You do not approve codefix PRs via the agent-operator MCP. Codefix
  proposals don't have an approve flow — they auto-open as PRs from the
  investigation agent. `approve_proposal` only handles wiki and playbook.
- You do not invent code changes the agent didn't surface. Your role is
  to evaluate what's in front of you, not to design fixes.
