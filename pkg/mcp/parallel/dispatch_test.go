package parallel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// fakeUpstream builds a Registry whose every Client() returns a session
// connected to an in-memory MCP server that hosts one tool, `analyze_change`,
// which sleeps for `delay`, then returns the supplied text. Useful for
// timing + error-injection tests without subprocesses.
func fakeUpstream(t *testing.T, alias string, delay time.Duration, body string, errBody string) *Registry {
	t.Helper()
	return newRegistryForTest(
		map[string]UpstreamSpec{alias: {Command: "/dev/null"}},
		func(_ UpstreamSpec) (*sdkmcp.ClientSession, func() error, error) {
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "v0"}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "analyze_change", Description: "test"},
				func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
					if errBody != "" {
						return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: errBody}}}, nil, nil
					}
					return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: body}}}, map[string]any{"text": body}, nil
				})
			sT, cT := sdkmcp.NewInMemoryTransports()
			ctx := context.Background()
			_, err := server.Connect(ctx, sT, nil)
			require.NoError(t, err)
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
			sess, err := client.Connect(ctx, cT, nil)
			require.NoError(t, err)
			return sess, sess.Close, nil
		},
	)
}

func TestDispatch_RunsCallsInParallel(t *testing.T) {
	t.Parallel()
	reg := fakeUpstream(t, "fake-server", 80*time.Millisecond, "ok-body", "")
	defer func() { _ = reg.Close() }()

	calls := []SubCall{
		{Server: "fake-server", Tool: "analyze_change", Input: map[string]any{"q": "a"}},
		{Server: "fake-server", Tool: "analyze_change", Input: map[string]any{"q": "b"}},
		{Server: "fake-server", Tool: "analyze_change", Input: map[string]any{"q": "c"}},
	}
	start := time.Now()
	results := Dispatch(context.Background(), DispatchInput{
		Registry: reg, Allowlist: Allowlist{entries: []allowEntry{{"fake-server", "analyze_change"}}},
		Calls: calls, MaxConcurrency: 4, ParentToolID: "",
	})
	elapsed := time.Since(start)

	require.Len(t, results, 3)
	for i, r := range results {
		require.Truef(t, r.OK, "result %d not ok: %+v", i, r)
	}
	// Wall-clock should be roughly 80ms, not 240ms. Generous bound to
	// absorb test-environment jitter.
	require.Less(t, elapsed, 200*time.Millisecond, "expected parallel execution (~80ms), got %v", elapsed)
}

func TestDispatch_RespectsConcurrencyCap(t *testing.T) {
	t.Parallel()
	var inFlight, peak int32
	reg := newRegistryForTest(
		map[string]UpstreamSpec{"fake-server": {Command: "/dev/null"}},
		func(_ UpstreamSpec) (*sdkmcp.ClientSession, func() error, error) {
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "v0"}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "analyze_change", Description: "test"},
				func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
					n := atomic.AddInt32(&inFlight, 1)
					for {
						p := atomic.LoadInt32(&peak)
						if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
							break
						}
					}
					time.Sleep(50 * time.Millisecond)
					atomic.AddInt32(&inFlight, -1)
					return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, nil, nil
				})
			sT, cT := sdkmcp.NewInMemoryTransports()
			ctx := context.Background()
			_, _ = server.Connect(ctx, sT, nil)
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
			sess, _ := client.Connect(ctx, cT, nil)
			return sess, sess.Close, nil
		},
	)
	defer func() { _ = reg.Close() }()

	calls := make([]SubCall, 6)
	for i := range calls {
		calls[i] = SubCall{Server: "fake-server", Tool: "analyze_change", Input: map[string]any{}}
	}
	_ = Dispatch(context.Background(), DispatchInput{
		Registry: reg, Allowlist: Allowlist{entries: []allowEntry{{"fake-server", "analyze_change"}}},
		Calls: calls, MaxConcurrency: 2, ParentToolID: "",
	})
	require.LessOrEqual(t, int(atomic.LoadInt32(&peak)), 2, "peak in-flight must not exceed MaxConcurrency")
}

func TestDispatch_RejectedSubCalls_AreFlagged(t *testing.T) {
	t.Parallel()
	reg := fakeUpstream(t, "fake-server", 5*time.Millisecond, "ok", "")
	defer func() { _ = reg.Close() }()
	results := Dispatch(context.Background(), DispatchInput{
		Registry: reg, Allowlist: Allowlist{entries: []allowEntry{{"fake-server", "analyze_change"}}},
		Calls: []SubCall{
			{Server: "fake-server", Tool: "analyze_change"},
			{Server: "fake-server", Tool: "not_on_allowlist"},
		},
		MaxConcurrency: 4,
	})
	require.Len(t, results, 2)
	require.True(t, results[0].OK)
	require.False(t, results[1].OK)
	require.True(t, results[1].Rejected)
	require.Contains(t, results[1].Error, "allowlist")
}

func TestDispatch_OneCallFails_OthersStillResolve(t *testing.T) {
	t.Parallel()
	regOK := fakeUpstream(t, "fake-good", 5*time.Millisecond, "good", "")
	regBad := fakeUpstream(t, "fake-bad", 5*time.Millisecond, "", "upstream went boom")
	defer func() { _ = regOK.Close() }()
	defer func() { _ = regBad.Close() }()

	// Stitch a composite registry: it owns sessions sourced from the two
	// fakeUpstreams above. We yank the cached client out of each one and
	// hand ownership to the composite so its Close() drives the teardown.
	composite := newRegistryForTest(map[string]UpstreamSpec{
		"fake-good": {Command: "/dev/null"},
		"fake-bad":  {Command: "/dev/null"},
	}, nil)
	composite.cache["fake-good"] = mustClient(t, regOK, "fake-good")
	composite.cache["fake-bad"] = mustClient(t, regBad, "fake-bad")
	defer func() { _ = composite.Close() }()

	results := Dispatch(context.Background(), DispatchInput{
		Registry: composite, Allowlist: Allowlist{entries: []allowEntry{
			{"fake-good", "analyze_change"}, {"fake-bad", "analyze_change"},
		}},
		Calls: []SubCall{
			{Server: "fake-good", Tool: "analyze_change"},
			{Server: "fake-bad", Tool: "analyze_change"},
		},
		MaxConcurrency: 4,
	})
	require.Len(t, results, 2)
	require.True(t, results[0].OK, "good call should succeed")
	require.False(t, results[1].OK, "bad call should fail")
	require.Contains(t, results[1].Error, "upstream went boom")
}

// mustClient pulls a cachedClient out of the source registry so callers
// can hand its ownership to a composite test registry.
func mustClient(t *testing.T, reg *Registry, alias string) *cachedClient {
	t.Helper()
	_, err := reg.Client(context.Background(), alias)
	require.NoError(t, err)
	c := reg.cache[alias]
	delete(reg.cache, alias)
	require.NotNil(t, c)
	return c
}
