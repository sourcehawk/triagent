package strategies

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkPlaybook_DispatchSubagentRunsRunnerWithModelAndPrompt(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "wiki_proposal_test",
		Symptom:    "test",
		Dispatch:   DispatchSubagent,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "draft the wiki entry", TerminalAdvice: "done"},
		},
	}

	var capturedOpts subagent.Options
	runner := func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		capturedOpts = opts
		return subagent.Result{Summary: "dispatched ok"}, nil
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = runner
	srv.models = DispatchModels{Subagent: "claude-haiku-4-5-20251001"}
	srv.mcpConfigPath = "/launcher/session/mcp.json"
	srv.parentSessionState = func(string) (map[string]any, string, bool) {
		return map[string]any{"finding_a": "x"}, "the investigation summary", true
	}

	ctx := telemetry.WithToolID(context.Background(), "tooluid_walk_playbook_test")
	res, out, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID:      pb.ID,
		ParentSessionID: "parent-1",
		Notes:           "agent brief: propose extending stuck_reconciliation with a completed-update early exit",
	})
	require.NoError(t, err)
	require.Nil(t, res, "no error result on success")
	assert.Equal(t, "claude-haiku-4-5-20251001", capturedOpts.Model)
	assert.Contains(t, capturedOpts.Prompt, "draft the wiki entry")
	assert.Contains(t, capturedOpts.Prompt, "the investigation summary")
	assert.Contains(t, capturedOpts.Prompt, "finding_a")
	assert.Contains(t, capturedOpts.Prompt, "propose extending stuck_reconciliation", "notes must reach the dispatched sub-agent prompt")
	assert.Equal(t, "/launcher/session/mcp.json", capturedOpts.MCPConfigPath,
		"dispatch must forward the launcher's per-session mcp.json so the sub-agent can reach mcp__triagent-strategies__* and mcp__triagent-wiki__* tools its allowlist promises")
	assert.Equal(t, "tooluid_walk_playbook_test", capturedOpts.ParentToolID,
		"dispatch must forward the dispatching tool's id as ParentToolID so the sub-agent runner's relaySubEvents nests its tool_use/tool_result events under walk_playbook in the activity panel — without it postNestedToolUse early-returns and nothing nests")
	require.NotNil(t, out.Dispatched)
	assert.True(t, strings.HasPrefix(out.Dispatched.Summary, "dispatched"))
}

// TestWalkPlaybook_DispatchInjectsCurrentProposalState verifies that
// runDispatch reads the launcher's proposal store (via ListProposals)
// and seeds the dispatched sub-agent's prompt with the operator's
// recent decline notes — closing the gap where the sub-agent
// confidently summarized a "submission" of a proposal id the master
// agent's stale notes mentioned but the operator had actually just
// declined.
func TestWalkPlaybook_DispatchInjectsCurrentProposalState(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "playbook_proposal_test",
		Symptom:    "test",
		Dispatch:   DispatchSubagent,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "decide whether to propose", TerminalAdvice: "done"},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.userPlaybooksDir = t.TempDir()
	resolvedDir := filepath.Join(srv.userPlaybooksDir, ProposalsSubdir, ".resolved")
	require.NoError(t, os.MkdirAll(resolvedDir, 0o755))
	body, _ := json.Marshal(map[string]any{
		"outcome": "declined", "id": "stuck_reconciliation", "type": "investigation",
		"at": "2026-05-20T20:51:30Z", "note": "split into two entries — one per cluster",
	})
	require.NoError(t, os.WriteFile(filepath.Join(resolvedDir, "de222222bbbb.json"), body, 0o644))

	var captured subagent.Options
	srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		captured = opts
		return subagent.Result{Summary: "ok"}, nil
	}

	_, _, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{
		PlaybookID: pb.ID,
	})
	require.NoError(t, err)
	assert.Contains(t, captured.Prompt, "stuck_reconciliation",
		"dispatched prompt must surface declined playbook ids so the sub-agent doesn't re-propose the same shape")
	assert.Contains(t, captured.Prompt, "split into two entries — one per cluster",
		"dispatched prompt must surface the operator's decline note")
	assert.Contains(t, strings.ToLower(captured.Prompt), "proposal state")
}

// TestWalkPlaybook_DispatchSurfacesSubagentTimeout pins the contract that
// walk_playbook returns TimedOut / ExitCode / StderrTail from the sub-agent
// Result so the master agent can tell the difference between "subagent
// completed" and "subagent SIGKILL'd at the wall-clock cap". Without this,
// the master reads the partial summary as success and confabulates a
// proposal that was never actually submitted — see the SUPPORT-32936 session
// where a 5-min timeout swallowed the playbook_proposal_draft call.
func TestWalkPlaybook_DispatchSurfacesSubagentTimeout(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "playbook_proposal_timeout_test",
		Symptom:    "test",
		Dispatch:   DispatchSubagent,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "draft", TerminalAdvice: "done"},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		return subagent.Result{
			Summary:    "I have everything I need. Drafting and submitting…",
			TimedOut:   true,
			ExitCode:   -1,
			StderrTail: "signal: killed",
		}, nil
	}

	_, out, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	require.NotNil(t, out.Dispatched)
	assert.Equal(t, "I have everything I need. Drafting and submitting…", out.Dispatched.Summary)
	assert.True(t, out.Dispatched.TimedOut,
		"dispatch must surface TimedOut so the master agent doesn't read the partial summary as a completed proposal submission")
	assert.Equal(t, -1, out.Dispatched.ExitCode,
		"dispatch must surface non-zero ExitCode so abnormal subprocess exits are visible to the master agent")
	assert.Contains(t, out.Dispatched.StderrTail, "killed",
		"dispatch must surface StderrTail so the master agent has a concrete reason to relay back to the operator")
}

// TestRunDispatch_AppliesPerPlaybookTimeout pins the per-playbook timeout
// override. playbook_proposal does a lot of read+reason+draft work (schema,
// list_playbooks, list_proposals, get_playbook_raw on multiple existing
// playbooks, draft YAML, validate, re-validate, optional parent revision)
// and bumps into the 5-min subagent default. Mirrors the dispatchAllowedToolsFor
// per-playbook switch.
func TestRunDispatch_AppliesPerPlaybookTimeout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id      string
		wantMin time.Duration
	}{
		{id: "playbook_proposal", wantMin: 5 * time.Minute},
		{id: "wiki_proposal", wantMin: 5 * time.Minute},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			pb := &Playbook{
				ID:         tc.id,
				Symptom:    "test",
				Dispatch:   DispatchSubagent,
				Entrypoint: "a",
				Nodes: map[string]Node{
					"a": {ID: "a", Description: "draft", TerminalAdvice: "done"},
				},
			}
			var captured subagent.Options
			srv := newEmptyServer(t)
			srv.playbooks[pb.ID] = pb
			srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
				captured = opts
				return subagent.Result{Summary: "ok"}, nil
			}

			_, _, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
			require.NoError(t, err)
			assert.Greater(t, captured.Timeout, tc.wantMin,
				"dispatch for %q must override the default 5-min subagent timeout — the proposal flow legitimately needs longer", tc.id)
		})
	}
}

// TestRunDispatch_NoTimeoutOverrideForUnknownDispatch verifies that
// dispatchTimeoutFor only extends the cap for known dispatch playbooks;
// future dispatch-mode playbooks fall back to the subagent package's
// default (5 min) until their owner explicitly opts in.
func TestRunDispatch_NoTimeoutOverrideForUnknownDispatch(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "unknown_future_dispatch_playbook",
		Symptom:    "test",
		Dispatch:   DispatchSubagent,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "draft", TerminalAdvice: "done"},
		},
	}
	var captured subagent.Options
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		captured = opts
		return subagent.Result{Summary: "ok"}, nil
	}

	_, _, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.Zero(t, captured.Timeout,
		"unknown dispatch playbooks must leave Timeout zero so subagent.Run falls back to defaultTimeout — opt-in, not implicit")
}

func TestWalkPlaybook_DefaultDispatchUsesWalker(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "regular_pb",
		Symptom:    "test",
		Dispatch:   DispatchDefault,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "step", TerminalAdvice: "done"},
		},
	}
	runnerCalled := false
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		runnerCalled = true
		return subagent.Result{}, nil
	}

	ctx := context.Background()
	_, out, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.False(t, runnerCalled, "default-dispatch playbooks must not call the sub-agent runner")
	assert.NotEmpty(t, out.SessionID, "default dispatch returns a walker session id")
	assert.Equal(t, "a", out.Step.NodeID)
}

// TestWalkPlaybook_DispatchResumesAndForcesTerminal: a proposal dispatch that
// returns without calling a terminal tool is resumed (same session) with a
// forcing follow-up; once it calls the submit tool the outcome is "submitted".
func TestWalkPlaybook_DispatchResumesAndForcesTerminal(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "playbook_proposal", Dispatch: DispatchSubagent, Entrypoint: "a",
		Nodes: map[string]Node{"a": {ID: "a", Description: "draft a playbook"}}}
	var calls []subagent.Options
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(_ context.Context, opts subagent.Options) (subagent.Result, error) {
		calls = append(calls, opts)
		if len(calls) == 1 {
			return subagent.Result{Summary: "I wrote the YAML to a file.", SessionID: "sess-1"}, nil
		}
		return subagent.Result{Summary: "Submitted.", SessionID: "sess-1",
			TerminalToolsCalled: []string{"mcp__triagent-strategies__playbook_proposal_draft"}}, nil
	}

	_, out, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	require.Len(t, calls, 2, "a no-terminal run must be resumed and forced once")
	assert.Equal(t, "sess-1", calls[1].ResumeSessionID, "the force-retry resumes the same conversation")
	assert.NotEmpty(t, calls[1].Prompt, "the force-retry carries a forcing follow-up prompt")
	assert.Contains(t, calls[0].RequiredTerminalTools, "mcp__triagent-strategies__playbook_proposal_draft")
	require.NotNil(t, out.Dispatched)
	assert.Equal(t, "submitted", out.Dispatched.ProposalOutcome)
}

// TestWalkPlaybook_DispatchDeclineIsNotForced: a sub-agent that explicitly
// calls decline_proposal reached a terminal — no retry, outcome "declined".
func TestWalkPlaybook_DispatchDeclineIsNotForced(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "wiki_proposal", Dispatch: DispatchSubagent, Entrypoint: "a",
		Nodes: map[string]Node{"a": {ID: "a", Description: "draft a wiki entry"}}}
	var calls int
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(_ context.Context, _ subagent.Options) (subagent.Result, error) {
		calls++
		return subagent.Result{Summary: "Below the bar.", SessionID: "s",
			TerminalToolsCalled: []string{"mcp__triagent-strategies__decline_proposal"}}, nil
	}
	_, out, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "a decline is a valid terminal — no force-retry")
	assert.Equal(t, "declined", out.Dispatched.ProposalOutcome)
}

// TestWalkPlaybook_DispatchNoTerminalSurfacesNone: when no terminal ever fires,
// the outcome is "none" and the summary is prefixed so the master can't read it
// as success.
func TestWalkPlaybook_DispatchNoTerminalSurfacesNone(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "playbook_proposal", Dispatch: DispatchSubagent, Entrypoint: "a",
		Nodes: map[string]Node{"a": {ID: "a", Description: "draft"}}}
	var calls int
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(_ context.Context, _ subagent.Options) (subagent.Result, error) {
		calls++
		return subagent.Result{Summary: "Here is what I would propose.", SessionID: "s"}, nil
	}
	_, out, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.Equal(t, 1+maxForceDispatchRetries, calls, "initial run plus the bounded force-retries")
	assert.Equal(t, "none", out.Dispatched.ProposalOutcome)
	assert.Contains(t, out.Dispatched.Summary, "NO PROPOSAL WAS SUBMITTED")
}

// TestWalkPlaybook_DispatchTimeoutIsNotForced: a timed-out run is surfaced, not
// retried (the cap fired mid-work; retrying compounds it).
func TestWalkPlaybook_DispatchTimeoutIsNotForced(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "playbook_proposal", Dispatch: DispatchSubagent, Entrypoint: "a",
		Nodes: map[string]Node{"a": {ID: "a", Description: "draft"}}}
	var calls int
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(_ context.Context, _ subagent.Options) (subagent.Result, error) {
		calls++
		return subagent.Result{Summary: "partial", SessionID: "s", TimedOut: true}, nil
	}
	_, out, err := srv.walkPlaybook(context.Background(), nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "a timed-out run is not force-retried")
	assert.Equal(t, "none", out.Dispatched.ProposalOutcome)
	assert.True(t, out.Dispatched.TimedOut)
}
