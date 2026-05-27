// Package agentoperator implements the triagent-mcp `agent-operator` MCP server.
// The operator agent (a second claude session per investigation, run by the
// investigate launcher) is the only consumer. Three tools — send_message,
// finish, request_takeover — POST back to the launcher loopback using the
// same telemetry-token pattern as triagent-meta.
package agentoperator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	LauncherURL   string       // TRIAGENT_MCP_TELEMETRY_URL
	LauncherToken string       // TRIAGENT_MCP_TELEMETRY_TOKEN
	TraceID       string       // TRIAGENT_MCP_TRACE_ID (investigation id)
	HTTPClient    *http.Client // optional override; nil → http.DefaultClient
}

type Server struct {
	impl *mcp.Server
	opts Options
}

func New(opts Options) (*Server, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-agent-operator",
		Version: "0.1.0",
	}, nil)
	s := &Server{impl: impl, opts: opts}
	s.register()
	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

const MaxMessageLen = 4096

type sendMessageIn struct {
	Text string `json:"text" jsonschema:"the operator-side message (max 4096 chars). One or two sentences; terse and factual."`
}

type sendMessageOut struct {
	OK bool `json:"ok"`
}

func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "send_message",
		Description: "Speak as the operator. The investigation agent's next turn will include `text` as a user follow-up. Use one or two sentences; the operator's voice is terse and factual.",
	}, telemetry.Wrap("send_message", s.handleSendMessage))
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "finish",
		Description: "End auto mode for this investigation. Terminal — future wake-ups are no-ops. Use only after the capture flow has settled and the investigation agent has stopped emitting. Provide a one-sentence reason for the activity log.",
	}, telemetry.Wrap("finish", s.handleFinish))
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "request_takeover",
		Description: "Yield control back to a human. Use when the situation is outside your competence — operator-side decisions with operational consequences, customer-specific context you don't have, repeated vague answers. The human sees your reason as a pink chat note.",
	}, telemetry.Wrap("request_takeover", s.handleRequestTakeover))
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "approve_proposal",
		Description: "Approve a wiki or playbook draft the investigation agent staged. Use only when the capture decision routes to wiki/playbook AND the corresponding draft envelope has landed in the transcript (you'll see its proposal_id in a `propose_wiki_draft` or `playbook_proposal_draft` tool result). Pass kind=\"wiki\" or kind=\"playbook\" and the proposal_id from that envelope.",
	}, telemetry.Wrap("approve_proposal", s.handleApproveProposal))
}

func (s *Server) handleSendMessage(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageIn) (*mcp.CallToolResult, sendMessageOut, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return errorResult("text cannot be empty"), sendMessageOut{}, nil
	}
	if len(text) > MaxMessageLen {
		return errorResult(fmt.Sprintf("text too long (got %d, max %d)", len(text), MaxMessageLen)), sendMessageOut{}, nil
	}
	if err := s.post(ctx, "send-message", map[string]string{"text": text}); err != nil {
		return errorResult(err.Error()), sendMessageOut{}, nil
	}
	return nil, sendMessageOut{OK: true}, nil
}

type finishIn struct {
	Reason string `json:"reason" jsonschema:"one-sentence reason. Becomes part of the activity log."`
}
type finishOut struct {
	OK      bool `json:"ok"`
	Already bool `json:"already,omitempty"`
}

func (s *Server) handleFinish(ctx context.Context, _ *mcp.CallToolRequest, in finishIn) (*mcp.CallToolResult, finishOut, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return errorResult("reason cannot be empty — say why auto mode is ending"), finishOut{}, nil
	}
	if err := s.post(ctx, "finish", map[string]string{"reason": reason}); err != nil {
		return errorResult(err.Error()), finishOut{}, nil
	}
	return nil, finishOut{OK: true}, nil
}

type requestTakeoverIn struct {
	Reason string `json:"reason" jsonschema:"why you're yielding. State the specific decision the human needs to make."`
}
type requestTakeoverOut struct {
	OK bool `json:"ok"`
}

func (s *Server) handleRequestTakeover(ctx context.Context, _ *mcp.CallToolRequest, in requestTakeoverIn) (*mcp.CallToolResult, requestTakeoverOut, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return errorResult("reason cannot be empty — state why a human is needed"), requestTakeoverOut{}, nil
	}
	if err := s.post(ctx, "request-takeover", map[string]string{"reason": reason}); err != nil {
		return errorResult(err.Error()), requestTakeoverOut{}, nil
	}
	return nil, requestTakeoverOut{OK: true}, nil
}

type approveProposalIn struct {
	Kind       string `json:"kind" jsonschema:"\"wiki\" or \"playbook\""`
	ProposalID string `json:"proposal_id" jsonschema:"the proposal id from the most recent proposal envelope (e.g. \"prop-42ec5c16c183\")"`
}

type approveProposalOut struct {
	OK bool `json:"ok"`
}

func (s *Server) handleApproveProposal(ctx context.Context, _ *mcp.CallToolRequest, in approveProposalIn) (*mcp.CallToolResult, approveProposalOut, error) {
	kind := strings.TrimSpace(in.Kind)
	id := strings.TrimSpace(in.ProposalID)
	if kind != "wiki" && kind != "playbook" {
		return errorResult(`kind must be "wiki" or "playbook"`), approveProposalOut{}, nil
	}
	if id == "" {
		return errorResult("proposal_id cannot be empty"), approveProposalOut{}, nil
	}
	if err := s.post(ctx, "approve-proposal", map[string]string{"kind": kind, "proposal_id": id}); err != nil {
		return errorResult(err.Error()), approveProposalOut{}, nil
	}
	return nil, approveProposalOut{OK: true}, nil
}

// post is shared by all three operator tools. Path is the trailing segment
// under /api/internal/investigations/<id>/auto/<path>.
func (s *Server) post(ctx context.Context, path string, body any) error {
	if s.opts.LauncherURL == "" || s.opts.TraceID == "" {
		return fmt.Errorf("launcher unreachable: agent-operator MCP requires TRIAGENT_MCP_TELEMETRY_URL and TRIAGENT_MCP_TRACE_ID")
	}
	base, err := launcherOrigin(s.opts.LauncherURL)
	if err != nil {
		return fmt.Errorf("parse launcher URL: %w", err)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	url := base + "/api/internal/investigations/" + s.opts.TraceID + "/auto/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.opts.LauncherToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.LauncherToken)
	}
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("call launcher: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body2, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("launcher returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body2)))
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func launcherOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL missing scheme or host: %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
