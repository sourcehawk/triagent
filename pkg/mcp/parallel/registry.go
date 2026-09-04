package parallel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// EnvUpstreams is the env var preflight writes for the broker. Value is
// JSON-encoded map[alias]UpstreamSpec. Empty/unset → broker rejects every
// sub-call as "unknown server alias".
const EnvUpstreams = "TRIAGENT_MCP_PARALLEL_UPSTREAMS"

// UpstreamSpec is one upstream MCP server's spawn recipe — mirrors a
// single entry in `mcp.json`'s `mcpServers`.
type UpstreamSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ParseUpstreams decodes the JSON value of EnvUpstreams. An empty input
// yields an empty (non-nil) map; malformed JSON returns an error.
func ParseUpstreams(raw string) (map[string]UpstreamSpec, error) {
	if raw == "" {
		return map[string]UpstreamSpec{}, nil
	}
	var out map[string]UpstreamSpec
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvUpstreams, err)
	}
	if out == nil {
		out = map[string]UpstreamSpec{}
	}
	return out, nil
}

// spawnFn is the seam between production (CommandTransport against a real
// subprocess) and tests (in-memory transport). It returns the client
// session plus a cleanup hook the registry will call on shutdown.
type spawnFn func(UpstreamSpec) (*sdkmcp.ClientSession, func() error, error)

// Registry owns the broker's persistent client connections to upstream
// MCP servers. Connections are lazy — the first Client(alias) call spawns
// the subprocess and completes the MCP initialize handshake; subsequent
// calls return the cached session.
type Registry struct {
	upstreams map[string]UpstreamSpec
	spawn     spawnFn

	mu    sync.Mutex
	cache map[string]*cachedClient
}

type cachedClient struct {
	session *sdkmcp.ClientSession
	close   func() error
}

// NewRegistry builds a production Registry from a parsed upstreams map.
// Uses subprocess-backed CommandTransport for each upstream.
func NewRegistry(upstreams map[string]UpstreamSpec) *Registry {
	return &Registry{
		upstreams: upstreams,
		spawn:     prodSpawner,
		cache:     map[string]*cachedClient{},
	}
}

// newRegistryForTest is the test-only entry point. A nil spawn fn means
// "tests don't expect any spawns" — calls will panic if attempted.
func newRegistryForTest(upstreams map[string]UpstreamSpec, spawn spawnFn) *Registry {
	return &Registry{
		upstreams: upstreams,
		spawn:     spawn,
		cache:     map[string]*cachedClient{},
	}
}

// Client returns a connected client session for alias. First call to a
// given alias spawns the upstream; subsequent calls reuse the cached
// session.
func (r *Registry) Client(ctx context.Context, alias string) (*sdkmcp.ClientSession, error) {
	r.mu.Lock()
	if c, ok := r.cache[alias]; ok {
		r.mu.Unlock()
		return c.session, nil
	}
	spec, ok := r.upstreams[alias]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("unknown upstream alias %q (not in %s)", alias, EnvUpstreams)
	}
	// Release the lock around the spawn — initialize handshake can take a
	// non-trivial moment, and we don't want a slow spawn for one alias to
	// block reads for another. We re-acquire to insert into the cache; if
	// a parallel spawn won the race, we close ours and return theirs.
	r.mu.Unlock()
	sess, closeFn, err := r.spawn(spec)
	if err != nil {
		return nil, fmt.Errorf("spawn upstream %q: %w", alias, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[alias]; ok {
		// Lost the race. Close our extra session.
		_ = closeFn()
		return c.session, nil
	}
	r.cache[alias] = &cachedClient{session: sess, close: closeFn}
	return sess, nil
}

// Close terminates every cached client session. Returns the first error
// encountered, but always attempts to close every entry. Safe to call
// when Registry was never used.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for alias, c := range r.cache {
		if err := c.close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close %q: %w", alias, err)
		}
	}
	r.cache = map[string]*cachedClient{}
	return firstErr
}

// prodSpawner is the production implementation of spawnFn — spawns a
// subprocess via CommandTransport and connects an SDK client to it.
func prodSpawner(spec UpstreamSpec) (*sdkmcp.ClientSession, func() error, error) {
	cmd := buildUpstreamCommand(spec)
	transport := &sdkmcp.CommandTransport{Command: cmd}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "triagent-mcp-parallel-broker", Version: "0.1.0"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	closeFn := func() error {
		cancel()
		return sess.Close()
	}
	return sess, closeFn, nil
}

// buildUpstreamCommand assembles the *exec.Cmd for a given upstream. The
// subprocess gets the broker's own environment with the spec's env block
// layered on top, which is the same shape Claude Code gives an MCP it
// launches directly: PATH, HOME and the rest come from the parent so an
// upstream that shells out (git fetch, kubectl) can find its binaries, and
// the spec wins on conflict so each upstream keeps its own telemetry tool
// prefix instead of the broker's. The broker's upstream registry blob is
// dropped: it is large, carries every upstream's secrets, and means nothing
// to a child.
func buildUpstreamCommand(spec UpstreamSpec) *exec.Cmd {
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Env = upstreamEnv(os.Environ(), spec.Env)
	return cmd
}

func upstreamEnv(parent []string, overrides map[string]string) []string {
	env := make([]string, 0, len(parent)+len(overrides))
	for _, kv := range parent {
		key, _, _ := strings.Cut(kv, "=")
		if key == EnvUpstreams {
			continue
		}
		if _, overridden := overrides[key]; overridden {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		if k == EnvUpstreams {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
