package watches

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ClearOpts controls which logs ClearLogs truncates.
type ClearOpts struct {
	Items     bool
	Signals   bool
	OlderThan time.Duration // 0 = wipe entire matching file
}

// Publisher is the seam over server.Manager's PublishWatchStatus /
// PublishSignalCreated / PublishItemCaptured. The watches package must
// not import the server package (server already imports watches; the
// reverse would be a cycle). The launcher wires up a shim in
// server.New that adapts these calls into server.GlobalEventEnvelope.
type Publisher interface {
	PublishWatchStatus(watchID, status string, lastPolledAt time.Time, errorCount, running, queued int)
	PublishSignalCreated(watchID, signalID, outcome, investigationID string, manuallyStarted bool)
	PublishItemCaptured(watchID, itemID, signalID string, filtered bool)
	// PublishIngestRunStarted fires when ClaudeIngestor.Run spawns
	// claude. The UI uses it to render an "in flight" indicator on the
	// runs panel without polling the disk.
	PublishIngestRunStarted(watchID, runID string, startedAt time.Time, itemCount int)
	// PublishIngestRunFinished fires when ClaudeIngestor.Run returns —
	// regardless of success / error. The UI uses it to flip the
	// "in flight" row to its terminal state.
	PublishIngestRunFinished(watchID, runID, status string, durationMs int64, errorMsg string)
}

// NopPublisher is the no-op implementation of Publisher. Used as the
// default when no publisher is wired (tests, Milestone 1 launcher).
type NopPublisher struct{}

func (NopPublisher) PublishWatchStatus(string, string, time.Time, int, int, int)    {}
func (NopPublisher) PublishSignalCreated(string, string, string, string, bool)      {}
func (NopPublisher) PublishItemCaptured(string, string, string, bool)               {}
func (NopPublisher) PublishIngestRunStarted(string, string, time.Time, int)         {}
func (NopPublisher) PublishIngestRunFinished(string, string, string, int64, string) {}

// ManagerOpts wires the Manager to the rest of the launcher.
type ManagerOpts struct {
	UserWatchesPath string
	Sources         *SourceRegistry
	Ingestor        Ingestor
	IDGen           func() string
	Publisher       Publisher
	// Create is the seam over server.Manager.CreateInvestigation. The
	// per-watch Spawner calls this when it pops a queued signal.
	// SpawnFromSignal returns the queued signalID immediately; Create runs
	// asynchronously and the wrapper mutates the signal row on its return.
	Create CreateFunc
	// IsLive is the reconciliation seam over server.Manager. The per-
	// watch Spawner uses it to drop dead invIDs from state.Running so a
	// missed NotifyTerminal (archive while no Spawner existed, etc.)
	// doesn't pin the slot forever. Nil disables reconciliation, which
	// is the right default for unit tests that don't need the seam.
	IsLive IsLiveFunc
}

// Manager owns user_watches.yaml + the per-watch Pollers. Concurrency-
// safe; the HTTP layer (Milestone 2) calls Create/Patch/Delete via Mgr.
type Manager struct {
	opts     ManagerOpts
	mu       sync.RWMutex
	watches  map[string]Watch
	pollers  map[string]*Poller
	spawners map[string]*Spawner
	ctx      context.Context
}

func NewManager(opts ManagerOpts) (*Manager, error) {
	if opts.UserWatchesPath == "" {
		return nil, errors.New("UserWatchesPath required")
	}
	if opts.Sources == nil {
		return nil, errors.New("sources required")
	}
	if opts.Ingestor == nil {
		opts.Ingestor = NoOpIngestor{}
	}
	if opts.Publisher == nil {
		opts.Publisher = NopPublisher{}
	}
	if opts.IDGen == nil {
		opts.IDGen = func() string { return fmt.Sprintf("w-%d", time.Now().UnixNano()) }
	}
	if opts.Create == nil {
		opts.Create = func(_ context.Context, _ CreateRequest) (string, error) {
			return "", errors.New("watches Manager: no Create callback configured")
		}
	}
	m := &Manager{opts: opts, watches: map[string]Watch{}, pollers: map[string]*Poller{}, spawners: map[string]*Spawner{}}
	loaded, err := LoadWatches(opts.UserWatchesPath)
	if err != nil {
		return nil, err
	}
	for _, w := range loaded {
		m.watches[w.ID] = w
	}
	return m, nil
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = ctx
	for id, w := range m.watches {
		if !w.Enabled {
			continue
		}
		m.spawnLocked(id, w)
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pollers {
		p.Stop()
		delete(m.pollers, id)
	}
}

func (m *Manager) spawnLocked(id string, w Watch) {
	src, ok := m.opts.Sources.Get(w.Source.Kind)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "watch %s: no source registered for kind %q\n", id, w.Source.Kind)
		return
	}
	dir := WatchDir(m.opts.UserWatchesPath, id)
	p := NewPoller(w, dir, src, m.opts.Ingestor, m.opts.Publisher)
	p.Start(m.ctx)
	m.pollers[id] = p
	// Eagerly resume the spawner if a prior process left queued
	// auto-start entries on disk — without this, persisted retry state
	// would only resume on the next inbound signal, leaving in-flight
	// retries stranded across launcher restarts.
	//
	// Construct on Running != [] as well: a previous run may have
	// archived a watch-spawned investigation while the Spawner was
	// absent (lazy construction, no queued items at the time), leaving
	// stale Running entries that block the queue. NewSpawner's eager
	// reconciliation via IsLive heals those entries on startup so the
	// next signal can pop.
	if w.AutoStart.Enabled {
		state, _ := LoadQueue(dir)
		if len(state.Queued) > 0 || len(state.Running) > 0 {
			sp := NewSpawner(dir, id, maxOne(w.AutoStart.MaxConcurrent), m.makeSpawnerCreate(id), m.spawnerOptions()...)
			m.spawners[id] = sp
			go sp.Run(m.ctx)
			sp.signalKick()
		}
	}
}

// spawnerOptions builds the variadic option slice every NewSpawner
// call site needs. Centralises the IsLive plumbing so future seams
// can be added in one place.
func (m *Manager) spawnerOptions() []SpawnerOption {
	if m.opts.IsLive == nil {
		return nil
	}
	return []SpawnerOption{WithIsLive(m.opts.IsLive)}
}

// UserWatchesPath returns the launcher-resolved path to user_watches.yaml.
func (m *Manager) UserWatchesPath() string { return m.opts.UserWatchesPath }

func (m *Manager) List() []Watch {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Watch, 0, len(m.watches))
	for _, w := range m.watches {
		if p := m.pollers[w.ID]; p != nil {
			w.Ingesting = p.Ingesting()
		}
		out = append(out, w)
	}
	return out
}

func (m *Manager) Get(id string) (Watch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.watches[id]
	if ok {
		if p := m.pollers[id]; p != nil {
			w.Ingesting = p.Ingesting()
		}
	}
	return w, ok
}

// WatchPatch is the partial-update shape for PATCH /api/watches/{id}.
// Nil pointer = field unchanged.
type WatchPatch struct {
	Name        *string          `json:"name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Source      *SourceConfig    `json:"source,omitempty"`
	Polling     *PollingConfig   `json:"polling,omitempty"`
	Ingest      *IngestConfig    `json:"ingest,omitempty"`
	AutoStart   *AutoStartConfig `json:"autoStart,omitempty"`
	Enabled     *bool            `json:"enabled,omitempty"`
}

func (m *Manager) Create(in Watch) (Watch, error) {
	in.ID = m.opts.IDGen()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	if err := in.Validate(); err != nil {
		return Watch{}, err
	}
	if _, ok := m.opts.Sources.Get(in.Source.Kind); !ok {
		return Watch{}, fmt.Errorf("source kind %q is not available — connect the underlying service first (e.g. link a Slack token in the Connections panel for slack_channel)", in.Source.Kind)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watches[in.ID] = in
	if err := m.persistLocked(); err != nil {
		delete(m.watches, in.ID)
		return Watch{}, err
	}
	// Seed the watermark to "now" so the first poll doesn't backfill
	// the entire upstream history (slack channel scroll-back, all-open
	// github issues). The watch should report what arrives *after*
	// creation, not what was already there.
	_ = SaveWatermark(filepath.Join(WatchDir(m.opts.UserWatchesPath, in.ID), "state.json"), InitialWatermark(in.Source.Kind, time.Now().UTC()))
	if in.Enabled {
		m.spawnLocked(in.ID, in)
	}
	return in, nil
}

func (m *Manager) Patch(id string, p WatchPatch) (Watch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.watches[id]
	if !ok {
		return Watch{}, fmt.Errorf("watch %q not found", id)
	}
	sourceChanged := false
	if p.Name != nil {
		w.Name = *p.Name
	}
	if p.Description != nil {
		w.Description = *p.Description
	}
	if p.Source != nil {
		sourceChanged = !sourceEqual(w.Source, *p.Source)
		w.Source = *p.Source
	}
	if p.Polling != nil {
		w.Polling = *p.Polling
	}
	if p.Ingest != nil {
		w.Ingest = *p.Ingest
	}
	if p.AutoStart != nil {
		w.AutoStart = *p.AutoStart
	}
	if p.Enabled != nil {
		w.Enabled = *p.Enabled
	}
	if err := w.Validate(); err != nil {
		return Watch{}, err
	}
	if _, ok := m.opts.Sources.Get(w.Source.Kind); !ok {
		return Watch{}, fmt.Errorf("source kind %q is not available — connect the underlying service first (e.g. link a Slack token in the Connections panel for slack_channel)", w.Source.Kind)
	}
	m.watches[id] = w
	if err := m.persistLocked(); err != nil {
		return Watch{}, err
	}
	if old, ok := m.pollers[id]; ok {
		old.Stop()
		delete(m.pollers, id)
	}
	if sourceChanged {
		// Source switch is treated as a fresh start: re-seed the
		// watermark so the new source doesn't backfill all history.
		_ = SaveWatermark(filepath.Join(WatchDir(m.opts.UserWatchesPath, id), "state.json"), InitialWatermark(w.Source.Kind, time.Now().UTC()))
	}
	if w.Enabled {
		m.spawnLocked(id, w)
	}
	return w, nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.watches[id]; !ok {
		return fmt.Errorf("watch %q not found", id)
	}
	if p, ok := m.pollers[id]; ok {
		p.Stop()
		delete(m.pollers, id)
	}
	delete(m.watches, id)
	return m.persistLocked()
}

func (m *Manager) persistLocked() error {
	out := make([]Watch, 0, len(m.watches))
	for _, w := range m.watches {
		out = append(out, w)
	}
	return SaveWatches(m.opts.UserWatchesPath, out)
}

func sourceEqual(a, b SourceConfig) bool {
	return a.Kind == b.Kind && a.Owner == b.Owner && a.Repo == b.Repo &&
		a.ChannelID == b.ChannelID && a.IncludeThreadReplies == b.IncludeThreadReplies &&
		stringSlicesEqual(a.Labels, b.Labels) && stringSlicesEqual(a.States, b.States)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type PollNowResult struct {
	ItemsCaptured  int   `json:"itemsCaptured"`
	SignalsCreated int   `json:"signalsCreated"`
	DurationMs     int64 `json:"durationMs"`
}

// PollNow runs the watch's pollOnce in a goroutine bound to the
// Manager's parent context, then waits up to waitCtx's deadline for
// the result. Decoupling the work from the HTTP request context means
// a client-side timeout (or a client disconnect) doesn't SIGKILL a
// still-running ingestion agent — the agent keeps producing signals
// against the data dir and the operator can refresh to see them.
func (m *Manager) PollNow(waitCtx context.Context, id string) (PollNowResult, error) {
	m.mu.RLock()
	p, ok := m.pollers[id]
	w, wOk := m.watches[id]
	parentCtx := m.ctx
	m.mu.RUnlock()
	if !ok || !wOk {
		return PollNowResult{}, fmt.Errorf("watch %q not found or not running", id)
	}
	wDir := WatchDir(m.opts.UserWatchesPath, id)
	itemsBefore, _ := ReadItems(filepath.Join(wDir, "items.jsonl"), ReadOpts{Limit: 1000000, IncludeFiltered: true})
	signalsBefore, _ := ReadSignals(filepath.Join(wDir, "signals.jsonl"), ReadOpts{Limit: 1000000})
	start := time.Now().UTC()

	type pollResult struct {
		err error
	}
	done := make(chan pollResult, 1)
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	go func() {
		done <- pollResult{err: p.PollOnce(parentCtx, start)}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return PollNowResult{}, res.err
		}
		itemsAfter, _ := ReadItems(filepath.Join(wDir, "items.jsonl"), ReadOpts{Limit: 1000000, IncludeFiltered: true})
		signalsAfter, _ := ReadSignals(filepath.Join(wDir, "signals.jsonl"), ReadOpts{Limit: 1000000})
		_ = w
		return PollNowResult{
			ItemsCaptured:  len(itemsAfter) - len(itemsBefore),
			SignalsCreated: len(signalsAfter) - len(signalsBefore),
			DurationMs:     time.Since(start).Milliseconds(),
		}, nil
	case <-waitCtx.Done():
		// The HTTP request is going away but the poll keeps going.
		// Return a deadline-exceeded error so the handler can surface
		// "still in progress" — the operator can watch the
		// ingestion-runs panel for the final outcome.
		return PollNowResult{}, waitCtx.Err()
	}
}

// ClearLogs truncates a watch's items.jsonl, signals.jsonl, or both,
// optionally only entries older than now-OlderThan. The read-modify-
// write cycle for each path runs under the per-path mutex so the
// poller's PruneItems / AppendItem can't race with the clear and lose
// the truncation. Returns (itemsRemoved, signalsRemoved, error).
func (m *Manager) ClearLogs(id string, o ClearOpts, now time.Time) (int, int, error) {
	dir := WatchDir(m.opts.UserWatchesPath, id)
	itemsRm := 0
	signalsRm := 0
	if o.Items {
		n, err := clearItemsFile(filepath.Join(dir, "items.jsonl"), o.OlderThan, now)
		if err != nil {
			return 0, 0, err
		}
		itemsRm = n
	}
	if o.Signals {
		n, err := clearSignalsFile(filepath.Join(dir, "signals.jsonl"), o.OlderThan, now)
		if err != nil {
			return itemsRm, 0, err
		}
		signalsRm = n
	}
	return itemsRm, signalsRm, nil
}

// clearItemsFile holds the per-path mutex across read+filter+rewrite so
// concurrent appenders / PruneItems can't slip a write between our read
// and our rename.
func clearItemsFile(path string, olderThan time.Duration, now time.Time) (int, error) {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	rows, err := readAll(path, func() any { return &Item{} })
	if err != nil {
		return 0, err
	}
	before := make([]Item, 0, len(rows))
	for _, r := range rows {
		before = append(before, *(r.(*Item)))
	}
	kept := filterItemsByRetention(before, olderThan, now)
	if err := rewriteJSONL(path, kept); err != nil {
		return 0, err
	}
	return len(before) - len(kept), nil
}

func clearSignalsFile(path string, olderThan time.Duration, now time.Time) (int, error) {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	rows, err := readAll(path, func() any { return &Signal{} })
	if err != nil {
		return 0, err
	}
	before := make([]Signal, 0, len(rows))
	for _, r := range rows {
		before = append(before, *(r.(*Signal)))
	}
	kept := filterSignalsByRetention(before, olderThan, now)
	if err := rewriteJSONL(path, kept); err != nil {
		return 0, err
	}
	return len(before) - len(kept), nil
}

func filterItemsByRetention(in []Item, olderThan time.Duration, now time.Time) []Item {
	if olderThan <= 0 {
		return nil
	}
	cutoff := now.Add(-olderThan)
	kept := make([]Item, 0, len(in))
	for _, it := range in {
		if !it.CapturedAt.Before(cutoff) {
			kept = append(kept, it)
		}
	}
	return kept
}

func filterSignalsByRetention(in []Signal, olderThan time.Duration, now time.Time) []Signal {
	if olderThan <= 0 {
		return nil
	}
	cutoff := now.Add(-olderThan)
	kept := make([]Signal, 0, len(in))
	for _, s := range in {
		if !s.CreatedAt.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	return kept
}

// SpawnFromSignalReq carries the fields the ingest agent passes via
// start_investigation. In M5 SpawnFromSignal synchronously appends an
// outcome:investigation_started signal — the actual investigation create
// is left to M6's spawner refactor (which will bridge to the server.Manager
// via a callback to avoid an import cycle).
type SpawnFromSignalReq struct {
	Briefing      string
	CitedItemIDs  []string
	Clusters      []string
	SlackChannel  string
	IncidentioURL string
	Repos         []string
	AutoMode      bool
}

// SpawnFromSignal writes a queued signal row immediately, then enqueues
// into the per-watch Spawner. The Spawner's wrapped CreateFunc handles
// the eventual mutation: success appends an investigation_started row
// with the real investigationID; failure appends a failed row with
// errorMessage. Returns the queued signalID; invID is always empty on
// the immediate return path (caller observes invID via the mutated row).
func (m *Manager) SpawnFromSignal(_ context.Context, watchID string, req SpawnFromSignalReq) (signalID, invID string, err error) {
	w, ok := m.Get(watchID)
	if !ok {
		return "", "", fmt.Errorf("watch %s not found", watchID)
	}
	dir := WatchDir(m.opts.UserWatchesPath, watchID)
	signalID = newSignalULID()

	// AutoStart=off: the ingestion agent flagged this batch as
	// investigation-worthy, but the watch's policy doesn't auto-spawn.
	// Write a "proposed" signal so the operator can review and click
	// Start in the UI — that path goes through ManualStartFromSignal
	// which synchronously creates the investigation.
	if !w.AutoStart.Enabled {
		proposed := Signal{
			ID:            signalID,
			WatchID:       watchID,
			CreatedAt:     time.Now().UTC(),
			CitedItemIDs:  req.CitedItemIDs,
			Outcome:       OutcomeProposed,
			Clusters:      req.Clusters,
			Briefing:      req.Briefing,
			AutoMode:      req.AutoMode,
			SlackChannel:  req.SlackChannel,
			IncidentioURL: req.IncidentioURL,
			Repos:         req.Repos,
		}
		if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), proposed); err != nil {
			return "", "", err
		}
		m.opts.Publisher.PublishSignalCreated(watchID, proposed.ID, string(proposed.Outcome), "", proposed.ManuallyStarted)
		return signalID, "", nil
	}

	// Compose the auto-trigger preamble once at enqueue. The composed
	// briefing is persisted both on the queued signal row (so the UI
	// shows what the agent will actually receive) and in the spawner's
	// in-memory request, which survives retries without being re-wrapped.
	composedBriefing := composeAutoTriggeredBriefing(w.Name, signalID, req.Clusters, req.Briefing)

	queued := Signal{
		ID:            signalID,
		WatchID:       watchID,
		CreatedAt:     time.Now().UTC(),
		CitedItemIDs:  req.CitedItemIDs,
		Outcome:       OutcomeQueued,
		Clusters:      req.Clusters,
		Briefing:      composedBriefing,
		AutoMode:      req.AutoMode,
		SlackChannel:  req.SlackChannel,
		IncidentioURL: req.IncidentioURL,
		Repos:         req.Repos,
	}
	if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), queued); err != nil {
		return "", "", err
	}
	m.opts.Publisher.PublishSignalCreated(watchID, queued.ID, string(queued.Outcome), "", queued.ManuallyStarted)

	m.mu.Lock()
	sp, ok := m.spawners[watchID]
	if !ok {
		sp = NewSpawner(dir, watchID, maxOne(w.AutoStart.MaxConcurrent), m.makeSpawnerCreate(watchID), m.spawnerOptions()...)
		m.spawners[watchID] = sp
		go sp.Run(m.ctx)
	}
	m.mu.Unlock()

	sp.Enqueue(CreateRequest{
		WatchID:       watchID,
		SignalID:      signalID,
		Briefing:      composedBriefing,
		Clusters:      req.Clusters,
		SlackChannel:  req.SlackChannel,
		IncidentioURL: req.IncidentioURL,
		Repos:         req.Repos,
		AutoMode:      req.AutoMode,
		CitedItemIDs:  req.CitedItemIDs,
	})
	return signalID, "", nil
}

// makeSpawnerCreate wraps m.opts.Create so the Spawner can record the
// result of each create into signals.jsonl. The wrapped function calls
// the launcher's CreateInvestigation and appends a mutated row on
// terminal outcomes (investigation_started on success, failed on the
// final retry exhaustion). Intermediate retry failures log to stderr and
// return the error to the Spawner, which re-enqueues with a backoff —
// the queued signal row stays "queued" through the retry window.
func (m *Manager) makeSpawnerCreate(watchID string) CreateFunc {
	return func(ctx context.Context, cr CreateRequest) (string, error) {
		invID, createErr := m.opts.Create(ctx, cr)
		if createErr != nil {
			_, _ = fmt.Fprintf(stderr, "watch %s auto-start signal %s attempt %d/%d failed: %v\n",
				watchID, cr.SignalID, cr.Attempt, cr.MaxAttempts, createErr)
			// Not the final attempt — let the Spawner re-enqueue. We
			// don't append a terminal signal row, so the queued row
			// stays visible to the UI through the retry window.
			if cr.MaxAttempts > 0 && cr.Attempt < cr.MaxAttempts {
				return "", createErr
			}
		}
		dir := WatchDir(m.opts.UserWatchesPath, watchID)
		outcome := OutcomeInvestigationStarted
		errMsg := ""
		if createErr != nil {
			outcome = OutcomeFailed
			if cr.MaxAttempts > 1 {
				errMsg = fmt.Sprintf("auto-start failed after %d attempts: %v", cr.MaxAttempts, createErr)
			} else {
				errMsg = createErr.Error()
			}
		}
		mutated := Signal{
			ID:              cr.SignalID,
			WatchID:         watchID,
			CreatedAt:       time.Now().UTC(),
			CitedItemIDs:    cr.CitedItemIDs,
			Outcome:         outcome,
			InvestigationID: invID,
			Clusters:        cr.Clusters,
			Briefing:        cr.Briefing,
			AutoMode:        cr.AutoMode,
			SlackChannel:    cr.SlackChannel,
			IncidentioURL:   cr.IncidentioURL,
			Repos:           cr.Repos,
			ManuallyStarted: cr.ManuallyStarted,
			ErrorMessage:    errMsg,
		}
		if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), mutated); err != nil {
			return invID, err
		}
		m.opts.Publisher.PublishSignalCreated(watchID, mutated.ID, string(mutated.Outcome), mutated.InvestigationID, mutated.ManuallyStarted)
		return invID, createErr
	}
}

// maxOne is a small helper for the AutoStart.MaxConcurrent fallback.
// A zero / negative configured value becomes 1.
func maxOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// QueueSnapshotResult is the read-only view of one watch's spawner state
// the HTTP endpoint returns. MaxConcurrent reflects what the spawner is
// actually using (matches the AutoStart config, defaulted to 1 if zero).
type QueueSnapshotResult struct {
	MaxConcurrent int           `json:"maxConcurrent"`
	Running       []string      `json:"running"`
	Queued        []QueuedEntry `json:"queued"`
}

// QueueSnapshot returns the current per-watch queue state. Returns a
// zero-value snapshot when no spawner has been created yet (the watch
// hasn't fired any signals). Always emits non-nil Running/Queued so the
// JSON wire shape is `[]` rather than `null` — frontend code expects to
// call `.length` on these unconditionally.
func (m *Manager) QueueSnapshot(watchID string) (QueueSnapshotResult, error) {
	if _, ok := m.Get(watchID); !ok {
		return QueueSnapshotResult{}, fmt.Errorf("watch %s not found", watchID)
	}
	m.mu.RLock()
	sp := m.spawners[watchID]
	m.mu.RUnlock()
	if sp == nil {
		// No spawner yet. Report an empty queue with maxConcurrent
		// inferred from the watch's configured value (defaulted to 1).
		w, _ := m.Get(watchID)
		return QueueSnapshotResult{
			MaxConcurrent: maxOne(w.AutoStart.MaxConcurrent),
			Running:       []string{},
			Queued:        []QueuedEntry{},
		}, nil
	}
	snap := sp.Snapshot()
	running := snap.Running
	if running == nil {
		running = []string{}
	}
	queued := snap.Queued
	if queued == nil {
		queued = []QueuedEntry{}
	}
	return QueueSnapshotResult{MaxConcurrent: sp.MaxConcurrent(), Running: running, Queued: queued}, nil
}

// CancelQueued removes a queued signal from the per-watch Spawner and
// appends a dismissed-signal mutation so readers (latest-row-per-id) see
// the cancellation. Returns (false, nil) when the signal isn't in the
// queue (already started or never enqueued).
func (m *Manager) CancelQueued(watchID, signalID string) (bool, error) {
	if _, ok := m.Get(watchID); !ok {
		return false, fmt.Errorf("watch %s not found", watchID)
	}
	m.mu.RLock()
	sp := m.spawners[watchID]
	m.mu.RUnlock()
	if sp == nil {
		return false, nil
	}
	if !sp.Cancel(signalID) {
		return false, nil
	}
	dir := WatchDir(m.opts.UserWatchesPath, watchID)
	mutation := Signal{
		ID:        signalID,
		WatchID:   watchID,
		CreatedAt: time.Now().UTC(),
		Outcome:   OutcomeDismissed,
		Reason:    "cancelled by operator",
	}
	if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), mutation); err != nil {
		return true, err
	}
	m.opts.Publisher.PublishSignalCreated(watchID, signalID, string(OutcomeDismissed), "", false)
	return true, nil
}

// NotifyTerminal releases a running slot on the per-watch Spawner. The
// launcher calls this when an investigation that was spawned from a
// signal-watch reaches a terminal lifecycle state (archive, delete, or
// auto-mode finished/aborted). No-op when no Spawner exists for the watch
// yet (the watch may have been deleted, or the process restarted before the
// investigation finished).
func (m *Manager) NotifyTerminal(watchID, invID string) {
	m.mu.RLock()
	sp := m.spawners[watchID]
	m.mu.RUnlock()
	if sp != nil {
		sp.OnInvestigationTerminal(invID)
	}
}

// ManualStartFromSignal launches an investigation for an existing signal
// the operator has clicked through (typically Outcome=unclear / disabled /
// failed / dismissed). Unlike SpawnFromSignal (which queues behind any
// in-flight autoStart spawns), manual start calls Create synchronously
// and returns the invID — the operator just pressed a button and expects
// the investigation to materialize.
//
// Appends a single mutation row with the SAME SignalID:
//   - on Create success: OutcomeInvestigationStarted, ManuallyStarted=true, InvestigationID set
//   - on Create failure: OutcomeFailed with ErrorMessage
func (m *Manager) ManualStartFromSignal(ctx context.Context, watchID, signalID string, clusters, repos []string) (string, error) {
	w, ok := m.Get(watchID)
	if !ok {
		return "", fmt.Errorf("watch %s not found", watchID)
	}
	dir := WatchDir(m.opts.UserWatchesPath, watchID)
	existing, briefing, err := m.lookupSignalAndBriefing(dir, signalID)
	if err != nil {
		return "", err
	}

	cr := CreateRequest{
		WatchID:         watchID,
		SignalID:        signalID,
		Briefing:        briefing,
		Clusters:        clusters,
		Repos:           repos,
		CitedItemIDs:    existing.CitedItemIDs,
		AutoMode:        existing.AutoMode,
		ManuallyStarted: true,
	}
	invID, createErr := m.opts.Create(ctx, cr)

	mutation := Signal{
		ID:              signalID,
		WatchID:         watchID,
		CreatedAt:       time.Now().UTC(),
		CitedItemIDs:    existing.CitedItemIDs,
		Outcome:         OutcomeInvestigationStarted,
		InvestigationID: invID,
		Clusters:        clusters,
		Briefing:        briefing,
		AutoMode:        existing.AutoMode,
		ManuallyStarted: true,
		Repos:           repos,
	}
	if createErr != nil {
		mutation.Outcome = OutcomeFailed
		mutation.ErrorMessage = createErr.Error()
	}
	if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), mutation); err != nil {
		return invID, err
	}
	m.opts.Publisher.PublishSignalCreated(watchID, mutation.ID, string(mutation.Outcome), mutation.InvestigationID, mutation.ManuallyStarted)
	if createErr != nil {
		return "", createErr
	}
	_ = w
	return invID, nil
}

// RetryFailedSignal re-attempts a previously-failed manual-start (or
// queue-spawned) signal. The signal's latest outcome must be Failed.
// Re-runs Create synchronously and writes the new mutation row.
func (m *Manager) RetryFailedSignal(ctx context.Context, watchID, signalID string) error {
	if _, ok := m.Get(watchID); !ok {
		return fmt.Errorf("watch %s not found", watchID)
	}
	dir := WatchDir(m.opts.UserWatchesPath, watchID)
	existing, briefing, err := m.lookupSignalAndBriefing(dir, signalID)
	if err != nil {
		return err
	}
	if existing.Outcome != OutcomeFailed {
		return fmt.Errorf("signal %s is not failed (outcome=%s)", signalID, existing.Outcome)
	}
	cr := CreateRequest{
		WatchID:         watchID,
		SignalID:        signalID,
		Briefing:        briefing,
		Clusters:        existing.Clusters,
		Repos:           existing.Repos,
		CitedItemIDs:    existing.CitedItemIDs,
		AutoMode:        existing.AutoMode,
		ManuallyStarted: existing.ManuallyStarted,
	}
	invID, createErr := m.opts.Create(ctx, cr)
	mutation := Signal{
		ID:              signalID,
		WatchID:         watchID,
		CreatedAt:       time.Now().UTC(),
		CitedItemIDs:    existing.CitedItemIDs,
		Outcome:         OutcomeInvestigationStarted,
		InvestigationID: invID,
		Clusters:        existing.Clusters,
		Briefing:        briefing,
		AutoMode:        existing.AutoMode,
		ManuallyStarted: existing.ManuallyStarted,
		Repos:           existing.Repos,
	}
	if createErr != nil {
		mutation.Outcome = OutcomeFailed
		mutation.ErrorMessage = createErr.Error()
	}
	if err := AppendSignal(filepath.Join(dir, "signals.jsonl"), mutation); err != nil {
		return err
	}
	m.opts.Publisher.PublishSignalCreated(watchID, mutation.ID, string(mutation.Outcome), mutation.InvestigationID, mutation.ManuallyStarted)
	return createErr
}

// lookupSignalAndBriefing finds the latest projection of a signal by ID
// and composes a briefing from its cited items' snapshots when no
// briefing is already set. Returns ("not found") if the signal isn't in
// signals.jsonl.
func (m *Manager) lookupSignalAndBriefing(dir, signalID string) (Signal, string, error) {
	sigs, err := ReadSignals(filepath.Join(dir, "signals.jsonl"), ReadOpts{Limit: 10000})
	if err != nil {
		return Signal{}, "", err
	}
	var existing *Signal
	for i, s := range sigs {
		if s.ID == signalID {
			existing = &sigs[i]
			break
		}
	}
	if existing == nil {
		return Signal{}, "", fmt.Errorf("signal %s not found", signalID)
	}
	briefing := existing.Briefing
	if briefing == "" {
		briefing = m.composeBriefing(dir, existing.CitedItemIDs)
	}
	return *existing, briefing, nil
}

// composeBriefing builds a fallback briefing from cited items' titles
// when the signal itself doesn't carry one. Used by manual-start on
// signals (typically Outcome=unclear) that never had an agent-composed
// briefing in the first place.
func (m *Manager) composeBriefing(dir string, citedItemIDs []string) string {
	if len(citedItemIDs) == 0 {
		return ""
	}
	items, err := ReadItems(filepath.Join(dir, "items.jsonl"), ReadOpts{Limit: 10000, IncludeFiltered: true})
	if err != nil {
		return ""
	}
	idSet := map[string]bool{}
	for _, id := range citedItemIDs {
		idSet[id] = true
	}
	var parts []string
	for _, it := range items {
		if !idSet[it.ID] {
			continue
		}
		title := it.Snapshot.Title
		if title == "" {
			title = it.Snapshot.Text
		}
		if title == "" {
			continue
		}
		parts = append(parts, title)
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + " (and " + fmt.Sprint(len(parts)-1) + " related)"
}

// composeAutoTriggeredBriefing wraps an agent-composed briefing with a
// header that names the originating watch + signal and nudges the
// operator agent toward `wiki` at capture time when the outcome is a
// known noop. Called by the spawner's Create wrapper before the launcher
// hands the briefing to the operator/investigator agent.
func composeAutoTriggeredBriefing(watchName, signalID string, clusters []string, briefing string) string {
	var b strings.Builder
	b.WriteString("> **Auto-triggered by signal-watch ingestion.**\n")
	b.WriteString("> Watch: ")
	b.WriteString(watchName)
	b.WriteString("\n> Signal: ")
	if len(signalID) >= 8 {
		b.WriteString(signalID[:8])
	} else {
		b.WriteString(signalID)
	}
	b.WriteString("\n")
	if len(clusters) > 0 {
		b.WriteString("> Suggested cluster(s): ")
		b.WriteString(strings.Join(clusters, ", "))
		b.WriteString("  (use triagent-k8s.switch_context to bind one)\n")
	}
	b.WriteString(">\n")
	b.WriteString("> If the conclusion is \"this is a known false-positive / noop\", documenting\n")
	b.WriteString("> it as a wiki entry with `status:wontfix` is high-value — it lets the\n")
	b.WriteString("> ingestion agent dismiss similar signals automatically on future runs.\n")
	b.WriteString("> Use `wiki` at capture_offer time, not `no`.\n\n")
	b.WriteString(briefing)
	return b.String()
}

// RehydrateOrphans scans each watch's items.jsonl for entries that the
// previous launcher run captured but never produced (or cited from) a
// signal. Items are considered orphaned only when:
//   - CapturedAt is at-or-before the saved watermark (so we ignore
//     items that landed after the last successful poll cycle), and
//   - the item isn't filtered out, and
//   - the item's ID doesn't already appear in any signal's CitedItemIDs
//     (autoStart=off watches don't set Item.SignalID — they cite items
//     from the trivial "disabled" signal, so the older signalID-only
//     check produced spurious "interrupted by restart" dismissals on
//     every launcher startup).
// All such orphan items get folded into a single dismissed signal per
// watch.
//
// Only runs for watches with ingest enabled: trivial-passthrough watches
// (ingest=off) write each item + its disabled signal atomically, so the
// orphan condition is impossible there. Skipping them prevents the
// historical "interrupted by restart" noise that confused operators.
func (m *Manager) RehydrateOrphans() error {
	m.mu.RLock()
	watches := make([]Watch, 0, len(m.watches))
	for _, w := range m.watches {
		watches = append(watches, w)
	}
	m.mu.RUnlock()
	for _, w := range watches {
		if !w.Ingest.Enabled && !w.AutoStart.Enabled {
			continue
		}
		wDir := WatchDir(m.opts.UserWatchesPath, w.ID)
		state, _ := LoadWatermark(filepath.Join(wDir, "state.json"))
		if state.LastPolledAt.IsZero() {
			continue
		}
		items, err := ReadItems(filepath.Join(wDir, "items.jsonl"), ReadOpts{Limit: 1000000, IncludeFiltered: true})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "watch %s rehydrate read: %v\n", w.ID, err)
			continue
		}
		// Build the set of item IDs already cited by some signal —
		// autoStart=off appends "disabled" signals that cite each
		// item by ID without ever setting Item.SignalID.
		signals, err := ReadSignals(filepath.Join(wDir, "signals.jsonl"), ReadOpts{Limit: 1000000})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "watch %s rehydrate read signals: %v\n", w.ID, err)
			continue
		}
		cited := make(map[string]struct{}, len(signals)*2)
		for _, s := range signals {
			for _, id := range s.CitedItemIDs {
				cited[id] = struct{}{}
			}
		}
		orphanIDs := []string{}
		for _, it := range items {
			if it.SignalID != "" || it.Filtered != nil {
				continue
			}
			if _, ok := cited[it.ID]; ok {
				continue
			}
			if it.CapturedAt.After(state.LastPolledAt) {
				continue
			}
			orphanIDs = append(orphanIDs, it.ID)
		}
		if len(orphanIDs) == 0 {
			continue
		}
		sig := Signal{
			ID:           newSignalULID(),
			WatchID:      w.ID,
			CreatedAt:    time.Now().UTC(),
			CitedItemIDs: orphanIDs,
			Outcome:      OutcomeDismissed,
			Reason:       "interrupted by restart",
		}
		if err := AppendSignal(filepath.Join(wDir, "signals.jsonl"), sig); err != nil {
			_, _ = fmt.Fprintf(stderr, "watch %s rehydrate write: %v\n", w.ID, err)
		}
	}
	return nil
}
