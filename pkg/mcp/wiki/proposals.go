package wiki

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewProposalID returns a fresh "prop-<12 hex>" identifier. 12 hex
// characters = 48 bits — collisions are not a concern for human-driven
// approval flow throughput.
func NewProposalID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on Linux; fall back to a constant
		// rather than panicking so a tool call still returns
		// something the agent can act on.
		return "prop-000000000000"
	}
	return "prop-" + hex.EncodeToString(b[:])
}

// proposalFilename builds the on-disk filename for a proposal.
// Format: <proposal_id>__<slug>.md so we can find a draft from either
// identifier.
func proposalFilename(proposalID, slug string) string {
	return proposalID + "__" + slug + ".md"
}

// WriteProposal writes content to the proposals dir, creating the dir
// if needed. Returns the absolute path of the written file.
func WriteProposal(dir, proposalID, slug string, content []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir proposals dir: %w", err)
	}
	full := filepath.Join(dir, proposalFilename(proposalID, slug))
	if err := os.WriteFile(full, content, 0o600); err != nil {
		return "", fmt.Errorf("write proposal %q: %w", proposalID, err)
	}
	return full, nil
}

// ReadProposal locates a proposal file by proposal id (the suffix encodes
// the slug, returned for convenience). Returns os.ErrNotExist when no
// draft matches.
func ReadProposal(dir, proposalID string) (content []byte, slug string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	prefix := proposalID + "__"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug = strings.TrimSuffix(strings.TrimPrefix(e.Name(), prefix), ".md")
		// Skip entity stub files — they have the pattern __entity__<type>__<name>
		if strings.HasPrefix(slug, "entity__") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, "", err
		}
		return raw, slug, nil
	}
	return nil, "", os.ErrNotExist
}

// DeleteProposal removes a proposal file and any sibling entity stub files by
// id. No-op (returns nil) when the files are already gone — both decline and
// successful promote want to delete; idempotent semantics keep the call sites
// simple. Sibling entity stub files have the form
// <proposal_id>__entity__<type>__<name>.md.
func DeleteProposal(dir, proposalID string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prefix := proposalID + "__"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
