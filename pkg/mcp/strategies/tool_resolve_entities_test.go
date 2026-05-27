package strategies

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaybookResolveEntities_ExactAndNear(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("zeebe_oom")
	pb.Services = []string{"zeebe-broker"}
	pb.Errors = []string{"oom-kill"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookResolveEntities(context.Background(), nil, resolveEntitiesIn{
		Services: []string{"zeebe-broker", "Zeebe Broker"},
		Errors:   []string{"oom kill"},
	})
	require.NoError(t, err)
	require.Len(t, out.Resolution, 3, "one resolution per input")

	assert.True(t, out.Resolution[0].Exact, "first input is canonical → exact match")
	assert.False(t, out.Resolution[1].Exact, "fuzzy capitals → not exact")
	require.NotEmpty(t, out.Resolution[1].Near)
	assert.Equal(t, "zeebe-broker", out.Resolution[1].Near[0])

	assert.False(t, out.Resolution[2].Exact, "spaces aren't canonical")
	require.NotEmpty(t, out.Resolution[2].Near)
	assert.Equal(t, "oom-kill", out.Resolution[2].Near[0])
}

func TestPlaybookResolveEntities_EmptyInput(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("x")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	res, out, err := s.playbookResolveEntities(context.Background(), nil, resolveEntitiesIn{})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.NotNil(t, out.Resolution, "[] not null")
	assert.Empty(t, out.Resolution)
}

func TestPlaybookResolveEntities_AcceptsFuzzyInputWithoutValidating(t *testing.T) {
	t.Parallel()
	// Unlike playbook_correlate, this tool MUST accept fuzzy input —
	// "Bad Name" with spaces is not an error here.
	pb := fixturePlaybook("x")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	res, _, err := s.playbookResolveEntities(context.Background(), nil, resolveEntitiesIn{
		Services: []string{"Zeebe Broker"},
	})
	require.NoError(t, err)
	assert.Nil(t, res, "fuzzy input must NOT produce an MCP error")
}

func TestPlaybookResolveEntities_KnownSetIsUnionAcrossLoadedPlaybooks(t *testing.T) {
	t.Parallel()
	// playbook_resolve_entities resolves against the UNION of every
	// loaded playbook's tags — not against a separate registry. So a
	// keyword that matches a tag on ANY playbook in the loaded set
	// should resolve to that canonical name.
	a := fixturePlaybook("a")
	a.Services = []string{"example-service"}
	b := fixturePlaybook("b")
	b.Errors = []string{"crashloopbackoff"}
	s := newServerWithPlaybooks(t, a, b)

	_, out, err := s.playbookResolveEntities(context.Background(), nil, resolveEntitiesIn{
		Services: []string{"example service"},   // → near: example-service (from playbook a)
		Errors:   []string{"crash loop backoff"}, // → near: crashloopbackoff (from playbook b)
	})
	require.NoError(t, err)
	require.Len(t, out.Resolution, 2)
	require.NotEmpty(t, out.Resolution[0].Near, "services resolution")
	assert.Equal(t, "example-service", out.Resolution[0].Near[0])
	require.NotEmpty(t, out.Resolution[1].Near, "errors resolution")
	assert.Equal(t, "crashloopbackoff", out.Resolution[1].Near[0])
}
