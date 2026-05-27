package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPlaybookFile writes <dir>/<typeName>/<id>.yaml with the given body
// and returns the relative path used by git helpers.
func seedPlaybookFile(t *testing.T, dir, typeName, id, body string) string {
	t.Helper()
	typeDir := filepath.Join(dir, typeName)
	require.NoError(t, os.MkdirAll(typeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(typeDir, id+".yaml"), []byte(body), 0o644))
	return filepath.Join(typeName, id+".yaml")
}

// newVersionsHandlersFixture builds an apiHandlers with an initialised git
// repo in a temp user dir containing one playbook.  It returns the handler,
// the user dir path, and the relative path of the seeded file.
func newVersionsHandlersFixture(t *testing.T) (a *apiHandlers, userDir string, relPath string) {
	t.Helper()
	skipIfNoGit(t)

	userDir = t.TempDir()
	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), userDir))

	const (
		typeName = "investigation"
		id       = "broker-crashloop"
	)

	relPath = seedPlaybookFile(t, userDir, typeName, id, "id: broker-crashloop\nactive: true\n")
	require.NoError(t, commitPlaybookChange(context.Background(), userDir, relPath, "broker-crashloop: initial"))

	a = &apiHandlers{
		opts:      Options{UserPlaybooksDir: userDir},
		metaCache: &metaCache{},
	}
	return a, userDir, relPath
}

// TestHandleListPlaybookCommits_ReturnsLocalCommits verifies that the list
// endpoint returns commits from the local user dir in newest-first order.
func TestHandleListPlaybookCommits_ReturnsLocalCommits(t *testing.T) {
	t.Parallel()
	a, userDir, relPath := newVersionsHandlersFixture(t)

	// Add a second commit.
	fullPath := filepath.Join(userDir, relPath)
	require.NoError(t, os.WriteFile(fullPath, []byte("id: broker-crashloop\nactive: true\nv: 2\n"), 0o644))
	require.NoError(t, commitPlaybookChange(context.Background(), userDir, relPath, "broker-crashloop: update"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits", nil)
	req.SetPathValue("id", "broker-crashloop")
	a.handleListPlaybookCommits(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Commits []playbookCommit `json:"commits"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.GreaterOrEqual(t, len(body.Commits), 2, "expect at least 2 commits")
	assert.Equal(t, "broker-crashloop: update", body.Commits[0].Summary, "newest first")
	assert.Equal(t, "local", body.Commits[0].Source)
}

// TestHandleListPlaybookCommits_LimitParam verifies the ?limit= query
// parameter caps the returned list.
func TestHandleListPlaybookCommits_LimitParam(t *testing.T) {
	t.Parallel()
	a, userDir, relPath := newVersionsHandlersFixture(t)

	// Add 3 more commits (total ≥ 4 for the file).
	for i := 2; i <= 4; i++ {
		content := "id: broker-crashloop\nactive: true\nv: " + string(rune('0'+i)) + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(userDir, relPath), []byte(content), 0o644))
		require.NoError(t, commitPlaybookChange(context.Background(), userDir, relPath, "broker-crashloop: bump"))
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits?limit=2", nil)
	req.SetPathValue("id", "broker-crashloop")
	a.handleListPlaybookCommits(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Commits []playbookCommit `json:"commits"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body.Commits, 2, "response must respect ?limit=2")
}

// TestHandleListPlaybookCommits_RespectsBefore verifies that the ?before=
// cursor excludes the named commit and everything newer.
func TestHandleListPlaybookCommits_RespectsBefore(t *testing.T) {
	t.Parallel()
	a, userDir, relPath := newVersionsHandlersFixture(t)

	// Add two more commits after the initial one.
	for _, msg := range []string{"broker-crashloop: v2", "broker-crashloop: v3"} {
		content := "id: broker-crashloop\nactive: true\n# " + msg + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(userDir, relPath), []byte(content), 0o644))
		require.NoError(t, commitPlaybookChange(context.Background(), userDir, relPath, msg))
	}

	// Get all to find the sha of the newest commit.
	allCommits, err := gitLogForPlaybook(context.Background(), userDir, relPath, "local", "", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(allCommits), 3)
	newestSha := allCommits[0].Sha

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits?before="+newestSha, nil)
	req.SetPathValue("id", "broker-crashloop")
	a.handleListPlaybookCommits(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Commits []playbookCommit `json:"commits"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	for _, c := range body.Commits {
		assert.NotEqual(t, newestSha, c.Sha, "before= sha must not appear in the response")
	}
	assert.GreaterOrEqual(t, len(body.Commits), 1, "should have at least one older commit")
}

// TestHandleListPlaybookCommits_MergesLocalAndUpstream builds two repos and
// verifies the merged list carries entries from both with correct source tags.
func TestHandleListPlaybookCommits_MergesLocalAndUpstream(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)

	userDir := t.TempDir()
	upstreamDir := t.TempDir()

	const (
		typeName = "investigation"
		id       = "broker-crashloop"
	)
	relPath := filepath.Join(typeName, id+".yaml")

	// Init both repos and write one commit in each.
	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), userDir))
	seedPlaybookFile(t, userDir, typeName, id, "id: broker-crashloop\n# local v1\n")
	require.NoError(t, commitPlaybookChange(context.Background(), userDir, relPath, "broker-crashloop: local"))

	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), upstreamDir))
	seedPlaybookFile(t, upstreamDir, typeName, id, "id: broker-crashloop\n# upstream v1\n")
	require.NoError(t, commitPlaybookChange(context.Background(), upstreamDir, relPath, "broker-crashloop: upstream"))

	a := &apiHandlers{
		opts: Options{
			UserPlaybooksDir:   userDir,
			PluginPlaybooksDir: upstreamDir,
		},
		metaCache: &metaCache{},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits", nil)
	req.SetPathValue("id", "broker-crashloop")
	a.handleListPlaybookCommits(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Commits []playbookCommit `json:"commits"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	sources := map[string]bool{}
	for _, c := range body.Commits {
		sources[c.Source] = true
	}
	assert.True(t, sources["local"], "merged list must contain local commits")
	assert.True(t, sources["upstream"], "merged list must contain upstream commits")
}

// TestHandleGetPlaybookCommit_Local seeds a committed file, retrieves it at
// its sha, and verifies the body and source=local come back correctly.
func TestHandleGetPlaybookCommit_Local(t *testing.T) {
	t.Parallel()
	a, userDir, relPath := newVersionsHandlersFixture(t)

	// Find the sha of the only commit touching the file.
	commits, err := gitLogForPlaybook(context.Background(), userDir, relPath, "local", "", 5)
	require.NoError(t, err)
	require.NotEmpty(t, commits)
	sha := commits[0].Sha

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits/"+sha, nil)
	req.SetPathValue("id", "broker-crashloop")
	req.SetPathValue("sha", sha)
	a.handleGetPlaybookCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "local", body["source"])
	assert.Equal(t, sha, body["sha"])
	yamlBody, ok := body["yaml"].(string)
	require.True(t, ok, "yaml field must be a string")
	assert.Contains(t, yamlBody, "broker-crashloop")
}

// TestHandleGetPlaybookCommit_NotFound verifies that an unknown sha returns
// 404.
func TestHandleGetPlaybookCommit_NotFound(t *testing.T) {
	t.Parallel()
	a, _, _ := newVersionsHandlersFixture(t)

	unknownSha := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/broker-crashloop/commits/"+unknownSha, nil)
	req.SetPathValue("id", "broker-crashloop")
	req.SetPathValue("sha", unknownSha)
	a.handleGetPlaybookCommit(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleGetPlaybookCommit_UnknownID verifies that a request for an id
// not present in either user dir or metaCache returns 404.
func TestHandleGetPlaybookCommit_UnknownID(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	a := &apiHandlers{
		opts:      Options{UserPlaybooksDir: dir},
		metaCache: &metaCache{},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbooks/nonexistent/commits/abc123", nil)
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("sha", "abc123")
	a.handleGetPlaybookCommit(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestMergePlaybookCommits_MergesAndCaps is a pure-unit test for the
// merge helper: two slices with overlapping dates are merged newest-first
// and capped at the limit.
func TestMergePlaybookCommits_MergesAndCaps(t *testing.T) {
	t.Parallel()
	local := []playbookCommit{
		{Sha: "aaa", Date: "2026-05-08T10:00:00Z", Summary: "local newest", Source: "local"},
		{Sha: "bbb", Date: "2026-05-08T08:00:00Z", Summary: "local older", Source: "local"},
	}
	upstream := []playbookCommit{
		{Sha: "ccc", Date: "2026-05-08T09:00:00Z", Summary: "upstream mid", Source: "upstream"},
		{Sha: "ddd", Date: "2026-05-08T07:00:00Z", Summary: "upstream oldest", Source: "upstream"},
	}

	merged := mergePlaybookCommits(local, upstream, 3)
	require.Len(t, merged, 3, "capped at limit=3")
	assert.Equal(t, "aaa", merged[0].Sha, "newest overall")
	assert.Equal(t, "ccc", merged[1].Sha, "upstream interleaved")
	assert.Equal(t, "bbb", merged[2].Sha, "local older before upstream oldest")
}
