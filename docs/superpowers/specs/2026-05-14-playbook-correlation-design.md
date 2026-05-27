# Playbook entity correlation

**Status:** approved design — implementation pending
**Date:** 2026-05-14
**Author:** Ægir Máni Hauksson + Claude

## Problem

As the playbook library grows, the agent's "pick a playbook" step (the `route` node in `investigation.yaml`) loads all playbook summaries via `c1-strategies/list_playbooks` and selects based on the human-readable `symptom:` prose. Two costs scale poorly:

1. **Context tokens.** Every routing step ships every playbook's id, symptom prose, description, and type to the model. Today's dozen-ish playbooks are fine; a library of fifty is not.
2. **Selection precision.** Prose matching on `symptom:` is fuzzy. The agent can pick the wrong playbook when two have similar-sounding symptoms but address different components.

Wikis solved the analogous problem with canonical entity arrays (`services`, `errors`, `symptoms`) on frontmatter plus `wiki_correlate`, which ranks entries by entity overlap with a query set. Playbooks should adopt the same vocabulary so the agent can correlate against playbooks symmetrically.

## Goals

- Let the agent narrow playbook candidates by entity overlap before falling back to the full listing.
- Reuse the wiki's canonical entity vocabulary so an agent that lifted `zeebe-broker` / `oom-kill` / `pod-restart-loop` from findings can hit both `wiki_correlate` and a new `playbook_correlate` without reshaping its inputs.
- Surface "related playbooks" in the editor so operators can navigate the library by entity affinity, not just by id.
- Keep `list_playbooks` working as the panoramic fallback — untagged playbooks remain discoverable.

## Non-goals

- **Replacing `list_playbooks`.** Correlate is additive. The route node calls both.
- **Unified entity registry across wikis + playbooks.** Each MCP builds its own known-set from its own vault. A shared "all known entities" registry is deferred — bring it in only when a third consumer appears or the divergence becomes a footgun.
- **Multi-hop lifting (depth ≥ 2).** v1 lifts entities from one-hop children only. Extending the BFS depth is a same-day change later if needed.
- **Per-node entity tagging.** Tags live on the playbook top-level only. The walker enters at the entrypoint, so node-level tags can't change which playbook gets loaded — they'd just add noise.

## Design

### 1. Frontmatter additions

Three new optional arrays on `Playbook` (`mcp/internal/strategies/playbook.go:18`):

```yaml
id: zeebe_oom
schema_version: 1
symptom: "Zeebe broker OOM-killed"   # unchanged — prose headline for humans
description: |
  ...
services: [zeebe-broker]              # NEW — canonical service entity names
errors:   [oom-kill]                  # NEW — canonical error entity names
symptoms: [pod-restart-loop]          # NEW — canonical symptom entity names (plural; distinct from prose `symptom:`)
entrypoint: ...
nodes: ...
```

Rules:
- All three fields are optional; empty arrays = "not tagged". Untagged playbooks can still surface via *lifting* from a tagged child (see §3), just not as direct matches.
- Entity names must match `^[a-z0-9][a-z0-9-]*$` — same regex wikis enforce.
- Duplicate entries within an array are deduped silently at load. Cross-array duplicates (`zeebe-broker` in both `services` and `symptoms`) are allowed and counted once per playbook.
- The prose `symptom:` field is preserved as-is. The singular/plural distinction (`symptom` vs `symptoms`) intentionally mirrors wiki frontmatter; renaming the prose field would churn every existing playbook for no semantic gain.

**Schema version stays at 1.** Additions are optional and back-compat; existing playbooks load unchanged. No migration shipped.

### 2. Shared entity package — `mcp/internal/entities/`

Move the wiki's pure entity helpers into a new shared package so strategies + wiki use the same validation, near-match, and resolution logic:

| Moves to `mcp/internal/entities/` | Stays in `mcp/internal/wiki/` |
| --- | --- |
| `validateEntityNames(field, names) error` | `loadKnownEntitiesByType(vaultPath)` — vault-specific (reads `.md` frontmatter) |
| `resolveKeywords(services, errors, symptoms, known) []EntityResolution` | |
| `resolveOne(field, input, known) EntityResolution` | |
| `EntityResolution` type | |
| The `^[a-z0-9][a-z0-9-]*$` regex | |

Wiki imports the new package and replaces its in-package calls. No behaviour change wiki-side; tests stay green by construction. Strategies imports the same package and uses it for tag validation + query resolution.

### 3. Scoring & one-hop lifting

```
direct_hit = entity in playbook's own services/errors/symptoms (deduped per playbook)
lifted_hit = entity in services/errors/symptoms of any one-hop child playbook
           (children = playbooks named by any node's `delegate_to` or `handoff[*]`
            — these are the only cross-playbook references; `next[*].goto`
            is intra-playbook only and does not lift)

score(P) = 3 * |direct_hits|  +  1 * |lifted_hits ∖ direct_hits|
```

Concretely:
- **Direct beats lifted for the same entity.** If `zeebe-broker` is both directly tagged on `cluster_health` and tagged on its child `elasticsearch`, the entity counts once (as direct). Prevents double-counting.
- **One hop only.** "Children" means cross-playbook references via `delegate_to` or `handoff[*]`. `next.goto` is intra-playbook (the validator rejects cross-playbook gotos) and is not a lift source. Multi-hop lifting is deferred.
- **Many-parents dedup.** If `connectivity` is a child of three different parents, each parent lifts `connectivity`'s entities exactly once — not once per linking node, not once per linking parent.
- **Cycle-safe.** BFS keeps a visited-set per playbook so cycles across `delegate_to` don't inflate scoring.
- **Tie-break:** alphabetical by id (deterministic; playbooks have no meaningful timestamp).

### 4. Tool surface — `c1-strategies/playbook_correlate`

New tool registered alongside `list_playbooks` etc. (file: `mcp/internal/strategies/tool_correlate.go`). Mirrors `wiki_correlate` so the agent's mental model is symmetric.

```go
type playbookCorrelateIn struct {
    Services []string `json:"services,omitempty" jsonschema:"canonical service entity names; ^[a-z0-9][a-z0-9-]*$"`
    Errors   []string `json:"errors,omitempty"   jsonschema:"canonical error entity names; ^[a-z0-9][a-z0-9-]*$"`
    Symptoms []string `json:"symptoms,omitempty" jsonschema:"canonical symptom entity names; ^[a-z0-9][a-z0-9-]*$"`
    Type     string   `json:"type,omitempty"     jsonschema:"optional type filter, e.g. 'investigation'"`
    Limit    int      `json:"limit,omitempty"    jsonschema:"max correlations; default 5"`
}

type playbookCorrelateMatch struct {
    ID          string             `json:"id"`
    Symptom     string             `json:"symptom"`
    Description string             `json:"description,omitempty"`
    Type        string             `json:"type"`
    Score       int                `json:"score"`
    MatchPath   playbookMatchPath  `json:"match_path"`
}

type playbookMatchPath struct {
    Direct []string         `json:"direct,omitempty"`
    Lifted []playbookLifted `json:"lifted,omitempty"`
}

type playbookLifted struct {
    Entity string `json:"entity"`
    Via    string `json:"via"` // child playbook id that contributed this entity
}

type playbookCorrelateOut struct {
    Correlations []playbookCorrelateMatch     `json:"correlations"` // always [] not null
    Resolution   []entities.EntityResolution `json:"resolution,omitempty"`
}
```

Behaviour:
- **Empty input** (all three arrays empty) → empty result, no error. Matches `wiki_correlate`.
- **Malformed entity names** → `errorResult` (loud).
- **Body never returned.** The output is metadata only. Full body load happens via existing `get_playbook` after the agent picks. This is the context-loading win.
- **Naive O(N) scan** over the loaded playbook set + one BFS per playbook for one-hop lift. Dozens of playbooks; complexity is fine. An inverted index can be slotted in later without API change.
- **Known set for resolution** is built lazily from the union of `services`/`errors`/`symptoms` across all loaded playbooks at correlate time. Cheap (dozens of items). Strategies does not consult the wiki vault.

### 5. Walker integration — `investigation.yaml` `route` node

The `route` node at `investigate/system/investigation.yaml:157` currently suggests one call: `c1-strategies/list_playbooks`. Update it to call correlate first and fall back to listing:

- **Primary suggested call:** `c1-strategies/playbook_correlate` with the canonical entity set the agent lifted from earlier findings (the same set used for `wiki_recall`).
- **Secondary suggested call (fallback):** `c1-strategies/list_playbooks` with `type=investigation`. The agent reads the prose: "use this when correlate returns nothing or you want to scan the full library."
- **Prose update:** explicit guidance that correlate's top match is usually the candidate; the agent confirms by reading the matched playbook's `symptom:` prose. If correlate returns empty (no tagged playbook covers the entities), fall back to list and pick by prose as today.

Untagged playbooks remain discoverable via the fallback path. Operators have no obligation to backfill tags on existing playbooks for the walker to keep working.

### 6. Editor integration

Two pieces, same PR:

**(a) "Related playbooks" panel** on the playbook detail / edit view.
- New launcher endpoint `GET /api/playbooks/{id}/related` (cookie auth, browser-facing per the CLAUDE.md "two URL spaces" rule).
- Handler resolves `{id}` to the playbook, extracts its own `services` / `errors` / `symptoms`, then calls into the strategies in-process API (or proxies to the MCP — implementation detail for the plan) with the playbook's own tags as the query, returning the top 5 *other* playbooks (filter out the queried id).
- Frontend renders the response as a small panel with clickable links to each matched playbook. Empty list → empty panel (or a "no related playbooks tagged yet" hint).

**(b) Entity chip inputs** in the playbook editor for the three new fields.
- Prefer extracting / reusing the wiki editor's existing chip pattern (`WikiNodeEditor.tsx` has the analogous inputs). If extractable to a shared `EntityChipsInput` component, do so and migrate wiki to it in the same PR. If the wiki implementation isn't extractable as-is, build a thin new shared component rather than duplicating.
- Validation mirrors the backend regex (`^[a-z0-9][a-z0-9-]*$`); reject malformed chips at input time with an inline error.

The chip inputs save to the playbook's frontmatter via the existing playbook write path (`WriteUserPlaybook` already round-trips through `renderPlaybookYAML`, which picks up the new fields automatically via yaml struct tags).

## Testing

Per CLAUDE.md TDD discipline — failing test first, watch fail for right reason, implement.

**Backend (`mcp/`):**
- `mcp/internal/entities/`: tests for `validateEntityNames` (valid, malformed, empty), `resolveKeywords` / `resolveOne` (exact, near, miss). Tests carried over from `mcp/internal/wiki/entity_match_test.go` move with the code.
- `mcp/internal/wiki/`: existing tests stay green after the wiki package switches to importing `entities` — proves the move is behaviour-preserving.
- `mcp/internal/strategies/playbook_test.go`: validation cases for the three new fields (well-formed, malformed entity name, duplicate dedup).
- `mcp/internal/strategies/tool_correlate_test.go` (new): direct match, lifted match via `delegate_to` / `handoff` / `next.goto`, dedup across many parents, cycle safety, type filter, limit, empty input → empty output, malformed input → error, resolution surfaces near-matches.

**Frontend (`investigate/frontend/`):**
- Vitest node mode for any pure helper added (e.g. response-shape normalisers).
- The related-playbooks panel and chip-input changes get smoke-level component verification per the existing convention (no jsdom unless needed).

**Walker integration:**
- The investigation.yaml change is covered by the existing playbook validation tests + the system embed test (which loads every yaml under `investigate/system/`).
- Optional: a smoke test in `mcp/internal/strategies/walker_test.go` that walks the updated route node and verifies the new suggested_call surfaces. Add only if existing walker tests already exercise route-node prose; otherwise rely on validation.

## Implementation order

1. **`mcp/internal/entities/` package** — extract from wiki, wiki switches to import. Tests stay green (move + import; no behaviour change). Commit: `refactor(mcp/entities): extract entity helpers from wiki for cross-MCP reuse`.
2. **Playbook frontmatter fields + validation** — add `services` / `errors` / `symptoms` to `Playbook`, validate via shared helpers. Commit: `feat(mcp/strategies): playbook entity tags (services/errors/symptoms)`.
3. **`playbook_correlate` tool** — scoring, one-hop lifting, near-match resolution, registration. Commit: `feat(mcp/strategies): playbook_correlate tool with one-hop entity lifting`.
4. **Walker integration** — update `investigation.yaml` `route` node. Commit: `feat(investigate/system): route via playbook_correlate, fall back to list_playbooks`.
5. **Editor: related-playbooks panel** — launcher endpoint + frontend panel. Commit: `feat(investigate/frontend): related playbooks panel on playbook detail`.
6. **Editor: entity chip inputs** — shared component + wire into playbook editor (and migrate wiki editor if extractable). Commit: `feat(investigate/frontend): entity chip inputs for playbook tags`.

Each commit keeps the build green per CLAUDE.md. The order means the walker change in step 4 *works* immediately even before any playbook is tagged — fallback to `list_playbooks` is unconditional. Tagging is the operator's incremental win.

## Open questions

None blocking. Some judgment calls deferred to implementation:
- Whether the `entities` package surface is exactly the wiki's current API, or whether the move is also a chance to tidy (e.g. consolidating field-name strings). Lean toward minimal-churn move; tidy in a separate pass.
- Whether the "related playbooks" panel calls the strategies MCP via the launcher's internal MCP-router, or via a direct in-process call. Both work; pick whatever the existing wiki "related" surfaces do for symmetry (verify during planning).
