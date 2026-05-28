package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/editor"
)

func TestStreamRing_AppendAndReplay(t *testing.T) {
	r := newStreamRing(4)
	for i := 1; i <= 3; i++ {
		r.append(StreamEnvelope{FanSeq: i, Kind: "test"})
	}
	got := r.replay(0)
	require.Len(t, got, 3, "replay(0) len")
	got = r.replay(2)
	require.Len(t, got, 1, "replay(2) len")
	assert.Equal(t, 3, got[0].FanSeq, "replay(2)[0].FanSeq")
}

func TestStreamRing_Eviction(t *testing.T) {
	r := newStreamRing(3)
	for i := 1; i <= 5; i++ {
		r.append(StreamEnvelope{FanSeq: i, Kind: "test"})
	}
	got := r.replay(0)
	require.Len(t, got, 3, "replay len (capacity)")
	assert.Equal(t, 3, got[0].FanSeq, "oldest in ring FanSeq")
}

func TestStreamRing_StaleAfterEviction(t *testing.T) {
	r := newStreamRing(3)
	for i := 1; i <= 5; i++ {
		r.append(StreamEnvelope{FanSeq: i, Kind: "test"})
	}
	got := r.replay(1)
	require.Len(t, got, 3, "replay(1) after eviction len")
	assert.Equal(t, 3, got[0].FanSeq, "got[0].FanSeq")
	_ = time.Now // silence unused import if test file has no time refs
}

func TestManager_PublishStream_FansOutToSubscribers(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	_, ch, _, cancel := m.SubscribeStream("tab-A", 0)
	t.Cleanup(cancel)

	m.PublishStream(StreamEnvelope{
		Seq:             7,
		Kind:            "assistant",
		InvestigationID: "inv-1",
		Text:            "hi",
	})
	select {
	case env := <-ch:
		assert.Equal(t, 1, env.FanSeq, "FanSeq (first publish stamps fanSeq=1)")
		assert.Equal(t, 7, env.Seq, "envelope Seq")
		assert.Equal(t, "inv-1", env.InvestigationID, "envelope InvestigationID")
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive published envelope")
	}
}

func TestManager_CloseStreamSubscriber_ClosesByToken(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	_, _, done, cancel := m.SubscribeStream("tab-A", 0)
	t.Cleanup(cancel)

	require.True(t, m.CloseStreamSubscriber("tab-A"), "CloseStreamSubscriber returned false for a registered token")
	// Teardown is signalled via done, not by closing the data channel.
	select {
	case <-done:
		// correct: done closed by CloseStreamSubscriber
	case <-time.After(time.Second):
		t.Fatal("done channel was not closed after CloseStreamSubscriber")
	}
	assert.False(t, m.CloseStreamSubscriber("tab-A"), "idempotent: second close should return false")
}

func TestManager_SubscribeStream_LastEventIDReplay(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	for i := 0; i < 3; i++ {
		m.PublishStream(StreamEnvelope{Kind: "x", Text: "msg"})
	}
	backlog, _, _, cancel := m.SubscribeStream("tab-late", 1)
	t.Cleanup(cancel)
	assert.Len(t, backlog, 2, "backlog len (FanSeq 2 and 3)")
}

func TestHandleInvestigationTranscript_ReturnsBacklog(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(context.Background(), dir)
	t.Cleanup(m.Shutdown)
	inv := &Investigation{ID: "inv-T", SessionDir: dir, CreatedAt: time.Now().UTC()}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	m.byID[inv.ID] = inv

	// Seed two events directly via publish so seq numbering is real.
	inv.publish(EventEnvelope{Kind: envKindUser, Text: "first"})
	inv.publish(EventEnvelope{Kind: envKindAssistant, Text: "second"})

	a := &apiHandlers{manager: m}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/investigations/"+inv.ID+"/transcript", nil)
	req.SetPathValue("id", inv.ID)
	a.handleInvestigationTranscript(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "status")
	var body struct {
		Events  []EventEnvelope `json:"events"`
		LastSeq int             `json:"lastSeq"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body), "unmarshal")
	require.Len(t, body.Events, 2, "len(events)")
	assert.Equal(t, 2, body.LastSeq, "lastSeq")
}

func TestHandleStream_DeliversEnvelope(t *testing.T) {
	// handleStream uses http.Hijacker, so httptest.NewRecorder doesn't
	// work — we need a real HTTP server. Spin one up, publish an
	// envelope into the ring before the client connects (so it's
	// replayed from the backlog), then read the SSE frame off the
	// wire.
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	a := &apiHandlers{manager: m}

	m.PublishStream(StreamEnvelope{Seq: 1, Kind: "assistant", InvestigationID: "x", Text: "hi"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream", a.handleStream)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream?conn=tab-A", nil)
	require.NoError(t, err, "new request")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err, "do")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "status")

	// Read until we've seen our expected envelope. Cancel the ctx to
	// exit the handler after.
	var buf [4096]byte
	deadline := time.Now().Add(time.Second)
	var collected strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf[:])
		if n > 0 {
			collected.Write(buf[:n])
		}
		body := collected.String()
		if strings.Contains(body, "event: assistant") &&
			strings.Contains(body, `"investigationId":"x"`) &&
			strings.Contains(body, "id: 1\n") {
			return // happy path
		}
		if err != nil {
			t.Fatalf("read body: %v (collected: %q)", err, body)
		}
	}
	t.Fatalf("expected SSE frame not received within deadline, got:\n%s", collected.String())
}

func TestPublishStream_NoPanicOnConcurrentClose(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	_, _, _, cancel := m.SubscribeStream("tab", 0)

	// Cancel-then-publish: simulates the close-during-fan-out race.
	cancel()
	// Should not panic.
	m.PublishStream(StreamEnvelope{Kind: "x"})

	// Idempotent: calling cancel twice should be safe.
	cancel()
}

func TestEditorPublish_ReachesMultiplexStream(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	t.Cleanup(m.Shutdown)
	_, ch, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	// Mimic the wiring done by handlers_editor: a Forward callback that
	// bridges editor events into the multiplex stream tagged with the
	// session id.
	sessionID := "ed-A"
	forward := func(ev editor.Event) {
		m.PublishStream(streamEnvelopeFromEditor(ev, sessionID))
	}
	forward(editor.Event{Seq: 3, Kind: editor.KindAssistant, Text: "hello"})

	select {
	case env := <-ch:
		assert.Equal(t, sessionID, env.EditorSessionID, "EditorSessionID")
		assert.Equal(t, "assistant", env.Kind, "Kind")
	case <-time.After(time.Second):
		t.Fatal("multiplex stream did not receive editor envelope")
	}
}
