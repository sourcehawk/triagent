package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestExtract_IncludesPRProposal confirms the new codefix meta-playbook
// ships with the embedded set and lands on disk after Extract — the
// strategies MCP loader picks it up from the type slot.
func TestExtract_IncludesPRProposal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	typeDir, err := Extract(root)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(typeDir, "pr_proposal.yaml"))
	require.NoError(t, err, "pr_proposal.yaml not extracted")

	var head struct {
		ID            string `yaml:"id"`
		SchemaVersion int    `yaml:"schema_version"`
		Type          string `yaml:"type"`
		Entrypoint    string `yaml:"entrypoint"`
	}
	require.NoError(t, yaml.Unmarshal(body, &head))
	assert.Equal(t, "pr_proposal", head.ID)
	assert.Equal(t, 1, head.SchemaVersion)
	assert.Equal(t, "general", head.Type)
	assert.Equal(t, "classify_entry_mode", head.Entrypoint)

	s := string(body)
	assert.Contains(t, s, "assess_warrantedness")
	assert.Contains(t, s, "identify_affected_repos")
	assert.Contains(t, s, "search_for_duplicates")
	assert.Contains(t, s, "draft_issue")
	assert.Contains(t, s, "implement_fix")
	assert.Contains(t, s, "edit_mode_refine")
	assert.Contains(t, s, "terminal_no_codefix")
	assert.Contains(t, s, "terminal_awaiting_review")
}

// TestExtract_CaptureOfferV3HasBugReportRoute confirms that capture_offer was
// bumped to v3 and that the bug-report terminal is wired alongside the
// existing codefix / all / both terminals (which must remain for backward
// compat with in-flight v1 / v2 sessions).
func TestExtract_CaptureOfferV3HasBugReportRoute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	typeDir, err := Extract(root)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(typeDir, "capture_offer.yaml"))
	require.NoError(t, err)

	var head struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	}
	require.NoError(t, yaml.Unmarshal(body, &head))
	assert.Equal(t, "capture_offer", head.ID)
	assert.Equal(t, "v3", head.Version)

	s := string(body)
	// v2 surfaces — still present.
	assert.Contains(t, s, "terminal_codefix")
	assert.Contains(t, s, "terminal_all")
	assert.Contains(t, s, "pr_proposal")
	// Backward compat: terminal_both still present so in-flight v1
	// walks don't break.
	assert.Contains(t, s, "terminal_both")
	// v3 additions.
	assert.Contains(t, s, "terminal_bug_report")
	assert.Contains(t, s, "bug_report_proposal")
	// Routing keyword must appear in the ask prose so the operator
	// knows it's a valid reply (mirrors the codefix/wiki/playbook
	// keyword check below).
	assert.Contains(t, s, "`bug`",
		"capture_offer ask must mention `bug` as a routing keyword")
}

// TestExtract_CaptureOfferAskNodeRequiresConcreteProposals locks in the
// instruction that the investigation agent must draft concrete capture
// proposals (named entries, specific playbook changes, codefix shape)
// *before* printing the menu. Without this, the agent reverts to the
// bare-menu form and the operator only sees "wiki/playbook/codefix?".
func TestExtract_CaptureOfferAskNodeRequiresConcreteProposals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	typeDir, err := Extract(root)
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(typeDir, "capture_offer.yaml"))
	require.NoError(t, err)
	s := string(body)
	// The ask prose must direct the agent to surface concrete artifacts
	// before printing the menu — and the routing keywords must still
	// land in the operator's view so the v2 matcher keeps working.
	assert.Contains(t, s, "Proposed captures",
		"capture_offer ask must instruct the agent to name concrete proposals (look for the 'Proposed captures' header it should print)")
	assert.Contains(t, s, "distinct shape",
		"capture_offer ask must mention splitting distinct shapes — otherwise the agent collapses two-shape incidents into one wiki entry")
	for _, kw := range []string{"`wiki`", "`playbook`", "`codefix`", "`all`", "`no`"} {
		assert.Contains(t, s, kw,
			"routing keyword %s must still appear in the ask prose so the operator knows it's a valid reply", kw)
	}
}

// TestExtract_IncludesBugReportProposal confirms the bug-report
// meta-playbook ships with the embedded set and lands on disk after
// Extract — the strategies MCP loader picks it up from the type slot.
func TestExtract_IncludesBugReportProposal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	typeDir, err := Extract(root)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(typeDir, "bug_report_proposal.yaml"))
	require.NoError(t, err, "bug_report_proposal.yaml not extracted")

	var head struct {
		ID            string `yaml:"id"`
		SchemaVersion int    `yaml:"schema_version"`
		Type          string `yaml:"type"`
		Entrypoint    string `yaml:"entrypoint"`
	}
	require.NoError(t, yaml.Unmarshal(body, &head))
	assert.Equal(t, "bug_report_proposal", head.ID)
	assert.Equal(t, 1, head.SchemaVersion)
	assert.Equal(t, "general", head.Type)
	assert.Equal(t, "assess_warrantedness", head.Entrypoint)

	s := string(body)
	assert.Contains(t, s, "assess_warrantedness")
	assert.Contains(t, s, "identify_affected_repos")
	assert.Contains(t, s, "search_for_duplicates")
	assert.Contains(t, s, "draft_issue")
	assert.Contains(t, s, "continue_or_stop")
	assert.Contains(t, s, "terminal_filed")
	assert.Contains(t, s, "terminal_no_bug_report")
	// Crucially, no draft_pr step — bug_report stops at issue filing.
	// (The description prose legitimately mentions `draft_pr` to
	// explain what this playbook deliberately doesn't do, so we check
	// for the suggested_calls wiring rather than the literal token.)
	assert.NotContains(t, s, "implement_fix",
		"bug_report_proposal must not include an implement_fix step — drafting the PR is pr_proposal's job")
	assert.NotContains(t, s, "triagent-git-alias/draft_pr",
		"bug_report_proposal must not suggest draft_pr — it stops at create_github_issue")
}

// TestExtract_IncludesBackfillIngestion confirms the new system
// playbook ships with the embedded set and lands on disk after
// Extract — the launcher needs both for the strategies MCP loader to
// find it.
func TestExtract_IncludesBackfillIngestion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	typeDir, err := Extract(root)
	require.NoError(t, err)

	dest := filepath.Join(typeDir, "wiki_backfill_ingestion.yaml")
	body, err := os.ReadFile(dest)
	require.NoError(t, err, "wiki_backfill_ingestion.yaml not extracted")

	var head struct {
		ID            string `yaml:"id"`
		SchemaVersion int    `yaml:"schema_version"`
		Type          string `yaml:"type"`
		Entrypoint    string `yaml:"entrypoint"`
	}
	require.NoError(t, yaml.Unmarshal(body, &head))
	assert.Equal(t, "wiki_backfill_ingestion", head.ID)
	assert.Equal(t, 1, head.SchemaVersion)
	assert.Equal(t, "general", head.Type)
	// verify_scope is the leading guard added when the MCP-wiring
	// gate moved out of the per-step skip predicate and into a single
	// up-front check; falling through to walk_incidentio still happens
	// once a source is confirmed.
	assert.Equal(t, "verify_scope", head.Entrypoint)

	s := string(body)
	assert.Contains(t, s, "verify_scope")
	assert.Contains(t, s, "terminal_no_sources")
	assert.Contains(t, s, "gather_incidentio_context")
	assert.Contains(t, s, "gather_slack_context")
	assert.Contains(t, s, "propose_wiki_draft")
}
