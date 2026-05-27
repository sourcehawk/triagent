package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/sourcehawk/triagent/internal/editor"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/internal/sessions"
	operatorskills "github.com/sourcehawk/triagent/operator-skills"
)

// Sentinel errors returned from SendFollowUp's pre-checks. Exposed so
// handlers can map them to specific HTTP statuses and the frontend can
// display the right copy.
var (
	ErrArchived     = errors.New("investigation is archived; start a new one to continue")
	ErrStreaming    = errors.New("session is currently streaming; wait for it to idle")
	ErrNotStreaming = errors.New("session is not currently streaming; nothing to interrupt")
	ErrNotStarted   = errors.New("session not started")
	ErrRehydrating  = errors.New("rehydrate in progress; retry after the rehydrate event resolves")
)

// investigationSession is the small claude-conversation surface
// Investigation.Start/SendFollowUp exercise. *sessions.Session satisfies
// it implicitly; tests inject a no-op fake to drive handler flows that
// would otherwise spawn claude.
type investigationSession interface {
	Start(ctx context.Context) (<-chan claude.Event, error)
	Resume(ctx context.Context, prompt string) (<-chan claude.Event, error)
}

// OriginatingSignal is the back-reference from an investigation to the
// signal (and watch) that produced it. Persisted so reload paths can
// surface the chip without re-querying the watch.
type OriginatingSignal struct {
	WatchID  string `json:"watchID"`
	SignalID string `json:"signalID"`
}

// Investigation is the server-side state for a single browser-driven flow:
// from "user picked a cluster" through preflight, then the streaming
// claude session. It survives across HTTP requests so the cookie auth +
// UUID is enough to re-attach a tab to the in-flight investigation.
//
// The claude session — Close() cancels its per-investigation context,
// draining any in-flight CLI work.
type Investigation struct {
	// Immutable after Register; safe to read without holding the mutex.
	ID              string
	Namespace       string
	IncidentURL      string
	SlackChannelURL  string
	SlackChannelID   string
	SlackChannelName string
	Notes            string
	// Label is the one-line summary shown in the sidebar history list.
	// Set by the agent via the triagent-meta MCP (set_session_label) and by
	// the operator via the inline rename UI. Both paths go through
	// Manager.SetLabel which serializes writes under inv.mu. Empty
	// until first set; the frontend renders "New Investigation" as a
	// placeholder rather than the empty string.
	Label           string
	MCPConfigPath   string
	DocsPrefix      string
	SessionDir      string
	SlackMCPEnabled      bool
	IncidentioMCPEnabled bool
	LinkedRepos     []repos.LinkedRepo
	Profile         *profile.Profile // investigation profile (prompt content + playbook IDs)
	CreatedAt       time.Time

	// Set on imported investigations (those adopted from a teammate's
	// share bundle). Both fields are written together; absence means a
	// native investigation. AdoptFromDir forces archived=true for share-
	// bundle imports — they have no live MCP/kubeconfig wiring on this
	// machine.
	ImportedFrom *ImportedFrom
	ImportedAt   time.Time

	// OriginatingWatchID is set when this investigation was spawned from a
	// signal-watch (autoStart=true watch via SpawnFromSignal, or
	// manually-started signal in M7). The terminal-hook uses it to call
	// watches.Manager.NotifyTerminal so the spawner's slot is released
	// when this investigation ends. Empty for normal preflight-created
	// investigations.
	OriginatingWatchID string

	// OriginatingSignal points back to the specific signal in the
	// watch's signals.jsonl that produced this investigation. Set
	// alongside OriginatingWatchID when the launcher spawns an
	// investigation from a watch (autoStart or manual). nil for
	// preflight-created investigations.
	OriginatingSignal *OriginatingSignal

	// Author is the git config user.name/user.email captured at session
	// creation. Empty for sessions created before this field landed.
	Author persistedAuthor

	// PushedAt is set once the session has been successfully pushed to
	// the upstream sessions repo. Nil otherwise.
	PushedAt *time.Time

	// PushURL is the GitHub PR URL returned by `gh pr create` when
	// PushedAt was stamped. Empty for unpushed sessions.
	PushURL string

	// PRState is the most recent gh-known PR lifecycle state for
	// PushURL: "open", "merged", "closed", or empty (unknown).
	// Refreshed wholesale by Manager.RefreshPRStates from a single
	// `gh pr list` call against the upstream sessions repo.
	PRState    string
	PRMergedAt *time.Time
	PRClosedAt *time.Time

	// PushInProgress is true while a kicked-off push goroutine is
	// running. Persisted so a refresh during the 30s–3m drafter run
	// can show the operator that work is happening even though no
	// browser tab owns the request anymore. Cleared on success
	// (PushedAt is set instead) or error (PushError is set).
	PushInProgress bool

	// PushStartedAt stamps the wall-clock moment the goroutine
	// started. Lets the UI show "running for 1m 23s" and gives the
	// startup-recovery sweep a way to detect obviously-stale flags
	// (though for now we treat any persisted true as orphaned).
	PushStartedAt *time.Time

	// PushError is the last attempted push's failure message. Set when
	// PushInProgress flips false due to error; cleared when a fresh
	// kick-off begins or on success. Replaces the per-request error
	// response we lose by going async.
	PushError string

	// Persisted resumability fields. ClaudeSessionID and LaunchCwd are
	// captured at session start; KubeconfigPath is captured at preflight.
	// All three persist across launcher restarts so the rehydrate path
	// can rebuild a working session.
	ClaudeSessionID string
	LaunchCwd       string
	KubeconfigPath  string

	// Auto holds the auto-mode lifecycle state. Zero value (Enabled=false)
	// means manual mode — preserves current behavior. Written by the
	// AutoOperator on every phase transition; rehydrate consults
	// IsActive() to decide whether to relaunch the boundary watcher.
	Auto auto.State

	// autoOp owns the AutoOperator runtime (claude session adapter +
	// wake state machine). nil for manual-mode investigations. Set by
	// Manager.EnableAuto, read by notifyAutoResume and the boundary
	// watcher goroutine.
	autoOp *auto.Operator

	// autoBackend exposes the backing claude session id so applyAutoState
	// can persist OperatorSessionID for rehydrate. Held as a small
	// interface (autoBackendish) so tests can swap in a fake. nil for
	// manual-mode investigations.
	autoBackend autoBackendish

	// autoBoundary, when non-nil, receives every published envelope on
	// this investigation so the auto-mode boundary watcher can react to
	// `end` envelopes (one wake per agent turn). publish() does a
	// non-blocking send; the watcher's goroutine reads. nil for manual-
	// mode investigations; set by EnableAuto.
	autoBoundary chan EventEnvelope

	// autoEnableInFlight gates concurrent EnableAuto callers across the
	// slow setup path (skill extraction + factory). The idempotency
	// check earlier in EnableAuto only catches duplicates AFTER autoOp
	// is assigned; without this sentinel, a retry that lands while the
	// first call is still in factory(opts) sees autoOp == nil and
	// proceeds to build a second operator + watcher. Held under inv.mu.
	autoEnableInFlight bool

	// Lifetime; cancelled on Close().
	ctx    context.Context
	cancel context.CancelFunc

	// Mutable state, all guarded by mu.
	mu sync.Mutex
	// session is the live claude conversation. Held as an interface so
	// tests can inject a no-op fake that satisfies Start/Resume without
	// spawning the claude binary; production wires *sessions.Session.
	session   investigationSession
	started   bool // first Start() succeeded
	streaming bool // a claude turn is currently producing events
	archived  bool // hydrated from disk; read-only

	// turnCancel cancels the per-turn context derived from inv.ctx, so
	// Interrupt() can stop the current claude subprocess without
	// tearing down the whole investigation. Set under inv.mu when a
	// turn starts; cleared (set to nil) by drain on exit.
	turnCancel context.CancelFunc

	// interrupted is set by Interrupt() and read by drain so the
	// post-turn cleanup knows the non-zero exit was operator-initiated
	// (suppress the error envelope, publish a breadcrumb) rather than a
	// real claude failure. Always cleared by drain after the turn ends.
	interrupted bool

	// needsRehydrate is set by Restore for sessions that came back from
	// disk and have not yet been re-attached to a live claude/MCP/port-
	// forward in this process. Cleared after the first successful
	// rehydrate. Not exposed in the DTO — frontend uses Resumable instead.
	needsRehydrate bool

	// rehydrating guards against parallel rehydrate attempts on the
	// same investigation (e.g. two browser tabs hitting the composer
	// simultaneously). Held only across the rehydrate orchestration's
	// IO; flipped under inv.mu.
	rehydrating bool
	events      []EventEnvelope // backlog buffer; new subscribers replay it
	nextSeq     int             // monotonic event id
	// totalUsage and totalCostUSD are the running aggregate of every
	// result envelope's token tally and total_cost_usd. Updated under
	// inv.mu on publish; replayed on Restore. Snapshot copies these
	// into the DTO so the sidebar / chat footer can render totals
	// without re-walking the transcript on each render.
	totalUsage   claude.Usage
	totalCostUSD float64

	// On-disk persistence. nil for in-memory-only investigations (none
	// today, but the field stays optional in case we want to skip persist
	// for, e.g., an ephemeral preflight retry).
	store *store

	// manager is a back-pointer so Investigation.publish can fan its
	// envelopes onto the multiplex stream. Set by Manager.Register
	// (and Manager.Restore for restored investigations). Nil for
	// investigations created outside those paths — they remain
	// read-only and never publish.
	manager *Manager
}

// autoBackendish is the slice of the auto-mode backend the Manager
// needs from the production *auto.ClaudeBackend: drive a prompt
// (Resume) and read the captured claude session id (SessionID). Held
// as an interface here so tests inject a stub without spawning the
// real claude binary.
type autoBackendish interface {
	auto.Backend
	SessionID() string
}

// InvestigationDTO is the JSON shape returned by /api/investigations and
// /api/preflight. Snapshotted under the lock so reads are race-free.
type InvestigationDTO struct {
	ID              string             `json:"id"`
	Namespace       string             `json:"namespace"`
	IncidentURL      string             `json:"incidentUrl,omitempty"`
	SlackChannelURL  string             `json:"slackChannelUrl,omitempty"`
	SlackChannelID   string             `json:"slackChannelId,omitempty"`
	SlackChannelName string             `json:"slackChannelName,omitempty"`
	Notes            string             `json:"notes,omitempty"`
	Label            string             `json:"label,omitempty"`
	MCPConfigPath   string             `json:"mcpConfigPath"`
	DocsPrefix      string             `json:"docsPrefix,omitempty"`
	SessionDir      string             `json:"sessionDir"`
	SlackMCPEnabled      bool               `json:"slackMCPEnabled,omitempty"`
	IncidentioMCPEnabled bool               `json:"incidentioMCPEnabled,omitempty"`
	LinkedRepos          []repos.LinkedRepo `json:"linkedRepos,omitempty"`
	CreatedAt       time.Time          `json:"createdAt"`
	Started         bool               `json:"started"`
	Streaming       bool               `json:"streaming"`
	Archived        bool               `json:"archived"`
	ImportedFrom    *ImportedFrom      `json:"importedFrom,omitempty"`
	ImportedAt      time.Time          `json:"importedAt,omitempty"`
	Author          persistedAuthor    `json:"author"`
	Pushed          bool               `json:"pushed"`
	PushedAt        *time.Time         `json:"pushedAt,omitempty"`
	PushURL         string             `json:"pushUrl,omitempty"`
	// PR lifecycle: refreshed by Manager.RefreshPRStates against
	// `gh pr list` on the upstream sessions repo. Lets the frontend
	// distinguish open vs merged vs closed and bring the push button
	// back when the operator closed without merging.
	PRState    string     `json:"prState,omitempty"`
	PRMergedAt *time.Time `json:"prMergedAt,omitempty"`
	PRClosedAt *time.Time `json:"prClosedAt,omitempty"`
	PushInProgress bool       `json:"pushInProgress,omitempty"`
	PushStartedAt  *time.Time `json:"pushStartedAt,omitempty"`
	PushError      string     `json:"pushError,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
	LaunchCwd       string `json:"launchCwd,omitempty"`
	KubeconfigPath  string `json:"kubeconfigPath,omitempty"`
	// Auto holds the auto-mode lifecycle state. The `omitempty` tag is
	// cosmetic — encoding/json does not skip non-pointer struct values,
	// so manual-mode investigations still emit `"auto":{"enabled":false}`.
	// Matches the existing Prom/Author pattern in this file.
	Auto auto.State `json:"auto,omitempty"`
	// OriginatingWatchID is non-empty when this investigation was spawned
	// from a signal-watch. Exposed so the frontend can link back to the watch.
	OriginatingWatchID string `json:"originatingWatchId,omitempty"`
	// OriginatingSignal is the back-reference to the specific signal that
	// produced this investigation. nil for preflight-created investigations.
	OriginatingSignal *OriginatingSignal `json:"originatingSignal,omitempty"`
	// Resumable is true for restored sessions whose claude conversation
	// can be picked up via --resume on the next follow-up. False once
	// rehydrated, when archived, or when no session id was captured.
	Resumable bool `json:"resumable"`
	// Slug is the canonical session-repo slug for this investigation
	// (sessions/<YYYY-MM>/<slug>/). Frontend uses it to match an
	// upstream session to its local counterpart so the "replay" button
	// can open the local view rather than create a duplicate.
	Slug string `json:"slug"`
	// SyncState is the resolver's authoritative answer to "is this
	// session in sync with upstream?". Sidebar checkmark, ImportedFromBadge
	// gate, and any future audit tool read this rather than rolling
	// their own (Pushed && PRState !== "closed") formulas — see
	// internal/server/sync_state.go.
	SyncState SyncState `json:"syncState"`
	// Usage is the per-investigation running token total summed from
	// every result envelope claude emitted across the session's
	// invocations. nil until the first result event lands. Frontend
	// reads this for the sidebar / chat footer totals.
	Usage *claude.Usage `json:"usage,omitempty"`
	// CostUSD is the running total_cost_usd summed from result
	// envelopes. Zero until the first result lands.
	CostUSD float64 `json:"costUsd,omitempty"`
}

// Snapshot returns a JSON-safe copy of the investigation's mutable state.
// Callers MUST go through this rather than serializing the struct directly,
// because the mutable fields require the mutex.
func (i *Investigation) Snapshot() InvestigationDTO {
	i.mu.Lock()
	defer i.mu.Unlock()
	return InvestigationDTO{
		ID:              i.ID,
		Namespace:       i.Namespace,
		IncidentURL:      i.IncidentURL,
		SlackChannelURL:  i.SlackChannelURL,
		SlackChannelID:   i.SlackChannelID,
		SlackChannelName: i.SlackChannelName,
		Notes:            i.Notes,
		Label:            i.Label,
		MCPConfigPath:   i.MCPConfigPath,
		DocsPrefix:      i.DocsPrefix,
		SessionDir:      i.SessionDir,
		SlackMCPEnabled:      i.SlackMCPEnabled,
		IncidentioMCPEnabled: i.IncidentioMCPEnabled,
		LinkedRepos:          i.LinkedRepos,
		CreatedAt:       i.CreatedAt,
		Started:         i.started,
		Streaming:       i.streaming,
		Archived:        i.archived,
		ImportedFrom:    i.ImportedFrom,
		ImportedAt:      i.ImportedAt,
		Author:          i.Author,
		Pushed:          i.PushedAt != nil,
		PushedAt:        i.PushedAt,
		PushURL:         i.PushURL,
		PRState:         i.PRState,
		PRMergedAt:      i.PRMergedAt,
		PRClosedAt:      i.PRClosedAt,
		PushInProgress:  i.PushInProgress,
		PushStartedAt:   i.PushStartedAt,
		PushError:       i.PushError,
		ClaudeSessionID: i.ClaudeSessionID,
		LaunchCwd:       i.LaunchCwd,
		KubeconfigPath:  i.KubeconfigPath,
		Auto:               i.Auto,
		OriginatingWatchID: i.OriginatingWatchID,
		OriginatingSignal:  i.OriginatingSignal,
		Resumable:          !i.archived && i.ClaudeSessionID != "" && i.needsRehydrate,
		Slug:            computeSessionSlug(i.CreatedAt, i.Namespace, i.ID),
		SyncState: sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal:   true, // we're snapshotting an in-memory Investigation, by definition local
			Pushed:     i.PushedAt != nil,
			PushURL:    i.PushURL,
			PRState:    i.PRState,
			PRMergedAt: i.PRMergedAt,
			PRClosedAt: i.PRClosedAt,
		}),
		Usage:   snapshotUsage(i.totalUsage),
		CostUSD: i.totalCostUSD,
	}
}

// snapshotUsage returns a pointer to a copy of the running usage total
// when any component is non-zero, or nil otherwise. The pointer keeps
// the InvestigationDTO JSON compact (omitempty) for sessions that
// haven't seen a result event yet, and matches the wire shape of
// per-envelope usage so the frontend handles both uniformly.
func snapshotUsage(u claude.Usage) *claude.Usage {
	if u == (claude.Usage{}) {
		return nil
	}
	cp := u
	return &cp
}

// snapshotEvents returns a copy of the in-memory event backlog. Used by
// tests that need to assert what was published without subscribing to
// the live stream. Race-free: the slice is copied under inv.mu.
func (i *Investigation) snapshotEvents() []EventEnvelope {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]EventEnvelope, len(i.events))
	copy(out, i.events)
	return out
}

// TranscriptSnapshot returns a consistent (events, lastSeq) snapshot
// of the in-memory backlog. The pair is taken under inv.mu so it
// matches: lastSeq == events[len-1].Seq when len > 0, else 0.
// Frontend uses lastSeq to filter the live multiplex stream — any
// envelope with seq <= lastSeq for the same investigation is a
// duplicate of something already in the snapshot.
func (i *Investigation) TranscriptSnapshot() (events []EventEnvelope, lastSeq int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]EventEnvelope, len(i.events))
	copy(out, i.events)
	if len(out) > 0 {
		lastSeq = out[len(out)-1].Seq
	}
	return out, lastSeq
}

// Start launches the claude session for this investigation. The first call
// builds + starts; subsequent calls are an error. Returns immediately —
// the actual streaming runs in a goroutine that fans events out to all
// subscribers. Returns an error for archived investigations: the original
// process spawned this session, the port-forward is gone, and the kube
// context may have expired.
func (i *Investigation) Start() error {
	i.mu.Lock()
	if i.archived {
		i.mu.Unlock()
		return ErrArchived
	}
	if i.started {
		i.mu.Unlock()
		return errors.New("session already started")
	}
	if i.LaunchCwd == "" {
		i.LaunchCwd = i.SessionDir
	}
	sess, err := sessions.New(sessions.Options{
		Namespace:              i.Namespace,
		UserNotes:              i.Notes,
		IncidentURL:            i.IncidentURL,
		SlackChannelURL:        i.SlackChannelURL,
		SlackChannelID:         i.SlackChannelID,
		SlackChannelName:       i.SlackChannelName,
		MCPConfigPath:          i.MCPConfigPath,
		SlackMCPAvailable:      i.SlackMCPEnabled,
		IncidentioMCPAvailable: i.IncidentioMCPEnabled,
		LinkedRepos:            i.LinkedRepos,
		LaunchCwd:              i.LaunchCwd,
		KubeconfigPath:         i.KubeconfigPath,
		Profile:                i.Profile,
	})
	if err != nil {
		i.mu.Unlock()
		return fmt.Errorf("build session: %w", err)
	}
	i.session = sess
	i.started = true
	i.streaming = true
	// Derive a per-turn context so Interrupt() can cancel just this
	// turn without killing the whole investigation. drain clears
	// turnCancel after the turn ends.
	turnCtx, cancel := context.WithCancel(i.ctx)
	i.turnCancel = cancel
	i.mu.Unlock()

	events, err := sess.Start(turnCtx)
	if err != nil {
		i.mu.Lock()
		i.streaming = false
		i.turnCancel = nil
		i.mu.Unlock()
		cancel()
		i.publishError(err)
		return err
	}
	go i.drain(events)
	return nil
}

// SendFollowUp submits a follow-up prompt against the in-progress claude
// session. Errors if the session hasn't started, or a turn is currently
// streaming (claude itself can't accept overlapping prompts).
//
// The origin tag distinguishes "human" follow-ups (typed in the composer)
// from "operator" follow-ups (sent by the auto-mode operator agent via the
// agent-operator MCP) so the frontend can colour-code them. Empty origin
// is treated as "human" for backward-compat with internal callers (e.g.
// rehydrate) that don't care.
func (i *Investigation) SendFollowUp(origin, prompt string) error {
	i.mu.Lock()
	if i.archived {
		i.mu.Unlock()
		return ErrArchived
	}
	if !i.started || i.session == nil {
		i.mu.Unlock()
		return ErrNotStarted
	}
	if i.streaming {
		i.mu.Unlock()
		return ErrStreaming
	}
	i.streaming = true
	sess := i.session
	// Per-turn context — see Start for why.
	turnCtx, cancel := context.WithCancel(i.ctx)
	i.turnCancel = cancel
	i.mu.Unlock()

	// Surface the operator's prompt in the transcript so reload-after-the-fact
	// shows what was asked.
	if origin == "" {
		origin = "human"
	}
	i.publish(EventEnvelope{Kind: envKindUser, Origin: origin, Text: prompt})

	events, err := sess.Resume(turnCtx, prompt)
	if err != nil {
		i.mu.Lock()
		i.streaming = false
		i.turnCancel = nil
		i.mu.Unlock()
		cancel()
		i.publishError(err)
		return err
	}
	go i.drain(events)
	return nil
}

// Interrupt cancels the in-flight claude turn. The current turn's
// context is cancelled, which SIGKILLs the claude subprocess; drain
// finishes naturally when the event channel closes, publishes a
// "(stopped)" breadcrumb instead of an error envelope, and emits
// envKindEnd. Returns ErrNotStreaming when no turn is in flight.
//
// This is a per-turn cancel — the investigation's lifetime context
// (i.ctx) is untouched, so the next SendFollowUp / Start works
// normally against the same claude session id.
func (i *Investigation) Interrupt() error {
	i.mu.Lock()
	if !i.streaming || i.turnCancel == nil {
		i.mu.Unlock()
		return ErrNotStreaming
	}
	i.interrupted = true
	cancel := i.turnCancel
	i.mu.Unlock()
	cancel()
	return nil
}

// drain reads from a claude event channel and fans envelopes out to
// subscribers until the channel closes. Emits a synthetic "end" event
// when the turn finishes. If the turn was operator-interrupted, the
// post-Wait error event from claude is suppressed (the cancel was ours,
// not a real failure) and replaced with a small audit-trail breadcrumb.
func (i *Investigation) drain(events <-chan claude.Event) {
	for ev := range events {
		env, ok := fromClaudeEvent(ev)
		if !ok {
			continue
		}
		// The interrupted check has to happen per-event, not once
		// upfront — Interrupt() flips the flag while drain is mid-loop.
		i.mu.Lock()
		suppressErr := i.interrupted && env.Kind == envKindError
		i.mu.Unlock()
		if suppressErr {
			continue
		}
		i.publish(env)
	}
	i.mu.Lock()
	interrupted := i.interrupted
	i.interrupted = false
	i.turnCancel = nil
	i.streaming = false
	i.mu.Unlock()
	if interrupted {
		// Audit-trail breadcrumb so reviewers can see this turn ended
		// because the operator hit stop, not because claude finished
		// naturally. Origin "human" reflects that the operator made
		// the call; the parenthesised text reads as a system note in
		// the existing user-envelope rendering.
		i.publish(EventEnvelope{
			Kind:   envKindUser,
			Origin: "human",
			Text:   "(stopped the agent)",
		})
	}
	i.publish(EventEnvelope{Kind: envKindEnd})
}

// publish appends env to the backlog, stamps it with the next seq +
// timestamp, fans out to the multiplex stream, and queues a disk-write
// through the per-investigation store.
func (i *Investigation) publish(env EventEnvelope) {
	i.mu.Lock()
	i.nextSeq++
	env.Seq = i.nextSeq
	if env.Timestamp.IsZero() {
		env.Timestamp = time.Now().UTC()
	}
	i.events = append(i.events, env)
	// Roll per-API-call usage into the running total. EventUsage envelopes
	// fire on every assistant message claude emits (one per API call within
	// the CLI invocation), so summing them gives the true invocation cost.
	// The earlier shape — summing the `result` envelope's usage — was wrong:
	// claude's result.usage is the LAST API call's snapshot, not the
	// invocation total, so it under-reported by 2x or more on tool-heavy
	// runs. CostUSD on the result envelope IS the invocation roll-up and
	// stays the source of truth for the cost number.
	if env.Kind == envKindUsage && env.Usage != nil {
		i.totalUsage.InputTokens += env.Usage.InputTokens
		i.totalUsage.OutputTokens += env.Usage.OutputTokens
		i.totalUsage.CacheCreationInputTokens += env.Usage.CacheCreationInputTokens
		i.totalUsage.CacheReadInputTokens += env.Usage.CacheReadInputTokens
	}
	if env.Kind == envKindResult {
		i.totalCostUSD += env.CostUSD
	}
	store := i.store
	// First time we observe a claude session id on an envelope, copy
	// it onto the investigation. The persistence happens after we drop
	// the lock so the metadata write doesn't block fan-out.
	idChanged := false
	if env.SessionID != "" && i.ClaudeSessionID == "" {
		i.ClaudeSessionID = env.SessionID
		idChanged = true
	}
	boundary := i.autoBoundary
	i.mu.Unlock()
	if i.manager != nil {
		i.manager.PublishStream(streamEnvelopeFromInvestigation(env, i.ID))
	}
	if boundary != nil {
		// Non-blocking: the boundary watcher reads from this channel
		// only to react to `end` envelopes. A full buffer means the
		// watcher is busy driving the operator's claude turn; we drop
		// the envelope here because the next `end` (post-turn) will
		// trigger a fresh diff that recomputes from inv.events anyway.
		select {
		case boundary <- env:
		default:
		}
	}
	if store != nil {
		store.queueEvent(env)
	}
	if idChanged && store != nil {
		_ = store.writeMetadata(i.Snapshot())
	}
}

func (i *Investigation) publishError(err error) {
	i.publish(EventEnvelope{Kind: envKindError, Text: err.Error()})
}

// publishPushState fans a PushStatePayload out to SSE subscribers as a
// push_state envelope. The frontend maps phase → toast (pending /
// success / error) without needing a separate HTTP round-trip.
func (i *Investigation) publishPushState(p PushStatePayload) {
	i.publish(EventEnvelope{
		Kind:      envKindPushState,
		PushState: &p,
	})
}

// publishRehydrateState fans a RehydrateStatePayload out as a
// rehydrate_state envelope. Mirrors publishPushState; the SessionView
// keys off Phase to flip a banner rather than a transcript message.
func (i *Investigation) publishRehydrateState(p RehydrateStatePayload) {
	i.publish(EventEnvelope{
		Kind:           envKindRehydrateState,
		RehydrateState: &p,
	})
}

// streamEnvelopeFromGlobal builds a multiplex StreamEnvelope from a
// launcher-wide GlobalEventEnvelope. No scope id — both
// InvestigationID and EditorSessionID are empty.
func streamEnvelopeFromGlobal(env GlobalEventEnvelope) StreamEnvelope {
	return StreamEnvelope{
		Seq:               env.Seq,
		Kind:              env.Kind,
		Timestamp:         env.Timestamp,
		RepoSummary:       env.RepoSummary,
		CodefixPRState:    env.CodefixPRState,
		WatchStatus:       env.WatchStatus,
		SignalCreated:     env.SignalCreated,
		ItemCaptured:      env.ItemCaptured,
		IngestRunStarted:  env.IngestRunStarted,
		IngestRunFinished: env.IngestRunFinished,
	}
}

// PublishWatchStatus fans a WatchStatusEvent out to the global stream.
func (m *Manager) PublishWatchStatus(ev WatchStatusEvent) {
	m.publishGlobalEvent(GlobalEventEnvelope{Kind: globalKindWatchStatus, WatchStatus: &ev})
}

// PublishSignalCreated fans a SignalCreatedEvent out to the global stream.
func (m *Manager) PublishSignalCreated(ev SignalCreatedEvent) {
	m.publishGlobalEvent(GlobalEventEnvelope{Kind: globalKindSignalCreated, SignalCreated: &ev})
}

// PublishItemCaptured fans an ItemCapturedEvent out to the global stream.
func (m *Manager) PublishItemCaptured(ev ItemCapturedEvent) {
	m.publishGlobalEvent(GlobalEventEnvelope{Kind: globalKindItemCaptured, ItemCaptured: &ev})
}

// PublishIngestRunStarted fans an IngestRunStartedEvent out to the global stream.
func (m *Manager) PublishIngestRunStarted(ev IngestRunStartedEvent) {
	m.publishGlobalEvent(GlobalEventEnvelope{Kind: globalKindIngestRunStarted, IngestRunStarted: &ev})
}

// PublishIngestRunFinished fans an IngestRunFinishedEvent out to the global stream.
func (m *Manager) PublishIngestRunFinished(ev IngestRunFinishedEvent) {
	m.publishGlobalEvent(GlobalEventEnvelope{Kind: globalKindIngestRunFinished, IngestRunFinished: &ev})
}

// streamEnvelopeFromInvestigation builds a multiplex StreamEnvelope
// from a per-investigation EventEnvelope. Copies all payload fields.
// Caller-set FanSeq is overwritten by Manager.PublishStream.
func streamEnvelopeFromInvestigation(env EventEnvelope, investigationID string) StreamEnvelope {
	return StreamEnvelope{
		Seq:             env.Seq,
		Kind:            env.Kind,
		Timestamp:       env.Timestamp,
		InvestigationID: investigationID,
		SessionID:       env.SessionID,
		Subtype:         env.Subtype,
		Text:            env.Text,
		ToolID:          env.ToolID,
		ToolName:        env.ToolName,
		ToolInput:       env.ToolInput,
		ParentToolID:    env.ParentToolID,
		PushState:       env.PushState,
		RehydrateState:  env.RehydrateState,
		Usage:           env.Usage,
		CostUSD:         env.CostUSD,
	}
}

// streamEnvelopeFromEditor builds a multiplex StreamEnvelope from an
// editor.Event. Editor envelopes share most field shapes with
// EventEnvelope but live in a separate package, so we convert
// explicitly rather than alias.
func streamEnvelopeFromEditor(env editor.Event, editorSessionID string) StreamEnvelope {
	return StreamEnvelope{
		Seq:             env.Seq,
		Kind:            env.Kind,
		Timestamp:       env.Timestamp,
		EditorSessionID: editorSessionID,
		SessionID:       env.SessionID,
		Subtype:         env.Subtype,
		Text:            env.Text,
		ToolID:          env.ToolID,
		ToolName:        env.ToolName,
		ToolInput:       env.ToolInput,
		ParentToolID:    env.ParentToolID,
	}
}

// stopLive halts the live-streaming side of the investigation —
// cancels the claude context — while leaving the persistence store
// open. Used by archive: archived investigations stay registered with
// the manager and still publish envelopes (push_state, pr_state,
// label) that must be persisted so they replay correctly after a
// launcher restart.
func (i *Investigation) stopLive() {
	if i.cancel != nil {
		i.cancel()
	}
}

// Close releases all resources owned by the investigation. Idempotent.
// Cancels the per-investigation context (drains any in-flight claude
// CLI), stops the port-forward, and drains the persistence store.
//
// Reserve this for terminal-lifecycle calls — Manager.Remove and
// Manager.Shutdown. Archive uses stopLive instead so post-archive
// publishes (push_state, pr_state, label) still reach disk.
func (i *Investigation) Close() {
	i.stopLive()
	if i.store != nil {
		i.store.close()
	}
}

// Manager tracks live investigations for the lifetime of a server. It's
// concurrency-safe; multiple browser tabs can each kick off their own
// investigation against different clusters in the same launcher process.
type Manager struct {
	mu           sync.RWMutex
	parentCtx    context.Context
	sessionsRoot string
	byID         map[string]*Investigation
	closed       bool

	// terminalHook is called when an investigation reaches a terminal
	// lifecycle state (archive, delete, auto-mode finished/aborted).
	// Wired by server.New to bridge the watches subsystem's slot-release.
	// nil disables the hook. Guarded by mu (R when reading, W when setting).
	terminalHook func(invID, watchID string)

	// closeHook is called when an investigation is removed or the
	// manager shuts down — every Close() path fires it. Used to
	// release per-investigation resources (e.g. prom port-forwards)
	// without entangling them in Investigation itself.
	// nil disables the hook. Guarded by mu (R when reading, W when setting).
	closeHook func(invID string)

	// globalMu guards globalNextSeq and globalRing. Acquired before
	// globalRing.mu — never the reverse.
	globalMu      sync.Mutex
	globalNextSeq int
	globalRing    *globalRing

	// Multiplex SSE pool — replaces per-investigation, per-global, and
	// per-editor subscriber pools. One persistent connection per
	// browser tab; every server-emitted event fans out here, tagged
	// with a scope id (InvestigationID, EditorSessionID, or none for
	// launcher-wide).
	streamMu          sync.Mutex
	streamFanSeq      int
	streamSubscribers map[string]*streamSubscriber
	streamRing        *streamRing
}

// streamSubscriber holds the per-tab fan-out channel and a done
// channel used to signal teardown. The data channel (ch) is never
// closed from the closer side — closing a channel that PublishStream
// may be concurrently sending to causes a panic. Instead, done is
// closed once (idempotent) to tell the SSE handler to exit, and the
// data channel is left for the GC once all references drop.
type streamSubscriber struct {
	ch   chan StreamEnvelope
	done chan struct{}
}

// NewManager returns a Manager rooted at parentCtx and writing per-session
// transcripts under sessionsRoot. When parentCtx is cancelled (e.g. on
// server shutdown), every per-investigation context derived via Register
// is cancelled too.
func NewManager(parentCtx context.Context, sessionsRoot string) *Manager {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	return &Manager{
		parentCtx:         parentCtx,
		sessionsRoot:      sessionsRoot,
		byID:              map[string]*Investigation{},
		globalRing:        newGlobalRing(),
		streamSubscribers: map[string]*streamSubscriber{},
		streamRing:        newStreamRing(256),
	}
}

// ParentContext returns the manager's lifetime context. Background
// goroutines (e.g. the async session push) use this so they outlive
// the HTTP request that kicked them off but still die when the
// launcher shuts down. Per-investigation contexts (inv.ctx) are
// cancelled on Close()/archive, so they're unsuitable here.
func (m *Manager) ParentContext() context.Context {
	return m.parentCtx
}

// SetTerminalHook registers a callback fired when an investigation reaches
// a terminal lifecycle state (archive, delete, auto-mode finished/aborted).
// nil disables the hook. Called by server.New to bridge the watches
// subsystem's per-watch Spawner slot-release.
func (m *Manager) SetTerminalHook(fn func(invID, watchID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminalHook = fn
}

// SetCloseHook registers a callback fired when an investigation's
// resources are torn down (Remove, Shutdown). The hook receives the
// investigation id. nil disables it.
func (m *Manager) SetCloseHook(fn func(invID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeHook = fn
}

// fireTerminal invokes the registered hook when present. Idempotent —
// callers may invoke it on every transition; the watches Spawner's
// OnInvestigationTerminal is itself idempotent. Only fires when the
// investigation has a non-empty OriginatingWatchID.
func (m *Manager) fireTerminal(inv *Investigation) {
	if inv == nil {
		return
	}
	inv.mu.Lock()
	watchID := inv.OriginatingWatchID
	invID := inv.ID
	inv.mu.Unlock()
	if watchID == "" {
		return
	}
	m.mu.RLock()
	hook := m.terminalHook
	m.mu.RUnlock()
	if hook != nil {
		hook(invID, watchID)
	}
}

// Restore scans sessionsRoot for previously-persisted investigations and
// loads them into the manager. Previously-live sessions (archived=false on
// disk) come back with needsRehydrate=true so the rehydrate path can re-
// attach a live claude/MCP/port-forward in this process. Explicitly-archived
// sessions come back with archived=true and needsRehydrate=false — they're
// read-only and the UI surfaces their transcript but prevents new prompts.
func (m *Manager) Restore() error {
	dirs, err := scanSessionDirs(m.sessionsRoot)
	if err != nil {
		return fmt.Errorf("scan sessions: %w", err)
	}
	for _, dir := range dirs {
		inv, err := loadInvestigation(dir)
		if err != nil || inv == nil {
			continue
		}
		// Derive a per-investigation context from the manager's
		// parentCtx — same shape as Register so Close() (cancel)
		// works, every code path inside Investigation can rely on
		// inv.ctx being non-nil, and a launcher shutdown cancels
		// restored sessions just like fresh ones.
		inv.ctx, inv.cancel = context.WithCancel(m.parentCtx)
		// Attach a store so post-restore mutations (archive bit,
		// PushedAt / PushURL on a fresh push, …) actually round-trip
		// to disk. Without this `Manager.persistMetadata` silently
		// no-ops because inv.store is nil, and the next launcher
		// restart loses the field. Mirrors what Register does for
		// freshly-created investigations.
		if inv.SessionDir != "" {
			inv.store = newStore(inv.SessionDir)
		}
		// Sweep stale stream files from the previous launcher run. These
		// are raw pod-log dumps that belong to tailFollow goroutines that
		// no longer exist. Removing them prevents unbounded disk growth
		// and avoids search_log_stream returning stale results. The next
		// stream_log_until call will recreate the streams dir as needed.
		if inv.SessionDir != "" {
			streamsDir := filepath.Join(inv.SessionDir, "streams")
			_ = os.RemoveAll(streamsDir) // best-effort; ignore error
		}
		// Recover orphaned push-in-progress state. The goroutine that
		// owned this flag is gone (server restarted); the triagent-mcp
		// subprocess, if it survived, has nowhere to deliver its
		// output. Surface the orphan to the operator as an error so
		// the UI re-enables the push button.
		if inv.PushInProgress {
			inv.PushInProgress = false
			inv.PushStartedAt = nil
			inv.PushError = "push interrupted by server restart"
			if inv.store != nil {
				_ = inv.store.writeMetadata(inv.Snapshot())
			}
		}
		// Skip if a current session somehow shares the id (shouldn't
		// happen — IDs are random — but keeping the invariant).
		m.mu.Lock()
		if _, exists := m.byID[inv.ID]; !exists {
			inv.manager = m
			m.byID[inv.ID] = inv
		}
		m.mu.Unlock()
	}
	return nil
}

// Register stores an investigation, gives it a context derived from the
// manager's parent context, attaches a persistence store rooted at
// SessionDir, writes the initial metadata.json, and returns it. If
// inv.ID is empty a fresh id is generated; callers can pre-allocate an
// id (e.g. to bake into the per-session MCP config before claude even
// spawns) and pass it through inv.ID.
func (m *Manager) Register(inv *Investigation) (*Investigation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("manager is shut down")
	}
	if inv.ID == "" {
		id, err := NewID()
		if err != nil {
			return nil, fmt.Errorf("generate investigation id: %w", err)
		}
		inv.ID = id
	} else if _, exists := m.byID[inv.ID]; exists {
		return nil, fmt.Errorf("investigation id %q already registered", inv.ID)
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	inv.ctx, inv.cancel = context.WithCancel(m.parentCtx)
	if inv.SessionDir != "" {
		inv.store = newStore(inv.SessionDir)
		if err := inv.store.writeMetadata(inv.Snapshot()); err != nil {
			// Persistence is best-effort: log to stderr but don't fail
			// the whole registration. The in-memory investigation is
			// still usable for the live session.
			fmt.Fprintf(os.Stderr, "investigate: write metadata for %s: %v\n", inv.ID, err)
		}
	}
	inv.manager = m
	m.byID[inv.ID] = inv
	return inv, nil
}

// RegisterForTest mints a minimal Investigation with the given id and
// inserts it into the manager. Exposes just enough wiring (id, session
// dir, store, parent context, manager back-pointer) for tests that
// exercise handler-level behaviour without going through preflight.
// Test-only — production callers use Register.
func (m *Manager) RegisterForTest(id string) *Investigation {
	inv := &Investigation{
		ID:         id,
		SessionDir: filepath.Join(m.sessionsRoot, id),
		CreatedAt:  time.Now().UTC(),
	}
	inv.ctx, inv.cancel = context.WithCancel(m.parentCtx)
	if inv.SessionDir != "" {
		inv.store = newStore(inv.SessionDir)
	}
	inv.manager = m
	m.mu.Lock()
	m.byID[inv.ID] = inv
	m.mu.Unlock()
	return inv
}

// AdoptFromDir loads a session directory already on disk and adds the
// resulting (archived) Investigation to the manager. Used by the share-
// import handler so the import path doesn't have to duplicate
// loadInvestigation. Errors if the directory has no metadata or if the
// generated id collides with an existing entry.
//
// Imported share-bundle sessions are always archived — they have no live
// MCP/kubeconfig wiring on this machine — regardless of the donor metadata's
// archived bit. Only AdoptFromDir forces this; Restore preserves whatever bit
// is on disk.
func (m *Manager) AdoptFromDir(dir string) (*Investigation, error) {
	inv, err := loadInvestigation(dir)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, fmt.Errorf("no metadata in %q", dir)
	}
	// Force archived regardless of donor metadata: this machine has no live
	// claude session, port-forward, or MCP wiring for a share-bundle import.
	inv.archived = true
	inv.needsRehydrate = false
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("manager is shut down")
	}
	if _, exists := m.byID[inv.ID]; exists {
		return nil, fmt.Errorf("investigation id %q already registered", inv.ID)
	}
	inv.manager = m
	m.byID[inv.ID] = inv
	return inv, nil
}

// Get returns an investigation by ID, or nil if not found.
func (m *Manager) Get(id string) *Investigation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id]
}

// List returns DTOs for every registered investigation, newest first.
// Snapshotting per-investigation under the inv's own lock keeps reads
// race-free even while a session is streaming.
func (m *Manager) List() []InvestigationDTO {
	m.mu.RLock()
	investigations := make([]*Investigation, 0, len(m.byID))
	for _, v := range m.byID {
		investigations = append(investigations, v)
	}
	m.mu.RUnlock()

	out := make([]InvestigationDTO, 0, len(investigations))
	for _, inv := range investigations {
		out = append(out, inv.Snapshot())
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].CreatedAt.Before(out[j].CreatedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Remove closes and forgets an investigation. Idempotent. Also deletes
// the on-disk session directory if removeFromDisk is true — used by the
// DELETE endpoint to fully purge an investigation. Returns whether the
// id was known.
func (m *Manager) Remove(id string, removeFromDisk bool) bool {
	m.mu.Lock()
	inv := m.byID[id]
	delete(m.byID, id)
	m.mu.Unlock()
	if inv == nil {
		return false
	}
	dir := inv.SessionDir
	inv.Close()
	m.mu.RLock()
	hook := m.closeHook
	m.mu.RUnlock()
	if hook != nil {
		hook(inv.ID)
	}
	if removeFromDisk && dir != "" {
		_ = os.RemoveAll(dir)
	}
	return true
}

// Shutdown closes every investigation. After Shutdown, Register fails.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	investigations := make([]*Investigation, 0, len(m.byID))
	for _, v := range m.byID {
		investigations = append(investigations, v)
	}
	m.byID = map[string]*Investigation{}
	m.closed = true
	m.mu.Unlock()
	m.mu.RLock()
	hook := m.closeHook
	m.mu.RUnlock()
	for _, inv := range investigations {
		inv.Close()
		if hook != nil {
			hook(inv.ID)
		}
	}
}

// ReconcileUpstreamPushed sweeps every investigation the manager knows
// about and backfills PushedAt where the corresponding session.md
// already exists in the upstream sessions clone. Lets sessions that
// were pushed before the per-session persistence path was reliable
// (or pushed by another machine and synced down) show up correctly as
// "synced" without the operator having to re-push.
//
// Best-effort: silently skips when sessionsPath is empty, when the
// session.md file isn't there, or when the per-investigation persist
// fails. Reads the file's mtime as the synthetic PushedAt timestamp —
// it's the closest cheap signal we have without a richer source of
// truth (the original push time is gone).
func (m *Manager) ReconcileUpstreamPushed(sessionsPath string) {
	if sessionsPath == "" {
		return
	}
	m.mu.RLock()
	all := make([]*Investigation, 0, len(m.byID))
	for _, inv := range m.byID {
		all = append(all, inv)
	}
	m.mu.RUnlock()

	for _, inv := range all {
		inv.mu.Lock()
		alreadyPushed := inv.PushedAt != nil
		slug := computeSessionSlug(inv.CreatedAt, inv.Namespace, inv.ID)
		inv.mu.Unlock()
		if alreadyPushed {
			continue
		}
		if !sessionSlugPattern.MatchString(slug) {
			continue
		}
		mdPath := filepath.Join(sessionsPath, monthDirForSlug(slug), slug, "session.md")
		st, err := os.Stat(mdPath)
		if err != nil {
			continue
		}
		mt := st.ModTime().UTC()
		inv.mu.Lock()
		inv.PushedAt = &mt
		inv.mu.Unlock()
		_ = m.persistMetadata(inv)
	}
}

// RefreshPRStates pulls a single `gh pr list` snapshot of the upstream
// sessions repo and reconciles each known PushURL against the result.
// Returns the count of investigations whose state changed (for caller
// logging / telemetry); errors from the gh call are returned to the
// caller — they're routine when gh isn't authed and we don't want to
// silently mask them.
//
// Wholesale (one network call, N updates) so the cost stays flat as
// the launcher accumulates pushed sessions. The caller decides cadence
// — startup, on-demand from the frontend, or a periodic ticker.
func (m *Manager) RefreshPRStates(ctx context.Context, sessionsRepo string) (int, error) {
	if sessionsRepo == "" {
		return 0, nil
	}
	states, err := fetchPRStates(ctx, sessionsRepo)
	if err != nil {
		return 0, err
	}

	m.mu.RLock()
	all := make([]*Investigation, 0, len(m.byID))
	for _, inv := range m.byID {
		all = append(all, inv)
	}
	m.mu.RUnlock()

	changed := 0
	for _, inv := range all {
		inv.mu.Lock()
		url := inv.PushURL
		prevState := inv.PRState
		prevMerged := inv.PRMergedAt
		prevClosed := inv.PRClosedAt
		inv.mu.Unlock()
		if url == "" {
			continue
		}
		info, ok := states[canonicalURL(url)]
		if !ok {
			// PR not found — could mean the PR was deleted, or gh
			// returned a paginated subset, or we're targeting a
			// different repo. Don't touch existing state; leave it
			// to the operator to refresh later.
			continue
		}
		if info.State == prevState &&
			samePtrTime(info.MergedAt, prevMerged) &&
			samePtrTime(info.ClosedAt, prevClosed) {
			continue
		}
		inv.mu.Lock()
		inv.PRState = info.State
		inv.PRMergedAt = info.MergedAt
		inv.PRClosedAt = info.ClosedAt
		inv.mu.Unlock()
		_ = m.persistMetadata(inv)
		changed++
	}
	return changed, nil
}

// samePtrTime reports whether two *time.Time pointers represent the
// same instant — including both-nil. Used to short-circuit
// RefreshPRStates writes when nothing actually changed for an
// investigation.
func samePtrTime(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

// MaxLabelLen mirrors mcp/internal/meta.MaxLabelLen — kept locally so
// the launcher rejects oversize labels without importing the meta MCP
// package (cross-binary dependency). Update both if the cap changes.
const MaxLabelLen = 80

// SetLabel mutates the investigation's sidebar label. Validates the
// input (trimmed, non-empty, ≤80), persists the new metadata, and
// publishes a label envelope so live subscribers refresh without
// polling.
//
// Last-write-wins on repeat calls. Both the meta MCP (agent) and the
// operator's pencil-rename go through this method, so the contention
// model is the inv.mu lock used by every other mutation here.
func (m *Manager) SetLabel(id, label string) error {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return fmt.Errorf("label cannot be empty")
	}
	if len(trimmed) > MaxLabelLen {
		return fmt.Errorf("label must be ≤%d characters (got %d)", MaxLabelLen, len(trimmed))
	}
	inv := m.Get(id)
	if inv == nil {
		return fmt.Errorf("investigation %q not found", id)
	}
	inv.mu.Lock()
	inv.Label = trimmed
	inv.mu.Unlock()
	if err := m.persistMetadata(inv); err != nil {
		return fmt.Errorf("persist label: %w", err)
	}
	inv.publish(EventEnvelope{Kind: envKindLabel, Text: trimmed})
	return nil
}

// persistMetadata writes the investigation's current state to its on-disk
// metadata.json. No-op if the investigation has no store (in-memory only).
func (m *Manager) persistMetadata(inv *Investigation) error {
	inv.mu.Lock()
	s := inv.store
	inv.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.writeMetadata(inv.Snapshot())
}

// persistAutoState writes the current Auto state to disk via the per-
// investigation store. Caller must NOT hold inv.mu — Snapshot acquires
// it internally to take a consistent DTO. Wired into the auto/finish,
// auto/takeover, auto/resume, and auto/request-takeover handlers so
// on-disk metadata.json always reflects the latest phase. Best-effort:
// errors are swallowed because the in-memory mutation has already
// happened and the next persistMetadata call (e.g. on a follow-up) will
// retry the write.
func (m *Manager) persistAutoState(i *Investigation) {
	if i == nil {
		return
	}
	i.mu.Lock()
	store := i.store
	i.mu.Unlock()
	if store == nil {
		return
	}
	_ = store.writeMetadata(i.Snapshot())
}

// AutoOptions configures Manager.EnableAuto. Production callers (the
// preflight path in T18) populate OperatorMCPConfigPath / OperatorCwd /
// Briefing / ClaudeBinary / Env; tests override BackendFactory to skip
// spawning the real claude binary.
type AutoOptions struct {
	// OperatorMCPConfigPath is the per-investigation op-mcp.json the
	// preflight pipeline writes for the operator agent. The operator-
	// side claude session is launched with --mcp-config <this>.
	OperatorMCPConfigPath string

	// OperatorCwd is the working directory for the operator agent's
	// claude session. Claude keys its on-disk session jsonl by cwd, so
	// this must stay stable across launcher restarts for Resume to find
	// the prior session. The operator skills are extracted into
	// <OperatorCwd>/.claude/skills/<slug>/SKILL.md.
	OperatorCwd string

	// Briefing is the opening prompt for the operator agent's Start
	// call. Composed by the caller from the investigation's incident
	// summary + cluster context.
	Briefing string

	// ClaudeBinary is reserved for an override-the-binary knob; the
	// claude.Session today picks it off PATH, so this field is unused
	// in the default factory. Kept on the struct so the API doesn't
	// change when a future task pipes a per-investigation override
	// through.
	ClaudeBinary string

	// Env is extra "KEY=value" entries appended to the operator
	// session's process env. The launcher uses this to pin KUBECONFIG
	// so the operator agent inherits the same cluster context the
	// investigation was preflighted against.
	Env []string

	// BackendFactory is a test seam. nil → defaultAutoBackendFactory
	// (spawns a real claude.Session). Tests override to inject a fake
	// implementing autoBackendish.
	BackendFactory func(AutoOptions) (autoBackendish, error)

	// Profile is the active deployment profile. Used to thread
	// Models.Investigation to the operator-agent claude session.
	Profile *profile.Profile
}

// defaultAutoBackendFactory builds a production claude-backed
// autoBackendish by spawning a fresh claude.Session for the operator
// agent (separate from the investigation's blue session). The umbrella
// operator-role SKILL.md is forwarded via --append-system-prompt so
// claude is anchored on the role even when on-disk skill discovery
// isn't consulted.
//
// The auto.ClaudeBackend it returns satisfies autoBackendish because
// *auto.ClaudeBackend has both Resume(ctx, prompt) error and
// SessionID() string.
func defaultAutoBackendFactory(opts AutoOptions) (autoBackendish, error) {
	system := string(operatorskills.UmbrellaContent())
	var investigationModel string
	if opts.Profile != nil {
		investigationModel = opts.Profile.Models.Investigation
	}
	sess, err := claude.NewSession(opts.OperatorMCPConfigPath, []string{
		"mcp__triagent-agent-operator__send_message",
		"mcp__triagent-agent-operator__finish",
		"mcp__triagent-agent-operator__request_takeover",
		"mcp__triagent-agent-operator__approve_proposal",
	}, claude.SessionOpts{
		Cwd:                opts.OperatorCwd,
		Env:                opts.Env,
		AppendSystemPrompt: system,
		Model:              investigationModel,
	})
	if err != nil {
		return nil, fmt.Errorf("new operator claude session: %w", err)
	}
	// onEvent is a no-op for T17: the spec defers operator-event
	// forwarding into the investigation transcript to a follow-up
	// task (after the frontend grows the styling hooks in T24). Drain
	// the events so the adapter's stdout goroutine doesn't block.
	// TODO(auto-mode): in the post-T24 follow-up, publish operator
	//   envelopes into the investigation transcript via a synthetic
	//   "operator_assistant" envelope kind so the human can see what
	//   the operator agent is saying without inspecting the operator
	//   session's jsonl directly.
	return auto.NewFromClaude(sess, func(_ claude.Event) {}), nil
}

// StartFromWatch is the post-Register hook the watches subsystem uses
// when a signal spawns an investigation. It launches the main claude
// session via the provided seam (production passes inv.Start; tests
// pass a fake so they don't exec the claude binary) and, when autoOpts
// is non-nil, fires EnableAuto in a background goroutine so the
// spawner's Running slot isn't held by claude's multi-second warm-up.
//
// Errors from start are logged, not returned: createFromWatch has
// already produced an invID and the Investigation is registered, so a
// failed Start leaves the session in started=false land where the
// operator can manually retry from the SessionView. Returning the error
// would propagate to the spawner, which would mark the signal failed —
// the worse user-facing outcome for what's typically a transient claude
// launch hiccup.
func (m *Manager) StartFromWatch(inv *Investigation, start func() error, autoOpts *AutoOptions) {
	if err := start(); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: start watch-spawned %s: %v\n", inv.ID, err)
	}
	if autoOpts != nil {
		go func() {
			if err := m.EnableAuto(inv, *autoOpts); err != nil {
				fmt.Fprintf(os.Stderr, "investigate: EnableAuto %s: %v\n", inv.ID, err)
			}
		}()
	}
}

// EnableAuto spawns the AutoOperator and starts the per-investigation
// boundary watcher. Idempotent — calling on an already-active auto-mode
// investigation is a no-op so /preflight retries don't double-up the
// operator goroutine.
//
// The opening Start call is synchronous: it dispatches the briefing
// prompt to the operator's claude session and blocks until that turn
// completes. Callers that don't want to block the preflight HTTP
// response on a multi-second claude warm-up should invoke EnableAuto in
// a goroutine; the boundary watcher and notifyAutoResume both run
// detached from the request that triggered them.
func (m *Manager) EnableAuto(inv *Investigation, opts AutoOptions) error {
	inv.mu.Lock()
	if inv.Auto.Enabled && inv.Auto.IsActive() && inv.autoOp != nil {
		inv.mu.Unlock()
		return nil
	}
	// Reserve the in-flight slot before we drop the lock so a concurrent
	// retry (e.g. /preflight invoked twice during the multi-second
	// claude warm-up) bails out instead of building a parallel operator
	// + watcher. The idempotency check above can't catch this on its
	// own — autoOp stays nil until after factory(opts) returns.
	if inv.autoEnableInFlight {
		inv.mu.Unlock()
		return nil
	}
	inv.autoEnableInFlight = true
	// Preserve LastSentSeq across the Auto state reset so a re-enable
	// (or a restart on the same Investigation) keeps the boundary
	// watcher's diff origin.
	inv.Auto = auto.State{
		Enabled:     true,
		Phase:       auto.PhaseStarted,
		LastSentSeq: inv.Auto.LastSentSeq,
	}
	inv.mu.Unlock()

	// Emit the initial auto_mode_state envelope so SSE consumers see auto
	// mode becoming active. The subsequent op.Start → snapshot →
	// applyAutoState path will see no phase change (prevPhase ==
	// PhaseStarted == s.Phase) and won't re-publish a duplicate.
	inv.publish(EventEnvelope{
		Kind:     envKindAutoModeState,
		AutoMode: &AutoModePayload{Phase: "started"},
	})

	// Clear the sentinel on every exit path so a failed setup can be
	// retried. The defer fires before the synchronous op.Start return
	// at the bottom of the function, which is fine — autoOp is set by
	// then, so the idempotency check at the top will catch concurrent
	// callers without needing the sentinel.
	defer func() {
		inv.mu.Lock()
		inv.autoEnableInFlight = false
		inv.mu.Unlock()
	}()

	if opts.OperatorCwd != "" {
		if err := operatorskills.Extract(opts.OperatorCwd); err != nil {
			return fmt.Errorf("extract operator skills: %w", err)
		}
	}
	factory := opts.BackendFactory
	if factory == nil {
		factory = defaultAutoBackendFactory
	}
	backend, err := factory(opts)
	if err != nil {
		return err
	}
	op := auto.New(auto.Config{
		Backend:   backend,
		PersistFn: func(s auto.State) { m.applyAutoState(inv, s) },
	})

	// Allocate the boundary channel BEFORE Start so any envelopes the
	// opening turn produces are observed by the watcher. Sized
	// generously: the watcher only reads on `end`, and publish() drops
	// on a full buffer (next `end` recomputes from inv.events anyway).
	boundary := make(chan EventEnvelope, 64)
	inv.mu.Lock()
	inv.autoOp = op
	inv.autoBackend = backend
	inv.autoBoundary = boundary
	inv.mu.Unlock()

	go m.runAutoBoundaryWatcher(inv, boundary)
	return op.Start(inv.ctx, opts.Briefing)
}

// applyAutoState is the AutoOperator's PersistFn. Every phase
// transition the operator makes flows through here so the in-memory
// Auto block, the captured OperatorSessionID, and the on-disk
// metadata.json all stay coherent. Emits an auto_mode_state envelope
// on phase change so the frontend's SSE listener sees the transition
// without a refresh.
func (m *Manager) applyAutoState(inv *Investigation, s auto.State) {
	inv.mu.Lock()
	prevPhase := inv.Auto.Phase
	// Capture the live claude session id off the backend after every
	// transition. The first Start populates it asynchronously (claude
	// emits it on its first stream-json event), so we re-read every
	// time rather than once at EnableAuto.
	if inv.autoBackend != nil {
		if id := inv.autoBackend.SessionID(); id != "" {
			s.OperatorSessionID = id
		} else if inv.Auto.OperatorSessionID != "" {
			s.OperatorSessionID = inv.Auto.OperatorSessionID
		}
	}
	// LastSentSeq is owned by the boundary watcher, not the operator.
	// Preserve the watcher's tracking across operator-driven persists —
	// otherwise the next `end` envelope replays the full transcript
	// instead of the diff since the last wake.
	s.LastSentSeq = inv.Auto.LastSentSeq
	inv.Auto = s
	inv.mu.Unlock()
	if prevPhase != s.Phase {
		inv.publish(EventEnvelope{
			Kind:     envKindAutoModeState,
			AutoMode: &AutoModePayload{Phase: string(s.Phase)},
		})
		if s.Phase == auto.PhaseFinished || s.Phase == auto.PhaseAborted {
			m.fireTerminal(inv) // M6.4: release spawner slot on auto-mode terminal
		}
	}
	m.persistAutoState(inv)
}

// runAutoBoundaryWatcher reads the per-investigation boundary channel
// and, on each `end` envelope, builds an EventLite diff from
// Auto.LastSentSeq+1 to the current nextSeq and pushes it to the
// operator via op.Wake. The watcher exits when ctx is cancelled (i.e.
// on Investigation.Close). Backed by the boundary channel rather than
// the multiplex stream so it's scoped to one investigation and immune
// to slow-consumer drops in the multiplex fan-out.
func (m *Manager) runAutoBoundaryWatcher(inv *Investigation, boundary <-chan EventEnvelope) {
	ctx := inv.ctx
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-boundary:
			if !ok {
				return
			}
			if env.Kind != envKindEnd {
				continue
			}
			inv.mu.Lock()
			op := inv.autoOp
			active := op != nil && inv.Auto.IsActive()
			lastSeen := inv.Auto.LastSentSeq
			all := append([]EventEnvelope(nil), inv.events...)
			nextSeq := inv.nextSeq
			inv.mu.Unlock()
			if !active {
				continue
			}
			var diff []auto.EventLite
			for _, e := range all {
				if e.Seq <= lastSeen {
					continue
				}
				diff = append(diff, auto.EventLite{
					Seq:      e.Seq,
					Kind:     e.Kind,
					Text:     e.Text,
					ToolName: e.ToolName,
					Origin:   e.Origin,
				})
			}
			inv.mu.Lock()
			inv.Auto.LastSentSeq = nextSeq
			inv.mu.Unlock()
			// op.Wake blocks until the operator's claude turn
			// finishes (synchronous adapter). Run in a goroutine so
			// the watcher remains responsive to subsequent `end`
			// envelopes — though publish() drops on a full buffer,
			// so a long-running Wake is recovered from on the next
			// `end` regardless.
			go func(d []auto.EventLite) { _ = op.Wake(ctx, d) }(diff)
		}
	}
}

// notifyAutoResume is called by the /auto/resume handler when the
// human hands control back to the operator agent. Builds the catch-up
// span from envelopes >= PausedAtSeq (the boundary captured when the
// take-over started) and dispatches op.Resume in a goroutine so the
// HTTP request can return 200 immediately. The operator agent's
// claude turn driven by Resume can take several seconds.
func (m *Manager) notifyAutoResume(inv *Investigation) {
	inv.mu.Lock()
	op := inv.autoOp
	pausedAt := inv.Auto.PausedAtSeq
	all := append([]EventEnvelope(nil), inv.events...)
	ctx := inv.ctx
	inv.mu.Unlock()
	if op == nil {
		return
	}
	var span []auto.EventLite
	for _, e := range all {
		if e.Seq < pausedAt {
			continue
		}
		span = append(span, auto.EventLite{
			Seq:      e.Seq,
			Kind:     e.Kind,
			Text:     e.Text,
			ToolName: e.ToolName,
			Origin:   e.Origin,
		})
	}
	go func() { _ = op.Resume(ctx, span) }()
}

// NewID returns a 16-byte hex-encoded random investigation id. Long enough
// to be unguessable; short enough to fit in a URL. Exported so handlers
// can pre-allocate one before Register (e.g. to bake into the per-session
// MCP config so triagent-mcp telemetry knows where to report tool calls).
func NewID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// publishGlobalEvent stamps Seq+Timestamp, appends to the ring, and
// fans out to the multiplex stream. The ring serves reconnecting
// subscribers via SubscribeStream's backlog replay.
func (m *Manager) publishGlobalEvent(env GlobalEventEnvelope) {
	m.globalMu.Lock()
	m.globalNextSeq++
	env.Seq = m.globalNextSeq
	if env.Timestamp.IsZero() {
		env.Timestamp = time.Now().UTC()
	}
	m.globalRing.append(env)
	m.globalMu.Unlock()

	m.PublishStream(streamEnvelopeFromGlobal(env))
}

// SubscribeStream registers a tab as a multiplex-stream listener.
// connToken is a per-tab UUID supplied by the SSE handler reading
// /api/stream?conn=<token>. lastEventID is the Last-Event-ID header
// the browser sends on reconnect; the caller gets the ring backlog
// matching FanSeq > lastEventID first, then live envelopes on the
// returned channel, and a done channel that is closed when the
// subscription is torn down (via cancel or CloseStreamSubscriber).
// cancel removes the subscription — the SSE handler MUST defer it.
//
// The data channel (events) is never closed by the closer side.
// Teardown is signalled exclusively via done to avoid the
// send-on-closed-channel panic that arises when PublishStream's
// fan-out loop races against a concurrent close.
func (m *Manager) SubscribeStream(connToken string, lastEventID int) (
	backlog []StreamEnvelope,
	events <-chan StreamEnvelope,
	done <-chan struct{},
	cancel func(),
) {
	m.streamMu.Lock()
	sub := &streamSubscriber{
		ch:   make(chan StreamEnvelope, 64),
		done: make(chan struct{}),
	}
	if connToken != "" {
		// Last-writer-wins on the (hopefully impossible) UUID
		// collision: prior subscriber loses its explicit-close handle
		// but its handler's defer cancel() will still close its own done.
		m.streamSubscribers[connToken] = sub
	}
	backlogCopy := m.streamRing.replay(lastEventID)
	m.streamMu.Unlock()

	cancelFn := func() {
		m.streamMu.Lock()
		defer m.streamMu.Unlock()
		if existing, ok := m.streamSubscribers[connToken]; ok && existing == sub {
			delete(m.streamSubscribers, connToken)
		}
		// Idempotent done close.
		select {
		case <-sub.done:
			// already closed
		default:
			close(sub.done)
		}
	}
	return backlogCopy, sub.ch, sub.done, cancelFn
}

// CloseStreamSubscriber tears down the subscription registered under
// connToken. Returns true if a matching subscriber was found and
// signalled, false otherwise. Idempotent: safe to call even if the
// handler's deferred cancel has already run.
//
// Teardown is signalled via the per-subscriber done channel, not by
// closing the data channel. This avoids the send-on-closed-channel
// panic that arises when PublishStream's fan-out races a concurrent
// close.
func (m *Manager) CloseStreamSubscriber(connToken string) bool {
	if connToken == "" {
		return false
	}
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	sub, ok := m.streamSubscribers[connToken]
	if !ok {
		return false
	}
	delete(m.streamSubscribers, connToken)
	// Idempotent close — handler's deferred cancel may have already run.
	select {
	case <-sub.done:
	default:
		close(sub.done)
	}
	return true
}

// PublishStream stamps env.FanSeq, appends to the Last-Event-ID ring,
// and fans out non-blocking to every current subscriber. The send is
// non-blocking — slow consumers drop envelopes and recover via either
// EventSource auto-reconnect (Last-Event-ID replay from the ring) or
// the /transcript REST endpoint on next mount. This mirrors the
// non-blocking fan-out in the prior per-scope publish helpers.
//
// The fan-out select races done against the buffered send: if the
// subscriber has been torn down between the snapshot and the send,
// done is closed and we drop safely — no send-on-closed-channel panic.
func (m *Manager) PublishStream(env StreamEnvelope) {
	m.streamMu.Lock()
	m.streamFanSeq++
	env.FanSeq = m.streamFanSeq
	if env.Timestamp.IsZero() {
		env.Timestamp = time.Now().UTC()
	}
	m.streamRing.append(env)
	subs := make([]*streamSubscriber, 0, len(m.streamSubscribers))
	for _, sub := range m.streamSubscribers {
		subs = append(subs, sub)
	}
	m.streamMu.Unlock()

	for _, sub := range subs {
		// Non-blocking send with done-channel guard. If the subscriber
		// has been torn down between the snapshot and now, its done
		// channel is closed and we drop the envelope safely (no
		// send-on-closed-channel panic). Slow consumers (buffer full
		// but not torn down) also fall through the default — they
		// recover via reconnect Last-Event-ID replay.
		select {
		case <-sub.done:
			// subscriber has been torn down; drop
		case sub.ch <- env:
		default:
			// buffer full; drop, consumer recovers via reconnect
		}
	}
}
