package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// newEntityStub holds the description the sub-agent wrote for a newly
// introduced entity. Used to populate entity stubs with real descriptions
// when the proposal is approved.
type newEntityStub struct {
	Type        string
	Name        string
	Description string
}

// readWikiNewEntityStubs scans the proposals dir for sibling entity stub files
// matching <proposalID>__entity__<type>__<name>.md and returns their parsed
// type, name, and description, plus any per-file parse errors so the approve
// flow can surface them rather than silently producing entities with empty
// Description sections in the vault.
func readWikiNewEntityStubs(proposalsDir, proposalID string) ([]newEntityStub, []string) {
	entries, err := os.ReadDir(proposalsDir)
	if err != nil {
		return nil, nil
	}
	prefix := proposalID + "__entity__"
	var stubs []newEntityStub
	var parseErrs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(e.Name(), prefix), ".md")
		sep := strings.Index(inner, "__")
		if sep < 0 {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: filename missing '__' between type and name", e.Name()))
			continue
		}
		typ := inner[:sep]
		name := inner[sep+2:]
		if typ == "" || name == "" {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: empty type or name segment in filename", e.Name()))
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proposalsDir, e.Name()))
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: read failed: %v", e.Name(), err))
			continue
		}
		desc, err := parseEntityStubDescription(raw)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		stubs = append(stubs, newEntityStub{Type: typ, Name: name, Description: desc})
	}
	return stubs, parseErrs
}

// parseEntityStubDescription extracts the description field from a simple
// frontmatter-only markdown file. Returns an error when the leading '---'
// delimiter is missing or YAML parsing fails — the silent "" fallback the
// previous version returned masked sub-agent malformed output.
func parseEntityStubDescription(raw []byte) (string, error) {
	const sep = "---\n"
	s := string(raw)
	if !strings.HasPrefix(s, sep) {
		return "", fmt.Errorf("missing leading '---' delimiter")
	}
	rest := s[len(sep):]
	end := strings.Index(rest, "\n---")
	fm := rest
	if end >= 0 {
		fm = rest[:end]
	}
	var parsed struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		return "", fmt.Errorf("frontmatter yaml parse: %w", err)
	}
	return strings.TrimSpace(parsed.Description), nil
}

// readWikiDraft reads a wiki proposal markdown file directly from the
// proposals dir. Skips the subprocess hop because the file format is
// stable and set by our own code. Mirrors handlers_proposals.go::readDraftFromDir.
//
// Filename format: <proposal_id>__<slug>.md. Sibling entity-stub files
// share the proposal-id prefix (<proposal_id>__entity__<type>__<name>.md)
// and would alphabetically sort first; skip them so the caller always
// gets the top-level draft. Mirrors mcp/internal/wiki.ReadProposal.
func readWikiDraft(proposalsDir, proposalID string) (slug, body string, err error) {
	entries, err := os.ReadDir(proposalsDir)
	if err != nil {
		return "", "", err
	}
	const sep = "__"
	prefix := proposalID + sep
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		slug = strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md")
		if strings.HasPrefix(slug, "entity"+sep) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proposalsDir, name))
		if err != nil {
			return "", "", err
		}
		return slug, string(raw), nil
	}
	return "", "", fmt.Errorf("no wiki proposal matching %s", proposalID)
}

// declineWikiViaSubprocess shells out to `triagent-mcp wiki-delete-proposal <id>`.
// Idempotent: an already-deleted proposal returns ok.
func declineWikiViaSubprocess(ctx context.Context, mcpBin, proposals, proposalID string) error {
	if mcpBin == "" {
		return fmt.Errorf("triagent-mcp binary path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, mcpBin, "wiki-delete-proposal", proposalID,
		"--proposals", proposals)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run wiki-delete-proposal: %w (stderr: %s, stdout: %s)", err, stderr.String(), stdout.String())
	}
	return nil
}

// resolveWikiProposalsPath returns the operator-configured proposals
// path. Always sourced from the profile's paths.wiki_proposals_dir
// (plumbed via Options.WikiProposalsPath); empty means the wiki feature
// has no proposals dir wired and the caller should treat it as
// disabled.
func resolveWikiProposalsPath(opts string) string {
	return opts
}

// resolveCodefixProposalsPath: same shape as resolveWikiProposalsPath
// — the profile's paths.codefix_proposals_dir flows through Options
// without further fallbacks.
func resolveCodefixProposalsPath(opts string) string {
	return opts
}
