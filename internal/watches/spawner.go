package watches

import (
	"context"
	"path/filepath"
	"sync"
	"time"
)

// autoStartMaxAttempts is the total number of Create calls (including
// the first) the spawner makes before giving up on a queued auto-start.
// Equivalent to "1 initial + (autoStartMaxAttempts-1) retries".
const autoStartMaxAttempts = 4

// autoStartBackoff returns the delay before the Nth retry. attempt is
// 1-based and counts the attempt that just failed (so the next attempt
// is attempt+1 and uses backoff for attempt+1). Roughly: 30s, 2min, 8min.
// Held as a package-level var so tests can swap in shorter delays.
var autoStartBackoff = func(failedAttempt int) time.Duration {
	switch failedAttempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	default:
		return 8 * time.Minute
	}
}

// CreateRequest is what the spawner hands to the launcher's
// CreateInvestigation callback. The watches package can't import server
// directly (cycle), so this struct + the CreateFunc seam decouple the
// two.
type CreateRequest struct {
	WatchID         string
	SignalID        string
	Briefing        string
	Clusters        []string
	SlackChannel    string
	IncidentioURL   string
	Repos           []string
	AutoMode        bool
	CitedItemIDs    []string
	ManuallyStarted bool
	// Attempt is 1-based — 1 for the initial create, >1 for retries.
	// MaxAttempts is autoStartMaxAttempts (or 1 for manual paths that
	// don't retry). The wrapped CreateFunc uses Attempt==MaxAttempts to
	// decide when to mark the signal Failed vs. let the spawner retry.
	Attempt     int
	MaxAttempts int
}

// CreateFunc is the seam over server.Manager.CreateInvestigation. The
// launcher wires this in cmd/start.go::runWeb.
type CreateFunc func(ctx context.Context, req CreateRequest) (investigationID string, err error)

// IsLiveFunc reports whether an investigation that previously occupied
// a spawner slot is still alive (i.e. not archived/deleted/missing).
// Returning false means the slot is leaked and the Spawner should drop
// the ID from state.Running. Nil means "don't reconcile" — the
// Spawner preserves whatever the on-disk state says, which is the
// historical behaviour for callers that haven't opted in.
type IsLiveFunc func(invID string) bool

// SpawnerOption configures a Spawner at construction. Functional
// options keep NewSpawner's positional surface stable; new optional
// hooks (reconciliation, etc.) plug in without breaking call sites
// that don't need them.
type SpawnerOption func(*Spawner)

// WithIsLive attaches a reconciliation callback. The Spawner will
// drop dead invIDs from state.Running at construction (so launcher
// restart heals the queue.json that prior runs leaked) and on every
// tryPopAndSpawn entry (so a missed NotifyTerminal still unblocks
// the queue once any kick fires).
func WithIsLive(fn IsLiveFunc) SpawnerOption {
	return func(s *Spawner) { s.isLive = fn }
}

// Spawner owns one watch's queue. One goroutine per Spawner via Run(ctx).
// Manager lazily constructs one when a watch's first signal arrives and
// reuses it for subsequent signals.
type Spawner struct {
	dir           string
	watchID       string
	maxConcurrent int
	create        CreateFunc
	isLive        IsLiveFunc

	mu      sync.Mutex
	state   QueueState
	pending map[string]CreateRequest // signalID → full request
	kick    chan struct{}
}

func NewSpawner(dir, watchID string, maxConcurrent int, create CreateFunc, opts ...SpawnerOption) *Spawner {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	s := &Spawner{
		dir:           dir,
		watchID:       watchID,
		maxConcurrent: maxConcurrent,
		create:        create,
		pending:       map[string]CreateRequest{},
		kick:          make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.state, _ = LoadQueue(dir)
	s.rehydratePending()
	// Eager reconciliation so a launcher restart with stale Running
	// entries heals queue.json before any signal arrives — without
	// this, the next freshly-arriving signal would inherit the leak
	// and refuse to pop.
	s.mu.Lock()
	s.reconcileLocked()
	s.mu.Unlock()
	return s
}

// reconcileLocked drops Running entries that isLive says are dead and
// persists if anything changed. Caller holds s.mu. No-op when isLive
// is nil (callers haven't opted in) or Running is empty.
func (s *Spawner) reconcileLocked() {
	if s.isLive == nil || len(s.state.Running) == 0 {
		return
	}
	kept := s.state.Running[:0]
	changed := false
	for _, id := range s.state.Running {
		if s.isLive(id) {
			kept = append(kept, id)
			continue
		}
		changed = true
	}
	if !changed {
		return
	}
	s.state.Running = kept
	_ = SaveQueue(s.dir, s.state)
}

// rehydratePending rebuilds the in-memory signalID→CreateRequest map
// from signals.jsonl after a launcher restart. Without this, queued
// retry entries point at requests we'd otherwise drop, defeating the
// "retries persist across restarts" contract.
func (s *Spawner) rehydratePending() {
	if len(s.state.Queued) == 0 {
		return
	}
	sigs, err := ReadSignals(filepath.Join(s.dir, "signals.jsonl"), ReadOpts{Limit: 0})
	if err != nil {
		return
	}
	want := make(map[string]struct{}, len(s.state.Queued))
	for _, e := range s.state.Queued {
		want[e.SignalID] = struct{}{}
	}
	for _, sig := range sigs {
		if _, ok := want[sig.ID]; !ok {
			continue
		}
		s.pending[sig.ID] = CreateRequest{
			WatchID:       s.watchID,
			SignalID:      sig.ID,
			Briefing:      sig.Briefing,
			Clusters:      sig.Clusters,
			SlackChannel:  sig.SlackChannel,
			IncidentioURL: sig.IncidentioURL,
			Repos:         sig.Repos,
			AutoMode:      sig.AutoMode,
			CitedItemIDs:  sig.CitedItemIDs,
		}
	}
}

// Enqueue adds a request to the FIFO. The CreateRequest carries every
// field the eventual mutated signal needs; the Spawner stores it until
// the create callback runs.
func (s *Spawner) Enqueue(req CreateRequest) {
	s.mu.Lock()
	s.state.Queued = append(s.state.Queued, QueuedEntry{
		SignalID:   req.SignalID,
		EnqueuedAt: time.Now().UTC().Format(time.RFC3339),
	})
	s.pending[req.SignalID] = req
	_ = SaveQueue(s.dir, s.state)
	s.mu.Unlock()
	s.signalKick()
}

// OnInvestigationTerminal releases a running slot. Idempotent — unknown
// invIDs are no-ops.
func (s *Spawner) OnInvestigationTerminal(invID string) {
	s.mu.Lock()
	out := s.state.Running[:0]
	for _, id := range s.state.Running {
		if id != invID {
			out = append(out, id)
		}
	}
	s.state.Running = out
	_ = SaveQueue(s.dir, s.state)
	s.mu.Unlock()
	s.signalKick()
}

// Cancel removes a queued signal. Returns false if the signal isn't in the
// queue (e.g., already popped or never enqueued).
func (s *Spawner) Cancel(signalID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.state.Queued {
		if e.SignalID == signalID {
			s.state.Queued = append(s.state.Queued[:i], s.state.Queued[i+1:]...)
			delete(s.pending, signalID)
			_ = SaveQueue(s.dir, s.state)
			return true
		}
	}
	return false
}

// Snapshot returns a copy of the current queue state.
func (s *Spawner) Snapshot() QueueState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return QueueState{
		Running: append([]string{}, s.state.Running...),
		Queued:  append([]QueuedEntry{}, s.state.Queued...),
	}
}

// MaxConcurrent returns the spawner's configured limit. Used by the HTTP
// queue endpoint so the frontend can render running/max.
func (s *Spawner) MaxConcurrent() int { return s.maxConcurrent }

func (s *Spawner) signalKick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run blocks until ctx cancels. It drains the queue whenever a kick
// arrives (enqueue / terminal release) or on context cancel.
func (s *Spawner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		}
		for s.tryPopAndSpawn(ctx) {
		}
	}
}

// tryPopAndSpawn pops the first ready entry (NotBefore unset or in the
// past) if a slot is free, then calls create. Returns true if something
// was popped (caller should loop). Returns false when the queue is empty,
// at capacity, or all queued entries are still gated by NotBefore — in
// the gated case, schedules a delayed kick so the loop wakes up when the
// earliest gate clears.
func (s *Spawner) tryPopAndSpawn(ctx context.Context) bool {
	s.mu.Lock()
	// Self-heal stale Running entries before the capacity check. A
	// missed NotifyTerminal (e.g. archive while no spawner existed)
	// would otherwise pin the queue indefinitely.
	s.reconcileLocked()
	if len(s.state.Running) >= s.maxConcurrent || len(s.state.Queued) == 0 {
		s.mu.Unlock()
		return false
	}
	now := time.Now()
	poppedIdx := -1
	var earliestFuture time.Time
	for i, e := range s.state.Queued {
		nb := parseNotBefore(e.NotBefore)
		if nb.IsZero() || !nb.After(now) {
			poppedIdx = i
			break
		}
		if earliestFuture.IsZero() || nb.Before(earliestFuture) {
			earliestFuture = nb
		}
	}
	if poppedIdx < 0 {
		s.mu.Unlock()
		if !earliestFuture.IsZero() {
			s.scheduleKickAt(earliestFuture)
		}
		return false
	}
	popped := s.state.Queued[poppedIdx]
	s.state.Queued = append(s.state.Queued[:poppedIdx], s.state.Queued[poppedIdx+1:]...)
	req, ok := s.pending[popped.SignalID]
	if !ok {
		// Cancelled or never rehydrated; persist and continue.
		_ = SaveQueue(s.dir, s.state)
		s.mu.Unlock()
		return true
	}
	delete(s.pending, popped.SignalID)
	_ = SaveQueue(s.dir, s.state)
	s.mu.Unlock()

	attempt := popped.Attempts + 1
	req.Attempt = attempt
	req.MaxAttempts = autoStartMaxAttempts
	invID, err := s.create(ctx, req)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && invID != "" {
		s.state.Running = append(s.state.Running, invID)
		_ = SaveQueue(s.dir, s.state)
		return true
	}
	if err != nil && attempt < autoStartMaxAttempts {
		backoff := autoStartBackoff(attempt)
		nextAt := time.Now().Add(backoff)
		s.state.Queued = append(s.state.Queued, QueuedEntry{
			SignalID:   popped.SignalID,
			EnqueuedAt: popped.EnqueuedAt,
			Attempts:   attempt,
			NotBefore:  nextAt.UTC().Format(time.RFC3339),
		})
		s.pending[popped.SignalID] = req
		_ = SaveQueue(s.dir, s.state)
		s.scheduleKickAt(nextAt)
		return true
	}
	// Final failure (wrapper has written OutcomeFailed) or empty invID
	// with no error (treated as terminal).
	_ = SaveQueue(s.dir, s.state)
	return true
}

// parseNotBefore parses an RFC3339 string; returns zero Time on empty/bad
// input so callers treat the entry as "ready now".
func parseNotBefore(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// scheduleKickAt fires a kick when t arrives. If t is already past, kicks
// immediately. Multiple pending timers can stack; signalKick coalesces
// them via the buffered kick channel.
func (s *Spawner) scheduleKickAt(t time.Time) {
	d := time.Until(t)
	if d <= 0 {
		s.signalKick()
		return
	}
	time.AfterFunc(d, s.signalKick)
}
