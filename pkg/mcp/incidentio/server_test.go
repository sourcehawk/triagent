package incidentio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubServer returns an httptest.Server that serves canned responses
// keyed by URL path. The test passes a func to set up routes; missing
// paths get 404.
func stubServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestNew_RejectsMissingToken(t *testing.T) {
	_, err := New(context.Background(), Options{})
	require.Error(t, err, "want error for missing token")
}

func TestNew_BootsWithoutIncidentScope(t *testing.T) {
	// Incident scope is per-call now; the MCP must boot on token alone.
	srv, err := New(context.Background(), Options{Token: "x", APIBase: "http://unused"})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestGetIncident_HappyPath(t *testing.T) {
	stub := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v2/incidents/427"):
			_, _ = w.Write([]byte(`{"incident":{
				"id":"abc-uuid",
				"reference":"INC-427",
				"name":"Broker OOM",
				"summary":"zeebe-broker keeps OOMing",
				"severity":{"name":"sev2"},
				"incident_status":{"name":"closed"},
				"created_at":"2026-04-01T10:00:00Z",
				"reported_at":"2026-04-01T09:55:00Z",
				"closed_at":"2026-04-01T13:00:00Z",
				"permalink_url":"https://app.incident.io/acme/incidents/427",
				"incident_role_assignments":[
					{"role":{"name":"Lead"},"assignee":{"name":"Alice"}}
				],
				"custom_field_entries":[
					{"custom_field":{"id":"cf1","name":"Affected service"},"values":[{"value_text":"zeebe-broker"}]}
				]
			}}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer stub.Close()

	srv, err := New(context.Background(), Options{Token: "x", APIBase: stub.URL})
	require.NoError(t, err)
	_, out, err := srv.handleGetIncident(context.Background(), nil, getIncidentIn{IncidentID: "427"})
	require.NoError(t, err)
	assert.Equal(t, "INC-427", out.Incident.Reference)
	assert.Equal(t, "sev2", out.Incident.Severity)
	require.Len(t, out.Incident.IncidentRoles, 1)
	assert.Equal(t, "Alice", out.Incident.IncidentRoles[0].UserName)
	require.Len(t, out.Incident.CustomFields, 1)
	assert.Equal(t, "zeebe-broker", out.Incident.CustomFields[0].Value)
}

func TestGetIncident_RequiresIncidentID(t *testing.T) {
	srv, err := New(context.Background(), Options{Token: "x", APIBase: "http://unused"})
	require.NoError(t, err)
	res, _, err := srv.handleGetIncident(context.Background(), nil, getIncidentIn{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestGetTimeline_MergesAndSorts(t *testing.T) {
	stub := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/incidents/1":
			_, _ = w.Write([]byte(`{"incident":{"id":"u1","reference":"INC-1","name":"x","severity":{"name":"sev2"},"incident_status":{"name":"closed"}}}`))
		case "/v2/incident_updates":
			_, _ = w.Write([]byte(`{"incident_updates":[
				{"id":"u1","created_at":"2026-04-01T11:00:00Z","message":"investigating","new_status":{"name":"investigating"},"updater":{"user":{"name":"Alice"}}}
			]}`))
		case "/v2/actions":
			_, _ = w.Write([]byte(`{"actions":[
				{"id":"a1","created_at":"2026-04-01T10:30:00Z","description":"page on-call","status":"done","assignee":{"name":"Bob"}}
			]}`))
		case "/v2/follow_ups":
			_, _ = w.Write([]byte(`{"follow_ups":[
				{"id":"f1","created_at":"2026-04-01T13:00:00Z","title":"add OOM alert","description":"prevent recurrence","status":"open"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer stub.Close()

	srv, err := New(context.Background(), Options{Token: "x", APIBase: stub.URL})
	require.NoError(t, err)
	_, out, err := srv.handleGetTimeline(context.Background(), nil, getTimelineIn{IncidentID: "1"})
	require.NoError(t, err)
	require.Len(t, out.Entries, 3)
	assert.Equal(t, "action", out.Entries[0].Kind)
	assert.Equal(t, "update", out.Entries[1].Kind)
	assert.Equal(t, "follow_up", out.Entries[2].Kind)
}

func TestGetPostmortem_NoneReturnsEmpty(t *testing.T) {
	stub := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/incidents/1":
			_, _ = w.Write([]byte(`{"incident":{"id":"u1","reference":"INC-1","name":"x","severity":{"name":"sev2"},"incident_status":{"name":"closed"}}}`))
		case "/v1/postmortem_documents":
			_, _ = w.Write([]byte(`{"postmortem_documents":[]}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer stub.Close()

	srv, _ := New(context.Background(), Options{Token: "x", APIBase: stub.URL})
	_, out, err := srv.handleGetPostmortem(context.Background(), nil, getPostmortemIn{IncidentID: "1"})
	require.NoError(t, err)
	assert.True(t, out.Empty, "want Empty=true")
}

func TestSearchRelated_FiltersByCustomField(t *testing.T) {
	calls := map[string]int{}
	stub := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/v2/incidents/1":
			_, _ = w.Write([]byte(`{"incident":{
				"id":"u1","reference":"INC-1","name":"x",
				"severity":{"name":"sev2"},"incident_status":{"name":"closed"},
				"custom_field_entries":[
					{"custom_field":{"id":"cf-svc","name":"Affected service"},"values":[{"value_text":"zeebe-broker"}]}
				]
			}}`))
		case "/v2/incidents":
			_, _ = w.Write([]byte(`{"incidents":[
				{"id":"u1","reference":"INC-1","name":"the-current-incident","severity":{"name":"sev2"},"incident_status":{"name":"closed"}},
				{"id":"u2","reference":"INC-7","name":"earlier broker oom","severity":{"name":"sev3"},"incident_status":{"name":"closed"}}
			]}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer stub.Close()

	srv, _ := New(context.Background(), Options{Token: "x", APIBase: stub.URL})
	_, out, err := srv.handleSearchRelated(context.Background(), nil, searchRelatedIn{IncidentID: "1", By: "service", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, "Affected service", out.Field)
	assert.Equal(t, "zeebe-broker", out.Value)
	require.Len(t, out.Related, 1, "self-reference not dropped")
	assert.Equal(t, "INC-7", out.Related[0].Reference)
}

func TestSearchRelated_RequiresIncidentID(t *testing.T) {
	srv, _ := New(context.Background(), Options{Token: "x", APIBase: "http://unused"})
	res, _, err := srv.handleSearchRelated(context.Background(), nil, searchRelatedIn{By: "service"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "want error result for missing incident_id")
}

func TestSearchRelated_RequiresBy(t *testing.T) {
	stub := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/incidents/1":
			_, _ = w.Write([]byte(`{"incident":{"id":"u1","reference":"INC-1","name":"x","severity":{"name":"sev2"},"incident_status":{"name":"closed"}}}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer stub.Close()

	srv, _ := New(context.Background(), Options{Token: "x", APIBase: stub.URL})
	res, _, err := srv.handleSearchRelated(context.Background(), nil, searchRelatedIn{IncidentID: "1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "want error result for missing 'by'")
}
