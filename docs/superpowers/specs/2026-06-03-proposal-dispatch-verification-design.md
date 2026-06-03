# Verifying proposal-dispatch terminals (closing the confabulation window)

## Problem

The `wiki_proposal` and `playbook_proposal` flows run in **dispatch mode** (`Dispatch == DispatchSubagent`): `walk_playbook` hands the flow to a free-form sub-agent as a prompt (`runDispatch` → `BuildDispatchPrompt`) and trusts it to call the terminal `*_proposal_draft` tool at the end. Nothing forces that terminal call. The walker is a *suggester*, not an enforcer ([ADR-0004](../../adrs/0004-walker-semantics.md)), and dispatch mode isn't even walked — so when the sub-agent diverges (writes the draft to a file, returns a prose summary) it ends without producing a proposal. `subagent.Result` carries only the prose `Summary`, and the master agent reads that summary as a successful submission. No proposal exists; the operator never sees one. This is the **confabulation window** the dispatch code already names ([`dispatch.go`](../../../pkg/mcp/strategies/dispatch.go)).

Observed in investigation `9ad42ab8…`: the `playbook_proposal` sub-agent used only `Bash`/`Write`/`ToolSearch`, wrote YAML to `/tmp`, returned a prose summary, and the master reported "the playbook proposal has been drafted." It never called `playbook_proposal_draft`.

A prior increment shipped the **explicit-terminal + prompt** half (commit `e55ef9f`): a `decline_proposal` tool (the verifiable "below the bar, no proposal" terminal), a mandatory `## Finishing` section in the dispatch prompt naming the submit tool and forbidding a prose/file ending, the decline tool in both sub-agents' allowlists, and a 10→15 min timeout. That makes the agent *more likely* to reach a terminal, but it is still prompt-level — nothing checks that it actually did. This spec is the load-bearing **verify** half.

## Goals

- Detect, at the dispatch boundary, whether the sub-agent actually called a terminal tool (a successful `*_proposal_draft` **or** `decline_proposal`).
- When it called neither, **resume the same conversation and force a terminal** (bounded retries), so a diverged sub-agent gets a second chance with its drafted context intact.
- Surface the outcome to the master agent as structured data (`submitted` / `declined` / `none`) so a missing proposal can never read as success.

## Non-goals

- Making the walker enforce steps — that contradicts ADR-0004's locked "suggester, not state machine" semantics.
- Converting the proposal flows to walked sub-agents — considered and rejected: it's a bigger refactor (nested walker session, recursion into the strategies MCP, more round-trips) and still wouldn't guarantee the terminal, because the walker only suggests.
- Forcing a proposal unconditionally — "no proposal (below the bar)" is a legitimate outcome (`terminal_no_proposal`); `decline_proposal` is its verifiable form.

## Design

### 1. Terminal-tool detection in `subagent`

`subagent.Options` gains `RequiredTerminalTools []string` — the full wire names that count as a valid terminal for this run (empty = no verification, current behaviour). `subagent.Result` gains `TerminalToolsCalled []string` — which of them the sub-agent invoked with a **successful** result.

`relaySubEvents` already parses the sub-agent's stream-json and forwards each `tool_use` / `tool_result` block. It will additionally correlate them: when a `tool_use` block's `Name` is in the required set, record its block `ID`; when a `tool_result` block's `ToolUseID` matches a recorded id and `IsError == false`, mark that tool name as successfully called. A tool that errored (e.g. `playbook_proposal_draft` returning validation errors, or `decline_proposal` rejecting an empty reason) does **not** count — it isn't a completed terminal.

`relaySubEvents`'s return is refactored from `(finalText, sessionID string, err error)` to a small `relayResult{finalText, sessionID string, terminalsCalled map[string]bool}` plus `error`, so the detection rides alongside the existing aggregation in one pass. Only `Run` calls it; the change is in-package.

### 2. Verification + bounded resume-force in `runDispatch`

A new `dispatchTerminalToolsFor(playbookID) []string` returns the valid terminal set per flow (full wire names):

- `playbook_proposal` → `[…__playbook_proposal_draft, …__decline_proposal]`
- `wiki_proposal` → `[…__propose_wiki_draft, …__decline_proposal]`
- otherwise `nil` (no verification)

`runDispatch` passes that set as `Options.RequiredTerminalTools`. After the run, if the set is non-empty and no terminal fired and the run did not time out, it **resumes the same conversation** (`Options.ResumeSessionID = res.SessionID`) with a forcing follow-up prompt and re-checks, up to `maxForceDispatchRetries = 2` times:

> You ended without reaching a terminal. You MUST now call either `<submit tool>` to submit the draft you prepared, or `decline_proposal` with a one-line reason. Call the tool now — do not reply with prose.

A timed-out run is **not** retried (the cap fired mid-work; retrying compounds it) — it surfaces as outcome `none` with `TimedOut` set, which the master already special-cases.

`runDispatch` returns the final `subagent.Result` plus a `ProposalOutcome` classified from `TerminalToolsCalled`: the submit tool present → `submitted`; else `decline_proposal` present → `declined`; else `none`.

### 3. Structured outcome on `DispatchedResult`

`DispatchedResult` (the `walk_playbook` dispatch response the master reads) gains `ProposalOutcome string` — `submitted` | `declined` | `none`. `walkPlaybook` sets it from `runDispatch`. When the outcome is `none`, `walkPlaybook` also prefixes the `Summary` with an explicit `NO PROPOSAL WAS SUBMITTED — the sub-agent ended without calling a terminal tool` line, so even a master that ignores the structured field cannot read the prose as success. `submitted` / `declined` pass the sub-agent summary through unchanged.

## Testing

- **`subagent` detection** (`relaySubEvents` direct, in-package, fed a `strings.Reader` of stream-json): a successful terminal `tool_use` + non-error `tool_result` → present in `terminalsCalled`; an error result → absent; a non-terminal tool → absent; no terminal call → empty.
- **`runDispatch` retry** (fake `subAgentRunner`): no terminal on the first run then a terminal on resume → retried once, `ResumeSessionID` threaded, outcome `submitted`; terminal on the first run → no resume; decline terminal → outcome `declined`; never a terminal across all attempts → outcome `none`; timed-out run → no resume, outcome `none` + `TimedOut`.
- **`walkPlaybook` surfacing**: `DispatchedResult.ProposalOutcome` is set, and a `none` outcome prefixes the no-proposal line onto the summary.

## Files

- `pkg/mcp/subagent/subagent.go` — `Options.RequiredTerminalTools`, `Result.TerminalToolsCalled`, `relaySubEvents` correlation + `relayResult`.
- `pkg/mcp/strategies/dispatch.go` — `dispatchTerminalToolsFor`, `maxForceDispatchRetries`, the verify+resume loop, `ProposalOutcome` classification.
- `pkg/mcp/strategies/server.go` — `DispatchedResult.ProposalOutcome`; `walkPlaybook` wiring + the `none` summary prefix.
- Tests alongside each.
