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
