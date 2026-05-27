package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// handleStream is the one persistent SSE per browser tab. Carries
// every server-emitted event tagged with a scope id (InvestigationID,
// EditorSessionID, or both empty for launcher-wide). Frontend filters
// in-app by scope; we don't filter server-side.
//
// Disconnect detection: we hijack the connection and run a parallel
// read goroutine. Go's heartbeat-write-fails-on-EPIPE model breaks
// down on Linux — small heartbeat writes are buffered silently by
// the kernel against a half-closed peer until TCP retransmit
// exhaustion (30+ seconds). A parallel Read with a tight deadline
// returns EOF (or RST) on client close within milliseconds. Without
// this, rapid in-tab navigation that creates a fresh StreamProvider
// per page (e.g. when soft-nav fails to preserve the layout) stacks
// dead-but-undetected SSE handlers on the server, saturating Chrome's
// 6-per-origin pool and stalling every other request on the page.
//
// GET /api/stream?conn=<token>
func (a *apiHandlers) handleStream(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported (hijack)")
		return
	}

	lastEventID := 0
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		if n, err := strconv.Atoi(h); err == nil {
			lastEventID = n
		}
	}

	connToken := r.URL.Query().Get("conn")

	// Hijack first, then write the status line + headers manually.
	// We skip w.WriteHeader because it would set Transfer-Encoding:
	// chunked (no Content-Length on a streaming response), but our
	// raw post-Hijack writes aren't framed as chunks. Writing the
	// HTTP response line + headers ourselves gives the client an
	// identity-encoded body that streams bytes until close — which
	// is what an SSE consumer expects.
	conn, brw, err := hijacker.Hijack()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hijack failed: "+err.Error())
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := brw.WriteString(
		"HTTP/1.1 200 OK\r\n" +
			"Content-Type: text/event-stream\r\n" +
			"Cache-Control: no-cache\r\n" +
			"Connection: keep-alive\r\n" +
			"X-Accel-Buffering: no\r\n" +
			"\r\n",
	); err != nil {
		return
	}
	if err := brw.Flush(); err != nil {
		return
	}

	backlog, ch, done, cancel := a.manager.SubscribeStream(connToken, lastEventID)
	defer cancel()

	for _, env := range backlog {
		if err := writeStreamSSEBrw(brw, env); err != nil {
			return
		}
	}
	if err := brw.Flush(); err != nil {
		return
	}

	// Parallel disconnect detector. EventSource is a server→client
	// stream; the client never sends bytes after the request line.
	// Setting a short read deadline + Read in a loop probes the
	// underlying socket: timeout means "still alive, nothing to read",
	// any other error (EOF, ECONNRESET, broken-pipe-on-read) means
	// the client is gone. Closing `disconnect` signals the writer
	// loop to exit.
	disconnect := make(chan struct{})
	go func() {
		defer close(disconnect)
		buf := make([]byte, 64)
		for {
			if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
				return
			}
			_, err := conn.Read(buf)
			if err == nil {
				// Unexpected — EventSource clients don't write. Loop.
				continue
			}
			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Timeout() {
				// Expected — no data within deadline. Loop.
				continue
			}
			// EOF / ECONNRESET / other — client is gone.
			return
		}
	}()

	// Clear any read deadline once we exit so the kernel doesn't
	// outlive us with a pending timer (defensive — Close handles
	// teardown).
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-disconnect:
			return
		case env, open := <-ch:
			if !open {
				return
			}
			if err := writeStreamSSEBrw(brw, env); err != nil {
				return
			}
			if err := brw.Flush(); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := brw.WriteString(": keep-alive\n\n"); err != nil {
				return
			}
			if err := brw.Flush(); err != nil {
				return
			}
		}
	}
}

// writeStreamSSEBrw emits one SSE frame on a hijacked connection.
// id: carries FanSeq for Last-Event-ID resume; event: carries Kind so
// EventSource addEventListener can dispatch. Writes go through the
// bufio.Writer returned by Hijack (the http.ResponseWriter is no
// longer usable after Hijack).
func writeStreamSSEBrw(brw *bufio.ReadWriter, env StreamEnvelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(brw, "id: %d\nevent: %s\ndata: %s\n\n", env.FanSeq, env.Kind, body)
	return err
}

// handleCloseStream is the explicit-close hook for /api/stream.
// Frontend calls it during graceful page-unload paths so the server
// can free the subscription before the heartbeat detects EPIPE.
//
// POST /api/stream/close?conn=<token>
func (a *apiHandlers) handleCloseStream(w http.ResponseWriter, r *http.Request) {
	connToken := r.URL.Query().Get("conn")
	a.manager.CloseStreamSubscriber(connToken)
	w.WriteHeader(http.StatusNoContent)
}

// handleInvestigationTranscript returns the in-memory backlog of
// events for an investigation as a one-shot REST response. Frontend
// consumes this on SessionView mount and uses lastSeq to dedupe live
// envelopes from the multiplex stream.
//
// GET /api/investigations/{id}/transcript
func (a *apiHandlers) handleInvestigationTranscript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	events, lastSeq := inv.TranscriptSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"events":  events,
		"lastSeq": lastSeq,
	})
}

// handleEditorTranscript mirrors handleInvestigationTranscript for
// editor sessions.
//
// GET /api/editor-sessions/{id}/transcript
func (a *apiHandlers) handleEditorTranscript(w http.ResponseWriter, r *http.Request) {
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	sess := a.editorMgr.Get(r.PathValue("id"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	events, lastSeq := sess.TranscriptSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"events":  events,
		"lastSeq": lastSeq,
	})
}

