package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// shareBundleSchemaVersion is the wire schema for exported investigations.
// Bumped when the on-disk shape we ship changes in a non-additive way.
const shareBundleSchemaVersion = 1

// ShareBundle is the JSON file shape an operator downloads for an
// investigation and uploads to a teammate's launcher. Local-only details
// (MCP config path, linked-repo paths, kubeconfig context, on-disk session
// dir) are intentionally absent: the receiving instance can't use them and
// they would leak filesystem layout from the exporter.
type ShareBundle struct {
	SchemaVersion int             `json:"schemaVersion"`
	ExportedAt    time.Time       `json:"exportedAt"`
	Source        ImportedFrom    `json:"source"`
	Events        []EventEnvelope `json:"events"`
}

// ImportedFrom is the provenance record carried in a share bundle and
// preserved on the receiver's imported investigation, so the SPA can render
// an "imported from <ctx>/<ns>" badge in the session header. Strictly
// non-sensitive fields only — no MCP/kubeconfig/linked-repo details.
type ImportedFrom struct {
	InvestigationID string    `json:"investigationId,omitempty"`
	Namespace       string    `json:"namespace"`
	Label            string    `json:"label,omitempty"`
	IncidentURL      string    `json:"incidentUrl,omitempty"`
	SlackChannelURL  string    `json:"slackChannelUrl,omitempty"`
	SlackChannelID   string    `json:"slackChannelId,omitempty"`
	SlackChannelName string    `json:"slackChannelName,omitempty"`
	Notes            string    `json:"notes,omitempty"`
	CreatedAt       time.Time `json:"createdAt,omitempty"`
}

// buildShareBundle reads metadata.json + events.jsonl from a session dir
// and assembles a redacted ShareBundle. Local-only persistence fields are
// not copied across.
func buildShareBundle(dir string) (*ShareBundle, error) {
	body, err := os.ReadFile(filepath.Join(dir, fileMetadata))
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	var meta persistedMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	events, err := loadEvents(dir)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	if events == nil {
		events = []EventEnvelope{}
	}
	created, _ := parseTimestamp(meta.CreatedAt) // best-effort; zero is acceptable

	// If the source itself was imported, hop through to the original
	// provenance — sharing a re-shared investigation should still surface
	// where it actually came from rather than the intermediate launcher.
	source := ImportedFrom{
		InvestigationID:  meta.ID,
		Namespace:        meta.Namespace,
		Label:            meta.Label,
		IncidentURL:      meta.IncidentURL,
		SlackChannelURL:  meta.SlackChannelURL,
		SlackChannelID:   meta.SlackChannelID,
		SlackChannelName: meta.SlackChannelName,
		Notes:            meta.Notes,
		CreatedAt:        created,
	}
	if meta.ImportedFrom != nil {
		source = *meta.ImportedFrom
	}

	return &ShareBundle{
		SchemaVersion: shareBundleSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Source:        source,
		Events:        events,
	}, nil
}

// decodeShareBundle parses a ShareBundle from a reader and validates the
// schema version. Caller is responsible for limiting the reader's size if
// the source is untrusted (e.g. a multipart upload handler).
func decodeShareBundle(r io.Reader) (*ShareBundle, error) {
	var b ShareBundle
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return nil, fmt.Errorf("parse bundle: %w", err)
	}
	if b.SchemaVersion != shareBundleSchemaVersion {
		return nil, fmt.Errorf("unsupported share bundle schema version %d (expected %d)", b.SchemaVersion, shareBundleSchemaVersion)
	}
	// Namespace is genuinely optional: preflight only requires "at least
	// one of (cluster, incident URL, slack channel, notes)", so a Slack-
	// alert-triggered session can land here with source.namespace empty.
	return &b, nil
}

// materializeImport writes a ShareBundle as a fresh on-disk session
// directory under root: metadata.json (with importedFrom/importedAt baked
// in so the SPA can render the provenance) and events.jsonl. Returns the
// new dir + investigation id; caller hands the dir to manager.AdoptFromDir
// so the inv surfaces in the sidebar.
//
// We preserve the source's InvestigationID and CreatedAt so the imported
// session's slug (date + namespace + id-prefix) matches the slug it would
// have on upstream. That match is what makes the upstream-pull flow in
// SessionDoc.openTranscript recognise an already-imported session by slug
// instead of re-importing the bundle on every click. It also lets
// ReconcileUpstreamPushed backfill PushedAt by finding the matching
// session.md in the upstream clone — which is what drives the "synced"
// checkmark in the sidebar.
func materializeImport(root string, b *ShareBundle) (string, string, error) {
	if b == nil {
		return "", "", errors.New("nil bundle")
	}
	dir, err := makeSessionDir(root)
	if err != nil {
		return "", "", fmt.Errorf("session dir: %w", err)
	}

	importedAt := time.Now().UTC()
	src := b.Source
	srcCopy := src
	// Bundles built before the slug-preserving change may have an empty
	// InvestigationID; fall back to a fresh id rather than refusing the
	// import. CreatedAt similarly falls back to importedAt when the
	// bundle didn't carry it (older sources, or a source whose metadata
	// had an unparseable createdAt).
	id := src.InvestigationID
	if id == "" {
		id, err = NewID()
		if err != nil {
			return "", "", fmt.Errorf("generate id: %w", err)
		}
	}
	createdAt := src.CreatedAt
	if createdAt.IsZero() {
		createdAt = importedAt
	}
	meta := persistedMetadata{
		ID:               id,
		Namespace:        src.Namespace,
		Label:            src.Label,
		IncidentURL:      src.IncidentURL,
		SlackChannelURL:  src.SlackChannelURL,
		SlackChannelID:   src.SlackChannelID,
		SlackChannelName: src.SlackChannelName,
		Notes:            src.Notes,
		SessionDir:       dir,
		CreatedAt:        createdAt.Format("2006-01-02T15:04:05Z07:00"),
		ImportedFrom:     &srcCopy,
		ImportedAt:       importedAt,
	}
	if err := writePersistedMetadata(dir, meta); err != nil {
		return "", "", err
	}
	if err := writeEventsFile(dir, b.Events); err != nil {
		return "", "", err
	}
	return dir, id, nil
}
