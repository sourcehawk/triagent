package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodefixProposal_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := CodefixProposalPayload{
		ProposalID:  "prop-codefix-aaaaaaaa",
		Repo:        "example-org/example-service",
		IssueURL:    "https://github.com/example-org/example-service/issues/42",
		IssueNumber: 42,
		PRURL:       "https://github.com/example-org/example-service/pull/77",
		PRNumber:    77,
		BranchName:  "triagent-proposal/42-aaaaaaaa",
		Summary:     "Tightened the timeout in foo.go.",
	}
	require.NoError(t, writeCodefixProposal(dir, p))
	got, err := readCodefixProposal(dir, p.ProposalID)
	require.NoError(t, err)
	require.Equal(t, p, got)
}

func TestCodefixProposal_ReadNotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := readCodefixProposal(dir, "prop-missing-xxxxxxxx")
	require.Error(t, err)
}

func TestCodefixProposal_DiscardLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeCodefixDiscardLedger(dir, "prop-x", codefixDiscardLedger{
		ProposalID:  "prop-x",
		DiscardedAt: "2026-05-10T20:00:00Z",
	}))
	led, ok, err := readCodefixDiscardLedger(dir, "prop-x")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "prop-x", led.ProposalID)
	require.Equal(t, "2026-05-10T20:00:00Z", led.DiscardedAt)

	_, ok, err = readCodefixDiscardLedger(dir, "prop-not-there")
	require.NoError(t, err)
	require.False(t, ok, "non-existent ledger entry should return ok=false, no error")
}

func TestCodefixProposal_WriteCreatesDir(t *testing.T) {
	// dir doesn't exist yet — writer should MkdirAll.
	t.Parallel()
	dir := t.TempDir() + "/nested/codefix-proposals"
	p := CodefixProposalPayload{ProposalID: "prop-y"}
	require.NoError(t, writeCodefixProposal(dir, p))
}
