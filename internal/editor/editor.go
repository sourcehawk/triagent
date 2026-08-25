// Package editor hosts editor chat sessions: cluster-less authoring
// assistants that live behind the playbook editor's chat drawer (and,
// once the wiki frontend is mounted, the wiki editor's drawer too).
// One Session is one chat — keyed by Subject (a playbook id+version, or
// a wiki incident/entity id). The package reuses the launcher's
// claude.Session machinery and the strategies / wiki MCP servers, but
// skips investigation-only context (k8s, prom).
//
// Sessions also carry an optional Sources block (Slack channel,
// incident.io incident) — when set, the per-session mcp.json picks up
// the corresponding triagent-slack / triagent-incidentio MCP servers and the
// authoring agent ingests context directly from those upstreams.
package editor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/prompts"
)

// Re-exports so callers (handlers, tests) can name subjects without
// importing prompts directly. Keeps the editor package's surface
// self-contained.
type (
	Subject         = prompts.Subject
	PlaybookSubject = prompts.PlaybookSubject
	WikiSubject     = prompts.WikiSubject
	Sources         = prompts.Sources
)

// ErrSessionNotFound is returned by Manager.Rebind (and any future
// id-based lookups that error rather than return nil) when no session
// matches the given id. Use errors.Is for matching.
var ErrSessionNotFound = errors.New("editor session not found")

// ErrSessionExists is returned by Manager.Rebind when the target
// Subject.Key() is already bound to a different session — callers
// decide whether to navigate to the existing session or surface.
var ErrSessionExists = errors.New("editor session already exists for that subject")

// Event is the in-package envelope for one transcript event. The
// server layer translates it to/from the SSE wire shape; keeping the
// type local means the editor package doesn't depend on internal/server.
type Event struct {
	Seq          int            `json:"seq"`
	Kind         string         `json:"kind"`
	Timestamp    time.Time      `json:"timestamp"`
	SessionID    string         `json:"sessionId,omitempty"`
	Subtype      string         `json:"subtype,omitempty"`
	Text         string         `json:"text,omitempty"`
	ToolID       string         `json:"toolId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	ToolInput    map[string]any `json:"toolInput,omitempty"`
	ParentToolID string         `json:"parentToolId,omitempty"`
}

// Event kinds. Mirror internal/server's vocabulary so the same
// frontend reducer works for both transcripts.
const (
	KindSystem     = "system"
	KindAssistant  = "assistant"
	KindToolUse    = "tool_use"
	KindToolResult = "tool_result"
	KindToolStatus = "tool_status" // synthetic: a sub-agent emitted a STATUS marker
	KindResult     = "result"
	KindError      = "error"
	KindUser       = "user"
	KindEnd        = "end"
)

// Session is one editor chat session. Frozen to a single Subject at
// creation time — switching subject closes the session (handled by the
// manager). Resumable across drawer toggles via the manager; the
// explicit X button is what closes it.
type Session struct {
	// Immutable after registration; safe to read without the lock.
	ID string
	// Subject is mutable only via Manager.Rebind under the manager lock;
	// readers outside the manager must not assume immutability.
	Subject                Subject
	Sources                Sources
	MCPConfigPath          string
	LinkedRepos            []repos.LinkedRepo         // mirrors investigation linked-repo set so triagent-git tools are available for authoring research
	ToolCatalog            []prompts.ToolCatalogEntry // full triagent-mcp tool catalog so the agent can reference any server's tools in suggested_calls
	SlackMCPAvailable      bool                       // triagent-slack wired in mcp.json (slack token linked)
	IncidentioMCPAvailable bool                       // triagent-incidentio wired in mcp.json (incidentio token linked)
	Profile                *profile.Profile           // supplies editor.md / wiki_editor.md prompt content
	SessionDir             string
	CreatedAt              time.Time

	// Lifetime; cancelled on Close.
	ctx    context.Context
	cancel context.CancelFunc

	// Forward, when set, is called by publish() with each event
	// alongside the per-session subscriber fan-out. The server uses
	// this to bridge editor events into the launcher's multiplex
	// stream — the editor package itself doesn't know about the
	// stream, only about a single-arg callback.
	Forward func(Event)

	// Mutable state behind mu.
	mu        sync.Mutex
	inner     conversation
	started   bool
	streaming bool
	events    []Event
	nextSeq   int
	// turnCancel cancels the in-flight turn's context (a child of ctx)
	// without touching the session lifetime. Nil when no turn is in
	// flight. interrupted marks that the current turn was stopped by
	// the operator, so drain swaps claude's post-kill error for a
	// breadcrumb.
	turnCancel  context.CancelFunc
	interrupted bool
}

// conversation is the claude-conversation surface Session drives.
// *claude.Session satisfies it; tests substitute a fake so they can
// observe per-turn context cancellation without spawning the binary.
type conversation interface {
	Start(ctx context.Context, prompt string) (<-chan claude.Event, error)
	Resume(ctx context.Context, prompt string) (<-chan claude.Event, error)
}

// ErrNotStreaming is returned by Interrupt when no turn is in flight.
var ErrNotStreaming = errors.New("session is not currently streaming; nothing to interrupt")

// DTO is the JSON-safe projection returned by the HTTP layer. The
// shape carries enough subject info for the frontend to wire its
// resume key (`{kind, key}`) without reaching into the live session.
type DTO struct {
	ID               string    `json:"id"`
	SubjectKind      string    `json:"subjectKind"`
	SubjectKey       string    `json:"subjectKey"`
	SessionDir       string    `json:"sessionDir"`
	CreatedAt        time.Time `json:"createdAt"`
	Started          bool      `json:"started"`
	Streaming        bool      `json:"streaming"`
	HasInvestigation bool      `json:"hasInvestigation,omitempty"`
	HasSlack         bool      `json:"hasSlack,omitempty"`
	HasIncidentio    bool      `json:"hasIncidentio,omitempty"`

	// Subject-specific fields, populated based on Subject's dynamic
	// type so the frontend doesn't have to type-switch on a separate
	// payload. Empty for non-matching kinds.
	Playbook *PlaybookDTO `json:"playbook,omitempty"`
	Wiki     *WikiDTO     `json:"wiki,omitempty"`
}

// PlaybookDTO is the playbook-subject projection.
type PlaybookDTO struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Type    string `json:"type,omitempty"`
}

// WikiDTO is the wiki-subject projection.
type WikiDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Snapshot returns a JSON-safe copy of the mutable session state.
func (s *Session) Snapshot() DTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	dto := DTO{
		ID:               s.ID,
		SessionDir:       s.SessionDir,
		CreatedAt:        s.CreatedAt,
		Started:          s.started,
		Streaming:        s.streaming,
		HasInvestigation: s.Sources.HasInvestigation(),
		HasSlack:         s.Sources.HasSlack(),
		HasIncidentio:    s.Sources.HasIncidentio(),
	}
	if s.Subject != nil {
		dto.SubjectKind = s.Subject.SubjectKind()
		dto.SubjectKey = s.Subject.Key()
	}
	switch sub := s.Subject.(type) {
	case PlaybookSubject:
		dto.Playbook = &PlaybookDTO{ID: sub.ID, Version: sub.Version, Type: sub.Type}
	case WikiSubject:
		dto.Wiki = &WikiDTO{Kind: sub.Kind, ID: sub.ID}
	}
	return dto
}

// Start launches claude with the editor system prompt. First call
// builds + starts; subsequent calls return an error. Returns
// immediately — events stream into the goroutine drain.
func (s *Session) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("editor session already started")
	}
	inner, err := claude.NewSession(s.MCPConfigPath, allowedTools(s.Profile, s.LinkedRepos, s.Subject, s.SlackMCPAvailable, s.IncidentioMCPAvailable))
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("build session: %w", err)
	}
	s.inner = inner
	s.started = true
	s.streaming = true
	ctx, cancel := context.WithCancel(s.ctx)
	s.turnCancel = cancel
	// Snapshot Subject under the lock — Manager.Rebind may rewrite
	// it concurrently after we release s.mu, and the prompt builder
	// below needs a stable view.
	subject := s.Subject
	s.mu.Unlock()

	prompt := prompts.BuildEditor(subject, prompts.BaseEnv{
		LinkedRepos:            s.LinkedRepos,
		ToolCatalog:            s.ToolCatalog,
		Sources:                s.Sources,
		SlackMCPAvailable:      s.SlackMCPAvailable,
		IncidentioMCPAvailable: s.IncidentioMCPAvailable,
	}, s.Profile)
	events, err := inner.Start(ctx, prompt)
	if err != nil {
		s.clearTurn()
		cancel()
		s.publishError(err)
		return err
	}
	go s.drain(events)
	return nil
}

// clearTurn marks the session idle after a turn failed to launch.
func (s *Session) clearTurn() {
	s.mu.Lock()
	s.streaming = false
	s.turnCancel = nil
	s.mu.Unlock()
}

// SendFollowUp resumes the conversation with a follow-up prompt. The
// operator's text is also published as a synthetic "user" event so the
// transcript scrolls through their message before claude's reply.
func (s *Session) SendFollowUp(text string) error {
	s.mu.Lock()
	if !s.started || s.inner == nil {
		s.mu.Unlock()
		return errors.New("session not started")
	}
	if s.streaming {
		s.mu.Unlock()
		return errors.New("session is currently streaming; wait for it to idle")
	}
	s.streaming = true
	inner := s.inner
	ctx, cancel := context.WithCancel(s.ctx)
	s.turnCancel = cancel
	s.mu.Unlock()

	s.publish(Event{Kind: KindUser, Text: text})

	events, err := inner.Resume(ctx, text)
	if err != nil {
		s.clearTurn()
		cancel()
		s.publishError(err)
		return err
	}
	go s.drain(events)
	return nil
}

// Interrupt cancels the in-flight turn. Only the per-turn context is
// cancelled (which SIGKILLs the claude subprocess); the session's
// lifetime context is untouched, so the next SendFollowUp resumes the
// same claude session id. drain finishes when the event channel
// closes, replaces claude's post-kill error with a "(stopped the
// agent)" breadcrumb, and emits KindEnd. Returns ErrNotStreaming when
// no turn is in flight.
func (s *Session) Interrupt() error {
	s.mu.Lock()
	if !s.streaming || s.turnCancel == nil {
		s.mu.Unlock()
		return ErrNotStreaming
	}
	s.interrupted = true
	cancel := s.turnCancel
	s.mu.Unlock()
	cancel()
	return nil
}

// TranscriptSnapshot returns a consistent (events, lastSeq) view of
// the in-memory backlog. Symmetric with Investigation.TranscriptSnapshot
// — used by the editor's /transcript REST endpoint.
func (s *Session) TranscriptSnapshot() (events []Event, lastSeq int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	if len(out) > 0 {
		lastSeq = out[len(out)-1].Seq
	}
	return out, lastSeq
}

// PublishToolEvent records a tool-call event posted by triagent-mcp's
// telemetry hook. Used by the launcher's /api/internal/tool-events
// handler so editor sessions get the same activity stream as
// investigations.
func (s *Session) PublishToolEvent(ev Event) {
	s.publish(ev)
}

// Close cancels the session context (drains the claude CLI) and
// (when removeFromDisk is true) removes the per-session directory.
// Idempotent.
func (s *Session) Close(removeFromDisk bool) {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	dir := s.SessionDir
	s.mu.Unlock()
	if removeFromDisk && dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (s *Session) drain(events <-chan claude.Event) {
	for ev := range events {
		env, ok := envelopeFromClaude(ev)
		if !ok {
			continue
		}
		// Checked per-event: Interrupt flips the flag while drain is
		// mid-loop, and the error it must swallow arrives after that.
		s.mu.Lock()
		suppressErr := s.interrupted && env.Kind == KindError
		s.mu.Unlock()
		if suppressErr {
			continue
		}
		s.publish(env)
	}
	s.mu.Lock()
	interrupted := s.interrupted
	s.interrupted = false
	s.turnCancel = nil
	s.mu.Unlock()
	if interrupted {
		// Audit-trail breadcrumb so the transcript shows this turn
		// ended because the operator hit stop, not because claude
		// finished on its own.
		s.publish(Event{Kind: KindUser, Text: "(stopped the agent)"})
	}
	s.publish(Event{Kind: KindEnd})
	// streaming flips false last so observers polling on !streaming
	// see the breadcrumb and end events by the time the flag clears.
	s.mu.Lock()
	s.streaming = false
	s.mu.Unlock()
}

func (s *Session) publish(env Event) {
	s.mu.Lock()
	s.nextSeq++
	env.Seq = s.nextSeq
	if env.Timestamp.IsZero() {
		env.Timestamp = time.Now().UTC()
	}
	s.events = append(s.events, env)
	s.mu.Unlock()
	if s.Forward != nil {
		s.Forward(env)
	}
}

func (s *Session) publishError(err error) {
	s.publish(Event{Kind: KindError, Text: err.Error()})
}

// allowedTools is the editor session's claude --allowedTools glob set.
// The exact MCP allow-list depends on the subject kind and which MCPs
// were wired into the per-session mcp.json:
//   - playbook subject: strategies (authoring), git (linked repos),
//     extra MCPs from profile. No wiki, no slack, no incident.io.
//   - wiki subject: wiki (read + draft), strategies, git, extra MCPs,
//     plus slack / incident.io when their tokens are linked (independent
//     of whether a specific channel/incident was pinned to this session).
//
// k8s/prom are absent from both — there's no cluster context attached
// to an editor session.
func allowedTools(prof *profile.Profile, linkedRepos []repos.LinkedRepo, subject Subject, slackAvail, incidentioAvail bool) []string {
	out := []string{
		"mcp__" + preflight.MCPAliasStrategies + "__*",
	}
	if _, ok := subject.(WikiSubject); ok {
		out = append(out, "mcp__"+preflight.MCPAliasWiki+"__*")
	}
	if prof != nil {
		for _, m := range prof.ExtraMCPs {
			if len(m.AllowedTools) > 0 {
				out = append(out, m.AllowedTools...)
			} else {
				out = append(out, "mcp__"+m.Alias+"__*")
			}
		}
	}
	for _, r := range linkedRepos {
		out = append(out, "mcp__"+preflight.MCPAliasGitPrefix+r.EffectiveAlias()+"__*")
	}
	if slackAvail {
		out = append(out, "mcp__"+preflight.MCPAliasSlack+"__*")
	}
	if incidentioAvail {
		out = append(out, "mcp__"+preflight.MCPAliasIncidentio+"__*")
	}
	return out
}

// envelopeFromClaude mirrors fromClaudeEvent in internal/server. We
// duplicate it here (rather than import server) so the editor package
// stays free of UI/server entanglement; the conversion is small enough
// that the duplication cost is below the cross-package coupling cost.
func envelopeFromClaude(e claude.Event) (Event, bool) {
	env := Event{}
	switch e.Kind {
	case claude.EventSystem:
		env.Kind = KindSystem
		env.Subtype = e.Subtype
		env.SessionID = e.SessionID
	case claude.EventAssistant:
		if e.Text == "" {
			return Event{}, false
		}
		env.Kind = KindAssistant
		env.Text = e.Text
	case claude.EventResult:
		env.Kind = KindResult
		env.Subtype = e.Subtype
		env.Text = e.FinalText
	case claude.EventError:
		env.Kind = KindError
		if e.Err != nil {
			env.Text = e.Err.Error()
		}
	case claude.EventToolUse, claude.EventToolResult, claude.EventUnknown:
		return Event{}, false
	default:
		return Event{}, false
	}
	return env, true
}

// Manager tracks live editor sessions for one launcher process. At
// most one session exists per Subject.Key() — opening a new one for a
// different subject closes the prior session for that key. Switching
// playbook id (or version, or wiki entry) is a different key, so
// they coexist.
type Manager struct {
	mu        sync.RWMutex
	parentCtx context.Context
	root      string
	byID      map[string]*Session
	byKey     map[string]*Session
	closed    bool
}

// NewManager returns a Manager rooted at parentCtx. Cancellation of
// parentCtx tears down every editor session; same pattern as the
// investigation Manager.
func NewManager(parentCtx context.Context, root string) *Manager {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	return &Manager{
		parentCtx: parentCtx,
		root:      root,
		byID:      map[string]*Session{},
		byKey:     map[string]*Session{},
	}
}

// Existing returns the live session for the given Subject, or nil. The
// drawer calls this on mount to decide between resume and create.
func (m *Manager) Existing(subject Subject) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byKey[subject.Key()]
}

// ExistingByKey is the URL-driven lookup used by GET handlers that
// receive subject_kind+subject_key as query params.
func (m *Manager) ExistingByKey(key string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byKey[key]
}

// Get looks up a session by id (the URL surface uses ids; Existing
// is the subject-keyed lookup).
func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id]
}

// Register stores a new session, derives its context from the parent,
// and closes any prior session for the same Subject.Key() (frontend
// rule: switching closes prior).
func (m *Manager) Register(s *Session) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("editor manager is shut down")
	}
	if s.Subject == nil {
		return nil, errors.New("editor session subject is required")
	}
	if s.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, fmt.Errorf("generate editor session id: %w", err)
		}
		s.ID = id
	} else if _, exists := m.byID[s.ID]; exists {
		return nil, fmt.Errorf("editor session %q already registered", s.ID)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.ctx, s.cancel = context.WithCancel(m.parentCtx)

	key := s.Subject.Key()
	if prior, ok := m.byKey[key]; ok {
		delete(m.byID, prior.ID)
		delete(m.byKey, key)
		go prior.Close(true)
	}

	m.byID[s.ID] = s
	m.byKey[key] = s
	return s, nil
}

// Remove closes and forgets a session. Returns whether the id was
// known. removeFromDisk deletes the session directory; the X button
// passes true, the parent-shutdown path passes false.
func (m *Manager) Remove(id string, removeFromDisk bool) bool {
	m.mu.Lock()
	s := m.byID[id]
	if s != nil {
		delete(m.byID, id)
		key := s.Subject.Key()
		if cur, ok := m.byKey[key]; ok && cur.ID == id {
			delete(m.byKey, key)
		}
	}
	m.mu.Unlock()
	if s == nil {
		return false
	}
	s.Close(removeFromDisk)
	return true
}

// Rebind retargets a live session at a new Subject. Used by the
// playbook editor's "+ new playbook" → accept-proposal flow, where
// the in-flight chat session must follow the editor onto the
// freshly-saved playbook id without tearing down the agent
// conversation.
//
// The session's id is preserved; only Subject and the manager's
// byKey index move. Returns the existing session unchanged when
// newSubject.Key() already matches.
//
// Errors:
//   - ErrSessionNotFound when no session has the given id.
//   - ErrSessionExists when byKey already binds a *different*
//     session to newSubject.Key(). Callers decide whether to
//     navigate to the existing session or surface.
func (m *Manager) Rebind(sessionID string, newSubject Subject) (*Session, error) {
	if newSubject == nil {
		return nil, errors.New("editor session subject is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.byID[sessionID]
	if !ok {
		return nil, fmt.Errorf("editor session %q: %w", sessionID, ErrSessionNotFound)
	}
	newKey := newSubject.Key()
	oldKey := sess.Subject.Key()
	if newKey == oldKey {
		return sess, nil
	}
	if other, exists := m.byKey[newKey]; exists && other.ID != sess.ID {
		return nil, fmt.Errorf("editor session for %q: %w", newKey, ErrSessionExists)
	}
	delete(m.byKey, oldKey)
	m.byKey[newKey] = sess
	sess.mu.Lock()
	sess.Subject = newSubject
	sess.mu.Unlock()
	return sess, nil
}

// Shutdown closes every session and rejects further registrations.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.byID))
	for _, s := range m.byID {
		sessions = append(sessions, s)
	}
	m.byID = map[string]*Session{}
	m.byKey = map[string]*Session{}
	m.closed = true
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close(true)
	}
}

// Root returns the configured root dir for editor session
// directories. Handlers use it to allocate per-session subdirs.
func (m *Manager) Root() string { return m.root }

// newID returns a 16-byte hex random id. Same shape as investigation
// ids.
func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// wikiSlugIncidentioRefPattern matches the wiki-entry slug convention
// `inc-<n>-<slug>` and captures the numeric incident.io reference. Only
// the unprefixed variant is captured: `inc-support-<n>-<slug>` is a
// distinct numbering space in incident.io and not safely derivable
// without the explicit URL.
var wikiSlugIncidentioRefPattern = regexp.MustCompile(`^inc-(\d+)-[a-z0-9-]+$`)

// DeriveIncidentioRefFromSlug returns the incident.io reference encoded
// in a wiki entry slug when the operator followed the `inc-<n>-<slug>`
// convention. Lets the launcher wire the triagent-incidentio MCP for backfill
// sessions even when the operator didn't paste an explicit URL.
//
// Returns the empty string for slugs that don't match the unprefixed
// `inc-<n>-<slug>` shape (entity ids, `inv-`/`alert-` prefixed slugs,
// `inc-support-…`, malformed slugs). Callers treat empty as "not
// derivable — leave incidentio unwired".
func DeriveIncidentioRefFromSlug(slug string) string {
	m := wikiSlugIncidentioRefPattern.FindStringSubmatch(strings.TrimSpace(slug))
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// ParseIncidentioURL extracts the trailing reference (numeric or UUID)
// from an incident.io dashboard URL such as
//
//	https://app.incident.io/<org>/incidents/<ref>
//
// The v2 API accepts the trailing ref directly at GET /v2/incidents/{ref}
// — UUIDs and numeric references both work — so we don't need to
// resolve through any other endpoint. Returns an error for non-incident.io
// hosts and for URLs that don't end in /incidents/<ref>.
func ParseIncidentioURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Host == "" {
		return "", errors.New("missing host")
	}
	if !strings.HasSuffix(u.Host, "incident.io") {
		return "", fmt.Errorf("not an incident.io URL: host=%s", u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-2] != "incidents" {
		return "", fmt.Errorf("unrecognised path; want /<org>/incidents/<ref>, got %s", u.Path)
	}
	ref := strings.TrimSpace(parts[len(parts)-1])
	if ref == "" {
		return "", errors.New("empty incident reference")
	}
	return ref, nil
}
