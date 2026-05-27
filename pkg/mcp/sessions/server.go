// Package sessions exposes the triagent-mcp --kind sessions MCP server. v1 only
// has propose_session_draft, used by the launcher backend to AI-draft a
// session.md before pushing the session to the upstream sessions repo.
package sessions

import (
	"context"
	"fmt"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the sessions MCP server.
type Options struct {
	// ProposalsPath is where draft session.md files land. Required.
	ProposalsPath string
	// ClaudeBinary path. Empty falls back to `claude` on $PATH.
	ClaudeBinary string
	// SubAgentTimeout caps the drafter sub-agent. 0 → subagent.Run default.
	SubAgentTimeout time.Duration
}

// Server holds the MCP server and its configuration.
type Server struct {
	impl            *mcp.Server
	proposalsPath   string
	claudeBin       string
	subAgentTimeout time.Duration

	// runSubAgent is the seam for testing. Production wires it to
	// subagent.Run; tests replace it with a stub.
	runSubAgent func(ctx context.Context, prompt, parentToolID string) (string, error)
}

// New constructs a Server.
func New(opts Options) (*Server, error) {
	if opts.ProposalsPath == "" {
		return nil, fmt.Errorf("ProposalsPath is required")
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-sessions",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:            impl,
		proposalsPath:   opts.ProposalsPath,
		claudeBin:       opts.ClaudeBinary,
		subAgentTimeout: opts.SubAgentTimeout,
	}
	s.runSubAgent = func(ctx context.Context, prompt, parentToolID string) (string, error) {
		// Match RunOneShotDraft's options: broader tool whitelist (the
		// agent benefits from Glob/Grep when scanning a long
		// transcript), WorkingDir at the proposals dir, and a
		// 5-minute timeout that fits a thousand-line events.jsonl.
		// SubAgentTimeout overrides if explicitly set.
		timeout := s.subAgentTimeout
		if timeout == 0 {
			timeout = 5 * time.Minute
		}
		res, err := subagent.Run(ctx, subagent.Options{
			ClaudeBinary: s.claudeBin,
			WorkingDir:   s.proposalsPath,
			AllowedTools: "Read,Glob,Grep,Write,Edit",
			Prompt:       prompt,
			Timeout:      timeout,
			ParentToolID: parentToolID,
		})
		if err != nil {
			return res.Summary, err
		}
		return res.Summary, nil
	}
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
