// Package slack is a Slack Web API MCP server. The server holds one Slack
// token and exposes channel-aware tools; the agent passes a `channel_id`
// argument on every call. Each tool resolves the channel against a lazy
// per-channel cache so repeated reads of the same channel reuse the same
// materialised state without paying re-sync costs.
//
// The token is supplied via `--slack-token` / `$TRIAGENT_MCP_SLACK_TOKEN` at boot.
// No other launcher arguments are needed; channel and time-window scoping
// are agent-level concerns surfaced through the system prompt.
package slack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures a Slack MCP server.
type Options struct {
	// Token is the Slack user/bot token. Required.
	Token string

	// APIBase is the Slack API base URL. Empty defaults to
	// "https://slack.com/api". Tests override this with an httptest
	// server.
	APIBase string

	// ClaudeBinary is the claude CLI used by sub-agent tools (analyze_channel,
	// summarize_thread). Empty falls back to looking up `claude` on $PATH.
	ClaudeBinary string

	// AnalyzeCap caps how many parent messages syncFull materialises.
	// Default 2000.
	AnalyzeCap int

	// PeekCap caps how many parent messages syncPeek materialises.
	// Default 200.
	PeekCap int

	// SubAgentTimeout caps each sub-agent run; zero falls back to subagent.Run's
	// 90s default.
	SubAgentTimeout time.Duration

	// CacheDir is the parent directory the per-channel cache lives under.
	// Empty resolves $XDG_CACHE_HOME/triagent-mcp/slack (or ~/.cache/triagent-mcp/slack).
	CacheDir string
}

// Server holds the MCP server, the shared HTTP client, and a lazy
// per-channel store cache. Each tool call resolves a *Store on demand for
// the channel the agent named, so a single MCP process can serve any
// number of channels the token can read.
type Server struct {
	impl   *mcp.Server
	opts   Options
	client *Client

	cacheRoot       string
	claudeBin       string
	subAgentTimeout time.Duration

	mu     sync.Mutex
	stores map[string]*Store // channel id → materialised cache; populated lazily

	// Channel-name → match cache, populated by handleGetChannelID. Cleared
	// per-process on restart; no TTL — workspace renames are rare relative
	// to a session's lifetime.
	nameMu    sync.Mutex
	nameCache map[string]channelNameMatch

	// runSubAgent is the seam used by tool tests. It receives the
	// resolved working directory so per-channel store dirs reach the
	// sub-agent. resumeSessionID, when non-empty, continues the prior
	// claude conversation — used by the citation-correction retry so
	// the retry doesn't pay the cost of a fresh session.
	runSubAgent func(ctx context.Context, prompt, parentToolID, workingDir, resumeSessionID string) (subAgentResult, error)
}

// subAgentResult is the structured outcome of a sub-agent invocation. The
// citation fields are populated by runSubAgentWithCitations after parsing
// the <<<CITATIONS>>> block; the raw runSubAgent seam still returns
// Summary+TimedOut and leaves the citation fields zeroed (parsed by the
// caller).
type subAgentResult struct {
	Summary             string
	Citations           []citations.Citation
	CitationsParseError string
	TimedOut            bool
	// SessionID is the claude conversation id from the most recent
	// sub-agent invocation — surfaced so callers can thread it as
	// resumeSessionID on a subsequent invocation (e.g. citation retry).
	// Empty when the runner didn't produce one (stubs, transport errors).
	SessionID string
}

// channelNameMatch is one row of the name-resolution cache. Mirrors the
// shape returned by `slack_get_channel_id`.
type channelNameMatch struct {
	ID         string `json:"channel_id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived,omitempty"`
	IsMember   bool   `json:"is_member,omitempty"`
}

// New constructs and registers the Slack MCP server. The token is the
// only required input; channel scoping is per tool call.
func New(opts Options) (*Server, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("slack: token is required (set --slack-token or $TRIAGENT_MCP_SLACK_TOKEN)")
	}
	if !strings.HasPrefix(opts.Token, "xoxp-") && !strings.HasPrefix(opts.Token, "xoxb-") {
		return nil, fmt.Errorf("slack: token must start with xoxp- or xoxb-")
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://slack.com/api"
	}
	root := opts.CacheDir
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("slack: resolve user cache dir: %w", err)
		}
		root = filepath.Join(base, "triagent-mcp", "slack")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("slack: mkdir cache root: %w", err)
	}

	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-slack",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:            impl,
		opts:            opts,
		client:          NewClient(opts.APIBase, opts.Token),
		cacheRoot:       root,
		claudeBin:       opts.ClaudeBinary,
		subAgentTimeout: opts.SubAgentTimeout,
		stores:          map[string]*Store{},
		nameCache:       map[string]channelNameMatch{},
	}
	s.runSubAgent = s.runSubAgentReal
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// errorResult formats a tool-level error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// resolveStore returns (and lazily creates) the *Store for channelID.
// Empty channelID is a tool-call error: every channel-aware tool must
// receive the id from the agent.
func (s *Server) resolveStore(channelID string) (*Store, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[channelID]; ok {
		return st, nil
	}
	st, err := NewStore(StoreOptions{Root: s.cacheRoot, ChannelID: channelID})
	if err != nil {
		return nil, err
	}
	st.client = s.client
	st.channelID = channelID
	if s.opts.AnalyzeCap > 0 {
		st.analyzeCap = s.opts.AnalyzeCap
	}
	if s.opts.PeekCap > 0 {
		st.peekCap = s.opts.PeekCap
	}
	s.stores[channelID] = st
	return st, nil
}

// register wires every tool. Each handler is wrapped in telemetry.Wrap
// so the launcher's activity panel sees real call boundaries.
func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "slack_get_channel_id",
		Description: "Resolve a Slack channel name (e.g. `incidents-2026-04-01` or `#incidents-2026-04-01`) to its `C…` id. Use this first whenever you only have a name — every other slack tool needs the id. Looks up channels the token's owner has joined; for channels the operator isn't a member of, ask them for the id directly.",
	}, telemetry.Wrap("slack_get_channel_id", s.handleGetChannelID))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "slack_channel_overview",
		Description: "Cheap first-look at a Slack channel. Returns metadata (name, topic, purpose, members, creation time), pinned bookmarks (incident channels typically link the incident doc / status page here — check this first), and a cache peek (recency window, parent count, thread density, distinct users). The OPENING move on any channel. From here, decide: search by keyword (`slack_search_messages`), hand a single thread to a sub-agent (`summarize_thread`), or hand the whole channel to a sub-agent (`analyze_channel`). `peek_bound_hit:true` means the channel is bigger than this peek showed; treat parents_seen as a floor. `bookmarks_unavailable:true` means the operator token lacks bookmarks:read scope.",
	}, telemetry.Wrap("slack_channel_overview", s.handleChannelOverview))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "slack_search_messages",
		Description: "Substring (case-insensitive) search across a channel's cached content. Returns hits with `thread_ts` set to the parent of the conversation tree containing the match. Cheap and deterministic — no LLM cost. Natural next step: pass an interesting `thread_ts` to `summarize_thread` to read the full thread via sub-agent. Use this when you have a specific keyword in mind (\"503\", \"rollback\", \"postmortem\"). For \"what was the channel about?\" use `analyze_channel` instead. `partial_index:true` means the cache hit the materialisation cap and older messages aren't in the search corpus — widen `since_unix` if you need older coverage.",
	}, telemetry.Wrap("slack_search_messages", s.handleSearchMessages))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "summarize_thread",
		Description: "Spawn a focused sub-agent to read a single Slack thread and answer a specific question about it. Most production incidents live in one big thread — this is the cheap path when you already know which thread to look at (from a slack_search_messages hit, or a thread_ts surfaced by analyze_channel). For \"what is this whole channel about\" use analyze_channel. desired_findings is your question for the sub-agent: be concrete (\"which services were degraded and for how long\", \"what actions did responders take in order\"). Returns a natural-language summary plus a citations array (each entry is {channel_id, thread_ts, message_ts?}) for UI rendering; sub-agent's tool calls render as collapsed children in the activity panel.",
	}, telemetry.Wrap("summarize_thread", s.handleSummarizeThread))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "analyze_channel",
		Description: "Spawn a focused sub-agent to read an entire channel and answer a specific findings request. Use when you don't know which thread holds the answer, or when the answer spans multiple threads (e.g. cross-cutting incident timelines). Frame desired_findings as concrete asks (\"list every service mentioned as degraded\", not \"summarize\"). The channel is cached locally between calls — repeat calls re-sync incrementally rather than refetching. `truncated:true` means the channel is bigger than the materialization cap; widen `$TRIAGENT_MCP_SLACK_ANALYZE_CAP` or pass a `since_unix` lower bound to scope down.",
	}, telemetry.Wrap("analyze_channel", s.handleAnalyzeChannel))
}
