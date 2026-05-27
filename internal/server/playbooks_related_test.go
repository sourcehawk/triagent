package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoTaggedPlaybooksMeta builds a *Meta with two playbooks that share
// the service tag "zeebe" — playbook "a" (queried) and "b" (expected match).
func twoTaggedPlaybooksMeta() *Meta {
	yamlA := `id: a
symptom: zeebe down
description: Zeebe broker is unresponsive
entrypoint: n1
services:
  - zeebe
nodes:
  n1:
    description: check broker
`
	yamlB := `id: b
symptom: zeebe rebalance failing
description: Zeebe rebalance CronJob errors
entrypoint: n1
services:
  - zeebe
nodes:
  n1:
    description: check rebalance
`
	return &Meta{
		Playbooks: map[string]MetaPlaybook{
			"a": {YAML: yamlA, Source: "plugin", Type: "investigation"},
			"b": {YAML: yamlB, Source: "plugin", Type: "investigation"},
		},
	}
}

// TestRelatedPlaybooks_ReturnsCorrelatedMatches: two playbooks share a
// tag; hitting GET /api/playbooks/a/related must return b (not a) with
// a positive score.
func TestRelatedPlaybooks_ReturnsCorrelatedMatches(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	a := &apiHandlers{
		metaCache: func() *metaCache {
			c := &metaCache{}
			c.set(twoTaggedPlaybooksMeta())
			return c
		}(),
	}
	a.register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/a/related", nil)
	req.SetPathValue("id", "a")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	var resp struct {
		Related []struct {
			ID    string `json:"id"`
			Score int    `json:"score"`
		} `json:"related"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Related, 1, "expect exactly one related playbook")
	assert.Equal(t, "b", resp.Related[0].ID, "the related playbook must be b, not a")
	assert.Greater(t, resp.Related[0].Score, 0, "score must be positive")
}

// TestRelatedPlaybooks_UntaggedPlaybookReturnsEmpty: an untagged playbook
// returns an empty related slice with 200 OK.
func TestRelatedPlaybooks_UntaggedPlaybookReturnsEmpty(t *testing.T) {
	t.Parallel()
	untaggedYAML := `id: x
symptom: generic thing
entrypoint: n1
nodes:
  n1:
    description: check it
`
	meta := &Meta{
		Playbooks: map[string]MetaPlaybook{
			"x": {YAML: untaggedYAML, Source: "plugin", Type: "investigation"},
			"y": {YAML: `id: y
symptom: other thing
entrypoint: n1
services:
  - some-service
nodes:
  n1:
    description: check y
`, Source: "plugin", Type: "investigation"},
		},
	}
	mux := http.NewServeMux()
	a := &apiHandlers{
		metaCache: func() *metaCache {
			c := &metaCache{}
			c.set(meta)
			return c
		}(),
	}
	a.register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/x/related", nil)
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	var resp struct {
		Related []interface{} `json:"related"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Related, "untagged playbook must yield no related results")
}

// TestRelatedPlaybooks_LiftsViaDelegateTo: a parent playbook with no
// direct tags but delegate_to a child that IS tagged should still
// appear in the related list of any playbook sharing that child's tag.
// This is the lifting feature the spec requires.
func TestRelatedPlaybooks_LiftsViaDelegateTo(t *testing.T) {
	t.Parallel()
	// Setup: three playbooks
	//   "elasticsearch": tagged services=[es-data]
	//   "cluster_health": NO direct tags, but its start node has
	//     delegate_to=elasticsearch
	//   "search_outage": tagged services=[es-data]
	//
	// Hitting GET /api/playbooks/search_outage/related should return
	// BOTH elasticsearch (direct, score 3) AND cluster_health (lifted
	// via elasticsearch, score 1).

	yamlElasticsearch := `id: elasticsearch
symptom: Elasticsearch data node problems
entrypoint: n1
services:
  - es-data
nodes:
  n1:
    description: check es-data health
`
	yamlClusterHealth := `id: cluster_health
symptom: General cluster health check
entrypoint: start
nodes:
  start:
    description: Assess overall cluster health
    delegate_to: elasticsearch
    next:
      - goto: done
        condition: always
  done:
    description: Health check done
`
	yamlSearchOutage := `id: search_outage
symptom: Search service outage
entrypoint: n1
services:
  - es-data
nodes:
  n1:
    description: check search service
`
	meta := &Meta{
		Playbooks: map[string]MetaPlaybook{
			"elasticsearch":  {YAML: yamlElasticsearch, Source: "plugin", Type: "investigation"},
			"cluster_health": {YAML: yamlClusterHealth, Source: "plugin", Type: "investigation"},
			"search_outage":  {YAML: yamlSearchOutage, Source: "plugin", Type: "investigation"},
		},
	}

	mux := http.NewServeMux()
	a := &apiHandlers{
		metaCache: func() *metaCache {
			c := &metaCache{}
			c.set(meta)
			return c
		}(),
	}
	a.register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/search_outage/related", nil)
	req.SetPathValue("id", "search_outage")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	var resp struct {
		Related []struct {
			ID        string `json:"id"`
			Score     int    `json:"score"`
			MatchPath struct {
				Direct []string `json:"direct"`
				Lifted []struct {
					Entity string `json:"entity"`
					Via    string `json:"via"`
				} `json:"lifted"`
			} `json:"match_path"`
		} `json:"related"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	byID := map[string]struct {
		Score     int
		MatchPath struct {
			Direct []string `json:"direct"`
			Lifted []struct {
				Entity string `json:"entity"`
				Via    string `json:"via"`
			} `json:"lifted"`
		}
	}{}
	for _, r := range resp.Related {
		byID[r.ID] = struct {
			Score     int
			MatchPath struct {
				Direct []string `json:"direct"`
				Lifted []struct {
					Entity string `json:"entity"`
					Via    string `json:"via"`
				} `json:"lifted"`
			}
		}{Score: r.Score, MatchPath: r.MatchPath}
	}

	// elasticsearch: direct hit on es-data, score 3
	es, ok := byID["elasticsearch"]
	require.True(t, ok, "elasticsearch must appear in related (direct hit)")
	assert.Equal(t, 3, es.Score, "elasticsearch direct score must be 3")
	assert.Contains(t, es.MatchPath.Direct, "es-data", "elasticsearch direct match must include es-data")

	// cluster_health: lifted via elasticsearch (es-data on child), score 1
	ch, ok := byID["cluster_health"]
	require.True(t, ok, "cluster_health must appear in related (lifted via elasticsearch)")
	assert.Equal(t, 1, ch.Score, "cluster_health lifted score must be 1")
	require.Len(t, ch.MatchPath.Lifted, 1, "cluster_health must have one lifted entry")
	assert.Equal(t, "es-data", ch.MatchPath.Lifted[0].Entity)
	assert.Equal(t, "elasticsearch", ch.MatchPath.Lifted[0].Via)

	// elasticsearch must rank above cluster_health (higher score)
	require.GreaterOrEqual(t, len(resp.Related), 2)
	assert.Equal(t, "elasticsearch", resp.Related[0].ID, "elasticsearch must rank first (higher score)")
}

// TestRelatedPlaybooks_UnknownIDReturns404: GETting a non-existent
// playbook id must return 404.
func TestRelatedPlaybooks_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	a := &apiHandlers{
		metaCache: func() *metaCache {
			c := &metaCache{}
			c.set(&Meta{Playbooks: map[string]MetaPlaybook{}})
			return c
		}(),
	}
	a.register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/does-not-exist/related", nil)
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
