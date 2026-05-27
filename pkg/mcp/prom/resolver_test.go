package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promStubWithNames spins a httptest server that answers
// /api/v1/label/__name__/values + /api/v1/metadata so the catalog
// refresh after a resolver-driven rebind succeeds.
func promStubWithNames(t *testing.T, names []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/label/__name__/values", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": names})
	})
	mux.HandleFunc("/api/v1/metadata", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{}})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

type resolverResponse struct {
	URL    string
	Status int
	Body   string
}

func resolverStub(t *testing.T, resp resolverResponse, bearerWant string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		if bearerWant != "" && r.Header.Get("Authorization") != "Bearer "+bearerWant {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if resp.Status != 0 && resp.Status/100 != 2 {
			http.Error(w, resp.Body, resp.Status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": resp.URL})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolver_StaticEndpointWhenResolverUnset(t *testing.T) {
	t.Parallel()
	prom := promStubWithNames(t, []string{"static_metric"})
	srv, err := New(Options{Endpoint: prom.URL})
	require.NoError(t, err)
	require.NoError(t, srv.refreshCatalog(context.Background()))
	snap, err := srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, prom.URL, snap.endpoint)
	assert.Contains(t, snap.catalog.names, "static_metric")
}

func TestResolver_RebindsWhenResolverReturnsNewURL(t *testing.T) {
	t.Parallel()
	initial := promStubWithNames(t, []string{"initial_metric"})
	resolverTarget := promStubWithNames(t, []string{"resolved_metric_a", "resolved_metric_b"})
	resolver := resolverStub(t, resolverResponse{URL: resolverTarget.URL}, "tok", nil)

	srv, err := New(Options{Endpoint: initial.URL, EndpointResolver: resolver.URL, LauncherToken: "tok"})
	require.NoError(t, err)
	require.NoError(t, srv.refreshCatalog(context.Background()))
	require.Equal(t, initial.URL, srv.snapshot.Load().endpoint)

	snap, err := srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, resolverTarget.URL, snap.endpoint)
	assert.ElementsMatch(t, []string{"resolved_metric_a", "resolved_metric_b"}, snap.catalog.names)
}

func TestResolver_NoOpWhenURLUnchanged(t *testing.T) {
	t.Parallel()
	prom := promStubWithNames(t, []string{"same_metric"})
	var calls atomic.Int32
	resolver := resolverStub(t, resolverResponse{URL: prom.URL}, "tok", &calls)

	srv, err := New(Options{Endpoint: prom.URL, EndpointResolver: resolver.URL, LauncherToken: "tok"})
	require.NoError(t, err)
	require.NoError(t, srv.refreshCatalog(context.Background()))
	preNames := append([]string(nil), srv.snapshot.Load().catalog.names...)

	_, err = srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	assert.Equal(t, preNames, srv.snapshot.Load().catalog.names)
}

func TestResolver_PropagatesResolverError(t *testing.T) {
	t.Parallel()
	resolver := resolverStub(t, resolverResponse{Status: http.StatusServiceUnavailable, Body: "no port-forward yet"}, "tok", nil)
	srv, err := New(Options{Endpoint: "http://127.0.0.1:0", EndpointResolver: resolver.URL, LauncherToken: "tok"})
	require.NoError(t, err)
	_, err = srv.currentSnapshot(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolver")
}

func TestResolver_SendsBearer(t *testing.T) {
	t.Parallel()
	prom := promStubWithNames(t, []string{"m"})
	var calls atomic.Int32
	resolver := resolverStub(t, resolverResponse{URL: prom.URL}, "secret", &calls)
	srv, err := New(Options{Endpoint: prom.URL, EndpointResolver: resolver.URL, LauncherToken: "secret"})
	require.NoError(t, err)
	_, err = srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestResolver_RetriesRefreshWhenCachedCatalogIsEmpty(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	promStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/label/__name__/values" {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []string{"resolved_metric"}})
			return
		}
		if r.URL.Path == "/api/v1/metadata" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{}})
		}
	}))
	t.Cleanup(promStub.Close)
	resolver := resolverStub(t, resolverResponse{URL: promStub.URL}, "tok", nil)
	srv, err := New(Options{Endpoint: promStub.URL, EndpointResolver: resolver.URL, LauncherToken: "tok"})
	require.NoError(t, err)

	// First call: resolver returns same URL as initial, catalog refresh
	// fails → snapshot retains empty catalog.
	snap, err := srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	require.Empty(t, snap.catalog.names)

	// Second call: same URL, but cached catalog is empty → retry refresh,
	// this time succeeds.
	snap, err = srv.currentSnapshot(context.Background())
	require.NoError(t, err)
	require.Contains(t, snap.catalog.names, "resolved_metric")
}

func TestNew_AllowsEmptyEndpointWhenResolverSet(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{EndpointResolver: "http://127.0.0.1:0", LauncherToken: "tok"})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestNew_RequiresEndpointOrResolver(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Endpoint")
	require.Contains(t, err.Error(), "EndpointResolver")
}
