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

```go
func TestSomething(t *testing.T) {
    h := harness.Launch(t, harness.Options{
        Profile:    "minimal",            // fixtures/profiles/<name>/
        StubScript: "my-flow",            // fixtures/stub-scripts/<name>/main.jsonl
        GhScript:   "my-flow",            // fixtures/gh-scripts/<name>/responses.json
    })
    profile, version := h.Client.Healthz(t)
    // ... assert against h.Client, h.StubTrace(t, "main"), h.GhTrace(t)
}
```

`Launch` seeds a temp `XDG_CONFIG_HOME`, allocates a free port, execs
`triagent start --profile <path> --port <n>`, waits for `/healthz`, and
registers cleanup that SIGTERMs the launcher and dumps its logs on failure.

## Authoring a claude-stub script

`fixtures/stub-scripts/<test>/main.jsonl`, one action per line:

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
calls `h.ReleaseSignal(t, name)`. `expect_tool_call` / `expect_tool_result`
yield by reading one stdin line (the real MCP round-trip wiring lands with
the k8s flow).

The stub records argv, the resolved system-prompt body, the parsed
allowed-tools, the MCP config JSON, and every stdin event to
`<state_dir>/traces/claude-stub.trace.<role>.<pid>.jsonl`; read it via
`h.StubTrace(t, role)`.

## Authoring a gh-stub script

`fixtures/gh-scripts/<test>/responses.json` is an argv-prefix → response
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
