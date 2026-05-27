package strategies

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixturePlaybook builds a Playbook with sensible defaults for use in
// correlate tests. Override fields by mutating the returned pointer.
func fixturePlaybook(id string) *Playbook {
	return &Playbook{
		ID:         id,
		Symptom:    "test symptom for " + id,
		Entrypoint: "start",
		Nodes: map[string]Node{
			"start": {ID: "start", Description: "d", TerminalAdvice: "done"},
		},
	}
}

func newServerWithPlaybooks(t *testing.T, books ...*Playbook) *Server {
	t.Helper()
	m := make(map[string]*Playbook, len(books))
	for _, b := range books {
		m[b.ID] = b
	}
	return &Server{playbooks: m}
}

func TestPlaybookCorrelate_DirectMatch(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("zeebe_oom")
	pb.Services = []string{"zeebe-broker"}
	pb.Errors = []string{"oom-kill"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 1, "expected one match")
	m := out.Correlations[0]
	assert.Equal(t, "zeebe_oom", m.ID)
	assert.Equal(t, 3, m.Score, "direct hit = 3")
	assert.Equal(t, []string{"zeebe-broker"}, m.MatchPath.Direct)
	assert.Empty(t, m.MatchPath.Lifted)
}

func TestPlaybookCorrelate_LiftedViaDelegateTo(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("elasticsearch")
	child.Errors = []string{"shard-failure"}

	parent := fixturePlaybook("cluster_health")
	parent.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", DelegateTo: "elasticsearch", Next: []Branch{{Condition: "always", Goto: "done"}}},
		"done":  {ID: "done", Description: "d", TerminalAdvice: "ok"},
	}
	parent.Entrypoint = "start"

	s := newServerWithPlaybooks(t, parent, child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Errors: []string{"shard-failure"},
	})
	require.NoError(t, err)

	byID := map[string]playbookCorrelateMatch{}
	for _, m := range out.Correlations {
		byID[m.ID] = m
	}
	require.Contains(t, byID, "elasticsearch", "child should match directly")
	require.Contains(t, byID, "cluster_health", "parent should match via lift")
	assert.Equal(t, 3, byID["elasticsearch"].Score, "direct hit")
	assert.Equal(t, 1, byID["cluster_health"].Score, "lifted hit")
	require.Len(t, byID["cluster_health"].MatchPath.Lifted, 1)
	assert.Equal(t, "shard-failure", byID["cluster_health"].MatchPath.Lifted[0].Entity)
	assert.Equal(t, "elasticsearch", byID["cluster_health"].MatchPath.Lifted[0].Via)
}

func TestPlaybookCorrelate_LiftedViaHandoff(t *testing.T) {
	t.Parallel()
	target := fixturePlaybook("network")
	target.Symptoms = []string{"connectivity-loss"}

	source := fixturePlaybook("triage")
	source.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "hand off", Handoff: []string{"network"}},
	}
	source.Entrypoint = "start"

	s := newServerWithPlaybooks(t, source, target)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Symptoms: []string{"connectivity-loss"},
	})
	require.NoError(t, err)

	byID := map[string]playbookCorrelateMatch{}
	for _, m := range out.Correlations {
		byID[m.ID] = m
	}
	require.Contains(t, byID, "triage", "handoff source should lift the target's tags")
	assert.Equal(t, 1, byID["triage"].Score)
	require.Len(t, byID["triage"].MatchPath.Lifted, 1)
	assert.Equal(t, "network", byID["triage"].MatchPath.Lifted[0].Via)
}

func TestPlaybookCorrelate_DirectBeatsLifted(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("child")
	child.Services = []string{"zeebe-broker"}

	parent := fixturePlaybook("parent")
	parent.Services = []string{"zeebe-broker"} // also directly tagged
	parent.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", DelegateTo: "child", Next: []Branch{{Condition: "always", Goto: "done"}}},
		"done":  {ID: "done", Description: "d", TerminalAdvice: "ok"},
	}
	parent.Entrypoint = "start"

	s := newServerWithPlaybooks(t, parent, child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)

	var parentMatch *playbookCorrelateMatch
	for i := range out.Correlations {
		if out.Correlations[i].ID == "parent" {
			parentMatch = &out.Correlations[i]
		}
	}
	require.NotNil(t, parentMatch)
	assert.Equal(t, 3, parentMatch.Score, "direct hit wins; entity not double-counted as lifted")
	assert.Equal(t, []string{"zeebe-broker"}, parentMatch.MatchPath.Direct)
	assert.Empty(t, parentMatch.MatchPath.Lifted)
}

func TestPlaybookCorrelate_ManyParentsLiftSameChildOnce(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("conn")
	child.Symptoms = []string{"connectivity-loss"}

	mkParent := func(id string) *Playbook {
		p := fixturePlaybook(id)
		p.Nodes = map[string]Node{
			"start": {ID: "start", Description: "d", DelegateTo: "conn", Next: []Branch{{Condition: "always", Goto: "done"}}},
			"done":  {ID: "done", Description: "d", TerminalAdvice: "ok"},
		}
		p.Entrypoint = "start"
		return p
	}
	s := newServerWithPlaybooks(t, mkParent("alpha"), mkParent("beta"), mkParent("gamma"), child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Symptoms: []string{"connectivity-loss"},
	})
	require.NoError(t, err)

	scoreByID := map[string]int{}
	for _, m := range out.Correlations {
		scoreByID[m.ID] = m.Score
	}
	assert.Equal(t, 3, scoreByID["conn"], "child direct")
	assert.Equal(t, 1, scoreByID["alpha"], "lift counted once per parent")
	assert.Equal(t, 1, scoreByID["beta"])
	assert.Equal(t, 1, scoreByID["gamma"])
}

func TestPlaybookCorrelate_CycleSafe(t *testing.T) {
	t.Parallel()
	// A → B → A cycle through handoff. Lifting must terminate.
	a := fixturePlaybook("a")
	a.Services = []string{"svc-a"}
	a.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "go b", Handoff: []string{"b"}},
	}
	a.Entrypoint = "start"

	b := fixturePlaybook("b")
	b.Services = []string{"svc-b"}
	b.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "go a", Handoff: []string{"a"}},
	}
	b.Entrypoint = "start"

	s := newServerWithPlaybooks(t, a, b)

	_, _, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"svc-a"},
	})
	require.NoError(t, err, "cycle must not infinite-loop")
}

func TestPlaybookCorrelate_TypeFilter(t *testing.T) {
	t.Parallel()
	investigationPB := fixturePlaybook("inv")
	investigationPB.Type = "investigation"
	investigationPB.Services = []string{"zeebe-broker"}

	generalPB := fixturePlaybook("gen")
	generalPB.Type = "general"
	generalPB.Services = []string{"zeebe-broker"}

	s := newServerWithPlaybooks(t, investigationPB, generalPB)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
		Type:     "investigation",
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 1, "type filter should drop non-matching")
	assert.Equal(t, "inv", out.Correlations[0].ID)
}

func TestPlaybookCorrelate_Limit(t *testing.T) {
	t.Parallel()
	books := make([]*Playbook, 0, 10)
	for i := 0; i < 10; i++ {
		p := fixturePlaybook(string(rune('a' + i)))
		p.Services = []string{"zeebe-broker"}
		books = append(books, p)
	}
	s := newServerWithPlaybooks(t, books...)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
		Limit:    3,
	})
	require.NoError(t, err)
	assert.Len(t, out.Correlations, 3)
}

func TestPlaybookCorrelate_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("anything")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{})
	require.NoError(t, err)
	assert.NotNil(t, out.Correlations, "must be [] not null")
	assert.Empty(t, out.Correlations)
}

func TestPlaybookCorrelate_MalformedInputErrors(t *testing.T) {
	t.Parallel()
	s := newServerWithPlaybooks(t)
	res, _, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"Bad Name"},
	})
	require.NoError(t, err, "tool-level error, not transport error")
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestPlaybookCorrelate_SurfacesNearMatches(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("anything")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"broker"}, // near match: should resolve to zeebe-broker
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.Resolution, "near match should surface in Resolution")
	r := out.Resolution[0]
	assert.False(t, r.Exact)
	assert.Equal(t, "broker", r.Input)
	require.NotEmpty(t, r.Near)
	assert.Equal(t, "zeebe-broker", r.Near[0])
}

func TestPlaybookCorrelate_NextGotoDoesNotLift(t *testing.T) {
	t.Parallel()
	// `parent` references `sibling` only via next.goto, NOT via delegate_to
	// or handoff. Since next.goto is intra-playbook (the validator rejects
	// cross-playbook gotos), it must NOT contribute to lifting — even if
	// the "sibling" id happens to match a loaded playbook id.
	//
	// We construct a parent with a node `start` whose `next.goto` names
	// "sibling" (this would normally fail validateOne, but we bypass the
	// validator here by populating playbooks directly via newServerWithPlaybooks
	// — the invariant we're guarding is in the lifting code, not the loader).
	sibling := fixturePlaybook("sibling")
	sibling.Services = []string{"side-svc"}

	parent := fixturePlaybook("parent_with_next_only")
	parent.Nodes = map[string]Node{
		"start": {
			ID:          "start",
			Description: "d",
			Next:        []Branch{{Condition: "always", Goto: "sibling"}},
		},
		"sibling": {
			ID:             "sibling",
			Description:    "d",
			TerminalAdvice: "done",
		},
	}
	parent.Entrypoint = "start"

	s := newServerWithPlaybooks(t, parent, sibling)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"side-svc"},
	})
	require.NoError(t, err)

	byID := map[string]playbookCorrelateMatch{}
	for _, m := range out.Correlations {
		byID[m.ID] = m
	}
	// sibling is in the loaded set and directly tagged — should match.
	require.Contains(t, byID, "sibling", "sibling should match directly")
	assert.Equal(t, 3, byID["sibling"].Score)
	// parent must NOT match — its only reference to sibling is via next.goto,
	// which is not a lift source. If it appears, lifting is over-eagerly
	// walking node.Next.
	assert.NotContains(t, byID, "parent_with_next_only", "next.goto must not contribute to lifting")
}

func TestPlaybookCorrelate_DeterministicTieBreak(t *testing.T) {
	t.Parallel()
	a := fixturePlaybook("alpha")
	a.Services = []string{"zeebe-broker"}
	b := fixturePlaybook("beta")
	b.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, b, a) // construction order shouldn't matter

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 2)

	// Both have score 3 → tie-break alphabetical
	ids := []string{out.Correlations[0].ID, out.Correlations[1].ID}
	sortedIDs := make([]string, len(ids))
	copy(sortedIDs, ids)
	sort.Strings(sortedIDs)
	assert.Equal(t, sortedIDs, ids, "ties break alphabetical by id")
}
