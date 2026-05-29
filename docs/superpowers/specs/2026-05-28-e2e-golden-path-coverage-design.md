# End-to-end golden-path coverage for launcher, sessions, playbooks, wiki, and repos

## Problem

The platform is validated end-to-end by hand: a developer boots the
launcher, clicks through the UI, and talks to a real agent through a
made-up investigation to confirm the major features still work. That
manual smoke test is the only thing exercising the cross-package
through-line that defines the product — profile → preflight → MCP
config → claude subprocess → tool dispatch → SSE → React render. No
automated test runs that path against the actual binaries.

This spec automates that smoke test: an `e2e/` suite that drives the
real `triagent` binary through a synthetic session with a scripted
agent, with no human clicking and no live model. The cost of the gap
shows up specifically in:

1. **Profile drift.** A profile YAML field is added or renamed and the
   plumbing only half-lands. `make test` is green; the launcher boots;
   only an investigation that exercises that field surfaces the bug.
2. **Proposal round-trips.** Playbook / wiki / codefix / GitHub-issue
   proposals share a transcript shape but four different production
   code paths. A regression in any one only shows up when an operator
   reaches the proposal step of a real investigation.
3. **Editor surfaces.** The polymorphic `editor.Session` is shared
   between playbooks and wiki, with a chat panel that proposes edits
   and a manual-edit path. Persistence fidelity differs by path:
   playbooks are plain YAML re-marshalled through `yaml.v3` (semantically
   faithful, lossy on comments); a wiki **entry** save re-marshals its
   frontmatter through a typed struct (the structured fields — `links`,
   etc. — round-trip, but comments / byte-order are not preserved); only
   the wiki **entity-stub** save path preserves frontmatter bytes
   verbatim. Unit tests cover each layer; nothing tests the through-line
   "open editor → chat → AI proposal → accept → file on disk reflects the
   change."
4. **Repos page composition.** The repos page reconciles config
   `linked_repos`, user-local repos, cached `gh` metadata, and the
   repo-summary vault — four sources with independent failure modes.
   No test asserts what an operator sees when one of them is missing.
5. **Launcher boot options.** Every flag and env var the launcher
   accepts has a documented effect; no test confirms each effect.
   Help text drifts from behaviour silently.
6. **K8s MCP round-trip.** The most code-heavy MCP — kubeconfig
   discovery, hot-swappable client snapshot, per-call namespace
   resolution, tool dispatch — has no test that runs the full path
   agent → launcher → `triagent-mcp --kind=k8s` → apiserver. A
   regression in any layer only surfaces when an operator opens an
   investigation and the k8s tools silently misbehave.

This spec defines an `e2e/` test suite that drives the real `triagent`
binary against scripted stand-ins for `claude` and `gh`, covering the
golden-path flows above.

## Goals

- **Real subprocess boundaries.** Tests exec the real `triagent` and
  `triagent-mcp` binaries; external CLI dependencies (`claude`, `gh`)
  are replaced by scripted stubs on `$PATH`. No in-process fakes that
  bypass the IPC layer.
- **Two assertion layers, each covering only what it can.** Backend
  assertions (HTTP / SSE / stub trace / disk) and browser assertions
  (Playwright against the embedded frontend bundle) are not redundant:
  the backend sees trace files, exact event ordering, gh body matching
  and disk round-trips; the browser sees rendered DOM. Flows whose
  acceptance criteria are phrased in terms of UI state ("see in
  sidenav", "click", "shows up in proposal tab") are verified in the
  browser.
- **Backend assertions target durable invariants, not full payloads.**
  Backend assertions pin observable product contracts — proposal
  ordering, profile-derived knobs reaching the agent, tool round-trip
  completion, gh body matching the proposal, disk round-trip fidelity.
  They deliberately do **not** assert whole response bodies. This
  discipline keeps the suite from breaking on routine API-shape churn:
  a backend assertion that breaks should mean the golden path's
  *observable* shape changed, not that a field was renamed.
- **Deterministic.** No reliance on the real Claude API, the real
  GitHub API, real k8s clusters, or wall-clock timing. All external
  surfaces are stubbed; the stubs replay scripts from disk and dump
  a trace file each test can assert against.
- **Fast enough for CI on every PR to main.** Whole `e2e/` suite under
  five minutes wall-clock on the CI runner. Binaries built once via
  `TestMain`; per-test cost is launcher boot + a handful of HTTP
  calls + a few Playwright actions.
- **Isolated from `make test`.** Build tag `e2e` keeps the suite out
  of the fast unit-test loop. A separate `make test-e2e` runs it.

## Non-goals

- **Auto-mode operator coverage.** The agent-operator flow (second
  `claude` instance, briefing prompt, `send_message` /
  `request_takeover` semantics) needs a dual-stub harness and is
  large enough to deserve its own spec. Deferred.
- **Teleport auth path.** Teleport login → kubeContext resolution and
  `switch_context` hot-swap of the k8s client snapshot. This suite's
  envtest bypasses it by writing a static kubeconfig. Deferred.
- **MCP chaos / resilience.** Killing a `triagent-mcp` child
  mid-tool-call and asserting structured error surfacing is a distinct
  testing mode from golden-path. Deferred.
- **Frontend visual regression.** Playwright is used for behavioural
  assertions (selector exists, text contains, click triggers state).
  No screenshot-diffing. Deferred.
- **Exhaustive option matrix.** Each flag / env / proposal type gets
  one happy-path assertion; edge cases stay in unit tests. The flows
  are scoped to "major features working end to end", not "all
  combinations exercised."

## Architecture

### Repo layout

```
e2e/
  README.md                    — how to run, how to author scripts
  harness/
    harness.go                 — Launch(t, opts) → *Harness
    binaries.go                — TestMain builds triagent, triagent-mcp,
                                 claude-stub, gh-stub into a temp bin dir
    subprocess.go              — launcher process supervision, log capture,
                                 SIGTERM-then-SIGKILL cleanup, port allocation
    client.go                  — HTTP helpers (preflight, message,
                                 transcript REST, regenerate, etc.)
    stream.go                  — SSE consumer + filter API (WaitForEvent)
    fixtures.go                — seed XDG_CONFIG_HOME + state dir from
                                 fixtures/ for the requested scenario
    browser.go                 — write playwright config overlay with the
                                 launcher's port + fixture state; invoke
                                 `npx playwright test` and stream results
  cmd/
    claude-stub/
      main.go                  — scripted JSONL replay; role detection
                                 via --mcp config + system prompt
    gh-stub/
      main.go                  — scripted gh CLI replacement; argv +
                                 subcommand match → JSON response from
                                 a per-test script
  browser/
    playwright.config.ts       — base config (testDir: ./specs); per-run
                                 overlay written by harness.browser.Run()
    package.json               — @playwright/test pinned
    specs/                     — the Playwright tests, fenced off from
                                 config + helpers so the spec files are
                                 unambiguously the test surface
      investigation.spec.ts    — flow 2 browser assertions
      playbook.spec.ts         — flow 3
      wiki.spec.ts             — flow 4
      repos.spec.ts            — flow 5
    helpers/                   — selectors, waits, fixture loaders
  fixtures/
    profiles/<scenario>/       — minimal, with-linked-repos, with-prompts, etc.
    sessions/<scenario>/       — pre-baked Investigation dirs, each holding
                                 the <id>/ dir (events.jsonl, metadata.json)
    playbooks/<scenario>/      — pre-baked playbook vault state
    wiki/<scenario>/           — pre-baked wiki vault state
    repos/<scenario>/          — repo summaries vault + user-local repo dirs
    k8s/<scenario>/            — YAML manifests applied to envtest before
                                 each test (pods, namespaces, deployments)
    stub-scripts/<scenario>/   — named for the claude behaviour it scripts
      main.jsonl               — primary claude session script
    gh-scripts/<scenario>/     — named for the gh interaction it answers
      responses.json           — match-by-argv table for gh-stub
  launcher_test.go             — flow 1: boot options
  investigation_test.go        — flow 2: session lifecycle + proposals
  investigation_k8s_test.go    — flow 2b: real k8s tool round-trip
  playbook_test.go             — flow 3: playbook editor
  wiki_test.go                 — flow 4: wiki editor
  repos_test.go                — flow 5: repos page
```

`e2e/` shares `go.mod` with the rest of the repo; the `//go:build e2e`
tag keeps the suite out of `make test`. The browser code lives in its
own `e2e/browser/` package with its own `package.json` and Playwright
install; the Go harness orchestrates the launcher and invokes
`npx playwright test` with a config overlay describing where to point
the browser.

### `claude-stub` binary

The stub stands in for `claude` on `$PATH`. The launcher exec's it
with the same argv shape the real CLI accepts: `--mcp <file>`,
`--allowed-tools <csv>`, `--resume <id>`, `--model <name>`,
`--append-system-prompt <file>`, `--cwd <dir>`, plus the JSONL event
stream on stdin/stdout.

**Script format** (`fixtures/stub-scripts/<test>/main.jsonl`, one
action per line):

```jsonl
{"action": "record_args"}
{"action": "emit", "event": {"type": "assistant_message", "text": "..."}}
{"action": "expect_tool_call", "name": "summarize"}
{"action": "emit", "event": {"type": "tool_result", "ok": true}}
{"action": "emit", "event": {"type": "tool_call", "name": "propose_playbook",
                              "args": {...}}}
{"action": "wait_for_signal", "name": "regenerate-released"}
{"action": "emit", "event": {"type": "end"}}
{"action": "exit", "code": 0}
```

`wait_for_signal` pauses the stub until the harness writes a file
named `<state_dir>/signals/<name>` (HTTP-driven helper:
`h.ReleaseSignal(t, "regenerate-released")`). Used by tests that need
to observe an in-flight intermediate state before the stub finishes.

`expect_tool_result` is the real-round-trip counterpart to
`expect_tool_call`: the stub emits a `tool_call` event, then blocks
on stdin until a `tool_result` arrives from the launcher (which means
the MCP server actually dispatched and returned). The stub doesn't
inspect the result body; it just yields the loop so the next script
action can run. This is how the k8s flow exercises the real MCP path
end-to-end without the stub having to fake the response.

**Trace** (`<state_dir>/claude-stub.trace.<role>.<pid>.jsonl`) records
the full argv, the resolved system prompt body, the parsed
allowed-tools list, the MCP config JSON, and every event read on
stdin. Tests assert against the trace.

**Role detection.** At startup the stub picks its script via
`detectRole(argv)`:

1. If `--mcp <file>` contains an empty/no-servers config → sub-agent.
2. Else if `--append-system-prompt <file>` contains a known role
   marker → sub-agent.
3. Else → main.

In this suite `detectRole(argv)` always returns `main`; the dispatch
table for sub-agent roles lands with the auto-mode spec once a test
actually exercises a subagent path. The detection function ships now
with a unit test that pins the contract.

### `gh-stub` binary

Mirrors `claude-stub` for `gh`. Argv pattern → JSON response from
`fixtures/gh-scripts/<test>/responses.json`. Records every invocation
to `gh-stub.trace.jsonl`. Tests assert (a) the page rendered what the
stub returned and (b) for mutating subcommands (`gh issue create`),
the body matched the proposal that triggered the mutation. The stub
defaults to a clear "no matching response" error that names the
unmatched argv, so missing fixtures surface as test failures rather
than silent fallbacks.

### envtest

For tests that need real k8s round-trips (the k8s flow only), the
harness boots `sigs.k8s.io/controller-runtime/pkg/envtest` once in
`TestMain`, shared across all tests in the run. Each test that opts in
via `Options.K8s = true` gets:

- A fresh randomly-named namespace in the shared envtest.
- Pre-population: YAML manifests from `fixtures/k8s/<scenario>/`
  applied via the envtest client before the launcher starts.
- A static kubeconfig written into the temp XDG dir, pointing at
  envtest's apiserver URL with the right CA + client cert. The
  launcher discovers this kubeconfig the normal way; no Teleport in
  the loop.
- Namespace cleanup on test cleanup (delete-collection of namespaced
  resources, then delete the namespace).

`setup-envtest` (the k8s SIG tool) downloads the apiserver + etcd
binaries on first invocation and caches them. CI caches the
`~/.local/share/kubebuilder-envtest/` directory. First-run cost on
a cold CI runner: ~20s; subsequent runs: ~5s envtest startup +
near-zero binary download. Pinning the k8s minor version via
`sigs.k8s.io/controller-runtime` in `go.mod` keeps the bundle stable.

### Selector strategy (Playwright tests)

Locator priority, per Playwright's recommended order:

1. **`getByRole` with an accessible name.** First choice for any
   interactive element. Doubles as a11y assertion: if the role lookup
   fails, the component isn't accessible.
2. **`getByLabelText` / `getByText`** for stable copy. Avoid where the
   copy is likely to be reworded — use a testid instead.
3. **`getByTestId('triagent-…')`** as the deliberate fallback. Used
   for nodes without natural identity (list containers, status
   badges, "the in-flight spinner", "the active editor tab"), and
   for components where copy changes are expected.

Testid convention: `data-testid="triagent-<component>-<role>"`,
namespaced to avoid colliding with third-party libraries. The
prefix mirrors the existing `triagent:` DOM event / `triagent.`
localStorage namespaces. Testids are added on a minimal,
fallback-only basis — only on the components the e2e flows need to
locate fragilely (a small, named set; some already exist in
`Sidebar` and `SessionView`).

### Harness contract

```go
func Launch(t *testing.T, opts Options) *Harness

// Each scenario field is the bucket directory name, so the field names
// double as the fixtures/ layout. K8sEnvtest / Browser are toggles.
type Options struct {
    Profile    string  // fixture profile under fixtures/profiles/ (required)
    Session    string  // optional scenario under fixtures/sessions/
    Playbook   string  // optional scenario under fixtures/playbooks/
    Wiki       string  // optional scenario under fixtures/wiki/
    Repo       string  // optional scenario under fixtures/repos/
    K8s        string  // optional scenario under fixtures/k8s/ to pre-apply
    StubScript string  // scenario under fixtures/stub-scripts/
    GhScript   string  // scenario under fixtures/gh-scripts/
    K8sEnvtest bool    // attach an envtest apiserver and write kubeconfig
    Browser    bool    // launch Playwright runner for this test
}

type Harness struct {
    BaseURL    string         // http://127.0.0.1:<random-port>
    StateDir   string         // temp XDG_CONFIG_HOME root
    Client     *Client        // HTTP helpers
    Browser    *Browser       // nil unless Options.Browser
}

func (h *Harness) Close()                         // SIGTERM + SIGKILL fallback
func (h *Harness) StubTrace(t, role string) Trace // read claude-stub trace
func (h *Harness) GhTrace(t *testing.T) GhTrace   // read gh-stub trace
```

Launch sequence:

1. Allocate a free TCP port via `net.Listen(":0")`.
2. Create `t.TempDir()` for XDG roots.
3. Copy each requested fixture scenario into the temp dirs.
4. Build env: `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `PATH` prefixed
   with the binary bin dir, `TRIAGENT_PROFILE`, `CLAUDE_STUB_SCRIPT`,
   `GH_STUB_SCRIPT`, `CLAUDE_STUB_TRACE_DIR`, `GH_STUB_TRACE_DIR`.
5. Exec `triagent start --port=<n>` with captured stdout/stderr.
6. Poll `/healthz` until ready or timeout (10s).
7. If `Options.Browser`, invoke `npx playwright test` with a config
   overlay pointing at `BaseURL`; stream results back through `t.Log`.
8. Register `t.Cleanup` to SIGTERM the launcher, wait 5s, SIGKILL
   stragglers, dump captured logs on failure.

`TestMain` builds the four binaries once per `go test` invocation
and caches them in `os.TempDir()/triagent-e2e-bin/`.

### Health probe

This suite adds a `/healthz` endpoint to the launcher if it doesn't
already exist. Contract: 200 once the HTTP server is listening and the
session manager is initialised; body is a small JSON object reporting
at least the resolved profile name and the launcher version. Used by
the harness for readiness and by the boot-options flow to assert
profile resolution.

## Test surface (golden-path flows)

### Flow 1 — launcher boot options

**File:** `launcher_test.go`. **Browser:** no. **Claude-stub:** no.
**Gh-stub:** no.

Table-driven: `[ {name, options, observable, expect} ]`. One subtest
per knob. Verifies each documented flag and env var has its effect:

- `--port` / default port behaviour
- `--profile` and `TRIAGENT_PROFILE` precedence (resolved profile
  reported in the `/healthz` body)
- `XDG_CONFIG_HOME` redirects state dir
- `XDG_CACHE_HOME` redirects cache dir
- `--launch-browser` (default true) gates the startup browser-open; the
  harness always passes `--launch-browser=false` so the suite stays
  headless, and the Flow-1 subtest pins that the launcher boots with it
  set. The openBrowser gating itself is unit-tested in `cmd/triagent`.
- Telemetry token plumbing: `TRIAGENT_MCP_TELEMETRY_TOKEN` reachable
  from a spawned MCP child via the loopback contract

(`--cwd` was considered but dropped — the launcher resolves all paths from
the profile, so a working-directory flag had no real use; see #27.)

Exact knob list derived from `triagent start --help` at implementation
time; the test scans `--help` output for completeness to catch
documented-but-untested flags.

### Flow 2 — investigation session, end to end

**File:** `investigation_test.go` + `browser/investigation.spec.ts`.
**Browser:** yes. **Claude-stub:** yes. **Gh-stub:** yes.

**Setup:**
- Profile `with-prompts-and-linked-repo`.
- Session fixture `fixtures/sessions/in-progress/`: an existing
  Investigation already on disk with a kickoff + one assistant turn.
- Stub script that, for the investigation created live, emits across
  two turns:
  - Turn 1: `assistant_message` → `tool_call: summarize` →
    `tool_call: gather_evidence` → `tool_call: propose_playbook` →
    `tool_call: propose_wiki` → `tool_call: propose_codefix` →
    `tool_call: propose_github_issue` → `end`.
  - Turn 2 (after a harness follow-up message): `assistant_message`
    → `end`.

**Backend assertions** (Go, via HTTP — invariants only):
- Existing fixture investigation appears in `/api/investigations`.
- New investigation creates successfully via `/api/preflight`.
- Stub trace confirms profile-derived knobs reached the agent:
  allowed-tools list contains every tool from `linked_repos` and
  `extra_mcps`; system prompt body matches `profile/prompts/system.md`.
- Transcript REST returns four proposal events in the right order.
- Gh-stub trace confirms `gh issue create` was called only after the
  proposal landed in the transcript, with a body matching the
  proposal's body.

**Browser assertions** (Playwright) — a full operator walkthrough, browser-driven end to end (no deep-link-by-id shortcut):
- Land on launcher root → the seeded fixture investigation appears in the sidebar (`triagent-investigation-row`).
- Click "+ new investigation" → fill `InvestigationForm` → submit (real `/api/preflight` through the UI) → land on the new session → the new investigation now appears in the sidebar.
- `MCPStatusBar` shows the session's active (spawned) MCP chips (`triagent-mcp-chip`) — e.g. the linked-repo `triagent-git-payments` + `triagent-strategies`. Reference-mode `extra_mcps` (no `command`, e.g. `org-docs`) reach the agent's allowed-tools but are not spawned, so they render no chip; their wiring stays a backend invariant (`assertAllowedToolsCover`).
- Send the kickoff via the composer → the live turn streams: assistant message, the summary block, then the four `ProposalCard`s in order with non-empty previews.
- `ActivityPanel` lists the turn's tool calls (`triagent-activity-row`); the usage readout (`triagent-usage-readout`) shows non-zero tokens/cost.
- Send a follow-up via the composer → the second turn's assistant reply renders.

### Flow 2b — investigation with real k8s tool round-trip

**File:** `investigation_k8s_test.go`. **Browser:** no.
**Claude-stub:** yes (uses `expect_tool_result`). **Gh-stub:** no.
**Envtest:** yes.

Narrow flow that exercises agent → launcher → `triagent-mcp --kind=k8s`
→ envtest, with no proposal-card rendering or browser assertions
muddying the picture.

**Setup:**
- Profile `minimal-with-k8s` (no extra MCPs, no linked repos, no
  prompts — just enough to attach the k8s MCP).
- K8s fixture `fixtures/k8s/with-namespaces-and-pods/`: two namespaces
  (`team-a`, `team-b`); three pods across them (one `phase: Failed`).
- Stub script: `record_args` → `emit assistant_message` →
  `emit tool_call: list_namespaces` → `expect_tool_result` →
  `emit tool_call: list_pods {namespace: "team-a"}` →
  `expect_tool_result` → `emit assistant_message` → `emit end`.

**Assertions:**
- Both `expect_tool_result` actions complete within timeout, proving
  the launcher routed to the k8s MCP and got a response back.
- Stub trace records both tool results; harness re-parses them and
  asserts `list_namespaces` returned at least `team-a` and `team-b`,
  and `list_pods` for `team-a` returned the expected pod names.
- Launcher transcript records both tool calls in order with correct
  args (the namespace arg in particular).
- No `triagent-mcp --kind=k8s` child crashed; subprocess registry is
  clean at test end.

This flow does not verify the agent reasoned correctly about the
results — the stub is scripted. It verifies the wire path: profile →
k8s MCP boot → kubeconfig discovery → API call against envtest →
result back to claude → next script action runs.

### Flow 3 — playbook editor

**File:** `playbook_test.go` + `browser/playbook.spec.ts`.
**Browser:** yes. **Claude-stub:** yes. **Gh-stub:** no.

**Setup:**
- Profile `minimal`.
- Playbook fixture `fixtures/playbooks/with-pending-proposal/`: three
  playbook YAMLs in the vault; resolution ledger has a pending
  proposal for one of them.
- Stub script for the editor chat: `analyze_playbook` tool call →
  `propose_playbook_edit` tool call → `end`.

**Assertions** (operator walkthrough, browser-driven):
- Navigate to `/playbooks` → the three seeded playbooks are listed; the one with a pending proposal shows the proposed-badge.
- Create a new playbook through the UI (the new-playbook modal) → it appears in the list.
- Click a playbook → URL becomes `/playbooks?playbook=<id>`, editor mounts.
- Open chat panel → send message → the `ProposalPreview` populates with the AI-proposed diff → accept → the playbook file updates and the ledger entry clears.
- Apply a manual edit via the editor's textarea → save → the playbook YAML on disk reflects the change (edited fields round-trip through `WriteUserPlaybook` → `yaml.v3` marshal; playbooks are plain YAML, so comments are not preserved — verbatim byte preservation is a wiki entity-stub property, not a playbook one).

### Flow 4 — wiki editor

**File:** `wiki_test.go` + `browser/wiki.spec.ts`. **Browser:** yes.
**Claude-stub:** yes. **Gh-stub:** no.

Same operator-walkthrough shape as Flow 3 over `Subject.Kind=wiki`, reusing `browser/helpers/editor.ts` (the point: `editor.Session` is genuinely polymorphic): land on `/wiki` → seeded entries (with `links` frontmatter) listed → create a new entry via `NewWikiEntryModal` → open editor → chat → proposal → accept. The manual-save assertion pins what the entry path actually guarantees — the structured frontmatter (`links`) survives the re-marshal round-trip — **not** byte-verbatim comment preservation, which only the entity-stub save path provides (`handleUpdateWikiEntry` re-marshals entry frontmatter through a typed struct). A live AI proposal can't be generated (`detectRole` is `main`-only; sub-agent dispatch is a non-goal here), so the proposal→accept step uses a seeded pending-proposal fixture, the deterministic analogue of Flow 3's `with-pending-proposal`.

### Flow 5 — repos page

**File:** `repos_test.go` + `browser/repos.spec.ts`. **Browser:** yes.
**Claude-stub:** yes (the regenerate worker's summary sub-agent).
**Gh-stub:** no — the regenerate path never shells out to `gh`.

**Setup:**
- Profile `with-linked-repos` listing two linked repos.
- Repo fixture `fixtures/repos/mixed/`: two user-local repos under the
  launcher's user-repos directory; repo summaries vault (under
  `${XDG_CACHE_HOME}/triagent-mcp/<profile>/git/summaries`) with a
  summary present for one repo, absent for the others; a seeded local
  git clone with a `file://` origin so the regenerate worker's
  `EnsureClone` → `git fetch` succeeds offline.
- Stub script for the regenerate path: the launcher's
  `triagent-mcp generate-architecture-summary` spawns a claude
  sub-agent for the summary; the stub emits the summary body between
  the worker's `<<<BEGIN_SUMMARY` / `SUMMARY>>>` sentinels and gates
  its `end` on `wait_for_signal`.

**Assertions:**
- Navigate to repos page.
- Linked-repos group lists the two config repos.
- User-local group lists the two local repos.
- The repository-activity panel (`RepoActivityPanel`) renders the
  issues/PRs the agent opened across investigations (it is NOT a
  general gh-fed issues list).
- Repo with summary → details page renders the summary text; the
  refresh button is present.
- Repo without summary → details renders empty state; the refresh
  button is visible.
- Click refresh on the empty repo → wait for the SSE
  `repo_summary_state` phase=success event → summary text appears.
- Click refresh again immediately while in-flight → no second worker
  spawned (single-flight contract). The stub gates its `end` on
  `wait_for_signal`; the second `POST /api/repos/<o>/<n>/summary/refresh`
  returns **409** (and the status endpoint reports `inFlight=true`),
  proving the duplicate was never admitted; the test then releases the
  signal to let the one worker finish.

## CI integration

- `make test-e2e` → `cd e2e/browser && npm install` (cached) then
  `go test -tags=e2e -count=1 ./e2e/...`.
- `make test` unchanged; remains the fast unit-test loop.
- **CI runs the e2e suite only on pull requests targeting `main`**
  (`on: pull_request: branches: [main]`), in parallel with the
  unit-test job. Sub-PRs into a feature branch do not trigger it,
  keeping the multi-PR feature-branch loop fast; the suite gates the
  integration PR to main (and any direct-to-main PR).
- Per-test failures dump: launcher stdout/stderr, claude-stub trace,
  gh-stub trace, Playwright trace (`trace.zip`), and on browser tests
  a final screenshot.

## Risks and open questions

- **`gh` CLI shell-out surface size.** If the product makes more `gh`
  calls than expected, the responses fixture grows. Mitigation:
  `gh-stub` defaults to a clear "no matching response" error that
  names the unmatched argv, surfacing missing fixtures as test
  failures rather than silent fallbacks.
- **Playwright install size in CI.** First-time install pulls
  browsers (~300MB). Mitigation: CI caches `~/.cache/ms-playwright`.
- **envtest binary download.** `setup-envtest` fetches a versioned
  bundle (~80MB) on first run. CI caches
  `~/.local/share/kubebuilder-envtest/`. Pinning the k8s minor
  version in `go.mod` keeps the bundle stable.
- **Determinism of the single-flight assertion (Flow 5).** Asserting
  "a second click during in-flight is a no-op" requires the regenerate
  worker to take measurable time. The claude-stub script gates its
  `end` event on a `wait_for_signal` action; the test releases the
  signal after the second click is observed.

## Future specs

- **Auto-mode operator + Teleport auth path.** Dual claude-stub
  harness (one per session), briefing prompt assertions,
  `send_message` / `finish` / `request_takeover` semantics,
  phase-machine coverage. Also covers Teleport login → kubeContext
  resolution and `switch_context` hot-swap of the k8s client
  snapshot, both of which this suite's envtest bypasses by writing a
  static kubeconfig.
- **Cross-MCP resilience.** Real-MCP chaos: kill a `triagent-mcp`
  child mid-tool-call, verify the launcher surfaces a structured
  error and the session stays consistent.
- **Frontend visual regression.** Screenshot diffing across Playwright
  tests, if it becomes valuable.
