package strategies

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localTestPlaybooksDir is the on-disk path tests use as the
// "system" set — a small testdata fixture maintained alongside
// these tests. The upstream playbooks live in a separate repo
// (an external repo); we don't depend on that
// clone in unit tests.
const localTestPlaybooksDir = "testdata/playbooks"

// newServerWithFixtures constructs a *Server with the local test playbooks.
func newServerWithFixtures(t *testing.T) *Server {
	t.Helper()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")
	return srv
}

// TestLoadPlaybooks_EmbeddedAreValid asserts every shipped YAML loads cleanly
// and references its own entrypoint. If any branch's `goto` points at a
// non-existent node id, the next call (advance) catches it — but here we just
// guarantee the playbooks are well-formed enough to start investigations.
func TestLoadPlaybooks_LocalSeedAreValid(t *testing.T) {
	t.Parallel()
	// "playbooks" is the local seed for the upstream c1-investigation-playbooks
	// repo — kept in the source tree so this test can run without a network
	// dependency. Once the upstream repo becomes the source of truth and the
	// seed is deleted, swap this for a fixture under testdata/.
	books, err := LoadPlaybooks(localTestPlaybooksDir)
	require.NoError(t, err, "LoadPlaybooks")
	require.NotEmpty(t, books, "expected at least one playbook in the local seed")
	for id, pb := range books {
		assert.Equal(t, id, pb.ID, "playbook %q has mismatched ID field", id)
		assert.NotEmpty(t, pb.Symptom, "playbook %q has empty symptom", id)
		_, ok := pb.Nodes[pb.Entrypoint]
		assert.True(t, ok, "playbook %q entrypoint %q not in Nodes", id, pb.Entrypoint)
		// Every branch should target a real node.
		for nodeID, node := range pb.Nodes {
			for _, b := range node.Next {
				_, ok := pb.Nodes[b.Goto]
				assert.True(t, ok, "playbook %q node %q branches to nonexistent node %q", id, nodeID, b.Goto)
			}
		}
	}
}

// TestWalker_RoundTrip exercises the full lifecycle: start → step_complete
// → summarize. It uses the cluster_health playbook so it's tied to real
// shipped data; if that playbook is renamed, this test breaks loudly.
func TestWalker_RoundTrip(t *testing.T) {
	t.Parallel()

	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir}) // local seed; in-memory store
	require.NoError(t, err, "New")

	ctx := context.Background()

	// list_playbooks
	_, lp, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{})
	require.NoError(t, err, "listPlaybooks")
	var found bool
	for _, p := range lp.Playbooks {
		if p.ID == "cluster_health" {
			found = true
		}
	}
	require.True(t, found, "cluster_health playbook missing from list_playbooks output")

	// walk_playbook
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err, "walkPlaybook")
	require.NotEmpty(t, start.SessionID, "session_id empty")
	assert.Contains(t, start.Step.Description, "ZeebeCluster", "entrypoint description should mention ZeebeCluster")
	// Templating should have substituted ${cluster_id} into the suggested call args.
	require.NotEmpty(t, start.Step.SuggestedCalls, "no suggested_calls on entrypoint")
	assert.Equal(t, "abc", start.Step.SuggestedCalls[0].Args["name"], "expected suggested_call args.name=\"abc\" after templating")

	// step_complete: record finding and advance in one call
	require.NotEmpty(t, start.Step.NextOptions, "no next_options on entrypoint")
	target := start.Step.NextOptions[0].Goto
	_, adv, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Findings: []FindingEntry{
			{Key: "zeebe_subsystem_status", Value: "elasticsearch=NotReady", SourceTool: "triagent-k8s/get_resource"},
		},
		Goto: target,
	})
	require.NoError(t, err, "stepComplete")
	assert.Equal(t, target, adv.Step.NodeID, "stepComplete should move to target")

	// summarize: agent now passes the four operator-facing fields;
	// the tool returns a Slack-shareable verdict in Markdown and a
	// reviewer-facing evidence body in EvidenceMarkdown. Round-trip
	// both bodies and the session-metadata footer.
	_, sum, err := srv.summarize(ctx, nil, summarizeIn{
		SessionID: start.SessionID,
		Symptom:   "smoke test symptom",
		RootCause: "smoke test root cause",
		Evidence:  "- finding zeebe_subsystem_status was recorded",
		NextSteps: "- nothing; this is a smoke test",
	})
	require.NoError(t, err, "summarize")
	for _, want := range []string{
		"Symptom",
		"smoke test symptom",
		"Likely root cause",
		"abc", // cluster id in the metadata footer
	} {
		assert.Contains(t, sum.Markdown, want, "verdict missing %q", want)
	}
	for _, want := range []string{
		"Evidence",
		"zeebe_subsystem_status",
	} {
		assert.Contains(t, sum.EvidenceMarkdown, want, "evidence body missing %q", want)
	}
	// Confirm finding landed in the session.
	state, err := srv.store.get(start.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "elasticsearch=NotReady", state.Findings["zeebe_subsystem_status"])
}

// TestWalkPlaybook_RejectsMissingClusterIDForInvestigationType
// confirms cluster_id is still required for investigation playbooks —
// they triage a specific cluster and reference ${cluster_id} in
// suggested_calls, so an empty value would render bogus tool args.
func TestWalkPlaybook_RejectsMissingClusterIDForInvestigationType(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")
	res, _, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		Namespace:  "abc-zeebe",
		// ClusterID intentionally omitted.
	})
	require.NoError(t, err, "walkPlaybook returned protocol error")
	require.NotNil(t, res)
	assert.True(t, res.IsError, "expected tool-level error for missing cluster_id on investigation playbook")
}

// TestWalkPlaybook_AcceptsMissingClusterIDForGeneralType lets
// cluster-less callers (e.g. the wiki editor session) walk meta /
// general playbooks like wiki_proposal, wiki_backfill_ingestion,
// playbook_proposal. Those playbooks don't reference ${cluster_id}
// in templating and run in editor / sub-flow contexts that have no
// cluster identity to assert.
func TestWalkPlaybook_AcceptsMissingClusterIDForGeneralType(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")
	_, start, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{
		PlaybookID: "example_general",
		// ClusterID and Namespace intentionally omitted — meta playbooks
		// must accept cluster-less callers.
	})
	require.NoError(t, err, "walkPlaybook should accept missing cluster_id for general-type playbooks")
	assert.NotEmpty(t, start.SessionID, "general-type session should still get a session_id")
}

// TestStepComplete_RejectsUnknownNode guards against the agent typo'ing a goto.
func TestStepComplete_RejectsUnknownNode(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")
	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err, "walkPlaybook")
	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "no_such_node"})
	require.NoError(t, err, "stepComplete returned protocol error")
	require.NotNil(t, res)
	assert.True(t, res.IsError, "expected tool-level error result")
}

// TestWalkPlaybook_LoopGuard exercises the parent_session_id chain
// check: A → B is fine, B → A is rejected (re-entry into the same
// playbook), and an unrelated third playbook is fine. Also confirms
// that omitting parent_session_id intentionally bypasses the guard
// (the documented escape hatch).
func TestWalkPlaybook_LoopGuard(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")
	ctx := context.Background()

	// Top-level start: cluster_health.
	_, sessA, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err, "walkPlaybook A")

	// Handoff to elasticsearch with parent set — allowed.
	_, sessB, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID:      "elasticsearch",
		ClusterID:       "abc",
		Namespace:       "abc-zeebe",
		ParentSessionID: sessA.SessionID,
	})
	require.NoError(t, err, "walkPlaybook B")
	require.NotEmpty(t, sessB.SessionID, "expected sessB to be created")

	// Handoff back to cluster_health — REJECTED, that's a loop.
	res, _, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID:      "cluster_health",
		ClusterID:       "abc",
		Namespace:       "abc-zeebe",
		ParentSessionID: sessB.SessionID,
	})
	require.NoError(t, err, "walkPlaybook loop call returned protocol error")
	require.NotNil(t, res)
	require.True(t, res.IsError, "expected tool-level error for loop")

	// Same call WITHOUT parent_session_id is the documented escape
	// hatch and must succeed (operator may genuinely want to re-enter
	// after the elasticsearch detour).
	_, sessC, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err, "walkPlaybook escape hatch")
	require.NotEmpty(t, sessC.SessionID, "expected fresh top-level session")
	require.NotEqual(t, sessA.SessionID, sessC.SessionID, "expected fresh top-level session")

	// Unrelated third hop from B → connectivity is allowed (not in the chain).
	_, sessD, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID:      "connectivity",
		ClusterID:       "abc",
		Namespace:       "abc-zeebe",
		ParentSessionID: sessB.SessionID,
	})
	require.NoError(t, err, "walkPlaybook D")
	require.NotEmpty(t, sessD.SessionID, "expected sessD to be created")
}

// TestWalker_DelegateTo_RoundTrip exercises the push-on-delegate +
// pop-on-terminal_done flow: master.start → master.walk_target
// (delegate) → delegate_target.do_thing → delegate_target.terminal_done
// (auto-pop) → master.walk_target.next → master.terminal_done.
func TestWalker_DelegateTo_RoundTrip(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)
	ctx := context.Background()

	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "delegate_parent",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err)
	require.Equal(t, "start", start.Step.NodeID)

	// step_complete into the delegate node — the walker should auto-push and
	// surface the delegate's entrypoint, NOT the delegate node itself.
	_, adv, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "walk_target",
	})
	require.NoError(t, err)
	assert.Equal(t, "do_thing", adv.Step.NodeID,
		"after advancing into a delegate_to node, the walker should surface the delegate's entrypoint")

	// Confirm the call stack is populated.
	state, err := srv.store.get(start.SessionID)
	require.NoError(t, err)
	require.Len(t, state.CallStack, 1)
	assert.Equal(t, "delegate_parent", state.CallStack[0].ParentPlaybookID)
	assert.Equal(t, "walk_target", state.CallStack[0].ReturnNodeID)
	assert.Equal(t, "delegate_target", state.PlaybookID,
		"PlaybookID should now be the delegate's id")

	// step_complete to terminal_done — findings recorded alongside the advance
	// must land on the parent's findings map (single audit trail). Walker
	// auto-pops, returns the parent's resume node (walk_target) view, and
	// exposes the delegate's terminal_advice via DelegateReturned.
	_, adv2, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Findings:  []FindingEntry{{Key: "target_finding", Value: "captured-in-delegate"}},
		Goto:      "terminal_done",
	})
	require.NoError(t, err)
	assert.Equal(t, "walk_target", adv2.Step.NodeID,
		"after the delegate reaches terminal_done, the walker should auto-pop and resume at the delegate node")
	require.NotNil(t, adv2.DelegateReturned,
		"DelegateReturned must surface the popped delegate's id + terminal_advice")
	assert.Equal(t, "delegate_target", adv2.DelegateReturned.From)
	assert.Contains(t, adv2.DelegateReturned.Advice, "Returning to parent")

	// Stack should be empty post-pop.
	state, _ = srv.store.get(start.SessionID)
	assert.Empty(t, state.CallStack)
	assert.Equal(t, "delegate_parent", state.PlaybookID)

	// And findings recorded in the delegate are still visible.
	assert.Equal(t, "captured-in-delegate", state.Findings["target_finding"])

	// Continue: parent advances to its own terminal_done.
	_, adv3, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "terminal_done",
	})
	require.NoError(t, err)
	assert.Equal(t, "terminal_done", adv3.Step.NodeID)
	assert.Nil(t, adv3.DelegateReturned, "no delegate frame to pop on parent's own terminal")
}

// TestWalker_DelegateTo_UnknownTarget rejects advance into a delegate
// node whose target playbook isn't loaded.
func TestWalker_DelegateTo_UnknownTarget(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)

	pb := &Playbook{
		ID:         "rogue_parent",
		Symptom:    "test",
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {
				ID:          "a",
				Description: "delegate to ghost",
				DelegateTo:  "ghost_playbook_id",
				Next: []Branch{
					{Condition: "always", Goto: "b"},
				},
			},
			"b": {
				ID:             "b",
				Description:    "term",
				TerminalAdvice: "done",
			},
		},
	}
	srv.playbooks["rogue_parent"] = pb

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "rogue_parent",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err)

	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "a",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "advancing into a delegate node with unknown target must error")
}

// TestWalker_DelegateTo_CycleRejected refuses to push a frame for a
// playbook already on the call stack.
func TestWalker_DelegateTo_CycleRejected(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)

	// Mutually-recursive pair: A delegates to B, B delegates back to A.
	srv.playbooks["cycle_a"] = &Playbook{
		ID: "cycle_a", Symptom: "test", Entrypoint: "x",
		Nodes: map[string]Node{
			"x": {ID: "x", Description: "delegate to b", DelegateTo: "cycle_b",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "term", TerminalAdvice: "done"},
		},
	}
	srv.playbooks["cycle_b"] = &Playbook{
		ID: "cycle_b", Symptom: "test", Entrypoint: "y",
		Nodes: map[string]Node{
			"y": {ID: "y", Description: "delegate back to a", DelegateTo: "cycle_a",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "term", TerminalAdvice: "done"},
		},
	}

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cycle_a", ClusterID: "abc", Namespace: "abc-zeebe",
	})
	require.NoError(t, err)

	// First push: cycle_a → cycle_b (allowed).
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "x"})
	require.NoError(t, err)

	// Second push: cycle_b → cycle_a — this would re-enter cycle_a,
	// which is already on the stack as the parent. Reject.
	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "y"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "delegate_to cycle must be rejected")
}

// TestWalker_DelegateTo_RejectsTerminalHandoff verifies that a
// delegated playbook ending in handoff (rather than terminal_done)
// errors at pop time — delegated playbooks must return.
func TestWalker_DelegateTo_RejectsTerminalHandoff(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)

	srv.playbooks["bad_target"] = &Playbook{
		ID: "bad_target", Symptom: "test", Entrypoint: "x",
		Nodes: map[string]Node{
			"x": {ID: "x", Description: "leap to handoff",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "term with handoff",
				TerminalAdvice: "go elsewhere", Handoff: []string{"some_other"}},
		},
	}
	srv.playbooks["bad_parent"] = &Playbook{
		ID: "bad_parent", Symptom: "test", Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "delegate to bad_target",
				DelegateTo: "bad_target",
				Next:       []Branch{{Condition: "always", Goto: "b"}}},
			"b": {ID: "b", Description: "term", TerminalAdvice: "done"},
		},
	}

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "bad_parent", ClusterID: "abc", Namespace: "abc-zeebe",
	})
	require.NoError(t, err)
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "a"})
	require.NoError(t, err) // push succeeds
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "x"})
	require.NoError(t, err)

	// Reach the bad terminal — walker should error rather than auto-pop
	// because the terminal carries handoff.
	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "term"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "delegated terminal with handoff must error")
}

// TestWalker_DelegateTo_NestedDoublePop walks a parent that delegates
// to a child that itself delegates to a grandchild. Confirms that two
// successive auto-pops shrink the call stack correctly and that
// findings recorded at every level reach the same map.
func TestWalker_DelegateTo_NestedDoublePop(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)

	srv.playbooks["nested_root"] = &Playbook{
		ID: "nested_root", Symptom: "test", Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "delegate to mid", DelegateTo: "nested_mid",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "root term", TerminalAdvice: "root done"},
		},
	}
	srv.playbooks["nested_mid"] = &Playbook{
		ID: "nested_mid", Symptom: "test", Entrypoint: "b",
		Nodes: map[string]Node{
			"b": {ID: "b", Description: "delegate to leaf", DelegateTo: "nested_leaf",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "mid term", TerminalAdvice: "mid done"},
		},
	}
	srv.playbooks["nested_leaf"] = &Playbook{
		ID: "nested_leaf", Symptom: "test", Entrypoint: "c",
		Nodes: map[string]Node{
			"c": {ID: "c", Description: "leaf work",
				Next: []Branch{{Condition: "always", Goto: "term"}}},
			"term": {ID: "term", Description: "leaf term", TerminalAdvice: "leaf done"},
		},
	}

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "nested_root", ClusterID: "abc", Namespace: "abc-zeebe",
	})
	require.NoError(t, err)

	// Push: root.a → mid.b (auto-into mid's entrypoint).
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "a"})
	require.NoError(t, err)
	state, _ := srv.store.get(start.SessionID)
	assert.Equal(t, "nested_mid", state.PlaybookID)
	require.Len(t, state.CallStack, 1)

	// Push: mid.b → leaf.c (stack now has two frames).
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "b"})
	require.NoError(t, err)
	state, _ = srv.store.get(start.SessionID)
	assert.Equal(t, "nested_leaf", state.PlaybookID)
	require.Len(t, state.CallStack, 2)

	// First pop: leaf.c → leaf.term — record the finding alongside the advance.
	// Findings must show up on the parent's findings map (single audit trail).
	_, _, err = srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "c"})
	require.NoError(t, err)
	_, popLeaf, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Findings:  []FindingEntry{{Key: "leaf_finding", Value: "deep"}},
		Goto:      "term",
	})
	require.NoError(t, err)
	assert.Equal(t, "b", popLeaf.Step.NodeID)
	require.NotNil(t, popLeaf.DelegateReturned)
	assert.Equal(t, "nested_leaf", popLeaf.DelegateReturned.From)
	state, _ = srv.store.get(start.SessionID)
	assert.Equal(t, "nested_mid", state.PlaybookID)
	require.Len(t, state.CallStack, 1)

	// Second pop: mid.term → resume at root.a.
	_, popMid, err := srv.stepComplete(ctx, nil, stepCompleteIn{SessionID: start.SessionID, Goto: "term"})
	require.NoError(t, err)
	assert.Equal(t, "a", popMid.Step.NodeID)
	require.NotNil(t, popMid.DelegateReturned)
	assert.Equal(t, "nested_mid", popMid.DelegateReturned.From)
	state, _ = srv.store.get(start.SessionID)
	assert.Equal(t, "nested_root", state.PlaybookID)
	assert.Empty(t, state.CallStack)
	assert.Equal(t, "deep", state.Findings["leaf_finding"])
}

// TestPlaybookSchema_DocumentsDelegateTo confirms the editor-facing
// schema markdown surfaces the delegate_to field, the sub-flow
// authoring convention, and the worked example.
func TestPlaybookSchema_DocumentsDelegateTo(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err)
	ctx := context.Background()
	_, out, err := srv.playbookSchema(ctx, nil, playbookSchemaIn{})
	require.NoError(t, err)
	for _, want := range []string{
		"`delegate_to`",
		"sub-flow only",
		"Sub-flows via",
		"example_helper",
	} {
		assert.Contains(t, out.Schema, want, "playbook_schema markdown missing %q", want)
	}
}

// TestWalker_GatherSubflows_E2E walks the master investigation playbook
// with both gather sub-playbooks loaded, exercising the chained delegate
// flow. Treats the system tier as the source for these YAMLs (which is
// where they ship in production).
func TestWalker_GatherSubflows_E2E(t *testing.T) {
	t.Parallel()
	// Extract the launcher's embedded system playbooks into a temp dir
	// so that loadSystemTypedDir sees the canonical <root>/system/<id>.yaml
	// layout. The strategies loader receives <root> as SystemPlaybooksDir
	// (it walks <root>/<type>/*.yaml, treating "system" as the type slot).
	root := t.TempDir()
	if err := extractInvestigateSystemYAMLs(t, root); err != nil {
		t.Fatalf("extractInvestigateSystemYAMLs: %v", err)
	}
	srv, err := New(Options{SystemPlaybooksDir: root})
	require.NoError(t, err)

	// Confirm both gather playbooks loaded.
	require.Contains(t, srv.playbooks, "gather_incidentio_context")
	require.Contains(t, srv.playbooks, "gather_slack_context")
	require.Contains(t, srv.playbooks, "investigation")

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "investigation",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
		Notes:      "zeebe brokers crashlooping after rebalance",
	})
	require.NoError(t, err)
	assert.Equal(t, "confirm_context", start.Step.NodeID)

	// confirm_context → walk_incidentio (delegate). The walker should
	// surface gather_incidentio_context's entrypoint check_scope.
	_, adv1, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "walk_incidentio",
	})
	require.NoError(t, err)
	assert.Equal(t, "check_scope", adv1.Step.NodeID,
		"after delegating to gather_incidentio_context, walker should surface its entrypoint")

	state, _ := srv.store.get(start.SessionID)
	assert.Equal(t, "gather_incidentio_context", state.PlaybookID)
	require.Len(t, state.CallStack, 1)
	assert.Equal(t, "investigation", state.CallStack[0].ParentPlaybookID)
	assert.Equal(t, "walk_incidentio", state.CallStack[0].ReturnNodeID)

	// Skip path: incidentio MCP not wired in this test session, so
	// agent advances check_scope → terminal_skipped. Walker should
	// auto-pop and resume at walk_incidentio.
	_, adv2, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "terminal_skipped",
	})
	require.NoError(t, err)
	assert.Equal(t, "walk_incidentio", adv2.Step.NodeID,
		"auto-pop should resume at the parent's delegate node")
	require.NotNil(t, adv2.DelegateReturned)
	assert.Equal(t, "gather_incidentio_context", adv2.DelegateReturned.From)

	state, _ = srv.store.get(start.SessionID)
	assert.Empty(t, state.CallStack)
	assert.Equal(t, "investigation", state.PlaybookID)

	// Continue: walk_incidentio → walk_slack (the next branch).
	_, adv3, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "walk_slack",
	})
	require.NoError(t, err)
	assert.Equal(t, "check_scope", adv3.Step.NodeID,
		"second delegate should land on gather_slack_context.check_scope")

	state, _ = srv.store.get(start.SessionID)
	assert.Equal(t, "gather_slack_context", state.PlaybookID)

	// Skip slack too.
	_, adv4, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "terminal_skipped",
	})
	require.NoError(t, err)
	assert.Equal(t, "walk_slack", adv4.Step.NodeID)

	// Continue: walk_slack → hypothesize.
	_, adv5, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: start.SessionID,
		Goto:      "hypothesize",
	})
	require.NoError(t, err)
	assert.Equal(t, "hypothesize", adv5.Step.NodeID,
		"after both gather sub-flows return, master flow continues to hypothesize")
}

// TestGetPlaybookRaw_RoundTripsDelegateTo confirms that a playbook
// with a delegate_to node round-trips through the parser + renderer
// without losing the field. Uses the user-playbook code path
// (parse + re-render) since the system tier returns its raw bytes
// verbatim, which would mask a renderer bug.
func TestGetPlaybookRaw_RoundTripsDelegateTo(t *testing.T) {
	t.Parallel()
	yamlBytes := []byte(`id: round_trip_parent
schema_version: 1
symptom: "round-trip test"
entrypoint: a
nodes:
  a:
    description: "delegate then resume"
    delegate_to: round_trip_target
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	pb, errs := ParseAndValidatePlaybookYAML(yamlBytes)
	require.Empty(t, errs)
	require.Equal(t, "round_trip_target", pb.Nodes["a"].DelegateTo)

	rendered, err := renderPlaybookYAML(pb)
	require.NoError(t, err)
	assert.Contains(t, rendered, "delegate_to: round_trip_target",
		"renderPlaybookYAML must preserve delegate_to")

	// And the rendered output must itself parse + validate cleanly.
	pb2, errs2 := ParseAndValidatePlaybookYAML([]byte(rendered))
	require.Empty(t, errs2, "rendered YAML failed to re-validate: %v", errs2)
	assert.Equal(t, "round_trip_target", pb2.Nodes["a"].DelegateTo)
}

// TestListPlaybooks_FilterMatchesIDOrSymptomCaseInsensitive confirms
// that the filter parameter narrows playbooks by case-insensitive
// substring match against id + symptom.
// extractInvestigateSystemYAMLs copies the *.yaml files from the
// launcher's system/ source directory into
// <root>/system/<file> — the <type>/<id>.yaml layout that
// loadSystemTypedDir expects (the same layout that system.Extract
// produces at runtime). The strategies loader receives <root> as
// SystemPlaybooksDir and treats "system" as the playbook type slot.
func extractInvestigateSystemYAMLs(t *testing.T, root string) error {
	t.Helper()
	// Resolve path relative to this test file's package directory
	// (pkg/mcp/strategies → system).
	src := filepath.Join("..", "..", "..", "system")
	typeDir := filepath.Join(root, "system")
	if err := os.MkdirAll(typeDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		dest, err := os.Create(filepath.Join(typeDir, e.Name()))
		if err != nil {
			_ = data.Close()
			return err
		}
		_, copyErr := io.Copy(dest, data)
		_ = data.Close()
		_ = dest.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func TestListPlaybooks_FilterMatchesIDOrSymptomCaseInsensitive(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{Filter: "HEALTH"})
	require.NoError(t, err)
	require.NotEmpty(t, out.Playbooks)
	for _, pb := range out.Playbooks {
		assert.True(t,
			strings.Contains(strings.ToLower(pb.ID), "health") ||
				strings.Contains(strings.ToLower(pb.Symptom), "health"),
			"filtered entry %q/%q should match", pb.ID, pb.Symptom,
		)
	}
}

func TestListPlaybooks_DefaultOmitsDescription(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{})
	require.NoError(t, err)
	require.NotEmpty(t, out.Playbooks)
	for _, pb := range out.Playbooks {
		assert.Empty(t, pb.Description, "description must be omitted by default for %q", pb.ID)
	}
}

func TestListPlaybooks_IncludeDescriptionReturnsDescription(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{IncludeDescription: true})
	require.NoError(t, err)
	found := false
	for _, pb := range out.Playbooks {
		if pb.Description != "" {
			found = true
			break
		}
	}
	assert.True(t, found, "at least one fixture playbook should have a non-empty description when include_description=true")
}
