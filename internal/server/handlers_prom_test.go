package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/profile"
)

// ── resolvePromTarget overlay matrix ──────────────────────────────────────────

var profileWithProm = &profile.Profile{
	Defaults: profile.Defaults{
		Prometheus: profile.Prometheus{
			Service:   "default-prom",
			Namespace: "monitoring",
			Port:      9090,
		},
	},
}

// TestResolvePromTarget_ProfileOnly: no override → profile defaults apply.
func TestResolvePromTarget_ProfileOnly(t *testing.T) {
	t.Parallel()
	target, disabled := resolvePromTarget(profileWithProm, nil)
	require.False(t, disabled)
	require.NotNil(t, target)
	assert.Equal(t, "default-prom", target.Service)
	assert.Equal(t, "monitoring", target.Namespace)
	assert.Equal(t, 9090, target.Port)
}

// TestResolvePromTarget_OverrideOnly: override fields with no profile.
func TestResolvePromTarget_OverrideOnly(t *testing.T) {
	t.Parallel()
	override := &promOverrideBody{
		Service:   "custom-prom",
		Namespace: "custom-ns",
		Port:      8080,
	}
	target, disabled := resolvePromTarget(nil, override)
	require.False(t, disabled)
	require.NotNil(t, target)
	assert.Equal(t, "custom-prom", target.Service)
	assert.Equal(t, "custom-ns", target.Namespace)
	assert.Equal(t, 8080, target.Port)
}

// TestResolvePromTarget_OverlayFieldLevel: each non-zero override field wins,
// zero fields fall back to the profile.
func TestResolvePromTarget_OverlayFieldLevel(t *testing.T) {
	t.Parallel()
	// Override only service — namespace and port fall through to profile.
	override := &promOverrideBody{
		Service: "override-prom",
	}
	target, disabled := resolvePromTarget(profileWithProm, override)
	require.False(t, disabled)
	require.NotNil(t, target)
	assert.Equal(t, "override-prom", target.Service)
	assert.Equal(t, "monitoring", target.Namespace, "should inherit profile namespace")
	assert.Equal(t, 9090, target.Port, "should inherit profile port")
}

// TestResolvePromTarget_OverlayAllFields: every override field wins.
func TestResolvePromTarget_OverlayAllFields(t *testing.T) {
	t.Parallel()
	override := &promOverrideBody{
		Service:   "custom-svc",
		Namespace: "custom-ns",
		Port:      8888,
	}
	target, disabled := resolvePromTarget(profileWithProm, override)
	require.False(t, disabled)
	require.NotNil(t, target)
	assert.Equal(t, "custom-svc", target.Service)
	assert.Equal(t, "custom-ns", target.Namespace)
	assert.Equal(t, 8888, target.Port)
}

// TestResolvePromTarget_DisabledFlagSkips: disabled=true → (nil, true).
func TestResolvePromTarget_DisabledFlagSkips(t *testing.T) {
	t.Parallel()
	override := &promOverrideBody{Disabled: true}
	target, disabled := resolvePromTarget(profileWithProm, override)
	assert.True(t, disabled)
	assert.Nil(t, target)
}

// TestResolvePromTarget_NeitherSet: no profile, no override → (nil, false).
func TestResolvePromTarget_NeitherSet(t *testing.T) {
	t.Parallel()
	target, disabled := resolvePromTarget(nil, nil)
	assert.False(t, disabled)
	assert.Nil(t, target)
}

// TestResolvePromTarget_ProfileEmptyService: profile has empty service → nil.
func TestResolvePromTarget_ProfileEmptyService(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		Defaults: profile.Defaults{
			Prometheus: profile.Prometheus{Service: "", Namespace: "monitoring", Port: 9090},
		},
	}
	target, disabled := resolvePromTarget(prof, nil)
	assert.False(t, disabled)
	assert.Nil(t, target)
}

// ── GET /api/profile/prom-defaults ───────────────────────────────────────────

func TestHandleProfilePromDefaults_HappyPath(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		Defaults: profile.Defaults{
			Prometheus: profile.Prometheus{
				Service:   "prometheus-server",
				Namespace: "monitoring",
				Port:      9090,
			},
		},
	}
	a := &apiHandlers{prof: prof}
	req := httptest.NewRequest(http.MethodGet, "/api/profile/prom-defaults", nil)
	rr := httptest.NewRecorder()
	a.handleProfilePromDefaults(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "prometheus-server", body["service"])
	assert.Equal(t, "monitoring", body["namespace"])
	assert.Equal(t, float64(9090), body["port"])
}

func TestHandleProfilePromDefaults_NilProfileReturns503(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{prof: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/profile/prom-defaults", nil)
	rr := httptest.NewRecorder()
	a.handleProfilePromDefaults(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestHandleProfilePromDefaults_EmptyPrometheusFields(t *testing.T) {
	t.Parallel()
	// Profile exists but has no prometheus configured.
	a := &apiHandlers{prof: &profile.Profile{}}
	req := httptest.NewRequest(http.MethodGet, "/api/profile/prom-defaults", nil)
	rr := httptest.NewRecorder()
	a.handleProfilePromDefaults(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "", body["service"])
	assert.Equal(t, "", body["namespace"])
	assert.Equal(t, float64(0), body["port"])
}
