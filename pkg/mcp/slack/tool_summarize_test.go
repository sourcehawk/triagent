package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeThread_RefetchesAndPromptsSubAgent(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"500.000100","user":"U1","text":"parent"},
				{"ts":"510.000100","user":"U2","text":"reply"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `"}}`))
		}
	}))
	defer stub.Close()

	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	var capturedPrompt, capturedWorkingDir string
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		capturedPrompt = prompt
		capturedWorkingDir = workingDir
		return subAgentResult{
			Summary: "stub summary [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"500.000100\"}]\nCITATIONS>>>",
		}, nil
	}

	_, out, err := srv.handleSummarizeThread(context.Background(), nil, summarizeThreadIn{
		ChannelID:       "C1",
		ThreadTS:        "500.000100",
		DesiredFindings: "what was decided",
	})
	require.NoError(t, err)
	assert.Contains(t, out.Summary, "stub summary [1]")
	require.Len(t, out.Citations, 1)
	assert.Equal(t, "500.000100", out.Citations[0].ThreadTS)
	assert.Empty(t, out.CitationsParseError)
	assert.Contains(t, capturedPrompt, "what was decided", "prompt missing desired_findings")
	assert.Contains(t, capturedPrompt, "threads/500.000100.md", "prompt missing thread file path")
	assert.Contains(t, capturedPrompt, "<<<CITATIONS", "prompt must instruct the sub-agent to emit a citations block")
	assert.Contains(t, capturedPrompt, "Grep", "prompt must instruct self-verification via Grep")

	store, err := srv.resolveStore("C1")
	require.NoError(t, err)
	assert.Equal(t, store.Dir(), capturedWorkingDir, "sub-agent working dir must be the resolved channel's store dir")
	_, statErr := os.Stat(filepath.Join(store.Dir(), "threads", "500.000100.md"))
	require.NoError(t, statErr, "thread file not materialised")
}

func TestSummarizeThread_SoftFailsOnInvalidCitations(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"500.000100","user":"U1","text":"parent"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `"}}`))
		}
	}))
	defer stub.Close()

	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		return subAgentResult{
			Summary: "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"999.000000\"}]\nCITATIONS>>>",
		}, nil
	}
	_, out, err := srv.handleSummarizeThread(context.Background(), nil, summarizeThreadIn{
		ChannelID:       "C1",
		ThreadTS:        "500.000100",
		DesiredFindings: "what was decided",
	})
	require.NoError(t, err)
	assert.Empty(t, out.Citations)
	assert.NotEmpty(t, out.CitationsParseError)
	assert.Contains(t, out.Summary, "claim [1]", "prose still surfaced on soft-fail")
}

func TestSummarizeThread_RequiresAllInputs(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})

	res, _, err := srv.handleSummarizeThread(context.Background(), nil, summarizeThreadIn{ThreadTS: "1.0", DesiredFindings: "x"})
	require.NoError(t, err)
	require.NotNil(t, res, "want error result for missing channel_id")
	assert.True(t, res.IsError)

	res, _, err = srv.handleSummarizeThread(context.Background(), nil, summarizeThreadIn{ChannelID: "C1", ThreadTS: "1.0"})
	require.NoError(t, err)
	require.NotNil(t, res, "want error result for missing desired_findings")
	assert.True(t, res.IsError)

	res, _, err = srv.handleSummarizeThread(context.Background(), nil, summarizeThreadIn{ChannelID: "C1", DesiredFindings: "x"})
	require.NoError(t, err)
	require.NotNil(t, res, "want error result for missing thread_ts")
	assert.True(t, res.IsError)
}
