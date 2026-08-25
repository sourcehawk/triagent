package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

type proposeDraftInput struct {
	MetadataPath string `json:"metadata_path"`
	EventsPath   string `json:"events_path"`
	ProposalID   string `json:"proposal_id"`
}

type proposeDraftOutput struct {
	ProposalID   string `json:"proposal_id"`
	ProposalPath string `json:"proposal_path"`
}

func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "propose_session_draft",
		Description: "Drafts a session.md for an archived investigation session. The launcher overlays identity-bearing frontmatter fields after the draft returns.",
	}, telemetry.Wrap("propose_session_draft", s.proposeSessionDraft))
}

func (s *Server) proposeSessionDraft(ctx context.Context, _ *mcp.CallToolRequest, in proposeDraftInput) (*mcp.CallToolResult, proposeDraftOutput, error) {
	parentToolID := telemetry.CurrentToolID(ctx)
	out, err := s.proposeDraftInternal(ctx, in, parentToolID)
	if err != nil {
		return errorResult(err.Error()), proposeDraftOutput{}, nil
	}
	bytes, _ := json.Marshal(out)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(bytes)}},
	}, *out, nil
}

// proposeDraftInternal is the testable core: validates inputs, runs the
// sub-agent, checks that the output file was written, and returns the result.
func (s *Server) proposeDraftInternal(ctx context.Context, in proposeDraftInput, parentToolID string) (*proposeDraftOutput, error) {
	if in.MetadataPath == "" || in.EventsPath == "" || in.ProposalID == "" {
		return nil, fmt.Errorf("metadata_path, events_path, proposal_id all required")
	}
	if _, err := os.Stat(in.MetadataPath); err != nil {
		return nil, fmt.Errorf("metadata_path: %w", err)
	}
	if _, err := os.Stat(in.EventsPath); err != nil {
		return nil, fmt.Errorf("events_path: %w", err)
	}
	if err := os.MkdirAll(s.proposalsPath, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir proposals: %w", err)
	}
	outPath := filepath.Join(s.proposalsPath, in.ProposalID+".md")

	prompt := buildDraftPrompt(outPath, in.MetadataPath, in.EventsPath)
	if _, err := s.runSubAgent(ctx, prompt, parentToolID); err != nil {
		return nil, fmt.Errorf("sub-agent: %w", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		return nil, fmt.Errorf("sub-agent did not write %s", outPath)
	}
	return &proposeDraftOutput{
		ProposalID:   in.ProposalID,
		ProposalPath: outPath,
	}, nil
}
