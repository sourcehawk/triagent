package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleGetRepoSummary_Found(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	require.NoError(t, repos.WriteSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"), &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{
			GeneratedAt: time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC),
			Kind:        "freeform",
			ByteCount:   42,
		},
		Body: "# operate\n",
	}))

	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummary(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		Exists  bool   `json:"exists"`
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.True(t, body.Exists)
	assert.Equal(t, "freeform", body.Kind)
	assert.Contains(t, body.Content, "# operate")
}

func TestHandleGetRepoSummary_NotFound(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{opts: Options{GitCacheDir: t.TempDir()}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummary(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "200 with exists=false, NOT 404 (frontend treats both states uniformly)")
	var body struct {
		Exists bool   `json:"exists"`
		Hint   string `json:"hint"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.False(t, body.Exists)
	assert.NotEmpty(t, body.Hint)
}

func TestHandleRefreshRepoSummary_Started(t *testing.T) {
	t.Parallel()
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		// Block briefly so the in-flight state is observable in the next test
		// case — but not so long as to slow this one. The 50ms is non-flaky
		// because we don't assert on timing.
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	a := &apiHandlers{architectureWorker: w}
	req := httptest.NewRequest(http.MethodPost, "/api/repos/example-org/example-app/summary/refresh", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleRefreshRepoSummary(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
}

func TestHandleRefreshRepoSummary_Conflict_WhenInFlight(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		<-hold
		return nil
	})
	require.True(t, w.GenerateAsync(context.Background(), "example-org", "example-app", repos.GenerateRequest{}))

	a := &apiHandlers{architectureWorker: w}
	req := httptest.NewRequest(http.MethodPost, "/api/repos/example-org/example-app/summary/refresh", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleRefreshRepoSummary(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
	close(hold)
}

func TestHandleAddRepo_EnqueuesAutoGen(t *testing.T) {
	t.Parallel()
	called := make(chan struct{}, 1)
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		assert.Equal(t, "my-org", owner)
		assert.Equal(t, "my-repo", name)
		assert.Equal(t, "freeform", opts.Kind)
		called <- struct{}{}
		return nil
	})

	dir := t.TempDir()
	a := &apiHandlers{
		architectureWorker: w,
		opts: Options{
			UserReposPath: filepath.Join(dir, "user_repos.yaml"),
		},
	}

	body := strings.NewReader(`{"owner":"my-org","name":"my-repo","description":"A reasonable description that clears the 30-character minimum."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleAddRepo(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("auto-gen runFn was not invoked")
	}
}

func TestHandleGetRepoSummaryStatus_Found(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	when := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	require.NoError(t, repos.WriteSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"), &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{
			GeneratedAt: when,
			Kind:        "freeform",
			ByteCount:   8421,
		},
		Body: "# operate\n",
	}))

	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/status", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryStatus(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		Exists      bool   `json:"exists"`
		GeneratedAt string `json:"generatedAt"`
		ByteCount   int    `json:"byteCount"`
		Kind        string `json:"kind"`
		InFlight    bool   `json:"inFlight"`
		Error       string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.True(t, body.Exists)
	assert.Equal(t, "freeform", body.Kind)
	assert.Equal(t, 8421, body.ByteCount)
	assert.False(t, body.InFlight)
}

func TestHandleGetRepoSummaryStatus_NotFound(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{opts: Options{GitCacheDir: t.TempDir()}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/status", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryStatus(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "missing cache returns 200 + exists=false (consistent with full-summary endpoint)")
	var body struct {
		Exists   bool `json:"exists"`
		InFlight bool `json:"inFlight"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.False(t, body.Exists)
	assert.False(t, body.InFlight)
}

func TestHandleGetRepoSummaryStatus_InFlight(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		<-hold
		return nil
	})
	require.True(t, w.GenerateAsync(context.Background(), "example-org", "example-app", repos.GenerateRequest{}))

	a := &apiHandlers{
		opts:               Options{GitCacheDir: t.TempDir()},
		architectureWorker: w,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/status", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryStatus(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		Exists   bool `json:"exists"`
		InFlight bool `json:"inFlight"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.False(t, body.Exists, "no cache file yet — generation still running")
	assert.True(t, body.InFlight, "in-flight flag must reflect worker state")
	close(hold)
}

func TestHandleUpdateRepoSummary_WritesCacheFile(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}

	body := strings.NewReader(`{"content":"# example-org/example-app\n\n## Top-level structure\n\nexample-app is …\n","kind":"freeform"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/repos/example-org/example-app/summary", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleUpdateRepoSummary(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	got, err := repos.ReadSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"))
	require.NoError(t, err)
	assert.Contains(t, got.Body, "## Top-level structure")
	assert.Equal(t, "freeform", got.Frontmatter.Kind)
	assert.Equal(t, "operator-edit", got.Frontmatter.Model)
	assert.Equal(t, len(got.Body), got.Frontmatter.ByteCount)
}

func TestHandleUpdateRepoSummary_RejectsEmptyContent(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{opts: Options{GitCacheDir: t.TempDir()}}
	body := strings.NewReader(`{"content":"","kind":"freeform"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/repos/example-org/example-app/summary", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleUpdateRepoSummary(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleUpdateRepoSummary_ConflictWhenInFlight(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		<-hold
		return nil
	})
	require.True(t, w.GenerateAsync(context.Background(), "example-org", "example-app", repos.GenerateRequest{}))

	a := &apiHandlers{
		opts:               Options{GitCacheDir: t.TempDir()},
		architectureWorker: w,
	}
	body := strings.NewReader(`{"content":"# foo\n","kind":"freeform"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/repos/example-org/example-app/summary", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleUpdateRepoSummary(rr, req)
	assert.Equal(t, http.StatusConflict, rr.Code)
	close(hold)
}

func TestHandleAddRepo_ResponseCarriesSummaryStatusPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := repos.NewArchitectureWorkerForTest(func(ctx context.Context, owner, name string, opts repos.GenerateRequest) error {
		// Hold the generation so summaryStatus stays pending while we read the response.
		select {}
	})
	a := &apiHandlers{
		architectureWorker: w,
		opts: Options{
			UserReposPath: filepath.Join(dir, "user_repos.yaml"),
		},
	}

	body := strings.NewReader(`{"owner":"my-org","name":"my-repo","description":"A reasonable description that clears the 30-character minimum."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleAddRepo(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp struct {
		Owner         string `json:"owner"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		SummaryStatus string `json:"summaryStatus"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "my-org", resp.Owner)
	assert.Equal(t, "my-repo", resp.Name)
	assert.Equal(t, "pending", resp.SummaryStatus,
		"add response must signal that auto-gen is in flight")
}

func TestHandleAddRepo_NoWorker_OmitsSummaryStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{
		opts: Options{UserReposPath: filepath.Join(dir, "user_repos.yaml")},
	}

	body := strings.NewReader(`{"owner":"my-org","name":"my-repo","description":"A reasonable description that clears the 30-character minimum."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repos", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleAddRepo(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code)
	var resp struct {
		Owner         string `json:"owner"`
		SummaryStatus string `json:"summaryStatus"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "my-org", resp.Owner)
	assert.Empty(t, resp.SummaryStatus, "no worker → no auto-gen → no pending hint")
}

// handleGetRepoSummaryEdits returns the diff between the active body
// and the AI-generated baseline. Tests cover the four states the
// frontend distinguishes: no active summary at all, active matches
// baseline, no baseline written yet, and active diverged from baseline.

func TestHandleGetRepoSummaryEdits_NoSummary(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{opts: Options{GitCacheDir: t.TempDir()}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/edits", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryEdits(rr, req)

	require.Equal(t, http.StatusOK, rr.Code,
		"missing summary returns 200+hasEdits=false (consistent with sibling endpoints)")
	var body struct {
		HasEdits bool `json:"hasEdits"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.False(t, body.HasEdits)
}

func TestHandleGetRepoSummaryEdits_NoBaseline(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	// Active file present, but no baseline (operator hand-authored
	// without a prior AI run). Endpoint must still return cleanly.
	require.NoError(t, repos.WriteSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"), &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{
			GeneratedAt: time.Now().UTC(),
			Kind:        "freeform",
		},
		Body: "# operate\n",
	}))

	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/edits", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryEdits(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		HasEdits bool   `json:"hasEdits"`
		Diff     string `json:"diff"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.False(t, body.HasEdits, "no baseline → nothing to diff against → hasEdits=false")
	assert.Empty(t, body.Diff)
}

func TestHandleGetRepoSummaryEdits_BaselineMatchesActive(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	body := "# match\n\nthe same\n"
	require.NoError(t, repos.WriteSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"), &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{GeneratedAt: time.Now().UTC(), Kind: "freeform"},
		Body:        body,
	}))
	require.NoError(t, repos.WriteBaselineBody(repos.BaselinePath(cacheDir, "example-org", "example-app"), body))

	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/edits", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryEdits(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		HasEdits bool `json:"hasEdits"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.False(t, resp.HasEdits, "identical bodies → hasEdits=false")
}

func TestHandleGetRepoSummaryEdits_DivergedActive(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	baseline := "# h1\n\n## section\n\nline A\nline B\nline C\n"
	active := "# h1\n\n## section\n\nline A\noperator-added line\nline B\nline C\n"
	require.NoError(t, repos.WriteSummary(repos.SummaryPath(cacheDir, "example-org", "example-app"), &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{GeneratedAt: time.Now().UTC(), Kind: "freeform"},
		Body:        active,
	}))
	require.NoError(t, repos.WriteBaselineBody(repos.BaselinePath(cacheDir, "example-org", "example-app"), baseline))

	a := &apiHandlers{opts: Options{GitCacheDir: cacheDir}}
	req := httptest.NewRequest(http.MethodGet, "/api/repos/example-org/example-app/summary/edits", nil)
	req.SetPathValue("owner", "example-org")
	req.SetPathValue("name", "example-app")
	rr := httptest.NewRecorder()
	a.handleGetRepoSummaryEdits(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		HasEdits bool   `json:"hasEdits"`
		Diff     string `json:"diff"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.True(t, resp.HasEdits)
	assert.Contains(t, resp.Diff, "+operator-added line",
		"diff must surface the inserted line")
	assert.Contains(t, resp.Diff, "@@",
		"endpoint must return a unified diff, not a side-by-side")
}

