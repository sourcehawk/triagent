---
name: testing-a-feature
description:
  Use when writing tests for any non-trivial code change — before
  deciding which assertions to add.
---

# testing-a-feature

## When to invoke

Whenever you write a test. Especially when:
- You just wrote a function and are about to "make sure it works."
- You're tempted to copy an assertion from an existing test "to match the style."
- You're about to rewrite a test because the implementation changed.

Skip for generated test scaffolds where the assertions come straight from a tool you trust.

## Core principle

**Tests assert intent, not implementation.** The intent lives in the docstring, the function comment, the spec, the
issue's acceptance criteria — the public contract of the surface under test. The implementation is the body of the
function.

When an implementation changes but the contract doesn't, the tests should not change. When the contract changes, the
tests change first (per TDD) and the implementation follows.

This is what makes a docstring valuable: it's the **black box** the tests verify against. Without an intentional
docstring, you have nothing but the code to test against, and tests degrade into change-detectors.

## Workflow

### 1. Re-read the contract

Before adding any assertion, open the surface under test and read its docstring / contract. If the docstring is
absent or vague, that's the first bug to fix — a function without a contract can't be tested against intent.

For triagent specifically:
- Go exported functions: the godoc comment is the contract.
- Frontend exported hooks / utils: the JSDoc / type signature is the contract.
- MCP tools: the `Description:` field in `specs.go` (or `mcp.AddTool(...)` `Description`) is the contract for callers.
- Walkers / playbooks: the YAML `description:` field is the contract.

### 2. Self-review the docstring against intent

A clean docstring should already enumerate the surface's promises. If reading it raises questions ("what happens when
N is zero?", "does this retry on failure?", "is the result sorted?"), the docstring is incomplete. **Fix the
docstring first**, then write the tests. Docstring-first surfaces the edge cases before any assertion is written.

### 3. List edge cases

For each behavior the contract promises, ask:

- **Happy path** — the documented success case. Always test.
- **Boundary inputs** — zero, one, many; empty string, single char, max length; nil vs empty slice.
- **Error paths** — every error the contract names. Every error the contract _doesn't_ name but the code clearly
  can return (then either name it in the docstring or make the code not return it).
- **Invariants** — what should never happen, regardless of input. Concurrency safety, idempotency, atomicity.
- **Failure recovery** — partial failure mid-call, retry semantics, what state survives.

Don't test for behavior the contract doesn't promise. If you're tempted to, the contract is incomplete — fix the
contract first.

### 4. Write one test per edge case

Each test name reads as a contract statement: `TestStore_PersistsMultipleSessionsAcrossRestart`,
`TestGetChannelID_MissingScopeIsActionable`. The name describes the intent being verified, not the function being
called. Each test's assertion is what the contract promises, not what the implementation happens to do today.

### 5. When tempted to rewrite a test

Stop. Ask: did the **contract** change, or just the implementation?

- **Contract changed** → rewrite the test first, watch it fail for the right reason, then update the implementation.
- **Implementation changed** → the test should still pass. If it doesn't, either the test was coupled to
  implementation details (bad test, fix it) or the implementation regressed the contract (bad change, revert it).

Rewriting a test "to match" a new implementation when the contract is unchanged decouples the test from intent and
silently weakens the suite.

## Edge-case discovery checklist

Apply per surface, per change:

- [ ] **Happy path** — documented success case.
- [ ] **Empty / zero / nil inputs** — what does the contract say happens? If it doesn't say, fix the contract.
- [ ] **Boundary values** — first / last / single-element / off-by-one neighbors.
- [ ] **Error paths named in the contract** — each one triggered and asserted.
- [ ] **Concurrency** — race detector clean (Go: `-race`); concurrent writers if the contract promises safety.
- [ ] **Idempotency** — does calling twice produce the same result the contract promises?
- [ ] **Persistence** — does on-disk / on-network state survive restart, if the contract says so?
- [ ] **Partial failure** — what state survives a mid-call error?

## Anti-patterns

- **Testing the implementation, not the intent.** A test that breaks when a private helper is renamed is testing
  implementation, not contract.
- **Copy-pasting an assertion "to match the file's style"** without re-verifying that the asserted behavior is what
  the contract under test actually promises.
- **One mega-test per function.** One assertion per intent. Mega-tests fail with one signal even when N intents are
  broken.
- **Testing private functions directly.** If the contract is private, the test is testing implementation. Drive the
  private code via the public surface.
- **Rewriting a test to make it pass against a changed implementation, without verifying the contract changed.** This
  is how regressions slip through.
- **Skipping edge cases because "they're unlikely."** A reviewer asks "what happens when N=0?" — the test should
  answer.

## Red flags

| Thought                                                          | Reality                                                                                            |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| "The function is too small to test"                              | If it's worth writing, its contract is worth asserting.                                            |
| "The test is fragile, let me loosen the assertion"               | Fragile = coupled to implementation. Tighten the contract or rewrite the test against intent.      |
| "I'll skip the nil case, the caller will always pass non-nil"    | The contract should say "never call with nil" then. If it doesn't, test the nil case.              |
| "The docstring is wrong but the implementation is right"         | Fix the docstring (or the implementation, if the docstring is the source of truth) before testing. |
| "Rewriting the test to match the new code is faster"             | And weakens the suite silently. Verify the contract changed first.                                 |
| "Edge cases can wait for the next PR"                            | They get forgotten. List them now even if you don't write them all.                                |
