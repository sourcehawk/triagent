package watches

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Ingestor is the seam the poller uses for autoStart=true watches.
// Milestone 5 implements the real one in investigate/internal/watches/ingestor.go.
type Ingestor interface {
	Run(ctx context.Context, w Watch, items []Item, dataDir string) error
}

// NoOpIngestor is the milestone-1 placeholder. Returns nil; the poller
// will fall through to the autoStart=off path. Wired in tests + by the
// launcher until Milestone 5 lands.
type NoOpIngestor struct{}

func (NoOpIngestor) Run(_ context.Context, _ Watch, _ []Item, _ string) error { return nil }

// Poller owns one watch's polling loop. One goroutine per Poller; the
// Manager creates Pollers when a watch is enabled and Stop()s them when
// it's disabled or deleted.
type Poller struct {
	w        Watch
	dataDir  string
	src      Source
	ingestor Ingestor
	pub      Publisher
	stop     chan struct{}
	done     chan struct{}
	// pollMu serializes concurrent calls to pollOnce on the same
	// watch. Without it a Poll-now click could overlap with the
	// background loop's poll and spawn two claude processes for the
	// same batch.
	pollMu sync.Mutex
	// ingesting flips to 1 while the per-watch ingestor.Run is in
	// flight. Manager exposes it via Watch.Ingesting so the UI can
	// render a spinner on the list row + detail header.
	ingesting atomic.Bool
}

// Ingesting reports whether this poller's ingestor.Run is currently
// in flight. Cheap atomic read — safe from any goroutine.
func (p *Poller) Ingesting() bool { return p.ingesting.Load() }

func NewPoller(w Watch, dataDir string, src Source, ing Ingestor, pub Publisher) *Poller {
	if pub == nil {
		pub = NopPublisher{}
	}
	return &Poller{w: w, dataDir: dataDir, src: src, ingestor: ing, pub: pub, stop: make(chan struct{}), done: make(chan struct{})}
}

func (p *Poller) Start(ctx context.Context) {
	go p.loop(ctx)
}

func (p *Poller) Stop() {
	close(p.stop)
	<-p.done
}

// PollOnce is the exported wrapper around pollOnce; lets the manager
// trigger a one-shot poll outside the loop (e.g. for PollNow). The
// per-watch mutex prevents this and the background loop from running
// pollOnce concurrently.
func (p *Poller) PollOnce(ctx context.Context, now time.Time) error {
	p.pollMu.Lock()
	defer p.pollMu.Unlock()
	return p.pollOnce(ctx, now)
}

func (p *Poller) loop(ctx context.Context) {
	defer close(p.done)
	for {
		state, _ := LoadWatermark(filepath.Join(p.dataDir, "state.json"))
		nextTick := intervalUntilNextTick(state.LastPolledAt, p.w.PollInterval(), time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-time.After(nextTick):
		}
		now := time.Now().UTC()
		p.pollMu.Lock()
		err := p.pollOnce(ctx, now)
		p.pollMu.Unlock()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "watch %s poll error: %v\n", p.w.ID, err)
		}
	}
}

// pollOnce runs one tick.
func (p *Poller) pollOnce(ctx context.Context, now time.Time) error {
	state, err := LoadWatermark(filepath.Join(p.dataDir, "state.json"))
	if err != nil {
		return fmt.Errorf("load watermark: %w", err)
	}
	items, nextState, err := p.src.Poll(ctx, p.w, state)
	if err != nil {
		return fmt.Errorf("source poll: %w", err)
	}
	itemsPath := filepath.Join(p.dataDir, "items.jsonl")
	admitted := make([]Item, 0, len(items))
	for i := range items {
		if anno := ApplyFilters(items[i], p.w.Source.Filters); anno != nil {
			items[i].Filtered = anno
		}
		if err := AppendItem(itemsPath, items[i]); err != nil {
			return fmt.Errorf("append item: %w", err)
		}
		p.pub.PublishItemCaptured(p.w.ID, items[i].ID, "", items[i].Filtered != nil)
		if items[i].Filtered == nil {
			admitted = append(admitted, items[i])
		}
	}
	signalsPath := filepath.Join(p.dataDir, "signals.jsonl")
	// Ingest runs whenever the watch opts into either ingestion (rich
	// agent-classified signals) or auto-start (which strictly implies
	// ingestion). The `|| AutoStart.Enabled` clause keeps pre-toggle-
	// split watches working without a yaml migration.
	ingestRequested := p.w.Ingest.Enabled || p.w.AutoStart.Enabled
	if ingestRequested {
		if len(admitted) > 0 {
			if err := p.ingestor.Run(ctx, p.w, admitted, p.dataDir); err != nil {
				_, _ = fmt.Fprintf(stderr, "watch %s ingest error: %v\n", p.w.ID, err)
			}
		} else {
			// Persist a stub run so the operator can see "poll happened,
			// nothing for the agent to do" in the UI — much clearer than
			// silent absence. Matches the saveIngestRun on-disk shape.
			saveIngestRun(p.dataDir, IngestRunMeta{
				StartedAt: now,
				ItemCount: 0,
				Status:    "skipped",
				Error:     "no new items admitted — agent not invoked (watermark already past the captured items, or all items were filtered)",
			}, nil)
		}
	} else {
		for _, it := range admitted {
			sig := Signal{
				ID:           newSignalULID(),
				WatchID:      p.w.ID,
				CreatedAt:    now,
				CitedItemIDs: []string{it.ID},
				Outcome:      OutcomeDisabled,
			}
			if err := AppendSignal(signalsPath, sig); err != nil {
				return fmt.Errorf("append disabled signal: %w", err)
			}
			p.pub.PublishSignalCreated(p.w.ID, sig.ID, string(sig.Outcome), sig.InvestigationID, sig.ManuallyStarted)
		}
	}
	nextState.LastPolledAt = now
	if err := SaveWatermark(filepath.Join(p.dataDir, "state.json"), nextState); err != nil {
		return fmt.Errorf("save watermark: %w", err)
	}
	p.pub.PublishWatchStatus(p.w.ID, "healthy", now, 0, 0, 0)
	if err := PruneItems(itemsPath, now, ItemRetention); err != nil {
		_, _ = fmt.Fprintf(stderr, "watch %s prune: %v\n", p.w.ID, err)
	}
	return nil
}

// intervalUntilNextTick aligns to a stable cadence using lastPolledAt.
func intervalUntilNextTick(lastPolledAt time.Time, interval time.Duration, now time.Time) time.Duration {
	if lastPolledAt.IsZero() {
		return 0
	}
	due := lastPolledAt.Add(interval)
	if !due.After(now) {
		return 0
	}
	return due.Sub(now)
}

// newSignalULID is the seam tests stub. Default is time-based; the
// launcher wiring overrides with oklog/ulid.
var newSignalULID = func() string { return fmt.Sprintf("sig-%d", time.Now().UnixNano()) }

// NewSignalULID is the exported seam server handlers use to mint signal
// IDs when writing directly (e.g. report_unclear / dismiss_items). Same
// generator the poller uses internally; can be overridden in tests by
// reassigning newSignalULID.
func NewSignalULID() string { return newSignalULID() }

// stderr is the default writer for poller log lines; tests override.
var stderr = os.Stderr
