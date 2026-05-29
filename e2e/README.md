# e2e — golden-path end-to-end suite

Drives the real `triagent` launcher through synthetic investigations with
scripted stand-ins for `claude` and `gh`. No live model, no GitHub API, no
real cluster. Backend assertions cover durable invariants (HTTP / SSE /
stub trace / disk); the browser layer (Playwright, added in #16) covers
rendered DOM.

## Running

```
make test-e2e      # whole suite (go test -tags=e2e ./e2e/...)
```

The `e2e` build tag keeps the suite out of `make test`. `TestMain` (in
`harness/binaries.go`) and `harness.Launch` build the four binaries
(`triagent`, `triagent-mcp`, and the `claude` / `gh` stubs) once per
invocation into a temp bin dir, then prefix it onto `PATH` so the launcher
resolves the stubs.

## Writing a test

Every `Options` scenario field is named for its `fixtures/` bucket, so the
field tells you exactly which directory it loads from. Name a scenario for the
state it encodes (`resumable-investigation`) or the behaviour it scripts
(`summarize-and-resume`) — never for the flow that happens to use it, since
scenarios get reused.

```go
func TestSomething(t *testing.T) {
    h := harness.Launch(t, harness.Options{
        Profile:    "minimal",              // fixtures/profiles/<scenario>/
        StubScript: "summarize-and-resume", // fixtures/stub-scripts/<scenario>/main.jsonl
        GhScript:   "issue-create",         // fixtures/gh-scripts/<scenario>/responses.json
    })
    profile, version := h.Client.Healthz(t)
    // ... assert against h.Client, h.StubTrace(t, "main"), h.GhTrace(t)
}
```

The optional `Session` / `Playbook` / `Wiki` / `Repo` / `K8s` fields seed the
matching vault before the launcher boots; `K8sEnvtest` / `Browser` are toggles.
Browser specs live under `browser/specs/`; `h.Browser.Run(t, "<name>.spec.ts")`
runs one against the launched instance.

`Launch` seeds a temp `XDG_CONFIG_HOME`, allocates a free port, execs
`triagent start --profile <path> --port <n>`, waits for `/healthz`, and
registers cleanup that SIGTERMs the launcher and dumps its logs on failure.

## Authoring a claude-stub script

`fixtures/stub-scripts/<scenario>/main.jsonl`, one action per line:

```jsonl
{"action":"record_args"}
{"action":"emit","event":{"type":"assistant_message","text":"working on it"}}
{"action":"emit","event":{"type":"tool_call","name":"summarize","args":{}}}
{"action":"expect_tool_result"}
{"action":"wait_for_signal","name":"regenerate-released"}
{"action":"emit","event":{"type":"end"}}
{"action":"exit","code":0}
```

Emit event types map to claude stream-json: `assistant_message`,
`tool_call`, `tool_result`, `end`. `wait_for_signal` blocks until the test
calls `h.ReleaseSignal(t, name)`.

`expect_tool_call` records that the launcher is expected to dispatch the tool
the stub just emitted (asserted via the trace / transcript).
`expect_tool_result` performs a **real MCP tool round-trip**: the stub is the
MCP client (the role the real `claude` CLI plays), so it spawns the server
named in the launcher's `--mcp-config` and calls the last-emitted `tool_call`
against it, recording the structured result. One MCP session is kept alive
across the turn, so a `switch_context` binding survives into later calls. When
no `--mcp-config` is wired (non-k8s flows), `expect_tool_result` degrades to a
single stdin-yield, so flows that don't need the round-trip are unaffected.

The k8s flow (`investigation_k8s_test.go`) uses this against a real
`triagent-mcp --kind=k8s` backed by an envtest apiserver. Set `K8sEnvtest: true`
(and optionally `K8s: "<scenario>"` under `fixtures/k8s/`) on `Options`; the
harness boots a process-shared envtest, applies the fixture manifests, writes a
static kubeconfig, and points the launcher's `KUBECONFIG` at it. Envtest needs
the kubebuilder apiserver/etcd binaries; without them (`KUBEBUILDER_ASSETS` or
the `setup-envtest` cache) the k8s tests skip via `harness.EnvtestUnavailable()`.

The stub records argv, the resolved system-prompt body, the parsed
allowed-tools, the MCP config JSON, every stdin event, and every tool round-trip
result to `<state_dir>/traces/claude-stub.trace.<role>.<pid>.jsonl`; read it via
`h.StubTrace(t, role)` (results land in `Trace.ToolResults`).

## Authoring a gh-stub script

`fixtures/gh-scripts/<scenario>/responses.json` is an argv-prefix → response
table:

```json
[
  {"argv": ["issue", "list"], "stdout": "[]"},
  {"argv": ["issue", "create"], "stdout": "https://github.com/o/r/issues/1\n"}
]
```

The first entry whose `argv` is a prefix of the real invocation wins.
An unmatched argv exits non-zero naming the argv, so a missing fixture
fails loudly. Every invocation is recorded to
`<state_dir>/traces/gh-stub.trace.jsonl`; read it via `h.GhTrace(t)`.
