package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/sourcehawk/triagent/internal/promforward"
)

// File names under each session directory:
//
//   metadata.json   the InvestigationDTO (without volatile fields)
//   events.jsonl    one EventEnvelope per line, append-only
//   mcp.json        the per-session MCP config (already written by preflight)
const (
	fileMetadata = "metadata.json"
	fileEvents   = "events.jsonl"
)

// persistedAuthor holds the git config identity captured at session creation.
type persistedAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// persistedMetadata is the on-disk shape. It mirrors InvestigationDTO but
// drops the live-only fields (Started, Streaming) — those are recomputed
// on restore: never started, never streaming.
type persistedMetadata struct {
	ID              string `json:"id"`
	Namespace       string `json:"namespace"`
	IncidentURL      string `json:"incidentUrl,omitempty"`
	SlackChannelURL  string `json:"slackChannelUrl,omitempty"`
	SlackChannelID   string `json:"slackChannelId,omitempty"`
	SlackChannelName string `json:"slackChannelName,omitempty"`
	Notes            string `json:"notes,omitempty"`
	Label            string `json:"label,omitempty"`
	Playbook         string `json:"playbook,omitempty"`
	MCPConfigPath   string `json:"mcpConfigPath"`
	DocsPrefix      string `json:"docsPrefix,omitempty"`
	SessionDir      string `json:"sessionDir"`
	// SlackMCPEnabled / IncidentioMCPEnabled / LinkedRepos are derived from
	// external state (the connections manager and launcher repo config) and
	// re-evaluated by rehydrate, so persisting them would only mask config
	// drift between sessions.
	CreatedAt string `json:"createdAt"` // RFC3339 for stable readability on disk

	// Set when this investigation came from a teammate's share bundle
	// rather than a live preflight on this launcher. Both fields are
	// populated together; absence means "native" investigation.
	ImportedFrom *ImportedFrom `json:"importedFrom,omitempty"`
	ImportedAt   time.Time     `json:"importedAt,omitempty"`

	// Author is the git config user.name/user.email captured at session
	// creation, baked into the session.md frontmatter and the share
	// bundle. Empty for sessions persisted before this field landed; the
	// push-PR handler resolves and writes it back at push time.
	Author persistedAuthor `json:"author,omitempty"`

	// PushedAt is set once the session has been successfully pushed to
	// the upstream sessions repo. Nil otherwise. Drives the "unsynced"
	// sidebar icon.
	PushedAt *time.Time `json:"pushedAt,omitempty"`

	// PushURL is the GitHub PR URL returned by `gh pr create` when the
	// session was pushed. Lives next to PushedAt so the SessionView's
	// archived banner can render a clickable "pushed ↗" link rather
	// than a plain text indicator.
	PushURL string `json:"pushUrl,omitempty"`

	// PRState is the most recent state we've seen on the PR via
	// `gh pr list`: "open", "merged", "closed", or empty (unknown).
	// Refreshed wholesale by Manager.RefreshPRStates. Lets the UI
	// distinguish a still-open PR from a merged or abandoned one, and
	// re-enables the push-to-upstream button when the operator closed
	// the PR without merging.
	PRState    string     `json:"prState,omitempty"`
	PRMergedAt *time.Time `json:"prMergedAt,omitempty"`
	PRClosedAt *time.Time `json:"prClosedAt,omitempty"`

	// PushInProgress is persisted so a server restart can recognise
	// orphaned in-flight pushes (the goroutine is gone, no one will
	// finish the work) and convert them to PushError on Restore.
	PushInProgress bool       `json:"pushInProgress,omitempty"`
	PushStartedAt  *time.Time `json:"pushStartedAt,omitempty"`
	PushError      string     `json:"pushError,omitempty"`

	// ClaudeSessionID is the claude --resume key captured from stream-json
	// on the first event of this session's first claude turn. Empty for
	// pre-existing sessions; rehydrate handles that as a real failure
	// surfaced inline.
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`

	// LaunchCwd is the working directory we launched claude from. Claude
	// keys its on-disk session jsonl by a hash of cwd, so --resume only
	// works if we re-launch from the same dir. Defaults to SessionDir
	// for new sessions.
	LaunchCwd string `json:"launchCwd,omitempty"`

	// KubeconfigPath is the resolved $KUBECONFIG (or ~/.kube/config) at
	// preflight time. Persisted so rehydrate after a restart re-uses the
	// same file even if the operator's shell-level KUBECONFIG drifted.
	KubeconfigPath string `json:"kubeconfigPath,omitempty"`

	// Archived is the operator-explicit archive bit. Persisted so a
	// launcher restart preserves it; never set automatically by Restore.
	Archived bool `json:"archived,omitempty"`

	// Auto holds the auto-mode lifecycle state. The `omitempty` tag is
	// cosmetic — encoding/json does not skip non-pointer struct values,
	// so manual-mode investigations still write `"auto":{"enabled":false}`
	// to metadata.json. Matches the existing Prom/Author pattern in this
	// file.
	Auto auto.State `json:"auto,omitempty"`

	// OriginatingWatchID is set when this investigation was spawned from a
	// signal-watch. Persisted so a rehydrated investigation still has its
	// watch link available for slot-release on the next terminal transition.
	OriginatingWatchID string `json:"originatingWatchId,omitempty"`

	// OriginatingSignal is the back-reference to the specific signal in
	// the watch's signals.jsonl that produced this investigation. Persisted
	// so reload paths can surface the chip without re-querying the watch.
	OriginatingSignal *OriginatingSignal `json:"originatingSignal,omitempty"`

	// PromTarget is the resolved per-investigation Prometheus port-forward
	// target. Persisted so a rehydrated investigation re-attaches the same
	// prom config without re-running the preflight form. Nil when no target
	// was configured for this investigation.
	PromTarget *promPersistedTarget `json:"promTarget,omitempty"`
	// PromDisabled, when true, explicitly opts this investigation out of
	// the prom MCP. Persisted alongside PromTarget so the rehydrate path
	// can pass the correct flags into preflight.Options.
	PromDisabled bool `json:"promDisabled,omitempty"`
}

// promPersistedTarget mirrors promforward.Target on disk. Using a local
// type keeps the persistence shape decoupled from the runtime type.
type promPersistedTarget struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// store guards file IO for one investigation. Writes are async — events
// flow through a buffered channel and a single writer goroutine drains
// them — so publish() doesn't block on disk and a slow disk doesn't
// stall the SSE fan-out.
type store struct {
	dir   string
	queue chan EventEnvelope
	once  sync.Once
	done  chan struct{}
	// metaMu serializes metadata writes (which are full-rewrites). Events
	// are append-only so they don't need it.
	metaMu sync.Mutex
	// closeMu / closed gate queueEvent against a closed queue. `select`
	// with a `default` does NOT survive sending on a closed channel —
	// the send still panics — so a publish racing with close() would
	// otherwise crash the server. Holding closeMu as an RWMutex lets
	// many publishers run concurrently; close() takes the write lock
	// once.
	closeMu sync.RWMutex
	closed  bool
}

func newStore(dir string) *store {
	s := &store{
		dir:   dir,
		queue: make(chan EventEnvelope, 256),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

// writeMetadata serializes the DTO (minus live-only fields) and writes
// it atomically.
func (s *store) writeMetadata(dto InvestigationDTO) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return writePersistedMetadata(s.dir, persistedMetadata{
		ID:              dto.ID,
		Namespace:       dto.Namespace,
		IncidentURL:      dto.IncidentURL,
		SlackChannelURL:  dto.SlackChannelURL,
		SlackChannelID:   dto.SlackChannelID,
		SlackChannelName: dto.SlackChannelName,
		Notes:            dto.Notes,
		Label:            dto.Label,
		Playbook:         dto.Playbook,
		MCPConfigPath:   dto.MCPConfigPath,
		DocsPrefix:      dto.DocsPrefix,
		SessionDir:      dto.SessionDir,
		CreatedAt: dto.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ImportedFrom:    dto.ImportedFrom,
		ImportedAt:      dto.ImportedAt,
		Author:          dto.Author,
		PushedAt:        dto.PushedAt,
		PushURL:         dto.PushURL,
		PRState:         dto.PRState,
		PRMergedAt:      dto.PRMergedAt,
		PRClosedAt:      dto.PRClosedAt,
		PushInProgress:  dto.PushInProgress,
		PushStartedAt:   dto.PushStartedAt,
		PushError:       dto.PushError,
		ClaudeSessionID: dto.ClaudeSessionID,
		LaunchCwd:       dto.LaunchCwd,
		KubeconfigPath:  dto.KubeconfigPath,
		Archived:           dto.Archived,
		Auto:               dto.Auto,
		OriginatingWatchID: dto.OriginatingWatchID,
		OriginatingSignal:  dto.OriginatingSignal,
		PromDisabled:       dto.PromDisabled,
		PromTarget:         promTargetToPersistedTarget(dto.PromTarget),
	})
}

// writePersistedMetadata atomically writes a metadata file. Used by both
// the live persistence store and the share-import path (which produces an
// archived session and so doesn't keep a live store around).
func writePersistedMetadata(dir string, meta persistedMetadata) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fileMetadata+".tmp")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, fileMetadata))
}

// writeEventsFile serialises a slice of envelopes as one JSON object per
// line into events.jsonl, replacing any prior contents. Used by the import
// path to populate a fresh session dir with the donor transcript.
func writeEventsFile(dir string, events []EventEnvelope) error {
	f, err := os.OpenFile(filepath.Join(dir, fileEvents), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

// queueEvent enqueues one envelope for asynchronous persistence. Drops
// silently if the queue is full — at 256 buffered events that's a sign
// the disk is genuinely stuck, and we'd rather lose persistence than
// stall the live SSE stream. Refresh-from-disk would still show the bulk
// of the transcript. Also drops silently if the store has been closed,
// rather than panicking on a send-to-closed-channel.
func (s *store) queueEvent(env EventEnvelope) {
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- env:
	default:
	}
}

// run is the writer goroutine. Drains queue, appends to events.jsonl.
func (s *store) run() {
	defer close(s.done)
	path := filepath.Join(s.dir, fileEvents)
	for env := range s.queue {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		body, err := json.Marshal(env)
		if err != nil {
			_ = f.Close()
			continue
		}
		_, _ = f.Write(body)
		_, _ = f.Write([]byte("\n"))
		_ = f.Close()
	}
}

// close shuts down the writer goroutine after draining any queued events.
// Idempotent.
func (s *store) close() {
	s.once.Do(func() {
		s.closeMu.Lock()
		s.closed = true
		close(s.queue)
		s.closeMu.Unlock()
		<-s.done
	})
}

// loadInvestigation reconstructs an Investigation from a session directory
// produced by an earlier launch. The archived bit is preserved from disk:
// sessions that were explicitly archived stay archived; previously-live
// sessions (archived=false on disk) come back with needsRehydrate=true so
// the rehydrate path can re-attach a live claude/MCP/port-forward in this
// process. Returns (nil, nil) if the directory has no metadata, since
// some session dirs (e.g. failed preflight) never persisted.
func loadInvestigation(dir string) (*Investigation, error) {
	body, err := os.ReadFile(filepath.Join(dir, fileMetadata))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	var meta persistedMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	inv := &Investigation{
		ID:              meta.ID,
		Namespace:       meta.Namespace,
		IncidentURL:      meta.IncidentURL,
		SlackChannelURL:  meta.SlackChannelURL,
		SlackChannelID:   meta.SlackChannelID,
		SlackChannelName: meta.SlackChannelName,
		Notes:            meta.Notes,
		Label:            meta.Label,
		Playbook:         meta.Playbook,
		MCPConfigPath:   meta.MCPConfigPath,
		DocsPrefix:      meta.DocsPrefix,
		SessionDir:      meta.SessionDir,
		ImportedFrom:    meta.ImportedFrom,
		ImportedAt:      meta.ImportedAt,
		Author:          meta.Author,
		PushedAt:        meta.PushedAt,
		PushURL:         meta.PushURL,
		PRState:         meta.PRState,
		PRMergedAt:      meta.PRMergedAt,
		PRClosedAt:      meta.PRClosedAt,
		PushInProgress:  meta.PushInProgress,
		PushStartedAt:   meta.PushStartedAt,
		PushError:       meta.PushError,
		ClaudeSessionID: meta.ClaudeSessionID,
		LaunchCwd:       meta.LaunchCwd,
		KubeconfigPath:  meta.KubeconfigPath,
		Auto:               meta.Auto,
		OriginatingWatchID: meta.OriginatingWatchID,
		OriginatingSignal:  meta.OriginatingSignal,
		PromTarget:         persistedTargetToPromTarget(meta.PromTarget),
		PromDisabled:       meta.PromDisabled,
		archived:           meta.Archived,
		needsRehydrate:  !meta.Archived,
		// A non-archived restored session was started in some prior
		// launcher process — its claude conversation (if any) lives on
		// disk under the launchCwd's project hash. Marking started=true
		// makes Investigation.Start return "session already started" if
		// the frontend's auto-start useEffect fires; the rehydrate path
		// is the only correct way to bring the live wiring back up, and
		// it runs on the first follow-up. Archived sessions stay
		// started=false — they're read-only and never receive a Start.
		started: !meta.Archived,
	}
	if t, err := parseTimestamp(meta.CreatedAt); err == nil {
		inv.CreatedAt = t
	}

	// Hydrate ActiveContext from the on-disk file rather than from a
	// duplicate metadata field. The file is canonical — the k8s MCP
	// writes it on switch_context and the launcher's prom resolver
	// already reads it through readActiveContext. Errors and missing
	// files mean "no pre-selection" and leave ActiveContext empty.
	if name, err := readActiveContext(inv.SessionDir); err == nil {
		inv.ActiveContext = name
	}

	// Replay events into the backlog. New subscribers replay it on connect,
	// so the sidebar's "open" click renders the transcript exactly the way
	// the live session did.
	events, err := loadEvents(dir)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	inv.events = events
	if len(events) > 0 {
		inv.nextSeq = events[len(events)-1].Seq
	}
	// Replay usage / cost totals so sessions resurrected from disk surface
	// the same numbers in the sidebar / chat footer as they did before the
	// launcher restart. Mirrors the publish-time accumulation in
	// Investigation.publish (manager.go): per-call usage from EventUsage
	// envelopes; invocation roll-up cost from EventResult envelopes.
	//
	// Old sessions persisted under the previous (under-reporting) shape
	// have no envKindUsage envelopes — for those, fall back to the result
	// envelopes so their totals don't suddenly go to zero. The fallback is
	// detectably wrong (it's still the last-API-call snapshot, not the
	// true invocation total), but matching what the operator saw at the
	// time is the right behaviour for already-closed sessions.
	hasUsageEnvelopes := false
	for _, env := range events {
		if env.Kind == envKindUsage {
			hasUsageEnvelopes = true
			break
		}
	}
	for _, env := range events {
		switch {
		case env.Kind == envKindUsage && env.Usage != nil:
			inv.totalUsage.InputTokens += env.Usage.InputTokens
			inv.totalUsage.OutputTokens += env.Usage.OutputTokens
			inv.totalUsage.CacheCreationInputTokens += env.Usage.CacheCreationInputTokens
			inv.totalUsage.CacheReadInputTokens += env.Usage.CacheReadInputTokens
		case env.Kind == envKindResult:
			inv.totalCostUSD += env.CostUSD
			if !hasUsageEnvelopes && env.Usage != nil {
				inv.totalUsage.InputTokens += env.Usage.InputTokens
				inv.totalUsage.OutputTokens += env.Usage.OutputTokens
				inv.totalUsage.CacheCreationInputTokens += env.Usage.CacheCreationInputTokens
				inv.totalUsage.CacheReadInputTokens += env.Usage.CacheReadInputTokens
			}
		}
	}
	return inv, nil
}

// loadEvents reads events.jsonl line-by-line. Tolerates a missing file
// (no events yet) and skips malformed lines so a corrupt tail doesn't
// destroy an otherwise-valid transcript.
func loadEvents(dir string) ([]EventEnvelope, error) {
	f, err := os.Open(filepath.Join(dir, fileEvents))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []EventEnvelope
	sc := bufio.NewScanner(f)
	// Tool results can be very long; bump the scanner buffer to 4 MiB.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var env EventEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		out = append(out, env)
	}
	return out, sc.Err()
}

// scanSessionDirs lists every direct child of root that looks like a
// session directory we wrote. Returns oldest-first so the manager can
// register them in chronological order and List() picks them up.
func scanSessionDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(root, e.Name()))
	}
	sort.Strings(dirs)
	return dirs, nil
}

func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// promTargetToPersistedTarget converts a runtime promforward.Target pointer
// to its on-disk representation. Returns nil for a nil input.
func promTargetToPersistedTarget(t *promforward.Target) *promPersistedTarget {
	if t == nil {
		return nil
	}
	return &promPersistedTarget{
		Service:   t.Service,
		Namespace: t.Namespace,
		Port:      t.Port,
	}
}

// persistedTargetToPromTarget converts an on-disk promPersistedTarget back
// to a runtime promforward.Target pointer. Returns nil for a nil input.
func persistedTargetToPromTarget(t *promPersistedTarget) *promforward.Target {
	if t == nil {
		return nil
	}
	return &promforward.Target{
		Service:   t.Service,
		Namespace: t.Namespace,
		Port:      t.Port,
	}
}
