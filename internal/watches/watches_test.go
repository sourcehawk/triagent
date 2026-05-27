package watches

import (
	"testing"
	"time"
)

func TestWatchKey(t *testing.T) {
	w := Watch{ID: "0193abcd"}
	if got := w.Key(); got != "0193abcd" {
		t.Fatalf("Key() = %q, want %q", got, "0193abcd")
	}
}

func TestPollingIntervalDefault(t *testing.T) {
	w := Watch{}
	if got := w.PollInterval(); got != 5*time.Minute {
		t.Fatalf("PollInterval() = %v, want 5m", got)
	}
}

func TestPollingIntervalCustom(t *testing.T) {
	w := Watch{Polling: PollingConfig{IntervalSeconds: 60}}
	if got := w.PollInterval(); got != time.Minute {
		t.Fatalf("PollInterval() = %v, want 1m", got)
	}
}

func TestValidateOK_GitHub(t *testing.T) {
	w := Watch{
		ID:   "x",
		Name: "n",
		Source: SourceConfig{
			Kind:   SourceGitHubIssues,
			Owner:  "example-org",
			Repo:   "example-repo",
			Labels: []string{"bug"},
			States: []string{"open"},
		},
		Polling: PollingConfig{IntervalSeconds: 300},
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateOK_Slack(t *testing.T) {
	w := Watch{
		ID:   "x",
		Name: "n",
		Source: SourceConfig{
			Kind:      SourceSlackChannel,
			ChannelID: "C0123",
		},
		Polling: PollingConfig{IntervalSeconds: 600},
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsEmptyName(t *testing.T) {
	w := Watch{ID: "x", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for empty name")
	}
}

func TestValidateRejectsUnknownKind(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: "pagerduty"}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for unknown kind")
	}
}

func TestValidateRejectsGitHubMissingFields(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o"}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing repo")
	}
}

func TestValidateRejectsSlackMissingChannel(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceSlackChannel}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing channelID")
	}
}

func TestValidateRejectsIntervalOutOfRange(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 60}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for interval < 300s (5m)")
	}
	w.Polling.IntervalSeconds = 86401
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for interval > 86400s (24h)")
	}
}

func TestValidateRejectsMaxConcurrentOutOfRange(t *testing.T) {
	for _, n := range []int{0, 11} {
		w := Watch{
			ID: "x", Name: "n",
			Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
			Polling: PollingConfig{IntervalSeconds: 300},
			AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: n},
		}
		if err := w.Validate(); err == nil {
			t.Fatalf("Validate() = nil for MaxConcurrent=%d, want error", n)
		}
	}
}

func TestValidateRejectsCustomInstructionsTooLong(t *testing.T) {
	long := make([]byte, 4097)
	for i := range long {
		long[i] = 'x'
	}
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Ingest: IngestConfig{CustomInstructions: string(long)}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for over-cap customInstructions")
	}
}

func TestValidateFilterFieldsByKind(t *testing.T) {
	g := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r", Filters: []Filter{{Field: "text", Op: "contains", Value: "v"}}}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := g.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error: 'text' not allowed on github source")
	}
	s := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceSlackChannel, ChannelID: "C", Filters: []Filter{{Field: "title", Op: "contains", Value: "v"}}}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error: 'title' not allowed on slack source")
	}
}

func TestValidateFilterRejectsBadOp(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r", Filters: []Filter{{Field: "title", Op: "matches_word", Value: "v"}}}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for unknown op")
	}
}

func TestValidateFilterRejectsBadRegex(t *testing.T) {
	w := Watch{ID: "x", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r", Filters: []Filter{{Field: "title", Op: "regex_matches", Value: "[broken"}}}, Polling: PollingConfig{IntervalSeconds: 300}}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for bad regex")
	}
}
