# Investigation cost reductions (items 1–4)

**Status:** approved design — implementation pending
**Date:** 2026-05-18
**Author:** Ægir Máni Hauksson + Claude

## Problem

Analysis of session `86399bd8faff842de3abc1a211330ff6` (a Crossplane "managed
resources not ready" investigation on a prod EKS cluster) surfaced four
structural cost drivers that compound on every session of similar shape:

- **74 LLM turns** (83 tool calls); **~10M total tokens**; **~$9**; **~30 min wallclock**.
- Caching is healthy (96% cache-read ratio); the spend is not a caching bug.
  It is *too many turns × too-large stable context*. Each turn re-reads the
  cumulative prompt; by turn 74 each request was reading 265K cached tokens.

Top drivers in the trace (in events.jsonl token weight):

1. **`list_namespaces` returned 84 KB in a single response** — bigger than all
   27 `step_complete` results combined. Once cached into the prompt, every
   subsequent turn re-paid the cache-read cost. By the end of the session that
   one response accounts for ~3–4M of the 9.7M cache-read tokens
   (≈ 40% of total spend).
2. **Capture-tail (~20 turns)** — from `summarize` (seq 191) onward, the
   operator opted into `terminal_both` (wiki + playbook proposal) via
   `capture_offer.yaml`. The proposal sub-flows are walked by the main Sonnet
   agent across `wiki_list_entities`, `propose_wiki_draft`, `playbook_correlate`,
   `list_playbooks`, `get_playbook_raw`, `playbook_schema`, `playbook_proposal_draft`,
   `validate_playbook`, plus interleaved `step_complete`. Substantive root
   cause was already known by turn ~50.
3. **`list_playbooks` was called twice (29 KB each)** and is then cached into
   the prompt for the remainder of the session. The bulk is per-entry
   `description:` prose.
4. **Walker chatter** — 27 `step_complete` calls, several of them pure
   `{goto: X}` with no findings (e.g. seqs 18, 21). Each transparent transition
   costs a full LLM turn because the agent has to call `step_complete` again to
   move past the no-op node.

## Goals

- Cut session-shaped cost (tokens, wallclock, $) for typical investigations by
  ≥ 30%, by addressing all four drivers above.
- Make the main-agent model and the sub-agent default model **profile-configurable**
  so deployments can dial cost/quality.
- Preserve the existing audit trail (events.jsonl, activity panel) — no
  silent collapses; skipped walker nodes must remain visible.
- Backward-compatible: existing playbooks, existing profile YAMLs, existing
  caller tests continue to work unchanged unless they assert on the trimmed
  response fields.

## Non-goals

- **Rewriting the walker.** Auto-advance is a behaviour-preserving optimisation
  of `applyAdvance`, not a schema change.
- **Removing operator-opt-in capture.** `capture_offer.yaml` stays; the
  operator still chooses wiki / playbook / codefix / bug / all / no. Only the
  *cost* of running the proposal sub-flow when they say yes is changing.
- **Pre-warming the prompt cache.** The cache-read ratio is already 96%;
  pre-warming is a small latency win, not a cost win. Out of scope.
- **A launcher-side `list_playbooks` cache.** The trim + the new `filter` input
  on the tool make the catalog cheap enough that we don't need a separate
  caching layer; adding one would be more code than the saving warrants.
- **Per-tool sub-agent model overrides via profile.** Per-call `Options.Model`
  remains available (so `draft_pr` can opt up to Sonnet when code-reasoning
  warrants), but exposing a per-tool table in profile YAML is deferred until a
  second consumer appears.

## Design

### 1. `list_namespaces` — response trim + profile-driven preemption

**File:** `pkg/mcp/k8s/tools_namespaces.go`

Default response shape drops `Labels` and `CreationTimestamp`. Names are what
the agent needs to pick a workload; labels rarely drive selection and form
the bulk of the 84 KB on prod clusters.

```go
type ListNamespacesInput struct {
    Filter        string `json:"filter,omitempty"`
    Limit         int    `json:"limit,omitempty"`
    IncludeLabels bool   `json:"include_labels,omitempty" jsonschema:"Optional. When true, return labels and creationTimestamp for each namespace. Default false."`
}

type NamespaceInfo struct {
    Name              string            `json:"name"`
    Phase             string            `json:"phase,omitempty"`
    Labels            map[string]string `json:"labels,omitempty"`            // only set when IncludeLabels
    CreationTimestamp string            `json:"creationTimestamp,omitempty"` // only set when IncludeLabels
}
```

**File:** `internal/profile/profile.go`

Add an optional `namespace_derivation` block. Two shapes accepted; loader picks
whichever is set:

```yaml
namespace_derivation:
  # Simple form: a single template applied to all alerts whose payload exposes
  # the named fields. Tokens are ${field-name}; empty render → no hint.
  template: "${project_id}-${component}"

  # Or, for deployments with branchy rules:
  rules:
    - when: "${alert_kind} == 'CrossplaneHighNumberOfManagedResourceNotReady'"
      template: "crossplane-system"
    - when: "${project_id} != ''"
      template: "${project_id}-zeebe"
```

Where the alert-payload fields come from is launcher-side: `internal/server`'s
session-creation path already parses the operator's `notes` block. Field
extraction is the launcher's responsibility; the profile only owns the
template/rules.

The launcher renders the derived namespace(s) into a `Suggested namespace(s):`
line appended to the session's system prompt at preflight. The agent then has
a starting point and can skip `list_namespaces` entirely in the common case.
Falls back to the tool when the profile has no rule or the rule's output is
empty.

**Expected saving:** 84 KB → ~5 KB per call when called, and most sessions of
this shape stop calling it at all.

### 2. Capture-tail — trim + Haiku sub-agent dispatch

The capture flow is already operator-opt-in via `capture_offer.yaml`. This
change attacks cost *after* the operator says yes.

**Trims (no behaviour change):**

- **Move `playbook_schema` body into the playbook-proposal agent prompt.**
  Static content; calling it as a tool wastes a turn. Drop the tool's
  registration in the proposal sub-flow's allowed tool set, embed the
  current returned text into the prompt the sub-agent receives.
- **Drop `validate_playbook` as a separate call** in the proposal sub-flow.
  `playbook_proposal_draft` already validates on submit. Surface validation
  errors in its response structure (a `validation_errors []string` field) and
  have the sub-agent prompt instruct retry on error. The standalone tool stays
  registered for direct authoring flows in the playbook editor; it just isn't
  in the proposal sub-flow's allow-list.
- **Inline `playbook_resolve_entities` into `playbook_correlate`.** The
  correlate description already says it does this as a side-effect; make the
  resolution part of correlate's response shape and remove the separate call
  from the proposal sub-flow's allow-list. Standalone tool stays for the
  editor's direct entity-canonicalisation use case.

**Haiku sub-agent dispatch (the bigger win):**

- **`pkg/mcp/subagent/subagent.go`**: extend `Options` with
  `Model string` field. When non-empty, pass `--model <id>` to the `claude`
  subprocess.

  ```go
  type Options struct {
      // ... existing fields ...
      // Model passes through as --model to the claude CLI. Empty inherits the
      // parent session's model. Used by callers (and per-profile defaults)
      // to route lighter structured-output work to Haiku.
      Model string
  }
  ```

- **Integration point — where dispatch happens.** The existing
  `capture_offer.yaml` handoffs (`terminal_wiki` → `wiki_proposal`,
  `terminal_playbook` → `playbook_proposal`) stay unchanged. The change is on
  the receiving side: the strategies MCP gains a new optional top-level
  playbook field `dispatch: subagent` (default unset → today's walker
  behaviour). When `walk_playbook` is called against a dispatch-mode playbook,
  the strategies MCP does NOT return a walker stepView. Instead it spawns one
  `pkg/mcp/subagent` run with the prompt assembled from the playbook's
  body + the parent session's findings + the operator's refinement comment,
  using the profile's `models.subagent` default. The MCP returns a synthetic
  terminal stepView indicating "dispatched" once the sub-agent finishes. From
  the main agent's perspective, `walk_playbook(playbook_id: "wiki_proposal")`
  returns one short result instead of an entrypoint stepView, and the main
  agent is done with that playbook in a single turn.
- **Prompt assembly** for the sub-agent: the playbook's existing prose (nodes,
  terminal_advice, etc.) is rendered into a single linear prompt; the
  `playbook_schema` body is appended for the playbook-proposal case; the
  parent session's findings map + summarize output are appended verbatim;
  the operator's refinement comment (if any, captured by the
  `capture_offer` ask node) is appended last with explicit instruction to
  honour the refinement. Helper lives in
  `pkg/mcp/strategies/dispatch_prompt.go` (new file).
- **Constrained allow-list** for the sub-agent (`subagent.Options.AllowedTools`):
  `mcp__triagent-wiki__propose_wiki_draft`, `mcp__triagent-strategies__playbook_proposal_draft`,
  `mcp__triagent-strategies__playbook_correlate`, plus read-only k8s tools
  (`get_resource`, `list_resources`, `get_logs`) for any
  last-mile evidence gathering the proposal needs.
- **Output:** the existing `propose_wiki_draft` / `playbook_proposal_draft`
  tools already produce the proposal card on call. The sub-agent's final
  message is purely informational (status text in the activity panel); the
  proposal cards already surface to the operator via their own MCP-to-launcher
  paths.
- Activity panel shows one nested run per proposal (existing
  `pkg/mcp/subagent/status_marker.go` pattern) instead of ~10 main-agent turns.
- Operator UX unchanged: same proposal card, same approve/decline.

**Expected saving on the analysed session:** ~10 fewer turns × growing
context = ~2M tokens off the tail; wallclock improvement comes from running
the structured-output work on Haiku in parallel-equivalent time rather than
serially on Sonnet.

### 3. `list_playbooks` — trim + `filter`

**File:** `pkg/mcp/strategies/server.go` (handler) +
`pkg/mcp/strategies/playbook.go` (summary projection).

```go
type listPlaybooksIn struct {
    Type               string `json:"type,omitempty"`
    Filter             string `json:"filter,omitempty" jsonschema:"Optional case-insensitive substring matched against id + symptom. Empty returns all."`
    IncludeDescription bool   `json:"include_description,omitempty" jsonschema:"Optional. When true, include the longer description prose. Default false."`
}

type PlaybookSummary struct {
    ID          string `json:"id"`
    Symptom     string `json:"symptom"`
    Type        string `json:"type"`
    Description string `json:"description,omitempty"` // only set when IncludeDescription
}
```

The `summariesFiltered` helper picks up the filter and the description gate:

```go
func summariesFiltered(books map[string]*Playbook, typeFilter, nameFilter string, includeDescription bool) []PlaybookSummary
```

For an agent that already knows roughly what it's looking for —
`list_playbooks{filter: "crossplane"}` returns a tight slug list at ~200 bytes
per entry.

**Expected saving:** 29 KB → ~5 KB per default call; ~500 bytes for filtered
calls.

### 4. Walker auto-advance through pure-transition nodes

**File:** `pkg/mcp/strategies/server.go::applyAdvance`.

After computing the next node, check:

```go
isPureTransition := len(node.ExpectedFindings) == 0 &&
    len(node.SuggestedCalls) == 0 &&
    len(node.Next) == 1 &&
    node.DelegateTo == "" &&
    len(node.Handoff) == 0 &&
    node.TerminalAdvice == ""
```

When true, transparently advance to `node.Next[0].Goto` and re-evaluate.
Bound the chain at **10 hops** (loop guard); on exceeding, return the last
node with a warning so a malformed playbook surfaces visibly rather than
spinning.

Augment the response with `auto_advanced_through []string` listing the
skipped node ids:

```go
type stepCompleteOut struct {
    Step                stepView        `json:"step"`
    DelegateReturned    *DelegateReturn `json:"delegate_returned,omitempty"`
    AutoAdvancedThrough []string        `json:"auto_advanced_through,omitempty"`
}
```

`walk_playbook` gets the same treatment: if the entry node is a
pure-transition (unusual but possible), the walker advances past it before
returning.

Update `step_complete`'s tool description: one sentence noting that the walker
may transparently advance past nodes that have no agent-facing work, that
`step.node_id` is always the truth of where the agent is now, and that
`auto_advanced_through` lists any skipped intermediate ids.

**Why this is safe:** by definition, a pure-transition node contributes no
agent reasoning. The agent's only legal action on reaching one was
`step_complete{goto: <the single next>}`. Collapsing that turn is
correctness-preserving. The `auto_advanced_through` array + the
`Visited` audit trail in `Session.Visited` preserve the full path for the
events log and the activity panel.

**Expected saving:** ~5 turns on the analysed session shape; more on
playbooks with longer transparent-handoff chains.

### Cross-cutting — profile-configurable models

**File:** `internal/profile/profile.go`

Add a `Models` block:

```go
type Models struct {
    // Investigation is the model for the main investigation agent. Default
    // claude-sonnet-4-6. Pass-through to `claude --model`.
    Investigation string `yaml:"investigation,omitempty"`
    // Subagent is the default model for sub-agent dispatches via
    // pkg/mcp/subagent. Default claude-haiku-4-5-20251001. Individual
    // callers can override per-call via subagent.Options.Model — the
    // profile is the floor, not a ceiling.
    Subagent string `yaml:"subagent,omitempty"`
}

type Profile struct {
    // ... existing fields ...
    Models Models `yaml:"models,omitempty"`
}
```

YAML form:

```yaml
models:
  investigation: claude-sonnet-4-6
  subagent: claude-haiku-4-5-20251001
```

**File:** `internal/claude/session.go`

Add `Model string` to `SessionOpts`. When non-empty, `baseArgs()` appends
`--model <id>`. The launcher passes `profile.Models.Investigation` (with the
Sonnet default applied at profile load) when creating the main session.

**File:** `pkg/mcp/subagent/subagent.go`

`Options.Model` is the per-call override. Callers that want the profile
default look it up via the launcher's profile accessor (already plumbed for
other profile fields) and pass it in. Empty `Options.Model` inherits the
parent — important for backwards compat with existing callers that don't yet
know about the model knob.

Defaults applied at profile load time (so an absent `models:` block doesn't
break anything):

```go
func (p *Profile) applyDefaults() {
    if p.Models.Investigation == "" {
        p.Models.Investigation = "claude-sonnet-4-6"
    }
    if p.Models.Subagent == "" {
        p.Models.Subagent = "claude-haiku-4-5-20251001"
    }
}
```

## Backward compatibility

- `list_namespaces`: callers that needed labels/timestamps must now pass
  `include_labels: true`. The only in-repo consumer is the
  `tools_namespaces_test.go` fixtures; tests get updated alongside the change.
- `list_playbooks`: callers needing the description prose pass
  `include_description: true`. Same fixture-update story.
- Walker: `auto_advanced_through` is a new optional field. Existing clients
  ignore it. The behaviour change (the walker advancing past pure-transition
  nodes) is observable only as fewer turns; no test should be asserting on
  "agent called step_complete exactly N times" — but if any does, it gets
  updated with the new expected count and a comment pointing at this spec.
- Profile `models:` block is optional; absent profiles get the defaults
  injected at load.
- `subagent.Options.Model` is optional; existing callers pass zero and
  inherit the parent session's model.

## Testing strategy

TDD per item; each commit lands green-on-its-own.

- **`pkg/mcp/k8s/tools_namespaces_test.go`** — new cases: trim default omits
  labels; `include_labels: true` returns the full shape; existing filter +
  limit tests stay green.
- **`internal/profile/profile_test.go`** — namespace_derivation template form
  and rules form load + render correctly; missing block yields empty hint;
  malformed template surfaces a load-time error.
- **`internal/server/...`** — preflight renders the derived namespace into
  the session's appended system prompt when the profile produces one;
  asserts the line appears.
- **`pkg/mcp/strategies/server_step_complete_test.go`** + **`walker_test.go`** —
  a fixture playbook with a pure-transition chain (`A→B→C` where B is
  pure-transition) auto-advances; `auto_advanced_through` lists `["B"]`;
  the loop guard fires after 10 hops on a deliberately cyclic fixture.
- **`pkg/mcp/strategies/server.go` list_playbooks** — filter substring match
  on id + symptom (case-insensitive); description omitted by default and
  returned with `include_description: true`.
- **`pkg/mcp/subagent/subagent_test.go`** — new test verifies that
  `Options.Model = "claude-haiku-4-5-20251001"` causes `--model
  claude-haiku-4-5-20251001` to appear in the spawned command's argv.
- **`internal/claude/session_test.go`** (or wherever `baseArgs` is exercised)
  — `SessionOpts.Model` produces a `--model …` argument in the constructed
  command line.
- **`internal/profile/profile_test.go`** — `Models` defaults applied when
  absent; explicit YAML values override; round-trip preserves both.
- **`pkg/mcp/wiki/...`** + **`pkg/mcp/strategies/tools_proposal.go`** — the
  proposal terminal dispatches one sub-agent run with the profile's subagent
  model and the constrained allow-list; the stub runner in existing tests
  verifies the model flag and the allow-list are honoured.

Race-clean (`go test -race -count=1 ./...`) remains a hard requirement, per
CLAUDE.md.

## Implementation order (one commit per task, green between)

1. **`feat(k8s)`** — `list_namespaces` trim default + `include_labels` input.
2. **`feat(profile)`** — `namespace_derivation` field (template + rules) and
   launcher-side preflight rendering.
3. **`feat(strategies)`** — `list_playbooks` trim default + `filter` +
   `include_description`.
4. **`feat(strategies)`** — walker auto-advance through pure-transition nodes;
   `auto_advanced_through` in `stepCompleteOut` / `walkPlaybookOut`.
5. **`feat(profile,claude,subagent)`** — `Models` profile block;
   `SessionOpts.Model` + `--model` passthrough in `internal/claude/session.go`;
   `subagent.Options.Model` + `--model` passthrough in
   `pkg/mcp/subagent/subagent.go`.
6. **`feat(strategies)`** — trim the `wiki_proposal` and `playbook_proposal`
   playbook YAMLs (in `system/`): remove the nodes whose only purpose is to
   instruct calls to `validate_playbook`, `playbook_schema`, and
   `playbook_resolve_entities` (their guidance gets folded into adjacent
   substantive nodes' descriptions). Surface validation errors in
   `playbook_proposal_draft`'s response so the inlined-validation path works.
   At this point the main-agent-walked flow is shorter; commit 7 then replaces
   it with sub-agent dispatch.
7. **`feat(strategies,wiki)`** — add `dispatch: subagent` playbook field;
   make `walk_playbook` against a dispatch-mode playbook spawn a sub-agent
   via `pkg/mcp/subagent` with the profile's `models.subagent` default,
   the trimmed prompt, and the constrained allow-list. Mark `wiki_proposal`
   and `playbook_proposal` as `dispatch: subagent`.

## Future work (out of scope here)

- **Soften the walker's voice — guidance, not obligation.** Observation: the
  investigation agent over-fits on playbook node descriptions and follows them
  literally rather than treating them as suggestions. The current spec leans
  into that (auto-advance, dispatch, profile-rendered hints route around the
  cost rather than fighting it). A separate line of work should rewrite system
  playbook descriptions in *suggestive* rather than *directive* voice
  ("consider checking X if Y", "skip when findings already cover Z") and give
  the agent explicit permission to deviate from `suggested_calls` when prior
  findings make a call redundant. Needs sample-session A/B to measure whether
  it improves or regresses investigation quality — operator confidence in the
  audit trail today partly *relies* on the agent walking every step.
- **Parallel investigation.** `triagent-parallel.call` already lets the agent
  dispatch 2–8 independent MCP sub-calls in a single turn; trace shows it isn't
  being used in practice. Audit a sample of sessions for spots where
  independent reads (logs from N pods, resources across M namespaces) were
  serialised when they could have been parallel; add explicit "prefer parallel
  when reads are independent" guidance in `investigation.yaml` and
  `strategic_log_extraction.yaml`. Escalate to parallel sub-agents only if
  prompt-level encouragement doesn't move the needle.

## Expected impact (analysed session shape)

| Driver                     | Today                          | After                            |
| -------------------------- | ------------------------------ | -------------------------------- |
| `list_namespaces` response | 84 KB (cached for ~50 turns)   | ~5 KB or zero call               |
| Capture tail               | ~20 turns on Sonnet            | 1–2 sub-agent runs on Haiku      |
| `list_playbooks` × 2       | 58 KB total                    | ~10 KB                           |
| Walker chatter             | 27 step_completes              | ~22                              |
| **Session total**          | ~10 M tokens, ~30 min, ~$9     | ~4 M tokens, ~18 min, ~$4–5      |

Proportional savings on larger investigations will be smaller (the
investigation body dominates the spend on a 200-turn session), but the
four wins all stack additively regardless of session shape.
