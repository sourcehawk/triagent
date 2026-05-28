# End-to-end golden-path test suite — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement each sub-PR task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every sub-PR is TDD'd in its own worktree per `fanning-out-with-worktrees`; write the failing test first, watch it fail for the right reason, then implement.

**Goal:** Automate the manual smoke test — drive the real `triagent` binary through synthetic investigations with scripted `claude`/`gh` stubs, asserting the golden-path flows end to end across HTTP/SSE and the browser.

**Architecture:** A `//go:build e2e`-tagged `e2e/` suite execs the real `triagent` + `triagent-mcp` binaries; only the external CLIs are faked (scripted stub binaries on `$PATH` that replay scripts and dump trace files). Backend assertions (HTTP/SSE/trace/disk) cover durable invariants; Playwright covers "visible" flows. A shared Go harness owns launcher supervision, fixture seeding, and stub orchestration.

**Tech Stack:** Go (`testing`, `os/exec`, `net/http`, `bufio` SSE), `sigs.k8s.io/controller-runtime/pkg/envtest`, `@playwright/test`, `gh` CLI stub.

**Spec:** `docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md` (read it first).

**Epic:** #13. **Sub-PRs:** #14 (foundation), #15 (Flow 1), #16 (Flow 2), #17 (Flow 2b), #18 (Flows 3+4), #19 (Flow 5).

---

## PR shape & branch model

Multi-PR, feature-branch model on `feature/e2e-golden-path-coverage`. Each sub-PR targets the feature branch with `Towards #<sub-issue>`; the orchestrator self-merges and closes the sub-issue. The integration PR (`feature/e2e-golden-path-coverage` → `main`, `Closes #13`) collects the whole feature for external review.

## Dependency graph

```
#14 foundation ──┬── #15 Flow 1 (boot options)        [no browser]
                 ├── #16 Flow 2 (investigation) ──┬── #18 Flows 3+4 (editors)
                 │                                 └── #19 Flow 5 (repos)
                 └── #17 Flow 2b (k8s/envtest)         [no browser]
```

- **Wave 1:** #14 alone (everything depends on the harness contract).
- **Wave 2 (parallel):** #15, #16, #17 — all consume only the harness contract.
- **Wave 3 (parallel):** #18, #19 — consume the browser-harness contract produced by #16.

## File structure

```
e2e/
  README.md                    run instructions + script-authoring guide
  harness/                     Go harness (package harness)
    harness.go                 Launch(t, Options) *Harness; Options/Harness types
    binaries.go                TestMain: build triagent, triagent-mcp, claude-stub, gh-stub once
    subprocess.go              launcher supervision, port alloc, log capture, SIGTERM→SIGKILL
    client.go                  Client: preflight, message, transcript, regenerate, investigations
    stream.go                  SSE consumer + WaitForEvent filter
    fixtures.go                seed XDG_CONFIG_HOME/state from fixtures/<scenario>
    browser.go                 Browser: write playwright config overlay, run npx playwright test
    envtest.go                 (#17) shared envtest boot + per-test namespace + kubeconfig
  cmd/claude-stub/main.go      scripted JSONL replay + trace + detectRole
  cmd/gh-stub/main.go          argv→JSON responses + trace + loud no-match
  browser/                     (#16) package.json, playwright.config.ts, helpers/, *.spec.ts
  fixtures/                    profiles/ sessions/ playbooks/ wiki/ repos/ k8s/ stub-scripts/ gh-scripts/
  launcher_test.go             (#15) Flow 1
  investigation_test.go        (#16) Flow 2 backend
  investigation_k8s_test.go    (#17) Flow 2b
  playbook_test.go             (#18) Flow 3
  wiki_test.go                 (#18) Flow 4
  repos_test.go                (#19) Flow 5
internal/server/healthz.go     (#14) /healthz endpoint
Makefile                       (#14) test-e2e target
.github/workflows/             (#14) e2e job, on PRs to main
```

## Contracts

These are the wire shapes / signatures consumers write against before the producer's body exists. All are realized by **#14 landing first** (hard prerequisite — wave 1), so consumers branch from a feature branch that already contains the compiled contract.

| Name                  | Producer | Consumer            | Shape                                                                                                                                                                                                 | Realization                |
| --------------------- | -------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------- |
| `harness-api`         | #14      | #15,#16,#17,#18,#19 | `func Launch(t *testing.T, opts Options) *Harness`; `Options{Profile, SessionFixtures, PlaybookFixtures, WikiFixtures, RepoFixtures, K8s bool, K8sFixtures, StubScript, GhScript, Browser bool}`; `Harness{BaseURL string, StateDir string, Client *Client, Browser *Browser}`; `(*Harness).Close()`, `(*Harness).StubTrace(t, role string) Trace`, `(*Harness).GhTrace(t) GhTrace` | code: #14 merges first      |
| `stub-script-format`  | #14      | #16,#17,#18,#19     | JSONL actions: `{"action":"record_args"}`, `{"action":"emit","event":{...}}`, `{"action":"expect_tool_call","name":...}`, `{"action":"expect_tool_result"}`, `{"action":"wait_for_signal","name":...}`, `{"action":"exit","code":...}` at `fixtures/stub-scripts/<test>/main.jsonl` | data-only                  |
| `claude-stub-trace`   | #14      | #15,#16,#17         | `<state_dir>/claude-stub.trace.<role>.<pid>.jsonl`: records argv, resolved system-prompt body, parsed allowed-tools, MCP config JSON, every stdin event. `Trace` accessor exposes these.             | code: #14                  |
| `gh-stub-contract`    | #14      | #16,#19             | responses at `fixtures/gh-scripts/<test>/responses.json` (argv-match → JSON); `<state_dir>/gh-stub.trace.jsonl`; `GhTrace` accessor. Unmatched argv → non-zero exit naming the argv.                 | data-only + code: #14      |
| `healthz-shape`       | #14      | #15 (+ harness)     | `GET /healthz` → 200 JSON `{"profile": "<resolved-name>", "version": "<launcher-version>"}` once HTTP listening + session manager initialised.                                                        | code: #14                  |
| `browser-harness`     | #16      | #18,#19             | `e2e/browser/` package.json + pinned `@playwright/test`; harness writes a config overlay pointing at `Harness.BaseURL`; `browser/helpers/` exposes selectors + waits; `(*Harness).Browser.Run(t, spec string)`. Testid convention `data-testid="triagent-<component>-<role>"`. | code: #16 merges before wave 3 |
| `signal-release`      | #14      | #19                 | `(*Harness).ReleaseSignal(t, name string)` writes `<state_dir>/signals/<name>`; stub `wait_for_signal` blocks until present.                                                                          | code: #14                  |

`detectRole(argv)` ships in #14 returning `main` for all current callers, with a unit test pinning the contract; the sub-agent dispatch table is deferred to the auto-mode spec.

---

## #14 — Harness foundation (wave 1, no dependencies)

**Branch:** `e2e-harness-foundation` off `feature/e2e-golden-path-coverage`. **PR body:** `Towards #14`.

This PR is the contract producer; it must compile and ship the full `harness-api`, the two stubs, `/healthz`, the build tag, the make target, the CI job, and a smoke test. No flow assertions yet.

**Files:** see File structure (`e2e/harness/*` except `envtest.go`/`browser.go`, `cmd/claude-stub`, `cmd/gh-stub`, `internal/server/healthz.go`, `Makefile`, `.github/workflows/`).

- [ ] **Task 14.1 — `/healthz` endpoint (TDD against the real server).** Add a Go unit test in `internal/server` asserting `GET /healthz` returns 200 with `{"profile","version"}` once the server is wired; watch it fail; implement `healthz.go` + route registration; `make test-go` green. Commit `feat(internal/server): add /healthz readiness probe (#14)`.
- [ ] **Task 14.2 — `detectRole` + claude-stub skeleton.** Test `detectRole(argv)` returns `main` for a no-subagent argv and the empty-MCP/role-marker cases return `subagent`; watch fail; implement `cmd/claude-stub/main.go` role detection + JSONL action loop (`record_args`, `emit`, `exit`) + trace writer. `go test -tags=e2e ./e2e/...` green for the stub unit test. Commit.
- [ ] **Task 14.3 — claude-stub tool round-trip actions.** Add `expect_tool_call`, `expect_tool_result`, `wait_for_signal` to the action loop (blocking on stdin / signal file). Unit-test the action parser. Commit.
- [ ] **Task 14.4 — gh-stub.** Test argv-match returns the scripted JSON and an unmatched argv exits non-zero naming the argv; implement `cmd/gh-stub/main.go` + trace. Commit.
- [ ] **Task 14.5 — `binaries.go` TestMain.** Build the four binaries once into a temp bin dir; fail loudly if any build fails. Commit.
- [ ] **Task 14.6 — `subprocess.go` + `harness.go` Launch.** Port alloc via `net.Listen(":0")`, temp XDG seeding, env construction (`XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `PATH` prefixed with bin dir, `TRIAGENT_PROFILE`, `CLAUDE_STUB_SCRIPT`, `GH_STUB_SCRIPT`, `*_TRACE_DIR`), exec `triagent start --port=<n>`, `/healthz` poll (10s), `t.Cleanup` SIGTERM→5s→SIGKILL + log dump on failure. Commit.
- [ ] **Task 14.7 — `client.go` + `stream.go` + `fixtures.go`.** HTTP helpers for the endpoints wave-2 needs (investigations list, preflight, message, transcript, regenerate), SSE consumer with `WaitForEvent`, fixture-scenario copier, `StubTrace`/`GhTrace`/`ReleaseSignal` accessors. Commit.
- [ ] **Task 14.8 — smoke test.** `Launch(t, Options{Profile:"minimal"})` boots the launcher, `/healthz` returns the expected shape, `Close()` leaves no stray process. Commit.
- [ ] **Task 14.9 — `make test-e2e` + CI job.** `make test-e2e` → `cd e2e/browser && npm install` (no-op until #16 adds it; guard) then `go test -tags=e2e -count=1 ./e2e/...`. `.github/workflows/` e2e job `on: pull_request: branches: [main]`, parallel to the unit job, caching `~/.cache/ms-playwright` and `~/.local/share/kubebuilder-envtest/`. Commit. Verify locally `make test-e2e` green.

**Checkpoint:** orchestrator runs the `review` skill on the #14 sub-PR, self-merges into `feature/e2e-golden-path-coverage`, closes #14. The harness contract is now `locked`.

---

## #15 — Flow 1 boot options (wave 2, depends on #14)

**Branch:** `e2e-flow1-boot` off the post-#14 feature branch. **PR body:** `Towards #15`.

- [ ] **Task 15.1 — table-driven knob test.** `launcher_test.go`: one subtest per knob (`--port`, `--profile`/`TRIAGENT_PROFILE` precedence via `/healthz`, `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` redirects, `--cwd`, `--launch-browser=false`, telemetry token reachable from an MCP child). Each subtest uses `Launch` with the knob set and asserts the observable. Watch each fail (if any knob mis-wired, that's a real bug to surface), implement nothing in the harness (knobs already exist), green.
- [ ] **Task 15.2 — `--help` completeness scan.** Parse `triagent start --help`, assert every documented flag has a subtest name; fail listing untested flags. Commit `test(e2e): launcher boot options (#15)`.

**Checkpoint:** review → self-merge → close #15.

---

## #16 — Flow 2 investigation + proposals (wave 2, depends on #14; produces `browser-harness`)

**Branch:** `e2e-flow2-investigation`. **PR body:** `Towards #16`.

- [ ] **Task 16.1 — fixtures.** `fixtures/profiles/with-prompts-and-linked-repo/`, `fixtures/sessions/in-progress/`, `fixtures/stub-scripts/investigation/main.jsonl` (two turns: four proposals in order then a follow-up), `fixtures/gh-scripts/investigation/responses.json`. Commit.
- [ ] **Task 16.2 — backend invariants (`investigation_test.go`).** Fixture investigation listed; new session via `/api/preflight`; `StubTrace` confirms allowed-tools + system-prompt reached the agent; transcript REST returns four proposals in order; `GhTrace` confirms `gh issue create` body matches the proposal. TDD each assertion. Commit.
- [ ] **Task 16.3 — browser harness (`browser.go` + `e2e/browser/`).** `package.json` (pinned `@playwright/test`), `playwright.config.ts`, harness writes the per-run overlay pointing at `BaseURL`, `helpers/` selectors/waits, `(*Harness).Browser.Run`. This realizes the `browser-harness` contract. Add minimal testids (`triagent-proposal-card`, etc.) to the components the spec names. Commit.
- [ ] **Task 16.4 — `investigation.spec.ts`.** Four `ProposalCard`s render in order with title + non-empty preview; follow-up turn renders. Commit `test(e2e): investigation + proposals, backend + browser (#16)`.

**Checkpoint:** review → self-merge → close #16. The `browser-harness` contract is now `locked` (wave 3 can start).

---

## #17 — Flow 2b k8s round-trip (wave 2, depends on #14)

**Branch:** `e2e-flow2b-k8s`. **PR body:** `Towards #17`.

- [ ] **Task 17.1 — envtest wiring (`envtest.go`).** Add `sigs.k8s.io/controller-runtime` to `go.mod` (pin the k8s minor). Boot envtest once in `TestMain` (extend #14's, guarded by an opt-in so non-k8s tests don't pay for it). Per-test: fresh namespace, apply `fixtures/k8s/<scenario>/` manifests, write a static kubeconfig into the temp XDG dir, namespace cleanup on `t.Cleanup`. Commit.
- [ ] **Task 17.2 — fixtures + stub script.** `fixtures/profiles/minimal-with-k8s/`, `fixtures/k8s/with-namespaces-and-pods/` (team-a, team-b; three pods, one Failed), stub script with `list_namespaces`/`list_pods` + `expect_tool_result`. Commit.
- [ ] **Task 17.3 — `investigation_k8s_test.go`.** Both round-trips complete in timeout; `StubTrace` results contain expected namespaces/pods; transcript records calls + args in order; subprocess registry clean at end. TDD each. Commit `test(e2e): real k8s MCP round-trip via envtest (#17)`.

**Checkpoint:** review → self-merge → close #17.

---

## #18 — Flows 3+4 editors (wave 3, depends on #14 + #16)

**Branch:** `e2e-flows34-editors`. **PR body:** `Towards #18`.

- [ ] **Task 18.1 — fixtures + shared `browser/helpers/editor.ts`.** `fixtures/playbooks/with-pending-proposal/`, `fixtures/wiki/with-links/`, stub scripts for each editor chat. Extract shared editor selectors/waits. Commit.
- [ ] **Task 18.2 — playbook (`playbook_test.go` + `playbook.spec.ts`).** Sidenav lists three; proposed-badge; click → `?playbook=<id>` editor mounts; chat → `ProposalPreview` diff; manual edit save → disk YAML reflects change + frontmatter comments intact; accept proposal → file updates + ledger cleared. TDD. Commit.
- [ ] **Task 18.3 — wiki (`wiki_test.go` + `wiki.spec.ts`).** Same shape over `Subject.Kind=wiki`, reusing `editor.ts`. Asserts `editor.Session` polymorphism. Commit `test(e2e): polymorphic editor round-trip, playbook + wiki (#18)`.

**Checkpoint:** review → self-merge → close #18.

---

## #19 — Flow 5 repos page (wave 3, depends on #14 + #16)

**Branch:** `e2e-flow5-repos`. **PR body:** `Towards #19`.

- [ ] **Task 19.1 — fixtures.** `fixtures/profiles/with-linked-repos/`, `fixtures/repos/mixed/` (two user-local repos, one of four with a summary), `fixtures/gh-scripts/repos-mixed/responses.json`, regenerate-worker stub script (emits `write_repo_summary`, gates `end` on `wait_for_signal`). Commit.
- [ ] **Task 19.2 — `repos_test.go` + `repos.spec.ts`.** Linked + user-local groups; issues sidenav; summary vs empty-state; "Regenerate" → SSE completion → summary appears; second in-flight click → no second worker (release the signal after observing the already-running status). TDD. Commit `test(e2e): repos-page reconciliation + single-flight regenerate (#19)`.

**Checkpoint:** review → self-merge → close #19.

---

## Integration

After all six sub-PRs are self-merged and every contract is `locked`:

- [ ] **REQUIRED SUB-SKILL:** `reviewing-feature-progress` — re-read spec + plan + state, walk every sub-PR against acceptance criteria, run `make test-e2e` + `make test` + `make lint` on the whole feature worktree.
- [ ] Delete this plan + the state file in the last commit on the feature branch (the spec stays — durable).
- [ ] **REQUIRED SUB-SKILL:** `opening-a-pull-request` — integration PR `feature/e2e-golden-path-coverage` → `main`, body `Closes #13`.

## Self-review (spec coverage)

- Real subprocess boundaries → #14 (real binaries + stubs on `$PATH`). ✓
- Two assertion layers, invariants-only backend → #16/#18/#19 backend + browser; invariant discipline noted per flow. ✓
- Deterministic (no real Claude/GitHub/k8s/clock) → stubs + envtest. ✓
- Under 5 min, build once, e2e only on PRs to main → #14 TestMain + CI job. ✓
- Build-tag isolation + `make test-e2e` → #14. ✓
- Five flows → #15 (1), #16 (2), #17 (2b), #18 (3+4), #19 (5). ✓
- Non-goals (auto-mode, Teleport, chaos, visual regression) → not in any task; `detectRole` ships `main`-only with the sub-agent table deferred. ✓
