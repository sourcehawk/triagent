package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The e2e harness polls /healthz for readiness and the boot-options flow
// asserts the resolved profile name comes back in the body. Contract:
// 200 with {"profile","version"}.
func TestHealthz_ReportsProfileAndVersion(t *testing.T) {
	t.Parallel()

	h := healthzHandler("staging", "1.2.3")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Profile string `json:"profile"`
		Version string `json:"version"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "staging", body.Profile)
	assert.Equal(t, "1.2.3", body.Version)
}
