package strategies

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newServerWithUserPlaybooksDir returns a server with a writable userPlaybooksDir
// so the proposal handler can persist the drafted YAML. Reuses newEmptyServer
// from server_step_complete_test.go.
func newServerWithUserPlaybooksDir(t *testing.T) *Server {
	t.Helper()
	srv := newEmptyServer(t)
	srv.userPlaybooksDir = t.TempDir()
	return srv
}

func TestDeclineProposal_RequiresReason(t *testing.T) {
	t.Parallel()
	srv := newEmptyServer(t)
	res, _, err := srv.declineProposal(context.Background(), nil, declineProposalIn{Reason: "  "})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "an empty reason is an error — the decline must say why")
}

func TestDeclineProposal_AcknowledgesWithReason(t *testing.T) {
	t.Parallel()
	srv := newEmptyServer(t)
	res, out, err := srv.declineProposal(context.Background(), nil, declineProposalIn{
		Reason: "investigation was routine — below the novelty bar",
	})
	require.NoError(t, err)
	assert.Nil(t, res, "a valid decline is not an error result")
	assert.True(t, out.Acknowledged)
	assert.Contains(t, out.Message, "below the novelty bar", "the reason is echoed back so it's auditable")
}

func TestProposePlaybookDraft_ReturnsValidationErrorsInsteadOfErrorResult(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	ctx := context.Background()
	// Malformed YAML: missing entrypoint.
	_, out, err := srv.proposePlaybookDraft(ctx, nil, proposePlaybookDraftIn{
		YAML: "id: x\nschema_version: 1\nsymptom: foo\nnodes:\n  a:\n    description: a\n",
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.ValidationErrors, "validation errors must surface in the response, not as an error")
	assert.Empty(t, out.ProposalID, "no proposal_id when validation failed")
}

func TestProposePlaybookDraft_ValidYAMLProducesProposalNoValidationErrors(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	ctx := context.Background()
	yaml := `id: testpb
schema_version: 1
symptom: test
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	_, out, err := srv.proposePlaybookDraft(ctx, nil, proposePlaybookDraftIn{
		YAML: yaml,
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	assert.Empty(t, out.ValidationErrors)
	assert.NotEmpty(t, out.ProposalID)
}

func TestProposePlaybookDraft_ResultOmitsYAMLBodies(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	yaml := `id: testpb
schema_version: 1
symptom: test
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	_, out, err := srv.proposePlaybookDraft(context.Background(), nil, proposePlaybookDraftIn{
		YAML: yaml,
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.ProposalID)

	// The tool result lands in the model's context. A real playbook is
	// tens of KB per side, and inlining both diff bodies pushed the result
	// past the CLI's per-result cap, which replaces the JSON with an error
	// string the chat UI can't parse. The launcher serves the bodies via
	// GET /api/playbook-proposals/{id}; the result only identifies the draft.
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var keys map[string]any
	require.NoError(t, json.Unmarshal(raw, &keys))
	assert.NotContains(t, keys, "new_yaml")
	assert.NotContains(t, keys, "base_yaml")
	assert.Equal(t, "testpb", keys["playbook_id"])
	assert.Equal(t, "investigation", keys["type"])
}

// TestProposePlaybookDraft_RejectsTypeMismatchForExistingID pins the
// type slot of an existing id: a revision filed under a different
// type would land as a second file next to the original on approve,
// which the loader refuses to load at all.
func TestProposePlaybookDraft_RejectsTypeMismatchForExistingID(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	srv.playbooks["release_checks"] = &Playbook{
		ID:         "release_checks",
		Symptom:    "existing",
		Entrypoint: "a",
		Nodes:      map[string]Node{"a": {Description: "a", TerminalAdvice: "done"}},
		Type:       "verification",
	}
	yaml := `id: release_checks
schema_version: 1
symptom: revised
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	res, out, err := srv.proposePlaybookDraft(context.Background(), nil, proposePlaybookDraftIn{
		YAML: yaml,
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "a revision must stay in the type slot the id already lives in")
	assert.Contains(t, textOf(res), "verification", "the error names the slot the id lives in")
	assert.Empty(t, out.ProposalID)

	res, out, err = srv.proposePlaybookDraft(context.Background(), nil, proposePlaybookDraftIn{
		YAML: yaml,
		Type: "verification",
		Why:  "test",
	})
	require.NoError(t, err)
	assert.Nil(t, res, "the matching slot is accepted")
	assert.NotEmpty(t, out.ProposalID)
}

// TestGetPlaybookRaw_ReportsTypeAndPrefersUserOverride: the agent
// bases a revision on what get_playbook_raw returns, so it must be
// the active (user-overridden) copy, and it must carry the type slot
// the revision has to be filed under.
func TestGetPlaybookRaw_ReportsTypeAndPrefersUserOverride(t *testing.T) {
	t.Parallel()
	const body = `id: release_checks
schema_version: 1
symptom: %s
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	pluginDir := t.TempDir()
	userDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "verification"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(userDir, "verification"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "verification", "release_checks.yaml"), []byte(fmt.Sprintf(body, "upstream copy")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "verification", "release_checks.yaml"), []byte(fmt.Sprintf(body, "local override")), 0o644))

	srv, err := New(Options{PluginPlaybooksDir: pluginDir, UserPlaybooksDir: userDir})
	require.NoError(t, err)
	res, out, err := srv.getPlaybookRaw(context.Background(), nil, getPlaybookRawIn{ID: "release_checks"})
	require.NoError(t, err)
	require.Nil(t, res)
	assert.Equal(t, "verification", out.Type)
	assert.Equal(t, "user", out.Source)
	assert.Contains(t, out.YAML, "local override")
	assert.NotContains(t, out.YAML, "upstream copy")
}
