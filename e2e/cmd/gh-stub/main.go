//go:build e2e

// Command gh-stub stands in for the `gh` CLI on $PATH during the e2e suite.
// It matches the invocation argv against a per-test responses table
// (fixtures/gh-scripts/<test>/responses.json), prints the matched JSON to
// stdout, and records every invocation to gh-stub.trace.jsonl. An unmatched
// argv exits non-zero with a message naming the argv, so a missing fixture
// surfaces as a loud test failure rather than a silent fallback.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ghResponse is one entry of the responses table. Argv is a token list that
// must be a prefix of the real invocation argv; Stdout is written verbatim;
// ExitCode defaults to 0.
type ghResponse struct {
	Argv     []string `json:"argv"`
	Stdout   string   `json:"stdout"`
	ExitCode int      `json:"exitCode,omitempty"`
}

func main() {
	argv := os.Args[1:]
	recordInvocation(argv)

	entries, err := loadResponses()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gh-stub:", err)
		os.Exit(1)
	}

	resp, ok := matchResponse(entries, argv)
	if !ok {
		fmt.Fprintf(os.Stderr, "gh-stub: no matching response for argv %q\n", argv)
		os.Exit(1)
	}
	fmt.Print(resp.Stdout)
	os.Exit(resp.ExitCode)
}

// matchResponse returns the first entry whose Argv tokens are a prefix of
// the invocation argv. An entry with no tokens never matches.
func matchResponse(entries []ghResponse, argv []string) (ghResponse, bool) {
	for _, e := range entries {
		if isPrefix(e.Argv, argv) {
			return e, true
		}
	}
	return ghResponse{}, false
}

// isPrefix reports whether prefix is a non-empty leading subsequence of
// argv.
func isPrefix(prefix, argv []string) bool {
	if len(prefix) == 0 || len(prefix) > len(argv) {
		return false
	}
	for i, tok := range prefix {
		if argv[i] != tok {
			return false
		}
	}
	return true
}

// loadResponses reads the per-test responses table. GH_STUB_SCRIPT is the
// per-test directory under fixtures/gh-scripts/<test>/; the stub reads
// responses.json from it. An unset script dir means no fixtures are wired
// (e.g. the launcher's startup gh capability probe in a test that doesn't
// script gh) — return an empty table so the unmatched-argv path fires.
func loadResponses() ([]ghResponse, error) {
	dir := os.Getenv("GH_STUB_SCRIPT")
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "responses.json"))
	if err != nil {
		return nil, fmt.Errorf("read responses.json: %w", err)
	}
	var entries []ghResponse
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse responses.json: %w", err)
	}
	return entries, nil
}

// recordInvocation appends one trace line per invocation to
// <GH_STUB_TRACE_DIR>/gh-stub.trace.jsonl. Tests assert (a) what the page
// rendered came from the stub and (b) mutating subcommands carried the
// expected body. Best-effort: an unset/unwritable trace dir is silent so it
// never masks the real assertion.
func recordInvocation(argv []string) {
	dir := os.Getenv("GH_STUB_TRACE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "gh-stub.trace.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(map[string]any{"argv": argv})
}
