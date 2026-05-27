# Tool-result token budget: k8s log surface, lean `get_resource`, strategies `step_complete`

**Status:** approved design — implementation pending
**Date:** 2026-05-14
**Author:** Ægir Máni Hauksson + Claude

## Problem

A representative sample of four recent investigation sessions (5/13–5/14) burned **16.4M tokens / $16.17** in aggregate. Cache reads were **80–97%** of every session, and the cost was not driven by long multi-turn conversations — most sessions are one investigation prompt followed by a couple of proposal asks. Inside a single `claude --print` invocation the agent runs ~70–90 tool iterations; every byte of every tool result rides in the cache on every subsequent iteration, so a fat early-iteration result is multiplied by the remaining loop length.

The session profile (event-log analysis) localises the cost to three surfaces:

1. **Fat k8s log fetches.** Session 1 (6.79M tokens) included a **single 838 KB `get_logs` blob** — one tool result alone is ~210k tokens that then rides in the cache for the rest of the loop. `get_logs` has a `tailLines` cap (default 200) but no byte cap, and structured JSON logs run 2–5 KB per line.
2. **Unprojected `get_resource`.** `tools_resources.go:132` marshals the full raw unstructured object as YAML. Pods carry 5–15 KB of `metadata.managedFields`, kubectl/helm serialization annotations, and an unprojected `status:` block — none of which are useful for an LLM consumer. `list_resources` already does kind-aware status projection (`projectStatus()`); `get_resource` does not.
3. **Strategies round-trip churn.** The walker pattern is `tool_use → record_finding ×N → advance → next`. Per-step that's 3–5 strategies tool calls, each one a separate API iteration. Across a session, 25–37 strategies calls become 25–37 extra cache-read iterations, layered onto the per-iteration cost from #1/#2.

## Goals

- **Cap individual tool-result blast radius.** A single tool call should not be able to land 200k tokens of context that the rest of the loop has to re-read.
- **Provide an escalation path that doesn't widen the budget.** When the agent legitimately needs to find something in a large log, the volume must move *out of* the conversation, not into it.
- **Keep tool result quality.** Trimming should be reversible (opt-in `raw` view, opt-in larger byte cap) so an unusual case can still get the unredacted view.
- **Reduce walker overhead by collapsing the per-step call count.** The walker's value is the audit trail and the structured next-step suggestions, not the number of round-trips it takes to traverse one node.

## Non-goals

- **Touching `list_resources`.** Its projection is already good; the session data shows it isn't where the leak is.
- **Per-tool generic auto-summarisation.** Several MCP tool results are bulky but a generic LLM-side summarisation pass would add cost and unpredictability. Curated, per-tool trimming wins.
- **Auto-mode-specific changes.** The auto-operator's per-wake compounding is a real cost shape but a different one; this spec scopes to single-investigation savings. Auto-mode optimization is deferred.
- **Profile-extension of the lean-strip list.** The set of annotations stripped from `get_resource` lives in code; promoting it to a profile-level config is YAGNI until a second operator extends it.
- **Multi-hop or temporal filtering on `search_log_stream`.** v1 takes `grep` and key/value substring filters. Time-window slicing of the stream file is a same-day add later if it's needed.

## Design

The three changes are independent in code but share the same problem framing and rationale; they ship as separate commits under one spec.

### 1. `c1-k8s` log surface: byte cap + streaming search pair

#### 1a. `get_logs` byte cap (default-on)

Add input `maxBytes int` to `GetLogsInput`, default **32768**, hard cap **262144**, applied **per pod**. After grep is applied server-side, truncate at the nearest preceding line boundary so the response always ends on a clean line. On truncation, prepend a one-line marker before the (per-pod) body:

```
# truncated: showed N of M lines, K bytes more available — tighten grep/sinceSeconds, or use stream_log_until for keyword search.
```

`tailLines` stays as the soft line cap (default 200, cap 2000). The byte cap wins when it would produce less. Existing multi-pod `# pods=… tail=…` header stays.

**Tests** (`tools_logs_test.go`):
- 4 KB-per-line synthetic body × 50 lines → truncated, marker present, body ends at line boundary
- Single-pod + multi-pod
- Explicit `maxBytes` override raises the cap up to the hard cap
- Truncation marker counts match (`M of N lines, K bytes more`)
- Grep + byte cap interplay — grep runs first, then byte cap

#### 1b. `stream_log_until` + `search_log_stream`

Two new paired tools. The full log volume never enters the conversation — only the matched fragments do.

**`stream_log_until`**:

```go
type StreamLogUntilInput struct {
    Namespace      string
    Pod            string      // exactly one of Pod / LabelSelector / Workload
    LabelSelector  string
    Workload       string
    Container      string
    Keywords       [][]string  // disjunction of conjunctions; case-insensitive
    TimeoutSeconds int         // default 60, cap 300
    GraceSeconds   int         // default 5, cap 30
}

type StreamLogUntilOutput struct {
    StreamID         string
    LogLinesTotal    int
    KeywordHits      []KeywordGroupHit  // parallel to Keywords
    TimedOut         bool
    TruncatedAtCap   bool               // hit disk cap before keyword/timeout
}

type KeywordGroupHit struct {
    Group        []string `json:"group"`        // echo for clarity
    Hits         int      `json:"hits"`
    FirstLineNum int      `json:"first_line"`   // 1-indexed; 0 if no match
}
```

- **Keywords semantics**: `[[a, b], [c, d], [e]]` means "stop if (a AND b on the same line) OR (c AND d on the same line) OR (e alone)." Case-**insensitive** substring matching. Cost is O(k × line_length) per line for k groups — fine.
- **Stop trigger**: any group matching → start the `GraceSeconds` grace timer → close. Grace fires on first hit of any group (predictable; not "wait for last").
- **Disk cap**: hard per-stream **16 MB** ceiling on the file. Exceeding it sets `TruncatedAtCap: true` and closes the stream.
- **Per-investigation file**: `<sessionDir>/streams/<stream_id>.log`. Lazy-mkdir on first call.
- **Concurrency**: N parallel streams per investigation allowed (different `stream_id`s); each completes independently.

**`search_log_stream`**:

```go
type SearchLogStreamInput struct {
    StreamID  string
    Grep      string             // case-sensitive substring (mirrors get_logs.grep)
    KVFilters []KVFilter         // {Key, Value} substrings AND-joined
    TailLines int                // default unbounded; cap 2000
    MaxBytes  int                // default 32768, cap 262144 (same as get_logs)
}

type KVFilter struct {
    Key   string  // matches against `key=` or `key:` prefix in the line
    Value string  // substring on the value side
}
```

Reads the stream file, applies filters server-side, returns the matched body capped the same way `get_logs` is capped (line-boundary truncation, prepended `# truncated:` marker). Lines outside the byte budget are dropped from the response but **not** from the file — the agent can re-call with tighter filters without re-streaming.

**File lifecycle**: per-investigation `<sessionDir>/streams/` directory. Cleared on launcher boot via a sweep in `Investigation.Restore` (one place; matches the existing rehydrate hook).

**Tests**:
- Streams to file, stops at single keyword group, returns `KeywordGroupHit` with correct counts
- Multi-group keywords — first-hit-of-any triggers grace, hits across groups counted correctly
- Case-insensitive matching ("ERROR" keyword matches "error" line)
- Timeout path (`TimedOut: true`)
- Disk cap path (`TruncatedAtCap: true`)
- Multi-pod streaming into a single file with `[pod]` prefix lines
- `search_log_stream` over a captured file: grep, kvFilters, byte cap, truncation marker
- Launcher boot sweep removes the streams dir

#### 1c. `strategic_log_extraction` companion playbook

New `investigate/system/strategic_log_extraction.yaml`, a `general`-type meta-playbook reachable via `delegate_to` from investigation-side log-investigation branches in `investigation.yaml`.

Step shape:

1. **Probe structure**: `get_logs` with `tailLines: 5`. Findings: `log_format` (json/logfmt/free), `candidate_keyword`, `candidate_kv_keys`.
2. **Stream**: `stream_log_until` with `keywords` if a high-signal group was identified; otherwise timeout-only.
3. **Search**: one or more `search_log_stream` calls with progressively tighter `grep` / `kvFilters` until the relevant lines are in budget.
4. **Terminal**: return to the parent playbook with the captured evidence in findings.

The playbook teaches the AND-of-OR keyword shape inline so the agent doesn't have to derive it from the schema description alone.

#### Commit shape — three commits

- `feat(mcp/k8s): byte cap on get_logs` — handler change + updated description on `get_logs` in `mcp/internal/k8s/specs.go` (mention the byte cap + `maxBytes` knob so the frontend MCP Catalog View shows current behaviour)
- `feat(mcp/k8s): stream_log_until + search_log_stream` — new handlers, registrations, AND new `ToolSpec` entries in `mcp/internal/k8s/specs.go` so they appear in `c1-mcp dump-meta` and the catalog view. `tools_wire_test.go` catches missing entries structurally; descriptions are written fresh.
- `feat(investigate): strategic_log_extraction system playbook`

Each builds clean on top of the previous; embed test for the system playbook glob still passes because the loader picks up `*.yaml`.

### 2. `get_resource` lean projection

Default rendering becomes "lean"; opt-in `view: "raw"` restores today's behavior.

#### What "lean" strips

Always (regardless of kind):
- `metadata.managedFields`
- `metadata.annotations[kubectl.kubernetes.io/last-applied-configuration]`
- `metadata.annotations[meta.helm.sh/*]`, `deployment.kubernetes.io/revision-history`, similar serialization noise
- `metadata.resourceVersion`, `metadata.uid`, `metadata.generation`, `metadata.selfLink`

Per-kind status: reuse `projectStatus()` (already used by `list_resources`, `tools_resources.go:224`). Unknown kinds fall through to the generic `pick("conditions","phase","state","ready")` it already does.

The stripped-annotation list lives in code as `var leanStrippedAnnotations = []string{...}` in `tools_resources.go`; promoting to a config is deferred until a second operator wants to extend it.

#### Input change

```go
type GetResourceInput struct {
    Kind      string
    Namespace string
    Name      string
    Output    string  // yaml (default) / json / describe — unchanged
    View      string  // "" or "lean" (default) / "raw"  — NEW
}
```

`View` is orthogonal to `Output`: it selects content; `Output` selects format. Returned tool result prepends a one-line preamble when projection happened:

```
# lean view — stripped: managedFields, helm/kubectl annotations, status fields not in projection. Pass view: "raw" for the unprojected object.
```

So the agent knows what it's looking at and can escalate when needed.

Redaction (`applyRedactionInPlace` for ConfigMap) still applies in both `lean` and `raw` modes — projection happens before redaction, redaction happens before render.

#### Tests

- Pod with `managedFields` populated → stripped under `lean`, present under `raw`
- ConfigMap → unchanged shape (already small), redaction still applied
- Unknown CRD kind → falls through `projectStatus` generic pick, render succeeds
- Annotations: kubectl/helm noise stripped, custom annotations preserved
- `view: "raw"` round-trips the original object byte-equal to today's output
- Preamble appears for lean, absent for raw

#### Commit

- `feat(mcp/k8s): lean default for get_resource, opt-in view: raw` — handler change + updated description on `get_resource` in `mcp/internal/k8s/specs.go` (mention the lean default and the `view: "raw"` escape hatch so the frontend MCP Catalog View shows current behaviour).

### 3. `strategies` walker: `step_complete` replaces `record_finding` + `advance`

#### Contract

```go
type stepCompleteIn struct {
    SessionID string         `json:"session_id"`
    Findings  []FindingEntry `json:"findings,omitempty" jsonschema:"findings to record before transitioning; pass [] if none"`
    Goto      string         `json:"goto" jsonschema:"the node id to advance to; one of get_state's next_options[].goto"`
}

type FindingEntry struct {
    Key        string `json:"key"`
    Value      any    `json:"value"`
    SourceTool string `json:"source_tool,omitempty"`
}

type stepCompleteOut struct {
    Step             stepView        `json:"step"`
    DelegateReturned *DelegateReturn `json:"delegate_returned,omitempty"`
}
```

**Semantics**: atomic under the session lock — validate `goto` first, then record findings, then transition. All-or-nothing: an invalid `goto` rejects the whole call, no findings recorded. `Findings` may be empty (some steps are pure transitions). `get_state` stays as the read-only inspector.

Preserves all existing `advance` behaviour: `delegate_to` push, `handoff` chain, terminal-node landing, cycle guards, `DelegateReturned` on parent resume. None of that changes — only the call surface does.

#### Audit trail

`Session.RecordedCalls` still gets one entry per finding (no audit-trail regression). The activity panel renders one `tool_use` event per `step_complete` call (vs today's N+1 events for N findings + advance). The operator's UI gets a cleaner read; the agent's prompt gets the savings.

#### Commit shape — two commits

**A. `feat(mcp/strategies): step_complete tool`** (additive, build green):
- Add `step_complete` alongside existing `record_finding` + `advance`
- Extract shared internal helper `recordAndAdvance(sess, findings, goto)`; the three handlers become thin wrappers around it
- Add `step_complete` `ToolSpec` to `mcp/internal/strategies/specs.go` so it appears in `c1-mcp dump-meta` and the frontend MCP Catalog View immediately (the old entries remain until commit B)
- Tests: `step_complete` with 0/1/N findings, invalid goto rejected atomically (findings not recorded), `delegate_to` push, `handoff` terminal, `DelegateReturned` shape preserved

**B. `refactor(investigate/system): migrate playbooks to step_complete + delete old tools`**:
- Search-and-replace every `record_finding` / `advance` reference in `investigate/system/*.yaml`'s `suggested_calls` prose to `step_complete`
- Delete the `record_finding` + `advance` handlers, schemas, and tests
- Delete the `record_finding` + `advance` entries from `mcp/internal/strategies/specs.go`; `tools_wire_test.go` catches any catalog ↔ handler asymmetry. After this commit `c1-mcp dump-meta` and the frontend MCP Catalog View show only `step_complete`.
- Add `playbook_test.go` test that scans every loaded YAML's `suggested_calls` text for the old tool names; fails the build if any survive
- Update prose that still names the old tools so live UI / agent prompts stay consistent: `investigate/frontend/lib/mcps.ts:31` (c1-strategies catalog blurb), `investigate/frontend/public/docs/{playbooks,overview,investigations}.md`, `investigate/internal/profile/profiles/camunda/prompts/strategies.md`. These are static-text-only updates — no render-code changes.

### Compatibility with archived sessions

All three changes are **forward-only**; archived sessions (including those imported from upstream) continue to render unmodified.

- `events.jsonl` stores each tool_use envelope as `{toolName, toolInput, toolResult}` text at the time of the call. The frontend's `ActivityPanel` (`investigate/frontend/components/ActivityPanel.tsx`) reads events generically via `parseToolName(call.name)` — there is no special-case render path for `record_finding` / `advance` / `get_resource` / `get_logs`.
- `loadEvents` in `investigate/internal/server/persist.go` does not re-validate tool names against the current MCP catalog on rehydrate; tool_use envelopes for tools that no longer exist replay as plain text.
- The strategies MCP's persistent `RecordedCalls` list only matters for live sessions. Archived sessions are read-only and the activity panel renders from `events.jsonl`, not from a fresh `get_state` call.
- Old `get_logs` events with 800 KB blobs and old `get_resource` events with full-YAML output continue to render unchanged — byte cap and lean projection are applied at tool-call time, not on replay.

No migration path needed for existing on-disk sessions.

Build is green between A and B because A is purely additive. B's playbook updates and tool deletions land in the same commit so there is never a moment where a playbook references a deleted tool.

## Expected savings

Rough estimates against the four-session profile:

- **#1 (logs)**: largest variance reducer. The 838 KB blob from session 1 would have been ~32 KB under the byte cap; the streaming pair takes the worst-case "agent needs more" path out of the conversation entirely. Per-session save: **2–4M tokens** on log-heavy investigations.
- **#2 (`get_resource`)**: ~50–70% on Pod / Deployment / StatefulSet gets. Across a typical session that's **0.3–0.8M tokens** (smaller than #1 because gets fire fewer times, but compounded by remaining loop iterations).
- **#3 (`step_complete`)**: eliminates ~20 strategies round-trips per session; at ~75k tokens/iteration that's **~1.5M cache-read tokens** per session.

Combined, the spec targets a **~50–70% reduction** on typical investigation sessions, with the variance reducer being #1 (which alone handles the catastrophic-blob case).

## Risks

- **Lean `get_resource` hides a field the agent needs.** Mitigation: the preamble tells the agent what was stripped, and `view: "raw"` is one input flip away. Worst-case the agent learns to opt into raw for the CRDs that don't fit the generic projection.
- **Byte-cap truncation drops the relevant line.** Mitigation: the byte cap takes the *tail* of the tail (recent lines), so the truncation marker says "more available, tighten grep/sinceSeconds". Combined with the streaming pair the agent has a clear escalation path that doesn't widen the budget.
- **Playbook migration miss in commit 3B.** Mitigation: the new `playbook_test.go` scan and the existing `tools_wire_test.go` together catch references to deleted tool names at build time.
- **Stream-file disk usage.** Bounded by per-stream 16 MB cap × concurrent streams × per-investigation isolation, plus the launcher-boot sweep. A pathological run can still write up to N × 16 MB if N streams race. Acceptable.

## Out-of-scope follow-ups

- Auto-mode summary-via-meta-MCP (separate spec; the cost shape there is different).
- Per-tool dashboards / telemetry for tool-result byte sizes (would inform future trimming).
- Generic MCP-side tool-result byte cap as a framework feature (today each tool decides). Worth considering once two more tools want the same shape.
