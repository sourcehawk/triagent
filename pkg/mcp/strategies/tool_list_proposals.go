package strategies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProposalSummary is one row of the list_proposals output. Captures the
// state a dispatched sub-agent needs to decide whether to draft, refine,
// or back off: status (awaiting_review | approved | declined), the
// linkage back to the playbook id + type slot, when it happened, and
// any operator note attached to a decline.
type ProposalSummary struct {
	ProposalID string `json:"proposal_id"`
	PlaybookID string `json:"playbook_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Status     string `json:"status"`
	At         string `json:"at,omitempty"`
	Note       string `json:"note,omitempty"`
	Activated  bool   `json:"activated,omitempty"`
}

// resolvedMarker mirrors the on-disk shape written by the launcher's
// proposalResolution struct (internal/server). Re-declared here because
// strategies is a public package and cannot import internal/server; the
// on-disk JSON is the contract.
type resolvedMarker struct {
	Outcome   string `json:"outcome"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Activated bool   `json:"activated,omitempty"`
	At        string `json:"at"`
	Note      string `json:"note,omitempty"`
}

// ListProposals enumerates pending drafts under <dir>/proposals/<type>/
// and resolved markers under <dir>/proposals/.resolved/ and returns one
// ProposalSummary per known proposal id, sorted by At descending so
// callers see the most recent state first. Missing or empty
// proposals dir → empty slice, no error (fresh launcher case).
func ListProposals(dir string) ([]ProposalSummary, error) {
	if dir == "" {
		return []ProposalSummary{}, nil
	}
	out := []ProposalSummary{}
	proposalsRoot := filepath.Join(dir, ProposalsSubdir)

	// Pending drafts under each type subdir.
	typeDirs, err := os.ReadDir(proposalsRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read proposals dir: %w", err)
	}
	for _, td := range typeDirs {
		if !td.IsDir() || strings.HasPrefix(td.Name(), ".") {
			continue
		}
		typeName := td.Name()
		entries, err := os.ReadDir(filepath.Join(proposalsRoot, typeName))
		if err != nil {
			continue
		}
		for _, f := range entries {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			base := strings.TrimSuffix(f.Name(), ".yaml")
			parts := strings.SplitN(base, "__", 2)
			if len(parts) != 2 {
				continue
			}
			at := ""
			if info, err := f.Info(); err == nil {
				at = info.ModTime().UTC().Format(time.RFC3339)
			}
			out = append(out, ProposalSummary{
				ProposalID: parts[1],
				PlaybookID: parts[0],
				Type:       typeName,
				Status:     "awaiting_review",
				At:         at,
			})
		}
	}

	// Resolved markers — approved / declined.
	resolvedDir := filepath.Join(proposalsRoot, ".resolved")
	resolvedFiles, err := os.ReadDir(resolvedDir)
	if err == nil {
		for _, f := range resolvedFiles {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			proposalID := strings.TrimSuffix(f.Name(), ".json")
			body, err := os.ReadFile(filepath.Join(resolvedDir, f.Name()))
			if err != nil {
				continue
			}
			var m resolvedMarker
			if err := json.Unmarshal(body, &m); err != nil {
				continue
			}
			out = append(out, ProposalSummary{
				ProposalID: proposalID,
				PlaybookID: m.ID,
				Type:       m.Type,
				Status:     m.Outcome,
				At:         m.At,
				Note:       m.Note,
				Activated:  m.Activated,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At > out[j].At
		}
		return out[i].ProposalID < out[j].ProposalID
	})
	return out, nil
}

// ── list_proposals MCP tool ───────────────────────────────────────────

type listProposalsIn struct {
	PlaybookID string `json:"playbook_id,omitempty" jsonschema:"optional — restrict to proposals targeting this playbook id"`
	Status     string `json:"status,omitempty" jsonschema:"optional — restrict to one of 'awaiting_review', 'approved', 'declined'"`
}

type listProposalsOut struct {
	Proposals []ProposalSummary `json:"proposals"`
}

// listProposals reads the launcher's proposal store and surfaces the
// state of every known proposal id. Dispatched sub-agents (especially
// playbook_proposal) call this before re-submitting so they don't
// re-propose a shape the operator just declined.
func (s *Server) listProposals(ctx context.Context, req *mcp.CallToolRequest, in listProposalsIn) (*mcp.CallToolResult, listProposalsOut, error) {
	if s.userPlaybooksDir == "" {
		return errorResult("list_proposals is unavailable — strategies MCP started without a user playbooks dir"), listProposalsOut{}, nil
	}
	all, err := ListProposals(s.userPlaybooksDir)
	if err != nil {
		return errorResult(fmt.Sprintf("list proposals: %v", err)), listProposalsOut{}, nil
	}
	out := make([]ProposalSummary, 0, len(all))
	for _, p := range all {
		if in.PlaybookID != "" && p.PlaybookID != in.PlaybookID {
			continue
		}
		if in.Status != "" && p.Status != in.Status {
			continue
		}
		out = append(out, p)
	}
	return nil, listProposalsOut{Proposals: out}, nil
}
