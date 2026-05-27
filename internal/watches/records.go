package watches

import "time"

// Outcome is the closed enum of signal states from spec §3.3.
type Outcome string

const (
	OutcomeDisabled             Outcome = "disabled"
	OutcomeQueued               Outcome = "queued"
	OutcomeInvestigationStarted Outcome = "investigation_started"
	OutcomeProposed             Outcome = "proposed"
	OutcomeUnclear              Outcome = "unclear"
	OutcomeDismissed            Outcome = "dismissed"
	OutcomeFailed               Outcome = "failed"
)

// Item is one raw record polled from a source. Persisted to items.jsonl.
type Item struct {
	ID         string               `json:"id"`
	WatchID    string               `json:"watchID"`
	SourceKind string               `json:"sourceKind"`
	CapturedAt time.Time            `json:"capturedAt"`
	SourceRef  SourceRef            `json:"sourceRef"`
	Snapshot   Snapshot             `json:"snapshot"`
	SignalID   string               `json:"signalID,omitempty"`
	Filtered   *FilteredAnnotation  `json:"filtered,omitempty"`
}

// SourceRef carries the dedupe identity per source kind.
type SourceRef struct {
	Owner       string    `json:"owner,omitempty"`
	Repo        string    `json:"repo,omitempty"`
	IssueNumber int       `json:"issueNumber,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	ChannelID   string    `json:"channelID,omitempty"`
	TS          string    `json:"ts,omitempty"`
}

// Snapshot is the captured-at-poll-time content. Body capped at 4 KB at
// write time; full body never re-fetched (the source URL is preserved).
type Snapshot struct {
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	Text      string   `json:"text,omitempty"`
	Author    string   `json:"author,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	URL       string   `json:"url,omitempty"`
	Permalink string   `json:"permalink,omitempty"`
}

// FilteredAnnotation marks an item that failed the pre-ingestion filter.
type FilteredAnnotation struct {
	RuleIndex int    `json:"ruleIndex"`
	Summary   string `json:"summary"`
}

// Signal is one grouping result the ingestion agent (or the launcher,
// for autoStart=off and orphan-recovery cases) emitted. Mutated via
// append-with-same-id; readers always project latest-row-per-id.
type Signal struct {
	ID                 string    `json:"id"`
	WatchID            string    `json:"watchID"`
	CreatedAt          time.Time `json:"createdAt"`
	CitedItemIDs       []string  `json:"citedItemIDs"`
	Outcome            Outcome   `json:"outcome"`
	InvestigationID    string    `json:"investigationID,omitempty"`
	ManuallyStarted    bool      `json:"manuallyStarted,omitempty"`
	Clusters           []string  `json:"clusters,omitempty"`
	Briefing           string    `json:"briefing,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	AutoMode           bool      `json:"autoMode,omitempty"`
	SlackChannel       string    `json:"slackChannel,omitempty"`
	IncidentioURL      string    `json:"incidentioUrl,omitempty"`
	Repos              []string  `json:"repos,omitempty"`
	DismissedRelatedTo []string  `json:"dismissedRelatedTo,omitempty"`
	DismissedWikiSlugs []string  `json:"dismissedWikiSlugs,omitempty"`
	ErrorMessage       string    `json:"errorMessage,omitempty"`
}
