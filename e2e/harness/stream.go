//go:build e2e

package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// StreamEvent is one decoded SSE frame from GET /api/stream. Kind mirrors
// the launcher's StreamEnvelope.Kind; Data is the raw JSON payload so
// consumers decode the fields they need without the harness tracking every
// envelope shape.
type StreamEvent struct {
	ID   string
	Kind string
	Data json.RawMessage
}

// Stream is a live SSE subscription. Open it with (*Client).OpenStream;
// WaitForEvent blocks until a matching frame arrives or the deadline passes.
type Stream struct {
	cancel context.CancelFunc
	events chan StreamEvent
	errc   chan error
}

// OpenStream subscribes to the launcher's multiplexed SSE feed. The caller
// closes it via (*Stream).Close. The connection carries the launch token so
// the auth middleware lets it through.
func (c *Client) OpenStream(t *testing.T) *Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	url := c.baseURL + "/api/stream?conn=e2e-harness"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("stream: build request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	// Carry the launch token as the SPA cookie rather than ?token=: the
	// query-param path triggers a 303 redirect that sets the cookie and
	// re-requests, but a bare SSE client doesn't follow that cleanly, so
	// we present the cookie the redirect would have set directly.
	if c.token != "" {
		req.AddCookie(&http.Cookie{Name: "triagent_token", Value: c.token})
	}

	// A no-timeout client: the SSE body stays open until Close cancels ctx.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatalf("stream: connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("stream: status %d", resp.StatusCode)
	}

	s := &Stream{
		cancel: cancel,
		events: make(chan StreamEvent, 64),
		errc:   make(chan error, 1),
	}
	go s.read(resp)
	return s
}

// read parses SSE frames (id:/event:/data: lines separated by a blank line)
// off the response body and fans them onto the events channel.
func (s *Stream) read(resp *http.Response) {
	defer func() { _ = resp.Body.Close() }()
	defer close(s.events)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var ev StreamEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Kind != "" || len(ev.Data) > 0 {
				s.events <- ev
			}
			ev = StreamEvent{}
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			ev.Kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := sc.Err(); err != nil {
		select {
		case s.errc <- err:
		default:
		}
	}
}

// WaitForEvent blocks until a frame whose Kind satisfies match arrives, or
// the timeout elapses. It returns the matching event; on timeout it fails
// the test, naming the predicate so the failure is self-describing.
func (s *Stream) WaitForEvent(t *testing.T, desc string, match func(StreamEvent) bool, timeout time.Duration) StreamEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatalf("stream: closed before %q arrived", desc)
			}
			if match(ev) {
				return ev
			}
		case err := <-s.errc:
			t.Fatalf("stream: read error waiting for %q: %v", desc, err)
		case <-deadline:
			t.Fatalf("stream: timed out after %s waiting for %q", timeout, desc)
		}
	}
}

// WaitForKind is the common case of WaitForEvent: match a frame by exact
// Kind.
func (s *Stream) WaitForKind(t *testing.T, kind string, timeout time.Duration) StreamEvent {
	t.Helper()
	return s.WaitForEvent(t, fmt.Sprintf("event kind %q", kind), func(ev StreamEvent) bool {
		return ev.Kind == kind
	}, timeout)
}

// Close ends the subscription.
func (s *Stream) Close() { s.cancel() }
