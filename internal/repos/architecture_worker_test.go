package repos

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchitectureWorker_SingleFlightPerRepo(t *testing.T) {
	// Tests the internal tryAcquire/release primitives directly. Other
	// GenerateAsync_* tests exercise the public single-flight contract.
	t.Parallel()
	w := NewArchitectureWorker("/path/to/triagent-mcp", "/cache")
	ok1 := w.tryAcquire("example-org", "example-app")
	ok2 := w.tryAcquire("example-org", "example-app")
	ok3 := w.tryAcquire("example-org", "example-extra")
	assert.True(t, ok1, "first acquire should succeed")
	assert.False(t, ok2, "second acquire on same repo should fail")
	assert.True(t, ok3, "different repo should succeed")
	w.release("example-org", "example-app")
	ok4 := w.tryAcquire("example-org", "example-app")
	assert.True(t, ok4, "acquire after release should succeed")
}

func TestArchitectureWorker_GenerateAsync_RunsAndReleases(t *testing.T) {
	t.Parallel()
	w := NewArchitectureWorker("/path/to/triagent-mcp", "/cache")
	called := make(chan struct{})
	w.runFn = func(ctx context.Context, owner, name string, opts GenerateRequest) error {
		assert.Equal(t, "example-org", owner)
		assert.Equal(t, "example-app", name)
		close(called)
		return nil
	}

	ok := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	require.True(t, ok, "GenerateAsync should return true on first call")

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("runFn was not called within timeout")
	}

	// Wait for the goroutine's defer to release the lock.
	require.Eventually(t, func() bool {
		return !w.IsInFlight("example-org", "example-app")
	}, time.Second, 10*time.Millisecond, "lock should be released after run")
}

func TestArchitectureWorker_GenerateAsync_Refused_WhenInFlight(t *testing.T) {
	t.Parallel()
	w := NewArchitectureWorker("/path/to/triagent-mcp", "/cache")
	hold := make(chan struct{})
	w.runFn = func(ctx context.Context, owner, name string, opts GenerateRequest) error {
		<-hold
		return nil
	}

	ok1 := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	require.True(t, ok1)

	ok2 := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	assert.False(t, ok2, "concurrent generate for same repo should be refused")

	close(hold)

	// Tidy: wait for the first run to complete so the test doesn't leak goroutines.
	require.Eventually(t, func() bool {
		return !w.IsInFlight("example-org", "example-app")
	}, time.Second, 10*time.Millisecond)
}

func TestArchitectureWorker_GenerateAsync_ReleaseOnError(t *testing.T) {
	t.Parallel()
	w := NewArchitectureWorker("/path/to/triagent-mcp", "/cache")
	var ran sync.WaitGroup
	ran.Add(1)
	w.runFn = func(ctx context.Context, owner, name string, opts GenerateRequest) error {
		defer ran.Done()
		return errors.New("shell-out failed")
	}

	ok := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	require.True(t, ok)
	ran.Wait()

	require.Eventually(t, func() bool {
		return !w.IsInFlight("example-org", "example-app")
	}, time.Second, 10*time.Millisecond, "lock should be released even when runFn returns an error")
}

type capturedEvent struct {
	phase string
	owner string
	name  string
	err   string
}

type fakePublisher struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (p *fakePublisher) PublishStarted(owner, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{phase: "started", owner: owner, name: name})
}

func (p *fakePublisher) PublishSuccess(owner, name string, generatedAt time.Time, byteCount int, kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{phase: "success", owner: owner, name: name})
}

func (p *fakePublisher) PublishError(owner, name string, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{phase: "error", owner: owner, name: name, err: msg})
}

func (p *fakePublisher) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]capturedEvent, len(p.events))
	copy(out, p.events)
	return out
}

func TestArchitectureWorker_GenerateAsync_PublishesStartedAndSuccess(t *testing.T) {
	t.Parallel()
	w := NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts GenerateRequest) error {
		return nil
	})
	pub := &fakePublisher{}
	w.SetEventPublisher(pub)

	ok := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	require.True(t, ok)

	require.Eventually(t, func() bool {
		evts := pub.snapshot()
		return len(evts) >= 2 &&
			evts[0].phase == "started" &&
			evts[len(evts)-1].phase == "success"
	}, time.Second, 10*time.Millisecond, "expected started + success events")

	evts := pub.snapshot()
	assert.Equal(t, "example-org", evts[0].owner)
	assert.Equal(t, "example-app", evts[0].name)
}

func TestArchitectureWorker_GenerateAsync_PublishesError(t *testing.T) {
	t.Parallel()
	w := NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts GenerateRequest) error {
		return errors.New("shell-out failed")
	})
	pub := &fakePublisher{}
	w.SetEventPublisher(pub)

	ok := w.GenerateAsync(context.Background(), "example-org", "example-app", GenerateRequest{Kind: "freeform"})
	require.True(t, ok)

	require.Eventually(t, func() bool {
		evts := pub.snapshot()
		return len(evts) >= 2 && evts[len(evts)-1].phase == "error"
	}, time.Second, 10*time.Millisecond, "expected started + error events")

	evts := pub.snapshot()
	assert.Equal(t, "shell-out failed", evts[len(evts)-1].err)
}

// classifyShellOutError translates verbose triagent-mcp combined output
// into operator-facing toast messages. The cases below lock in the
// patterns against real-world failure strings — adding/changing a
// pattern should update this test in the same commit.
func TestClassifyShellOutError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		output   string
		contains string // substring expected in the classified message
	}{
		{
			name: "DNS resolution failure (verbatim from a real run)",
			output: "2026/05/10 10:44:23 ERRO ensure clone: git fetch example-org/example-platform: " +
				"git fetch --prune --tags: exit status 128 (output: ssh: Could not resolve hostname github.com: " +
				"Temporary failure in name resolution\nfatal: Could not read from remote repository.\n)",
			contains: "could not reach github.com",
		},
		{
			name:     "SSH auth failure",
			output:   "ERRO ensure clone: ... Permission denied (publickey).\nfatal: Could not read from remote repository.\n",
			contains: "SSH authentication failed",
		},
		{
			name:     "stale clone target",
			output:   "fatal: destination path '/home/user/.cache/triagent-mcp/git/x/y' already exists and is not an empty directory.\n",
			contains: "Stale cache state",
		},
		{
			name:     "repo not found",
			output:   "ERROR: Repository not found.\nfatal: Could not read from remote repository.\n",
			contains: "Repository not found",
		},
		{
			name:     "git lock contention",
			output:   "fatal: could not lock config file .git/config: File exists\n",
			contains: "Git lock contention",
		},
		{
			name:     "unknown pattern → trimmed raw output",
			output:   "some unfamiliar error nobody anticipated",
			contains: "some unfamiliar error nobody anticipated",
		},
		{
			name:     "very long unknown pattern → bounded",
			output:   strings.Repeat("x", 1000),
			contains: "…",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyShellOutError(tc.output)
			assert.Contains(t, got, tc.contains)
			assert.LessOrEqual(t, len(got), 500,
				"classified message must stay scannable (≤ 500 chars)")
		})
	}
}
