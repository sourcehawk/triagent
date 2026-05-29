---
feature: e2e-golden-path-coverage
spec: docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md
plan: docs/superpowers/plans/2026-05-28-e2e-golden-path-coverage-plan.md
tracking_issue: sourcehawk/triagent#13
feature_branch: feature/e2e-golden-path-coverage
feature_worktree: .claude/worktrees/e2e-golden-path-coverage
sub_pr_approval: autonomous                   # confirmed; operator asleep, full autonomy granted for this wave
integration_pr: sourcehawk/triagent#28        # DRAFT — holds until Phase 4 (walkthroughs) lands
status: walkthrough-wave
---

# End-to-end golden-path test suite — orchestration state

Phases 1–3 shipped (harness/stubs #14→#20, Flow 1 #15→#21, Flow 2 #16→#23, Flow 2b #17→#22, editors #18→#26, repos #19→#25, frontend-embed fixup #24, CI envtest + `-v` fixes). Integration PR #28 opened then converted to **draft**: the browser layer shipped too thin (deep-links + narrow render checks), so Phase 4 reworks every flow's browser spec into an operator walkthrough before #28 merges.

## Phases

- **Phase 4 wave A (foundational)** — `sourcehawk/triagent#30` (shared walkthrough infra: stub `summarize`/`usage`, testids, `walkthrough.ts`)
- **Phase 4 wave B (consumers, parallel)** — `#31` investigations, `#32` playbooks, `#33` wikis, `#34` repos

## PRs / worktrees

| Issue                  | Branch                       | Worktree path                                              | PR (→ base)                        | Status      |
| ---------------------- | ---------------------------- | ---------------------------------------------------------- | ---------------------------------- | ----------- |
| sourcehawk/triagent#30 | e2e-walkthrough-infra (del)  | (removed; merged)                                          | sourcehawk/triagent#35 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#31 | e2e-walk-investigations      | .claude/worktrees/e2e-golden-path-coverage--winv           | → feature/e2e-golden-path-coverage | dispatched  |
| sourcehawk/triagent#32 | e2e-walk-playbooks           | .claude/worktrees/e2e-golden-path-coverage--wpb            | → feature/e2e-golden-path-coverage | dispatched  |
| sourcehawk/triagent#33 | e2e-walk-wikis               | .claude/worktrees/e2e-golden-path-coverage--wwiki          | → feature/e2e-golden-path-coverage | dispatched  |
| sourcehawk/triagent#34 | e2e-walk-repos               | .claude/worktrees/e2e-golden-path-coverage--wrepos         | → feature/e2e-golden-path-coverage | dispatched  |

## Contracts

| Name                  | Realization              | Realized in | Status  |
| --------------------- | ------------------------ | ----------- | ------- |
| `walkthrough-testids` | code: #30 merges first   | sourcehawk/triagent#35 | locked  |
| `stub-summarize-usage`| code: #30                | sourcehawk/triagent#35 | locked  |
| `walkthrough-helpers` | code: #30                | sourcehawk/triagent#35 | locked  |

## Bubble-up log

- **2026-05-29 — epic reopened for Phase 4 (operator-walkthrough browser rework); #28 → draft.** Operator judged the shipped browser coverage unacceptable: specs deep-link by id and assert narrow render slices rather than the operator happy path (seed → create → operate → verify ambient panels). Filed sub-issues #30 (shared infra, producer) + #31/#32/#33/#34 (per-flow walkthroughs, consumers), linked under #13. Plan + state re-established (both were deleted in the pre-#28 teardown commit `4e4b277`; spec at `…-design.md` stays, already amended in that commit). `sub_pr_approval: autonomous` and full autonomy granted (operator asleep) — fan out wave A (#30) → checkpoint → wave B (#31-34 parallel) → amend spec Flow 2/3/4/5 browser assertions → re-verify whole suite → flip #28 ready. Do NOT merge #28 to main (operator's external-review call).
- **Carried from Phase 1–3 (still relevant):** browser tests run via `make test-e2e` which builds the frontend first (#24) and `Browser.Run` hard-fails if the embedded SPA is missing; CI installs envtest assets + exports `KUBEBUILDER_ASSETS` and runs the suite with `-v` so RUN/PASS/SKIP + Playwright output are visible. The k8s flow `t.Skip`s only when envtest assets are absent. Stub vocabulary already carries `proposal`/`record_prompt`/`resume.jsonl` (#16) and real `expect_tool_result` MCP round-trip (#17).

## Resume checklist

1. Read this state file in full, then the plan + spec.
2. `gh pr view 28` — confirm still draft; `gh issue view 13` — confirm open with sub-issues #30-34.
3. For each `in-progress`/`draft` row, `cd` the worktree and check `git status` + `git log --oneline feature/e2e-golden-path-coverage..HEAD`.
4. Wave A (#30) must be `self-merged` + contracts `locked` before wave B dispatches.
5. Re-dispatch per `fanning-out-with-worktrees`.
