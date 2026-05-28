//go:build e2e

package main

import "testing"

// matchResponse picks the first entry whose argv tokens are a prefix of the
// real argv. "gh issue create --title X" matches an entry keyed on
// ["issue","create"]. A more specific entry wins when it is listed first,
// so fixtures order specific-before-general.
func TestMatchResponse(t *testing.T) {
	entries := []ghResponse{
		{Argv: []string{"issue", "create"}, Stdout: "https://github.com/o/r/issues/1\n"},
		{Argv: []string{"issue", "list"}, Stdout: `[{"number":1}]`},
		{Argv: []string{"--version"}, Stdout: "gh version 2.0.0\n"},
	}

	cases := []struct {
		name       string
		argv       []string
		wantStdout string
		wantOK     bool
	}{
		{"issue create prefix", []string{"issue", "create", "--title", "x"}, "https://github.com/o/r/issues/1\n", true},
		{"issue list exact", []string{"issue", "list"}, `[{"number":1}]`, true},
		{"version", []string{"--version"}, "gh version 2.0.0\n", true},
		{"unmatched", []string{"pr", "merge"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchResponse(entries, tc.argv)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Stdout != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", got.Stdout, tc.wantStdout)
			}
		})
	}
}

// An entry with no argv tokens never matches — it would otherwise swallow
// every invocation as a prefix of length zero.
func TestMatchResponse_EmptyArgvNeverMatches(t *testing.T) {
	if _, ok := matchResponse([]ghResponse{{Argv: nil, Stdout: "x"}}, []string{"issue", "create"}); ok {
		t.Fatal("empty-argv entry should not match")
	}
}
