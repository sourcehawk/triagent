package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHandleToolEvent_StatusPhasePublishesEnvelope verifies that a
// phase=status telemetry POST results in a tool_status envelope on the
// investigation's SSE stream.
func TestHandleToolEvent_StatusPhasePublishesEnvelope(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{
		manager:        mgr,
		telemetryToken: "test-token",
	}

	// Subscribe to the multiplex stream before posting so we don't
	// miss the envelope. Filter by InvestigationID + Kind below.
	_, events, _, cancel := mgr.SubscribeStream("test-tok-"+t.Name(), 0)
	t.Cleanup(cancel)

	body := strings.NewReader(`{"phase":"status","traceId":"` + inv.ID + `","parentToolId":"abc","result":"writing failing test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/tool-events", body)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	a.handleToolEvent(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	// Drain the stream for envelopes scoped to this investigation;
	// the status envelope should arrive promptly.
	deadline := time.After(time.Second)
	for {
		select {
		case env := <-events:
			if env.InvestigationID != inv.ID || env.Kind != envKindToolStatus {
				continue
			}
			require.Equal(t, "abc", env.ParentToolID)
			require.Equal(t, "writing failing test", env.Text)
			return
		case <-deadline:
			t.Fatal("timed out waiting for tool_status envelope")
		}
	}
}

// TestHandleToolEvent_StatusPhase_UnknownTraceID returns 404 when the
// trace id doesn't match any investigation.
func TestHandleToolEvent_StatusPhase_UnknownTraceID(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{
		manager:        mgr,
		telemetryToken: "test-token",
	}

	body := strings.NewReader(`{"phase":"status","traceId":"nonexistent","parentToolId":"abc","result":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/tool-events", body)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	a.handleToolEvent(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleToolEvent_WikiProposalDraftPublishesGlobalEvent verifies that a
// successful propose_wiki_draft end-event fans a global wiki_proposal_created
// envelope out so the sidebar's pending-proposals list refreshes — regardless
// of whether the draft was made directly or nested inside a playbook sub-agent
// (the nesting is what kept the inline card from rendering; this is the
// surfacing path that doesn't depend on transcript nesting at all).
func TestHandleToolEvent_WikiProposalDraftPublishesGlobalEvent(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{
		manager:        mgr,
		telemetryToken: "test-token",
		mcpHealth:      newMCPHealth(),
	}

	_, events, _, cancel := mgr.SubscribeStream("test-tok-"+t.Name(), 0)
	t.Cleanup(cancel)

	// A nested call carries parentToolId; the global event must fire anyway.
	body := strings.NewReader(`{"phase":"end","traceId":"` + inv.ID + `","toolId":"sub_toolu_01","parentToolId":"dispatch_1","toolName":"mcp__triagent-wiki__propose_wiki_draft","result":"{\"kind\":\"wiki_proposal_draft\",\"proposal_id\":\"prop-deadbeef\",\"slug\":\"x\"}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/tool-events", body)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	a.handleToolEvent(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	deadline := time.After(time.Second)
	for {
		select {
		case env := <-events:
			if env.Kind != globalKindWikiProposalCreated {
				continue
			}
			require.NotNil(t, env.WikiProposalCreated)
			require.Equal(t, "prop-deadbeef", env.WikiProposalCreated.ProposalID)
			require.Equal(t, inv.ID, env.WikiProposalCreated.InvestigationID)
			return
		case <-deadline:
			t.Fatal("timed out waiting for wiki_proposal_created global envelope")
		}
	}
}
