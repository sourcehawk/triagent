package strategies

import (
	"context"
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
