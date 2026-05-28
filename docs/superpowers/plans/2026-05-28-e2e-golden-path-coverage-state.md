---
feature: e2e-golden-path-coverage
spec: docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md
plan: docs/superpowers/plans/2026-05-28-e2e-golden-path-coverage-plan.md
tracking_issue: sourcehawk/triagent#13
feature_branch: feature/e2e-golden-path-coverage
feature_worktree: .claude/worktrees/e2e-golden-path-coverage
sub_pr_approval: autonomous                   # confirmed at developing-a-feature Step 2
integration_pr:                               # filled in once the feature → main PR opens
status: consumer-wave
---

# End-to-end golden-path test suite — orchestration state

## Phases

- **Phase 1 (foundational)** — `sourcehawk/triagent#14` (harness contract producer)
- **Phase 2 (consumers, parallel)** — `sourcehawk/triagent#15`, `sourcehawk/triagent#16`, `sourcehawk/triagent#17`
- **Phase 3 (consumers, parallel)** — `sourcehawk/triagent#18`, `sourcehawk/triagent#19` (start after #16 locks `browser-harness`)

## PRs / worktrees

| Issue                     | Branch                       | Worktree path                                            | PR (→ base)              | Status      |
| ------------------------- | ---------------------------- | -------------------------------------------------------- | ------------------------ | ----------- |
| sourcehawk/triagent#14    | e2e-harness-foundation       | (removed; merged)                                        | sourcehawk/triagent#20 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#15    | e2e-flow1-boot (deleted)     | (removed; merged)                                        | sourcehawk/triagent#21 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#16    | e2e-flow2-investigation (del)| (removed; merged)                                        | sourcehawk/triagent#23 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#17    | e2e-flow2b-k8s (deleted)     | (removed; merged)                                        | sourcehawk/triagent#22 → feature/e2e-golden-path-coverage | self-merged |
| (fixup, no sub-issue)     | e2e-fix-frontend-embed (del) | (removed; merged)                                        | sourcehawk/triagent#24 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#18    | e2e-flows34-editors          | .claude/worktrees/e2e-golden-path-coverage--editors      | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#19    | e2e-flow5-repos              | .claude/worktrees/e2e-golden-path-coverage--repos        | → feature/e2e-golden-path-coverage | not-started |

## Contracts

| Name                 | Realization              | Realized in              | Status  |
| -------------------- | ------------------------ | ------------------------ | ------- |
| `harness-api`        | code: #14 merges first   | sourcehawk/triagent#20   | locked  |
| `stub-script-format` | data-only                | sourcehawk/triagent#20   | locked  |
| `claude-stub-trace`  | code: #14                | sourcehawk/triagent#20   | locked  |
| `gh-stub-contract`   | data-only + code: #14    | sourcehawk/triagent#20   | locked  |
| `healthz-shape`      | code: #14                | sourcehawk/triagent#20   | locked  |
| `browser-harness`    | code: #16 before wave 3  | sourcehawk/triagent#23   | locked  |
| `signal-release`     | code: #14                | sourcehawk/triagent#20   | locked  |

## Bubble-up log

- **2026-05-28 — wave 2 complete (all merged, full suite green).** #15(#21), #16(#23), #17(#22) self-merged into `feature/e2e-golden-path-coverage` + the frontend-embed fixup #24; sub-issues #15/#16/#17 closed; sub-worktrees + branches (local+remote) torn down. Feature tip `1047fd2`. Orchestrator verified the COMBINED state on the #17 branch (feature+#24 merged in) before landing #22: `make lint` 0 issues; `make test-e2e` EXIT 0 with frontend bundle built and every package `ok` — `e2e` (incl. `TestInvestigation_Browser` now green against the real SPA, and `TestInvestigationK8s_RealMCPRoundTrip` via envtest), `claude-stub`, `gh-stub`, `harness`. Next: wave-2 checkpoint (`reviewing-feature-progress`) then dispatch wave 3 (#18/#19). Crash-recovery note: a laptop crash mid-fixup left uncommitted Makefile+browser.go edits in the fixembed worktree; orchestrator verified, committed, and shipped them as #24.
- **2026-05-28 — #17 reconciled & merged-clean; integration defect found: embedded SPA missing in e2e (browser test red).** #17's subagent merged the post-#16 feature branch into `e2e-flow2b-k8s`, dropped its duplicate client.go auth fix (took #16's canonical cookie fix), unified the claude-stub action loop (`replay(actions,in,out,tr,replayDeps{mcpConfigPath,poster})`, `emitEvent→(*scriptEvent,error)`, all cases preserved), and force-pushed PR#22. Orchestrator independently verified: `make lint` 0 issues; go e2e backend (`claude-stub`/`gh-stub`/`harness`) + `TestFlow1_*` (#15) + `TestInvestigation_BackendInvariants` (#16) + `TestInvestigationK8s_RealMCPRoundTrip` (#17, envtest, 8.1s) all PASS. **But `TestInvestigation_Browser` FAILS** (Playwright: `triagent-proposal-card` count 0/4). Root cause (from the Playwright page snapshot showing a `.gitkeep` directory listing instead of the SPA): **`internal/web/dist/` holds only its `.gitkeep` anchor — the frontend bundle is never built before the e2e launcher binary is compiled.** `make test-e2e` runs `go test -tags=e2e` directly; `buildBinaries()` embeds the empty `dist` via `//go:embed all:dist`, so the launcher serves an empty SPA. Backend tests pass (they hit `/api/*` JSON, no SPA needed); only the browser test needs the real bundle. This is a #14/#16 build-infra gap (the target is #14's; the browser test that needs the SPA is #16's), not introduced by #17. **Fix:** follow-up sub-PR `e2e-fix-frontend-embed` makes `make test-e2e` depend on `make frontend` (mirrors `make build: frontend …`) so the embedded bundle is real, + a loud-fail guard if a browser test runs against an unbuilt dist. **Order:** land fixup → feature green → #17 merges updated feature (clean; #17 has no Makefile/frontend overlap) → wave-2 checkpoint → wave 3.
  **Spec divergence to surface at integration review (non-blocking):** #15 found the spec/plan name `--cwd` and `--launch-browser=false` boot flags that do NOT exist on `triagent start` (only `--port`/`--profile` are documented). #15 covers the real surface + a `--help` completeness scan that will flag those flags if added. Decision deferred to the user at the integration PR: amend the durable spec to drop them, or implement the flags. Logged here so it isn't silently inherited.
- **2026-05-28 — wave 2 returned; shared-infrastructure collision between #16 and #17 (resolution: serialize + rebase).** All three sub-PRs opened: #15→PR#21 (clean, independent), #16→PR#23 (Flow 2 + browser-harness), #17→PR#22 (Flow 2b k8s). Watch loop caught two genuine collisions on #14-owned shared files that both consumers edited on independent branches:
  1. **`e2e/harness/client.go` token auth.** Both independently found + fixed the same latent #14 bug (POST via `?token=` query → launcher 303 token→cookie redirect → Go client downgrades POST→GET → spurious 405). **#16's fix is canonical** (adds the `triagent_token` cookie per-request in `do()` AND on the SSE stream's fresh `http.Client{}` in `stream.go`). **#17's fix is incomplete** (seeds the cookie jar in `setToken()`, but `OpenStream` uses a fresh client with no jar, so the stream stays unauthenticated). #17 must DROP its client.go change and inherit #16's.
  2. **`e2e/cmd/claude-stub/` action loop (`main.go`+`script.go`).** Both rewrite `replay()` and its switch. #16 renames `replay`→`replayWith(...,p *poster)`, adds `proposal`+`record_prompt` actions and `Input`/`Result`/`Gh` struct fields, keeps `emitEvent` returning `error`. #17 keeps the name `replay(...,deps replayDeps{mcpConfigPath})`, changes `emitEvent`→`(*scriptEvent,error)` + adds `emitParsed`, tracks `lastToolCall`, splits `expect_tool_call`/`expect_tool_result` (the latter spawns the real `triagent-k8s` MCP via a new `mcpclient.go` pool). Complementary intent, conflicting form. Unified loop must carry BOTH the poster and the mcp pool, and adopt #17's `emitEvent→(*scriptEvent,error)` (needed for `lastToolCall`) while keeping #16's `proposal`/`record_prompt` cases.
  3. **`e2e/harness/harness.go`** touched by both (#16: Browser wiring; #17: `setupK8s`/envtest wiring) — reconcile both into the post-#16 base.
  **Resolution path:** merge #15 → merge #16 (canonical auth, gates wave-3 `browser-harness`, more additive stub changes) → re-dispatch #17's subagent (`a8c7354738dc0507b`, still alive) to rebase `e2e-flow2b-k8s` onto post-#16 `feature/e2e-golden-path-coverage`, drop its client.go change, reconcile the stub loop + harness.go, re-verify `make test-e2e`+`make lint`, force-push PR#22 → review → merge #17. #15 is unaffected by the auth bug (GET-only; 303 doesn't downgrade GET).
  **Stub-contract note for wave 3 (#18/#19):** the locked claude-stub contract now additionally carries #16's `proposal`/`record_prompt` actions + `resume.jsonl` per-turn selection, and #17's real `expect_tool_result` MCP round-trip (active only when an mcp-config is present; degrades to the #14 stdin-yield otherwise). Editors/repos flows reuse `proposal` for proposal/summary round-trips. Fold into #18/#19 dispatch prompts.
- **2026-05-28 — wave 2 dispatched (#15/#16/#17).** Three sub-worktrees created off `feature/e2e-golden-path-coverage` (`--flow1`/`--flow2`/`--flow2b` on branches `e2e-flow1-boot`/`e2e-flow2-investigation`/`e2e-flow2b-k8s`). Dispatch prompts carry the locked harness contract grounded against the merged #14 code (real signatures, not the plan's idealization): `Options{Profile, SessionFixtures, PlaybookFixtures, WikiFixtures, RepoFixtures, K8s bool, K8sFixtures, StubScript, GhScript, Browser bool}`; `Harness{BaseURL, StateDir, Client, Browser}` + `Close`/`ReleaseSignal`/`StubTrace(t,role)→Trace{Argv,SystemPrompt,AllowedTools,MCPConfig,StdinEvents}`/`GhTrace(t)→{Invocations [][]string}`; `Client.Healthz(t)→(profile,version)`, `Get`/`PostJSON`, `OpenStream(t)→Stream` with `WaitForKind`/`WaitForEvent`; gh `responses.json` is `[{argv,stdout}]` argv-prefix match; stub `main.jsonl` is one `{"action":...}` per line; traces at `<state_dir>/traces/`, signals at `<state_dir>/signals/<name>`. Plus the six #14 refinements below.
- **2026-05-28 — wave-1 checkpoint: intermittent race-suite flake (watch item).** `make test-go` FAILed once on the integrated feature branch, then passed 4 consecutive full `-race` runs; the failing run's output was truncated before naming a package. #14's changes are additive (`/healthz` + `--port`), so this reads as a pre-existing intermittent, not new drift. Resolution: not a blocker for wave 2; watch for recurrence — if it reproduces and points at e2e/#14 code, file a follow-up sub-issue. `make lint` 0 issues, `make test-e2e` green.
- **2026-05-28 — #14 contract refinements (propagate to wave 2/3 dispatch prompts).** The foundation subagent locked these concrete details on top of the plan's Contracts table; consumers must use them verbatim:
  1. `triagent start` gained a real `--port` flag (`127.0.0.1:<port>`; `0` = random). Flow 1 (#15) asserts it exists.
  2. `/healthz` version comes from a new `server.Options.Version` (threaded from `cmd/triagent`; defaults to `"dev"` locally). `/healthz` is **unauthenticated** (exempted in `authMiddleware`).
  3. Stub env vars the harness sets: `CLAUDE_STUB_SCRIPT`, `GH_STUB_SCRIPT`, `CLAUDE_STUB_TRACE_DIR`, `GH_STUB_TRACE_DIR`, `CLAUDE_STUB_SIGNAL_DIR`. Traces land at `<state_dir>/traces/`; signals at `<state_dir>/signals/<name>`.
  4. `seedFixtures` copies session/playbook/wiki/repo scenarios into `${XDG_CONFIG_HOME}/triagent/<profile>/<bucket>`; the profile is loaded via `--profile <fixture-path>` (not copied).
  5. `expect_tool_call`/`expect_tool_result` block on a stdin read only (no MCP client in the stub) — the real round-trip is realized by #17 (k8s flow).
  6. `Harness.Browser` is a stable placeholder type; #16 realizes the config-overlay + `Run`. `K8s`/`K8sFixtures` Options fields are reserved (wired by #17).
  Propagation: folded into the wave-2 (#15/#16/#17) and wave-3 (#18/#19) dispatch prompts; no running subagent to update.

## Resume checklist

For a fresh Claude session resuming this work:

1. Read this state file in full.
2. Read the plan at the path in the `plan:` frontmatter.
3. Read the spec at the path in the `spec:` frontmatter.
4. Verify each open PR's actual state via `gh pr view <num> --repo sourcehawk/triagent`.
5. For each `in-progress` or `draft` row, `cd` to the worktree path and check `git status` + `git log --oneline feature/e2e-golden-path-coverage..HEAD`.
6. Re-dispatch subagents as needed per `developing-a-feature` (parallel waves still in flight; the orchestrator watch loop continues).
