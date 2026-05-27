package repos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// GenerateRequest carries the per-call options for a generation. Mirrors the
// flags on the triagent-mcp generate-architecture-summary subcommand.
type GenerateRequest struct {
	Kind string // "freeform" for v1
	// Description is the operator-authored connection-time description
	// from user_repos.yaml / defaults_repos.yaml. Surfaced to the
	// sub-agent so it can self-allocate emphasis based on what the
	// repo is for. Optional but high-leverage when present — every
	// LinkedRepo added since the 30-char minimum has one.
	Description string
	Focus       string // optional operator hint, narrower than Description
	// OperatorEditsDiff is a unified diff (git format) representing
	// hand-edits the operator made to the prior cached summary,
	// computed by the caller before enqueuing. When non-empty the
	// worker writes it to a temp file and passes the path via
	// --operator-edits-file so the sub-agent can consider each hunk
	// when producing its fresh output. Empty means a clean regen.
	OperatorEditsDiff string
}

// EventPublisher is the seam used by the worker to surface phase
// transitions on the launcher's global SSE channel. The server
// package supplies an adapter that fans events out via
// Manager.publishGlobalEvent. nil disables eventing — production
// callers always wire a real publisher; tests use a fake.
type EventPublisher interface {
	PublishStarted(owner, name string)
	PublishSuccess(owner, name string, generatedAt time.Time, byteCount int, kind string)
	PublishError(owner, name string, msg string)
}

// ArchitectureWorker runs triagent-mcp generate-architecture-summary in the
// background. Single-flight per <owner>/<name>: a second GenerateAsync for the
// same repo while one is in flight returns false (caller surfaces 409).
type ArchitectureWorker struct {
	mcpBin      string // path to triagent-mcp binary
	gitCacheDir string

	mu       sync.Mutex
	inFlight map[string]struct{}

	// runFn is the test seam. Production wires it to runShellOut, which
	// shells out to triagent-mcp; tests stub it.
	runFn func(ctx context.Context, owner, name string, opts GenerateRequest) error

	// publisher fans phase transitions to the global SSE channel.
	// Optional; nil disables eventing. Held in an atomic.Value so
	// SetEventPublisher (typically called once at startup) and the
	// goroutine spawned by GenerateAsync can race-free even if a
	// future caller swaps the publisher mid-flight. Stored as
	// publisherSlot{p} so the underlying type stays consistent
	// (atomic.Value rejects mixed concrete types).
	publisher atomic.Value // publisherSlot
}

// publisherSlot wraps an EventPublisher (which is itself an interface)
// so atomic.Value sees a consistent concrete type across stores. A
// zero-valued slot (p == nil) means "no publisher wired".
type publisherSlot struct {
	p EventPublisher
}

// NewArchitectureWorker returns a worker wired for production. mcpBin is
// the path to the triagent-mcp binary the launcher already configures elsewhere.
// See GenerateAsync for the context-lifetime contract callers must honour.
func NewArchitectureWorker(mcpBin, gitCacheDir string) *ArchitectureWorker {
	w := &ArchitectureWorker{
		mcpBin:      mcpBin,
		gitCacheDir: gitCacheDir,
		inFlight:    map[string]struct{}{},
	}
	w.runFn = w.runShellOut
	return w
}

// NewArchitectureWorkerForTest builds a worker whose runFn is the caller's
// stub instead of the production shell-out. Lets handler tests in sibling
// packages drive the worker without spawning subprocesses. Production
// callers always use NewArchitectureWorker.
func NewArchitectureWorkerForTest(runFn func(ctx context.Context, owner, name string, opts GenerateRequest) error) *ArchitectureWorker {
	w := &ArchitectureWorker{inFlight: map[string]struct{}{}}
	w.runFn = runFn
	return w
}

// SetEventPublisher wires a publisher for repo_summary_state events.
// Optional; nil disables eventing (worker still runs and writes the
// cache file). Production callers wire this immediately after
// NewArchitectureWorker, before enqueuing any work — but the swap is
// race-free regardless, so a future caller could swap publishers
// while generations are in flight without data races.
func (w *ArchitectureWorker) SetEventPublisher(p EventPublisher) {
	w.publisher.Store(publisherSlot{p: p})
}

// loadPublisher returns the currently-wired publisher, or nil if none
// is set. Cheap: a single atomic.Value load.
func (w *ArchitectureWorker) loadPublisher() EventPublisher {
	v, ok := w.publisher.Load().(publisherSlot)
	if !ok {
		return nil
	}
	return v.p
}

// GenerateAsync kicks off a generation in a goroutine. Returns false when a
// generation is already in flight for this repo (caller surfaces 409).
//
// The ctx is the goroutine's lifetime — when it's cancelled, the triagent-mcp
// subprocess is killed via exec.CommandContext. Callers should pass a
// context that outlives the originating HTTP request (e.g.
// manager.ParentContext() in handlers — see handlers_sessions_pr.go's
// runPushSessionPR for the same pattern). Passing the request context
// directly will kill mid-flight generations when the operator's tab
// closes; that's almost always not what we want.
func (w *ArchitectureWorker) GenerateAsync(ctx context.Context, owner, name string, opts GenerateRequest) bool {
	if !w.tryAcquire(owner, name) {
		return false
	}
	// Snapshot the publisher once so all four phase calls go to the
	// same observer even if SetEventPublisher swaps it concurrently.
	pub := w.loadPublisher()
	if pub != nil {
		pub.PublishStarted(owner, name)
	}
	go func() {
		defer w.release(owner, name)
		err := w.runFn(ctx, owner, name, opts)
		if pub == nil {
			return
		}
		if err != nil {
			pub.PublishError(owner, name, err.Error())
			return
		}
		// Read the cache file the runFn just wrote so the
		// success event carries real frontmatter (generatedAt,
		// byteCount, kind). If the read fails — unlikely; runFn
		// already succeeded — fall back to a bare success event.
		path := SummaryPath(w.gitCacheDir, owner, name)
		sum, readErr := ReadSummary(path)
		if readErr != nil || sum == nil {
			pub.PublishSuccess(owner, name, time.Now().UTC(), 0, opts.Kind)
			return
		}
		// Generation may have produced an error-variant cache
		// file (e.g. sub-agent timeout). Surface that as an
		// error event despite runFn returning nil — the cache
		// file's frontmatter is the authoritative result.
		if sum.Frontmatter.Error != "" {
			pub.PublishError(owner, name, sum.Frontmatter.Error)
			return
		}
		pub.PublishSuccess(
			owner, name,
			sum.Frontmatter.GeneratedAt,
			sum.Frontmatter.ByteCount,
			sum.Frontmatter.Kind,
		)
	}()
	return true
}

func (w *ArchitectureWorker) tryAcquire(owner, name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := owner + "/" + name
	if _, ok := w.inFlight[key]; ok {
		return false
	}
	w.inFlight[key] = struct{}{}
	return true
}

func (w *ArchitectureWorker) release(owner, name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inFlight, owner+"/"+name)
}

// IsInFlight returns whether a generation is currently running for this repo.
// Used by HTTP handlers to surface state without acquiring the lock.
func (w *ArchitectureWorker) IsInFlight(owner, name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.inFlight[owner+"/"+name]
	return ok
}

// classifyShellOutError translates the verbose CombinedOutput from
// `triagent-mcp generate-architecture-summary` into a short actionable
// message for the operator-facing toast. Falls back to a trimmed
// version of the raw output when no known pattern matches — operators
// still get the underlying signal, just capped in length.
func classifyShellOutError(combinedOutput string) string {
	out := combinedOutput
	switch {
	case strings.Contains(out, "Could not resolve hostname"):
		return "Network error: could not reach github.com (DNS resolution failed). Retry once you're back online."
	case strings.Contains(out, "Permission denied (publickey)"):
		return "SSH authentication failed. Verify your GitHub SSH key with `ssh -T git@github.com`."
	case strings.Contains(out, "already exists and is not an empty directory"):
		return "Stale cache state — the clone target is non-empty. Remove the stale dir under ~/.cache/triagent-mcp/git/<owner>/<name> and retry."
	case strings.Contains(out, "Repository not found"),
		strings.Contains(out, "ERROR: Repository not found"):
		return "Repository not found, or your SSH key has no read access to it."
	case strings.Contains(out, "could not lock config file"):
		return "Git lock contention — another process is using the cache. Retry in a moment."
	}
	// Fall back to the raw output but keep it bounded so the toast
	// stays scannable.
	const cap = 400
	trimmed := strings.TrimSpace(out)
	if len(trimmed) > cap {
		trimmed = trimmed[:cap] + "…"
	}
	return trimmed
}

// runShellOut is the production wiring of runFn. Shells out to
// `triagent-mcp generate-architecture-summary --repo <owner/name> --cache-dir
// <dir> [--kind …] [--focus …] [--operator-edits-file …]`. The cache
// file is written by triagent-mcp, not by the worker — failures are captured
// in the cache file's frontmatter so even errors leave a readable
// artefact.
func (w *ArchitectureWorker) runShellOut(ctx context.Context, owner, name string, opts GenerateRequest) error {
	args := []string{
		"generate-architecture-summary",
		"--repo", owner + "/" + name,
		"--cache-dir", w.gitCacheDir,
	}
	if opts.Kind != "" {
		args = append(args, "--kind", opts.Kind)
	}
	if opts.Description != "" {
		args = append(args, "--description", opts.Description)
	}
	if opts.Focus != "" {
		args = append(args, "--focus", opts.Focus)
	}
	// Operator-edits diff is too large for a CLI flag value (kilobytes
	// of unified-diff text, often above the per-arg limit on some
	// shells). Write to a temp file, pass the path, clean up after.
	if opts.OperatorEditsDiff != "" {
		f, err := os.CreateTemp("", "c1-operator-edits-*.diff")
		if err != nil {
			return fmt.Errorf("create operator-edits tempfile: %w", err)
		}
		tmpPath := f.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		if _, err := f.WriteString(opts.OperatorEditsDiff); err != nil {
			_ = f.Close()
			return fmt.Errorf("write operator-edits tempfile: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close operator-edits tempfile: %w", err)
		}
		args = append(args, "--operator-edits-file", tmpPath)
	}
	cmd := exec.CommandContext(ctx, w.mcpBin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Log the structured error + full output to launcher stderr
		// for debugging; hand the operator-facing toast a short,
		// actionable classification rather than the raw multi-layer
		// wrapping (`exit status 1 (output: ERRO ensure clone: git
		// fetch ... ssh: Could not resolve …`).
		fmt.Fprintf(os.Stderr, "investigate/repos: generate-architecture-summary failed for %s/%s: %v\nfull output:\n%s\n",
			owner, name, err, out)
		return errors.New(classifyShellOutError(string(out)))
	}
	return nil
}
