package watches

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestItemJSONShape(t *testing.T) {
	it := Item{
		ID:          "01HXY1",
		WatchID:     "abc",
		SourceKind:  string(SourceGitHubIssues),
		CapturedAt:  time.Date(2026, 5, 11, 10, 5, 13, 0, time.UTC),
		SourceRef:   SourceRef{Owner: "example-org", Repo: "example-repo", IssueNumber: 4421},
		Snapshot:    Snapshot{Title: "Engine OOMs", Body: "...", Author: "alice", Labels: []string{"bug"}, URL: "https://github.com/example-org/example-repo/issues/4421"},
	}
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"id":"01HXY1"`, `"watchID":"abc"`, `"sourceKind":"github_issues"`,
		`"capturedAt":"2026-05-11T10:05:13Z"`,
		`"sourceRef":`, `"issueNumber":4421`,
		`"snapshot":`, `"title":"Engine OOMs"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in JSON: %s", want, s)
		}
	}
}

func TestSignalJSONOmitsZeroFields(t *testing.T) {
	s := Signal{
		ID: "S1", WatchID: "abc", CreatedAt: time.Now().UTC(),
		Outcome: OutcomeDismissed, CitedItemIDs: []string{"I1"}, Reason: "noise",
	}
	b, _ := json.Marshal(s)
	str := string(b)
	if strings.Contains(str, "investigationId") || strings.Contains(str, "clusters") || strings.Contains(str, "briefing") {
		t.Errorf("unexpected optional field present: %s", str)
	}
}
