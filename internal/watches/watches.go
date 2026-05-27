// Package watches owns the signal-watches subsystem: per-watch goroutines
// that poll GitHub issues / Slack channels on a fixed cadence, append items
// + signals to per-watch JSONL logs, and (when autoStart is enabled) run a
// short-lived ingestion agent over each poll batch.
//
// See docs/superpowers/specs/2026-05-11-signal-watches-design.md.
package watches

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultPollIntervalSeconds = 300

// SourceKind names a polled source. Only github_issues and slack_channel
// are supported in phase 1; the union is closed deliberately because the
// poller, the storage shape, and the filter schema all branch on it.
type SourceKind string

const (
	SourceGitHubIssues SourceKind = "github_issues"
	SourceSlackChannel SourceKind = "slack_channel"
)

// Watch is the launcher-side configuration for one polled source.
type Watch struct {
	ID          string          `yaml:"id" json:"id"`
	Name        string          `yaml:"name" json:"name"`
	Description string          `yaml:"description,omitempty" json:"description,omitempty"`
	Source      SourceConfig    `yaml:"source" json:"source"`
	Polling     PollingConfig   `yaml:"polling" json:"polling"`
	Ingest      IngestConfig    `yaml:"ingest,omitempty" json:"ingest,omitempty"`
	AutoStart   AutoStartConfig `yaml:"autoStart,omitempty" json:"autoStart,omitempty"`
	CreatedAt   time.Time       `yaml:"createdAt" json:"createdAt"`
	Enabled     bool            `yaml:"enabled" json:"enabled"`
	// Ingesting is a transient runtime flag, not persisted to YAML.
	// Manager.List() populates it from the per-watch poller's atomic
	// ingesting state so the UI can render a spinner while the agent
	// is in flight.
	Ingesting bool `yaml:"-" json:"ingesting,omitempty"`
}

func (w Watch) Key() string { return w.ID }

// PollInterval returns the configured polling cadence, defaulting to
// DefaultPollIntervalSeconds (5 minutes) when unset.
func (w Watch) PollInterval() time.Duration {
	if w.Polling.IntervalSeconds <= 0 {
		return time.Duration(DefaultPollIntervalSeconds) * time.Second
	}
	return time.Duration(w.Polling.IntervalSeconds) * time.Second
}

// SourceConfig is the per-kind source description. Most fields are kind-
// specific and the validator (see Validate) checks for the right shape.
type SourceConfig struct {
	Kind                 SourceKind `yaml:"kind" json:"kind"`
	Owner                string     `yaml:"owner,omitempty" json:"owner,omitempty"`              // github
	Repo                 string     `yaml:"repo,omitempty" json:"repo,omitempty"`                 // github
	Labels               []string   `yaml:"labels,omitempty" json:"labels,omitempty"`             // github (AND)
	States               []string   `yaml:"states,omitempty" json:"states,omitempty"`             // github (open/closed)
	ChannelID            string     `yaml:"channelID,omitempty" json:"channelID,omitempty"`        // slack
	ChannelName          string     `yaml:"channelName,omitempty" json:"channelName,omitempty"`    // slack
	IncludeThreadReplies bool       `yaml:"includeThreadReplies,omitempty" json:"includeThreadReplies,omitempty"`
	Filters              []Filter   `yaml:"filters,omitempty" json:"filters,omitempty"`
}

// Filter is one pre-ingestion predicate. See spec §3.4.
type Filter struct {
	Field string `yaml:"field" json:"field"`
	Op    string `yaml:"op" json:"op"`
	Value string `yaml:"value" json:"value"`
}

// PollingConfig holds the per-watch cadence. Validated to [60,86400] —
// from 1m up to 24h. Cadences longer than an hour are useful for daily
// low-volume digest watches (e.g. a once-a-day sweep of a backlog).
type PollingConfig struct {
	IntervalSeconds int `yaml:"intervalSeconds" json:"intervalSeconds"`
}

// IngestConfig gates the ingestion-agent run and carries its per-watch
// system-prompt addendum. When Enabled is true the poller runs the
// agent on every poll cycle to classify items into rich signals
// (investigation_started, unclear, dismissed). When false the launcher
// falls through to the trivial passthrough, appending one "disabled"
// signal per item — operators using the watch as a passive log.
//
// Independent of AutoStart: an Enabled ingestion run whose AutoStart is
// off produces "proposed" signals (agent recommends investigating, but
// the operator clicks Start).
type IngestConfig struct {
	Enabled            bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	CustomInstructions string `yaml:"customInstructions,omitempty" json:"customInstructions,omitempty"`
}

// AutoStartConfig holds the per-watch auto-start toggle and the
// spawner's concurrency cap.
type AutoStartConfig struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxConcurrent int  `yaml:"maxConcurrent,omitempty" json:"maxConcurrent,omitempty"`
}

const (
	// 5 minutes is the floor because an ingestion agent run typically
	// runs 30–90s; polling faster than that backs up against the
	// per-watch mutex without delivering any signal-emission speedup.
	MinPollIntervalSeconds = 300
	MaxPollIntervalSeconds = 86400
	MaxConcurrentMax       = 10
	CustomInstructionsMax  = 4096
)

// allowedFilterOps lists every filter `op` string accepted by Validate.
var allowedFilterOps = map[string]bool{
	"contains":          true,
	"does_not_contain":  true,
	"regex_matches":     true,
	"not_regex_matches": true,
}

// allowedFilterFields lists per-source-kind which filter `field` values
// are valid. github: title/body/author/label. slack: text/author.
var allowedFilterFields = map[SourceKind]map[string]bool{
	SourceGitHubIssues: {"title": true, "body": true, "author": true, "label": true},
	SourceSlackChannel: {"text": true, "author": true},
}

func validateFilters(kind SourceKind, filters []Filter) error {
	allowed := allowedFilterFields[kind]
	for i, f := range filters {
		if f.Value == "" {
			return fmt.Errorf("filter[%d]: value is empty", i)
		}
		if !allowed[f.Field] {
			return fmt.Errorf("filter[%d]: field %q not allowed for source kind %q (allowed: %v)",
				i, f.Field, kind, sortedKeys(allowed))
		}
		if !allowedFilterOps[f.Op] {
			return fmt.Errorf("filter[%d]: op %q not allowed", i, f.Op)
		}
		if f.Op == "regex_matches" || f.Op == "not_regex_matches" {
			if _, err := regexp.Compile(f.Value); err != nil {
				return fmt.Errorf("filter[%d]: regex compile: %w", i, err)
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate runs the write-time check on a watch config. Loaders (for
// historical user_watches.yaml files) intentionally do not call this —
// we accept older entries that pre-date a rule and only enforce on save.
func (w Watch) Validate() error {
	if strings.TrimSpace(w.Name) == "" {
		return errors.New("watch name is empty")
	}
	switch w.Source.Kind {
	case SourceGitHubIssues:
		if w.Source.Owner == "" || w.Source.Repo == "" {
			return errors.New("github_issues source requires owner and repo")
		}
	case SourceSlackChannel:
		if w.Source.ChannelID == "" {
			return errors.New("slack_channel source requires channelID")
		}
	default:
		return fmt.Errorf("unknown source kind %q", w.Source.Kind)
	}
	if err := validateFilters(w.Source.Kind, w.Source.Filters); err != nil {
		return err
	}
	if w.Polling.IntervalSeconds < MinPollIntervalSeconds || w.Polling.IntervalSeconds > MaxPollIntervalSeconds {
		return fmt.Errorf("polling.intervalSeconds=%d out of range [%d,%d]",
			w.Polling.IntervalSeconds, MinPollIntervalSeconds, MaxPollIntervalSeconds)
	}
	if w.AutoStart.Enabled {
		if w.AutoStart.MaxConcurrent < 1 || w.AutoStart.MaxConcurrent > MaxConcurrentMax {
			return fmt.Errorf("autoStart.maxConcurrent=%d out of range [1,%d]",
				w.AutoStart.MaxConcurrent, MaxConcurrentMax)
		}
	}
	if len(w.Ingest.CustomInstructions) > CustomInstructionsMax {
		return fmt.Errorf("ingest.customInstructions length %d exceeds cap %d",
			len(w.Ingest.CustomInstructions), CustomInstructionsMax)
	}
	return nil
}
