package watches

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEmptyFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadWatches(filepath.Join(dir, "user_watches.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d watches, want 0", len(got))
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user_watches.yaml")
	in := []Watch{
		{
			ID:        "abc",
			Name:      "c1 triage",
			Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "example-org", Repo: "example-repo", Labels: []string{"bug"}, States: []string{"open"}},
			Polling:   PollingConfig{IntervalSeconds: 300},
			CreatedAt: time.Now().UTC().Truncate(time.Second),
			Enabled:   true,
			AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		},
	}
	if err := SaveWatches(path, in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWatches(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d watches, want 1", len(got))
	}
	if got[0].ID != in[0].ID || got[0].Source.Kind != SourceGitHubIssues {
		t.Fatalf("round-trip mismatch: got %+v", got[0])
	}
}
