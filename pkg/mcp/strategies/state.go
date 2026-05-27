package strategies

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DelegateFrame is one entry on a session's call stack. Pushed when
// the walker enters a delegate_to node; popped when the delegate
// reaches a non-handoff terminal. Restores the parent playbook id and
// the delegate node id (so the parent's `next` branches are evaluable
// after pop).
type DelegateFrame struct {
	ParentPlaybookID string `json:"parent_playbook_id"`
	ReturnNodeID     string `json:"return_node_id"`
}

// Session is the per-investigation state the walker tracks. Persisted as
// JSON to <session_dir>/strategy.json after every transition so the walker
// can be restarted (e.g., across `--resume`) without losing context.
type Session struct {
	ID         string `json:"id"`
	PlaybookID string `json:"playbook_id"`
	// ParentSessionID links this session to the one that handed off to
	// it (set when walk_playbook was called with parent_session_id
	// after a terminal node's handoff). The walker walks this chain to
	// reject circular handoffs (A → B → A …). Empty for top-level
	// sessions.
	ParentSessionID string         `json:"parent_session_id,omitempty"`
	ClusterID       string         `json:"cluster_id"`
	Namespace       string         `json:"namespace"`
	Notes           string         `json:"notes,omitempty"`
	CurrentNode     string         `json:"current_node"`
	Visited         []string       `json:"visited"`
	Findings        map[string]any `json:"findings"`
	RecordedCalls   []RecordedCall `json:"recorded_calls"`
	StartedAt       time.Time      `json:"started_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	// CallStack records active delegate_to frames. Each frame names the
	// parent playbook id and the delegate node id to resume at when the
	// delegated walk reaches a non-handoff terminal. Empty for non-delegated
	// flows; persisted as part of the session snapshot.
	CallStack []DelegateFrame `json:"call_stack,omitempty"`
}

// RecordedCall is the audit-trail entry for one tool invocation the agent
// made on behalf of the walker.
type RecordedCall struct {
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args,omitempty"`
	FindingKey string         `json:"finding_key,omitempty"`
	At         time.Time      `json:"at"`
}

// store keeps sessions in memory and snapshots them to disk after each change.
// SessionDir may be empty — in that case the store skips persistence (useful
// for tests and for one-shot invocations).
type store struct {
	mu         sync.Mutex
	dir        string
	byID       map[string]*Session
	persistOne bool // when true, only one session at a time exists in dir; file is overwritten.
}

func newStore(dir string) *store {
	s := &store{
		dir:        dir,
		byID:       make(map[string]*Session),
		persistOne: dir != "",
	}
	s.loadFromDisk()
	return s
}

func (s *store) loadFromDisk() {
	if s.dir == "" {
		return
	}
	path := filepath.Join(s.dir, "strategy.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // no prior state; that's fine
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return // corrupted; ignore rather than crash
	}
	s.byID[sess.ID] = &sess
}

func (s *store) snapshot(sess *Session) error {
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", s.dir, err)
	}
	path := filepath.Join(s.dir, "strategy.json")
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (s *store) create(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[sess.ID]; exists {
		return fmt.Errorf("session %s already exists", sess.ID)
	}
	s.byID[sess.ID] = sess
	return s.snapshot(sess)
}

func (s *store) get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return sess, nil
}

func (s *store) update(id string, mutate func(*Session)) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	mutate(sess)
	sess.UpdatedAt = time.Now().UTC()
	if err := s.snapshot(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// newSessionID returns a short random hex string. Sessions are scoped to one
// investigation; collisions are vanishingly unlikely at this scale.
func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
