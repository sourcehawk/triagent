package strategies

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer returns a server backed by the shared local testdata playbooks
// (cluster_health, delegate_parent, delegate_target, etc.)
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// mustWalk opens a walker session for the given playbookID and returns the
// walk output. Fatals on error.
func mustWalk(t *testing.T, s *Server, playbookID, clusterID, namespace string) walkPlaybookOut {
	t.Helper()
	res, out, err := s.walkPlaybook(t.Context(), nil, walkPlaybookIn{
		PlaybookID: playbookID,
		ClusterID:  clusterID,
		Namespace:  namespace,
	})
	if err != nil {
		t.Fatalf("walkPlaybook: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("walkPlaybook error: %s", textOf(res))
	}
	return out
}

// mustGetState calls getState and returns the output. Fatals on error.
func mustGetState(t *testing.T, s *Server, sessionID string) getStateOut {
	t.Helper()
	res, out, err := s.getState(t.Context(), nil, getStateIn{SessionID: sessionID})
	if err != nil {
		t.Fatalf("getState: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("getState error: %s", textOf(res))
	}
	return out
}

// findOption returns the NextOption whose Goto field exactly matches gotoExact.
// Fatals if not found.
func findOption(t *testing.T, opts []NextOption, gotoExact string) NextOption {
	t.Helper()
	for _, o := range opts {
		if o.Goto == gotoExact {
			return o
		}
	}
	t.Fatalf("no next option with goto %q; got %+v", gotoExact, opts)
	return NextOption{}
}

// textOf extracts the text content from an error result.
func textOf(res *mcp.CallToolResult) string {
	if res == nil {
		return "<nil>"
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, " ")
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestStepComplete_RecordsThenAdvances(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "cluster_health", "cluster-1", "ns-1")
	res, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings: []FindingEntry{
			{Key: "pod_state", Value: "CrashLoopBackOff", SourceTool: "triagent-k8s/get_resource"},
			{Key: "restart_count", Value: 17, SourceTool: "triagent-k8s/get_resource"},
		},
		Goto: walkOut.Step.NextOptions[0].Goto,
	})
	if res != nil && res.IsError {
		t.Fatal(textOf(res))
	}
	if out.Step.Description == "" {
		t.Fatal("expected non-empty step description")
	}
	state := mustGetState(t, s, walkOut.SessionID)
	if state.Session.Findings["pod_state"] != "CrashLoopBackOff" {
		t.Fatalf("findings missing: %+v", state.Session.Findings)
	}
}

func TestStepComplete_InvalidGotoRejectsAtomically(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "cluster_health", "cluster-1", "ns-1")
	res, _, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings:  []FindingEntry{{Key: "pod_state", Value: "Running"}},
		Goto:      "nonexistent-node",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid goto")
	}
	state := mustGetState(t, s, walkOut.SessionID)
	if _, exists := state.Session.Findings["pod_state"]; exists {
		t.Fatal("findings should not be recorded on invalid goto")
	}
	if len(state.Session.RecordedCalls) != 0 {
		t.Fatalf("RecordedCalls should not be appended on invalid goto, got %d entries", len(state.Session.RecordedCalls))
	}
}

func TestStepComplete_EmptyFindings(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "cluster_health", "cluster-1", "ns-1")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Goto:      walkOut.Step.NextOptions[0].Goto,
	})
	if out.Step.Description == "" {
		t.Fatal("expected step advance with no findings")
	}
}

func TestStepComplete_RequiresGoto(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "cluster_health", "cluster-1", "ns-1")
	res, _, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings:  []FindingEntry{{Key: "pod_state", Value: "X"}},
		Goto:      "",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for empty goto")
	}
}

func TestStepComplete_DelegatePush(t *testing.T) {
	t.Parallel()
	// delegate_parent: start → walk_target (delegate_to: delegate_target) → terminal_done.
	// Advancing into walk_target should auto-push and surface delegate_target's entrypoint
	// (do_thing), not the walk_target node itself.
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "delegate_parent", "cluster-1", "ns-1")
	// The entrypoint is "start" with one branch: goto "walk_target".
	delegateOpt := findOption(t, walkOut.Step.NextOptions, "walk_target")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings:  []FindingEntry{{Key: "checked", Value: true}},
		Goto:      delegateOpt.Goto,
	})
	// The walker should descend into the delegate's entrypoint (do_thing),
	// not stay on the delegate_to node itself.
	if out.Step.NodeID == delegateOpt.Goto {
		t.Fatalf("expected descent into delegate, still at delegate_to node %q", out.Step.NodeID)
	}
	if out.Step.NodeID != "do_thing" {
		t.Fatalf("expected delegate entrypoint do_thing, got %q", out.Step.NodeID)
	}
}

func TestStepComplete_LandsOnTerminalAdvice(t *testing.T) {
	t.Parallel()
	// cluster_health: start → terminal_unhealthy (has terminal_advice).
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "cluster_health", "cluster-1", "ns-1")
	terminalOpt := findOption(t, walkOut.Step.NextOptions, "terminal_unhealthy")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Goto:      terminalOpt.Goto,
	})
	if out.Step.TerminalAdvice == "" {
		t.Fatalf("expected terminal_advice on the landed terminal step, got step: %+v", out.Step)
	}
}

func TestStepComplete_AutoAdvancesThroughPureTransitionNode(t *testing.T) {
	t.Parallel()
	srv := newServerWithAutoAdvanceFixture(t)
	ctx := context.Background()

	_, walkOut, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: "auto_advance_fixture"})
	require.NoError(t, err)
	require.Equal(t, "A", walkOut.Step.NodeID)
	sessID := walkOut.SessionID

	// step_complete{goto: B}. B is pure-transition (one next to C); the walker
	// should auto-advance to C and report B in auto_advanced_through.
	_, scOut, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: sessID,
		Goto:      "B",
	})
	require.NoError(t, err)
	assert.Equal(t, "C", scOut.Step.NodeID, "walker should auto-advance past B to C")
	assert.Equal(t, []string{"B"}, scOut.AutoAdvancedThrough, "should report B as skipped")
}

func TestStepComplete_AutoAdvanceLoopGuard(t *testing.T) {
	t.Parallel()
	srv := newServerWithLoopingFixture(t)
	ctx := context.Background()

	_, walkOut, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: "auto_advance_loop_fixture"})
	require.NoError(t, err)
	sessID := walkOut.SessionID

	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: sessID,
		Goto:      "L1",
	})
	require.NoError(t, err)
	require.NotNil(t, res, "loop guard should surface an error result rather than spinning")
}

// ── fixtures ────────────────────────────────────────────────────────────────

// newEmptyServer returns a *Server with an empty playbook map and an in-memory
// store. Tests that need custom playbooks inject them directly via srv.playbooks[id].
func newEmptyServer(t *testing.T) *Server {
	t.Helper()
	impl, err := New(Options{}) // no dirs → empty playbook set + in-memory store
	require.NoError(t, err, "newEmptyServer")
	return impl
}

func newServerWithAutoAdvanceFixture(t *testing.T) *Server {
	t.Helper()
	pb := &Playbook{
		ID:         "auto_advance_fixture",
		Symptom:    "auto-advance fixture",
		Entrypoint: "A",
		Nodes: map[string]Node{
			"A": {ID: "A", Description: "real work", ExpectedFindings: []string{"k"}, Next: []Branch{{Condition: "always", Goto: "B"}}},
			"B": {ID: "B", Description: "transient", Next: []Branch{{Condition: "always", Goto: "C"}}},
			"C": {ID: "C", Description: "real work again", ExpectedFindings: []string{"j"}, Next: []Branch{{Condition: "done", Goto: "D"}}},
			"D": {ID: "D", Description: "terminal", TerminalAdvice: "done"},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	return srv
}

func newServerWithLoopingFixture(t *testing.T) *Server {
	t.Helper()
	pb := &Playbook{
		ID:         "auto_advance_loop_fixture",
		Symptom:    "loop fixture",
		Entrypoint: "ENTRY",
		Nodes: map[string]Node{
			"ENTRY": {ID: "ENTRY", Description: "entry", ExpectedFindings: []string{"k"}, Next: []Branch{{Condition: "always", Goto: "L1"}}},
			"L1":    {ID: "L1", Description: "loop", Next: []Branch{{Condition: "always", Goto: "L2"}}},
			"L2":    {ID: "L2", Description: "loop", Next: []Branch{{Condition: "always", Goto: "L1"}}},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	return srv
}
