---
name: capture-decisions
description: Use when the investigation agent runs the closing capture_offer playbook — you'll see it ask "Reply with wiki / playbook / codefix / all / both / no". This skill is the rubric.
---

# Choosing a capture path

At the close of every investigation the agent asks how to capture it. Your
answer routes the rest of the session. The agent's state machine matches on
one of the literal keywords (`wiki` / `playbook` / `codefix` / `all` /
`both` / `no`), so the keyword **must appear** in your reply — but it
shouldn't be the *only* thing in your reply.

**Reason first, then the keyword.** A reviewer later reading the transcript
should be able to see why you chose what you chose. One sentence of
justification, then the keyword on its own line, is the minimum shape.

Example:

> The agent found a clear OOM root cause and proposed a specific
> memory_limiter config change — both wiki-worthy and a codefix candidate.
>
> all

The agent's matcher is lenient: it scans the reply for the keyword. Putting
it on a dedicated line at the end keeps the matcher reliable and the prose
human-readable.

## Engaging with the agent's proposals

The `capture_offer` playbook tells the investigation agent to draft
**concrete capture proposals before printing the menu** — named wiki
entries (one or more), a named playbook addition or "no playbook" with a
reason, a named codefix shape or "no codefix" with a reason, plus the
agent's recommended default. The agent has the full investigation
context; the proposals it surfaces are the load-bearing input to your
decision.

Your reply is not just a routing keyword. It's a **review of the agent's
proposals.** Three valid moves on each proposal:

1. **Accept as drafted** — short ack, move on.
2. **Refine in place** — keep the shape but tighten it. Splits and
   merges live here.
3. **Drop** — say why; the proposal flow won't be entered.

Each category supports more than one item. The "split into two distinct
shapes" pattern below is the most common move, but the same logic
applies to playbooks (one new + one extension to an existing one) and
codefixes (an alert split in one repo plus a docs gap in another) when
both are genuinely warranted. Don't pad — but don't collapse multiple
real items into one either.

Aggressive refinement of the *shape* is the highest-leverage move you
can make. Two patterns to watch for:

- **A wiki proposal that conflates two distinct shapes.** If the
  investigation surfaced two unrelated root causes hiding behind one
  symptom (e.g. one alert firing for both an OOM-driven failure on
  worker-9 and a conflict-requeue loop on worker-1), splitting into two
  wiki entries protects the next reader from being misled. The agent
  often defaults to one entry; you should call this out.
- **A codefix gesture in disguise.** "Add a circuit breaker" / "harden
  the pipeline" is not a codefix. If the agent's codefix proposal isn't
  a named file/repo/change or a named alert-rule split, push back and
  drop it — wiki captures the lesson; codefix would just churn a
  reviewer queue.
- **A playbook fix mislabelled as a codefix.** Renaming a `handoff:` /
  `goto:` target, fixing a broken delegate, adding a node — these are
  playbook YAML edits. They route through `playbook`, not `codefix`,
  even when the playbook lives in a linked repo. If you find yourself
  describing the fix under the codefix bullet of your reply, move it
  under the playbook bullet (refine the agent's existing playbook
  proposal, or add `playbook` to your reply if there isn't one).

Don't be shy about adding shapes the agent missed, either: if the agent
proposed a wiki entry but didn't see that the alert rule itself was the
real bug, suggest a codefix on the alert.

### Worked example

Agent's proposal (paraphrased):

> _Wiki:_ "OperatorContinuouslyReconciling — pod restart loop".
> _Playbook:_ stuck_reconciliation already covers this.
> _Codefix:_ no codefix — operational issue.
> _Default:_ wiki.

Your reply:

> Good calls. Going with all — and I'll structure the proposals to
> reflect the two distinct shapes:
>
> - Wiki: two entries (worker-9 OOM-driven shape vs worker-1
>   conflict-requeue shape) — they share an alert but not a root
>   cause; conflating them would be misleading next-time.
> - Playbook: a new dedicated `operator_continuously_reconciling`
>   playbook (alert-driven entry, pod-restart check first,
>   conflict-requeue check second, breaker check third) —
>   `stuck_reconciliation` is for the wrong shape.
> - Codefix: split the single alert into sub-rules in `example-org/alerts`
>   so the symptom and the cause are no longer hidden behind the same
>   firing.
>
> all

This reply does five things at once: refines the wiki proposal (split
into two distinct shapes), refines the playbook proposal (new playbook
rather than extending the wrong one), adds a codefix shape the agent
declined, justifies each move in one line, and ends with the routing
keyword on its own line.

Match this shape when the investigation surfaced enough texture to
warrant it. When it didn't — a single clean shape, agent's proposals
are already right — a one-line "agreed, all" is fine.

## Auto-triggered investigations are a special case

If you can see in the briefing or session metadata that this investigation
was auto-triggered by signal-watch ingestion (look for the "Auto-triggered
by signal-watch ingestion" prefix in the opening briefing), the capture
decision has an extra constraint:

- A **noop / false-positive outcome** is exactly the artifact that lets
  the ingestion agent automatically dismiss similar signals next time.
- For noops on auto-triggered runs, **choose `wiki`** (not `no`). Author
  the wiki entry with `status: wontfix` so it's clearly marked as
  documented-but-not-actionable. Include enough symptoms in the body
  (services, error keywords, timing patterns) for the ingestion agent's
  `wiki_correlate` to find it.

For non-noop outcomes on auto-triggered runs, follow the regular rubric.
For operator-initiated investigations, the existing "be willing to say no"
guidance applies as before.

## The five real choices

### `wiki` — promote to the team wiki

Use when the **symptom + resolution pair** would help a future operator
investigating something similar on this customer, component, or cluster
topology. Bias **toward** wiki for any incident with a clear narrative —
knowledge accrual is cheap, and the wiki proposal flow has a human review
gate anyway.

> Resolved Zeebe OOM tied to a known deploy → `wiki`.

### `playbook` — propose a new/improved playbook

Use only when the **method** the investigation followed generalizes — a
re-runnable procedure that the next operator would follow step by step. The
bar is real: a one-off discovery is **not** a playbook. A repeatable triage
sequence is.

> "When Operate shows stuck workflows, check X then Y then Z" → `playbook`.
> "Customer's broker config was wrong this one time" → not a playbook.

### `codefix` — propose a PR to a linked repo

Use when you can **name the file and the change**. A missing alert rule, a
docs gap that confused the investigation, a small code fix to prevent the
class of incident. If you can't be specific, this isn't a codefix.

**Not a codefix: playbook YAML edits.** If the change is to a playbook's
structure or content — adding a node, renaming a `handoff:` / `goto:`
target, tightening `expected_findings` — that's a `playbook` refinement,
**not a codefix.** Route it through the playbook proposal flow (refine the
agent's existing `playbook` proposal or, if there isn't one, add `playbook`
to your reply). This holds even when the playbook lives in a linked git
repo — codefix is for code, infra-as-code, and operational rules; playbook
YAML has its own proposal surface.

### `all` — wiki + playbook + codefix

Use for genuinely high-impact incidents where all three angles are present.
**Do not default to this.** "All" on a routine incident produces noise on
three review queues.

### `no` — close out as-is

Use when the investigation was trivial, inconclusive, or so customer-specific
that no artifact would help. A noise proposal is worse than no proposal. Be
willing to say no.

> "Customer's typo in their config" → `no`.

## The legacy keyword

`both` exists for backward compatibility (wiki + playbook). Prefer `all` or
`wiki` over `both`.

## When you're unsure

Between `wiki` and `all`: pick `wiki`. The other two can be requested
separately later.
Between `wiki` and `no`: pick `wiki` if there's a real narrative; pick `no`
if the resolution was "the customer fixed their own config."
Between `playbook` and `wiki`: pick `wiki` unless you can describe the
re-runnable procedure in one sentence.

## When you're not ready to decide

If the agent's summary leaves the codefix question ambiguous (e.g. it
recommended a change but didn't say which file or repo), **ask a follow-up
first** instead of guessing. The capture_offer playbook will reach you
again on the next turn after the agent answers. A back-and-forth that
sharpens the codefix scope produces a better proposal than a same-turn
guess.

> The recommendation to add a `memory_limiter` processor is concrete, but
> I'm not sure whether the collector pipeline config lives in
> example-org/service or example-org/platform. Which repo would this change
> target?

(Then on the next turn, after the agent answers, run the capture decision.)

## What the matcher needs

The agent matches on the keyword as a substring. So the minimum legal
shape is one sentence of reasoning followed by the keyword. The keyword
on its own line at the end is the cleanest form:

> Symptom-resolution narrative is clear, repeatable triage steps aren't,
> and there's no concrete file/change to ship. Going with knowledge
> capture only.
>
> wiki
