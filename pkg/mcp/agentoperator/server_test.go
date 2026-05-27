package agentoperator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_ReturnsServer(t *testing.T) {
	srv, err := New(Options{LauncherURL: "http://127.0.0.1:1234/api/internal/tool-events", TraceID: "inv-1", LauncherToken: "t"})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	srv, err := New(Options{LauncherURL: "http://127.0.0.1:1234/api/internal/tool-events", TraceID: "inv-1", LauncherToken: "t"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Run returns once stdio EOFs or ctx cancels. With ctx already cancelled,
	// this should return promptly with an error wrapping ctx.Err() or nil.
	_ = srv.Run(ctx) // no panic = pass
}

func TestSendMessage_PostsToLauncher(t *testing.T) {
	var got struct {
		Path   string
		Bearer string
		Body   string
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Path = r.URL.Path
		got.Bearer = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		got.Body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)

	srv, err := New(Options{
		LauncherURL:   stub.URL + "/api/internal/tool-events",
		LauncherToken: "tok",
		TraceID:       "inv-1",
	})
	require.NoError(t, err)

	res, out, err := srv.handleSendMessage(context.Background(), nil, sendMessageIn{Text: "hello"})
	require.NoError(t, err)
	require.Nil(t, res)
	require.True(t, out.OK)
	require.Equal(t, "/api/internal/investigations/inv-1/auto/send-message", got.Path)
	require.Equal(t, "Bearer tok", got.Bearer)
	require.Contains(t, got.Body, `"text":"hello"`)
}

func TestSendMessage_RejectsEmpty(t *testing.T) {
	srv, _ := New(Options{LauncherURL: "http://x/y", LauncherToken: "t", TraceID: "inv-1"})
	res, _, err := srv.handleSendMessage(context.Background(), nil, sendMessageIn{Text: "  "})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
}

func TestSendMessage_PropagatesPausedErrorFromLauncher(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusLocked) // 423
		_, _ = w.Write([]byte(`{"error":"paused"}`))
	}))
	t.Cleanup(stub.Close)

	srv, _ := New(Options{LauncherURL: stub.URL + "/x", LauncherToken: "t", TraceID: "inv-1"})
	res, _, err := srv.handleSendMessage(context.Background(), nil, sendMessageIn{Text: "hi"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
}

func TestFinish_PostsReason(t *testing.T) {
	var gotBody string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)
	srv, _ := New(Options{LauncherURL: stub.URL + "/x", LauncherToken: "t", TraceID: "inv-1"})
	_, out, err := srv.handleFinish(context.Background(), nil, finishIn{Reason: "capture flow done"})
	require.NoError(t, err)
	require.True(t, out.OK)
	require.Contains(t, gotBody, `"reason":"capture flow done"`)
}

func TestFinish_RejectsEmptyReason(t *testing.T) {
	srv, _ := New(Options{LauncherURL: "http://x/y", LauncherToken: "t", TraceID: "inv-1"})
	res, _, err := srv.handleFinish(context.Background(), nil, finishIn{Reason: ""})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestRequestTakeover_PostsReason(t *testing.T) {
	var gotPath, gotBody string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)
	srv, _ := New(Options{LauncherURL: stub.URL + "/x", LauncherToken: "t", TraceID: "inv-1"})
	_, out, err := srv.handleRequestTakeover(context.Background(), nil, requestTakeoverIn{Reason: "need human on this"})
	require.NoError(t, err)
	require.True(t, out.OK)
	require.Equal(t, "/api/internal/investigations/inv-1/auto/request-takeover", gotPath)
	require.Contains(t, gotBody, `"reason":"need human on this"`)
}

func TestApproveProposal_PostsToLauncher(t *testing.T) {
	var gotPath, gotBody string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)
	srv, _ := New(Options{LauncherURL: stub.URL + "/x", LauncherToken: "t", TraceID: "inv-1"})
	_, out, err := srv.handleApproveProposal(context.Background(), nil, approveProposalIn{Kind: "wiki", ProposalID: "prop-42ec5c16c183"})
	require.NoError(t, err)
	require.True(t, out.OK)
	require.Equal(t, "/api/internal/investigations/inv-1/auto/approve-proposal", gotPath)
	require.Contains(t, gotBody, `"kind":"wiki"`)
	require.Contains(t, gotBody, `"proposal_id":"prop-42ec5c16c183"`)
}

func TestApproveProposal_RejectsBadKind(t *testing.T) {
	srv, _ := New(Options{LauncherURL: "http://x/y", LauncherToken: "t", TraceID: "inv-1"})
	res, _, err := srv.handleApproveProposal(context.Background(), nil, approveProposalIn{Kind: "random", ProposalID: "prop-1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
}
