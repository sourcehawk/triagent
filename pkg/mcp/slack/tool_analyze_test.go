package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeChannel_PromptsSubAgentAndReturnsCitations(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"500.000100","user":"U1","text":"parent A","reply_count":0}
			],"response_metadata":{"next_cursor":""}}`))
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `"}}`))
		}
	}))
	defer stub.Close()

	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	var capturedPrompt string
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		capturedPrompt = prompt
		return subAgentResult{
			Summary: "rolled back at 14:02 [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"500.000100\"}]\nCITATIONS>>>",
		}, nil
	}

	_, out, err := srv.handleAnalyzeChannel(context.Background(), nil, analyzeChannelIn{
		ChannelID:       "C1",
		DesiredFindings: "list services degraded",
	})
	require.NoError(t, err)
	assert.Contains(t, out.Summary, "rolled back at 14:02 [1]")
	require.Len(t, out.Citations, 1)
	assert.Equal(t, "500.000100", out.Citations[0].ThreadTS)
	assert.Empty(t, out.CitationsParseError)
	assert.Contains(t, capturedPrompt, "list services degraded", "prompt missing desired_findings")
	assert.Contains(t, capturedPrompt, "<<<CITATIONS", "prompt must instruct the sub-agent to emit a citations block")
	assert.Contains(t, capturedPrompt, "Grep", "prompt must instruct self-verification via Grep")
}

func TestAnalyzeChannel_RequiresChannelID(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleAnalyzeChannel(context.Background(), nil, analyzeChannelIn{DesiredFindings: "list svcs"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestAnalyzeChannel_RequiresDesiredFindings(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleAnalyzeChannel(context.Background(), nil, analyzeChannelIn{ChannelID: "C1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}
