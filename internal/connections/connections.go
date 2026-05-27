// Package connections is the launcher's small token wallet for third-party
// integrations (Slack, incident.io). The launcher reads tokens from this
// package when it spawns a per-session MCP config; the HTTP API mutates
// them when an operator pastes a new token in the Connections panel.
//
// Storage is a single JSON file at <dir>/credentials.json with mode 0600.
// Environment variables override the file at read time so a launcher
// invocation can be bootstrapped on the command line without touching the
// settings UI:
//
//	TRIAGENT_SLACK_TOKEN
//	TRIAGENT_INCIDENTIO_TOKEN
package connections

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Kind names a connectable third-party integration. Used by the HTTP API
// for the kind path segment in /api/connections/{kind}.
type Kind string

const (
	KindSlack      Kind = "slack"
	KindIncidentio Kind = "incidentio"
)

// Status is the redacted view of which integrations have a token on file
// (or in env). Never carries the token itself — the panel only needs to
// know whether the pill should be green.
type Status struct {
	Slack      bool `json:"slack"`
	Incidentio bool `json:"incidentio"`
}

// Manager owns the credentials file. Concurrency-safe; writes serialize
// through a single mutex so a panic mid-write can't corrupt the file
// (atomic rename + 0600 perms keep it readable only by the operator).
type Manager struct {
	mu  sync.RWMutex
	dir string
}

// fileShape is the on-disk JSON. Field tags match the panel's expectations
// — keep stable for forward compatibility when more integrations land.
type fileShape struct {
	Slack             string `json:"slack_token,omitempty"`
	SlackWorkspaceURL string `json:"slack_workspace_url,omitempty"`
	Incidentio        string `json:"incidentio_token,omitempty"`
}

const (
	envSlack      = "TRIAGENT_SLACK_TOKEN"
	envIncidentio = "TRIAGENT_INCIDENTIO_TOKEN"
)

// NewWithDir constructs a Manager rooted at dir. The dir is created on
// first write; this is exported so tests can hand it a t.TempDir.
func NewWithDir(dir string) *Manager { return &Manager{dir: dir} }

func (m *Manager) path() string { return filepath.Join(m.dir, "credentials.json") }

func (m *Manager) load() (fileShape, error) {
	b, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return fileShape{}, nil
	}
	if err != nil {
		return fileShape{}, err
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return fileShape{}, fmt.Errorf("parse credentials.json: %w", err)
	}
	return f, nil
}

// save writes the file atomically with mode 0600. Same write pattern used
// by persist.go's writePersistedMetadata: write to a sibling .tmp, then
// rename so a partial write can't surface to a concurrent reader.
func (m *Manager) save(f fileShape) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path())
}

// GetSlackToken returns the Slack token, preferring env over file.
func (m *Manager) GetSlackToken() (string, error) {
	if v := os.Getenv(envSlack); v != "" {
		return v, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, err := m.load()
	return f.Slack, err
}

// SetSlackToken validates a Slack user/bot token and persists it alongside
// the workspace URL (captured from auth.test by the caller). Tokens in
// the wild start with xoxp- (user) or xoxb- (bot); rejecting other prefixes
// catches a paste of the workspace URL or a Slack app ID early. The
// workspace URL is optional — empty is accepted and means the picker's URL
// derivation will fall back to ID-only (see spec).
func (m *Manager) SetSlackToken(tok, workspaceURL string) error {
	if !strings.HasPrefix(tok, "xoxp-") && !strings.HasPrefix(tok, "xoxb-") {
		return errors.New("slack token must start with xoxp- or xoxb-")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.load()
	if err != nil {
		return err
	}
	f.Slack = tok
	f.SlackWorkspaceURL = strings.TrimRight(workspaceURL, "/")
	return m.save(f)
}

// GetSlackWorkspaceURL returns the workspace URL captured at token-set time
// (e.g. "https://example.slack.com"). Empty when no token is set or auth.test
// failed when the token was stored.
func (m *Manager) GetSlackWorkspaceURL() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, err := m.load()
	return f.SlackWorkspaceURL, err
}

// GetIncidentioToken returns the incident.io API key, env-first.
func (m *Manager) GetIncidentioToken() (string, error) {
	if v := os.Getenv(envIncidentio); v != "" {
		return v, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, err := m.load()
	return f.Incidentio, err
}

// SetIncidentioToken persists an incident.io API key. No prefix check;
// the caller is expected to have round-tripped the key through the
// identity endpoint first so a malformed key never reaches this point.
func (m *Manager) SetIncidentioToken(tok string) error {
	if strings.TrimSpace(tok) == "" {
		return errors.New("incidentio token must be non-empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.load()
	if err != nil {
		return err
	}
	f.Incidentio = tok
	return m.save(f)
}

// Clear removes the token for the given kind. Idempotent — clearing an
// already-empty slot is a no-op success. The env override survives a
// Clear; that's intentional, since env tokens are bootstrapped at launch
// and the panel can't disable them without restarting the launcher.
func (m *Manager) Clear(kind Kind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.load()
	if err != nil {
		return err
	}
	switch kind {
	case KindSlack:
		f.Slack = ""
	case KindIncidentio:
		f.Incidentio = ""
	default:
		return fmt.Errorf("unknown connection kind: %q", kind)
	}
	return m.save(f)
}

// Status is the panel's view: which integrations are connected. Reads
// both env and file so an env-only operator still sees a green pill.
func (m *Manager) Status() Status {
	slack, _ := m.GetSlackToken()
	incidentio, _ := m.GetIncidentioToken()
	return Status{
		Slack:      slack != "",
		Incidentio: incidentio != "",
	}
}
