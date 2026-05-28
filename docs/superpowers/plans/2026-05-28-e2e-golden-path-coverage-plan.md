# End-to-end golden-path test suite — Implementation Plan (Phase 4: operator walkthroughs)

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:test-driven-development` + `testing-a-feature` for every change. Each sub-PR is TDD'd in its own worktree per `fanning-out-with-worktrees`.

**Context:** Phases 1–3 (harness + stubs #14, Flow 1 #15, Flow 2 #16, Flow 2b #17, editors #18, repos #19, frontend-embed fixup #24, CI envtest + verbosity fixes) are merged into `feature/e2e-golden-path-coverage`. The integration PR (#28) is **draft** and does not merge until this phase lands.

**Why this phase exists:** The browser layer shipped thin — specs deep-link by id and assert narrow rendered slices (e.g. "four proposal cards exist") that are barely above render unit tests. They do not validate the operator's happy path. This phase reworks every flow's browser spec into a genuine operator walkthrough so the suite exercises the real through-line a human would click.

## The operator-walkthrough pattern (applied per flow)

1. **Seed** prior state → assert the list/sidebar reflects existing activity.
2. **Create** a new one through the UI → assert it appears in the list/sidebar.
3. **Operate** as an operator — drive the work, run follow-ups → assert the live through-line in the DOM.
4. **Verify the ambient panels** for that surface (investigations: activity sidebar + active-MCP sidebar + usage; editors: proposal preview + proposed-badge + ledger; repos: RepoActivityPanel).

## Sub-issues / PR breakdown

Multi-PR, feature-branch model. Each sub-PR targets `feature/e2e-golden-path-coverage` with `Towards #<sub-issue>`; the orchestrator self-merges and closes the sub-issue.

- **#30 — shared walkthrough infra (producer, wave A).** claude-stub `summarize` + `result`/`usage` emission; testids on `Sidebar` (investigation row + new-investigation entry), `MCPStatusBar` (+ chips), `ActivityPanel` (+ rows), `UsageReadout`, composer (input/send); `e2e/browser/helpers/walkthrough.ts`.
- **#31 — investigations walkthrough (consumer, wave B).**
- **#32 — playbooks walkthrough (consumer, wave B).**
- **#33 — wikis walkthrough (consumer, wave B).**
- **#34 — repos walkthrough (consumer, wave B).**

## Dependency graph

```
#30 shared infra ──┬── #31 investigations
                   ├── #32 playbooks
                   ├── #33 wikis
                   └── #34 repos
```

- **Wave A:** #30 alone (every walkthrough consumes its testids + stub vocabulary + helpers).
- **Wave B (parallel):** #31, #32, #33, #34 — all consume the shared infra.

## Contracts

| Name                  | Producer | Consumer            | Shape                                                                                                                                                                                                                  | Realization            |
| --------------------- | -------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- |
| `walkthrough-testids` | #30      | #31,#32,#33,#34     | `data-testid` on: `triagent-investigation-row` (+`data-investigation-id`), the new-investigation entry, `triagent-mcp-status-bar` + `triagent-mcp-chip` (+`data-server-alias`), `triagent-activity-panel` + `triagent-activity-row` (+`data-tool-id`), `triagent-usage-readout`, composer input + send. | code: #30 merges first |
| `stub-summarize-usage`| #30      | #31 (+ others)      | claude-stub script actions: a `summarize` tool call whose result `SessionView` renders as the summary block (enabling summary-gated action buttons), and a `usage`/`result` emission folded into `investigation.usage`/`costUsd`. | code: #30              |
| `walkthrough-helpers` | #30      | #31,#32,#33,#34     | `e2e/browser/helpers/walkthrough.ts` exporting the reusable seed/create/operate/verify-ambient steps. | code: #30              |

## File structure (new/changed)

```
e2e/cmd/claude-stub/        (#30) summarize + usage/result emission in the action loop
e2e/browser/helpers/walkthrough.ts   (#30) shared walkthrough steps
frontend/components/Sidebar.tsx, MCPStatusBar.tsx, ActivityPanel.tsx, SessionView.tsx (UsageReadout + composer)  (#30) testids
e2e/browser/investigation.spec.ts   (#31) rewritten as walkthrough
e2e/browser/playbook.spec.ts        (#32) rewritten as walkthrough
e2e/browser/wiki.spec.ts            (#33) rewritten as walkthrough
e2e/browser/repos.spec.ts           (#34) rewritten as walkthrough
docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md  (this phase) Flow 2/3/4/5 browser assertions rewritten to the walkthrough shape
```

## Integration

After all five sub-PRs are self-merged and the contracts are `locked`:

- **REQUIRED SUB-SKILL:** `reviewing-feature-progress` — re-read spec + plan + state, walk every sub-PR against acceptance criteria, run `make test-e2e` + `make test` + `make lint` + frontend typecheck on the whole feature worktree.
- Amend the spec's Flow 2/3/4/5 browser assertions to the walkthrough shape (durable spec).
- Delete this plan + the state file in the last commit on the feature branch.
- Flip integration PR #28 ready (it already targets `main`, `Closes #13`). External review + merge is the operator's.
