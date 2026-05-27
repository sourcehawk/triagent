package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateLabel(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "trims whitespace", input: "  hello world  ", want: "hello world"},
		{name: "rejects empty after trim", input: "   ", wantErr: "label cannot be empty"},
		{name: "rejects empty literal", input: "", wantErr: "label cannot be empty"},
		{name: "accepts max length", input: strings.Repeat("a", MaxLabelLen), want: strings.Repeat("a", MaxLabelLen)},
		{name: "rejects over-cap", input: strings.Repeat("a", MaxLabelLen+1), wantErr: "label must be ≤80 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateLabel(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error %q, got nil (out=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostLabel_Success(t *testing.T) {
	var gotURL, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	s := &Server{opts: Options{
		LauncherURL:   srv.URL,
		LauncherToken: "tkn",
		TraceID:       "abc",
		HTTPClient:    srv.Client(),
	}}
	if err := s.postLabel(context.Background(), "OOMKilled in zeebe-broker"); err != nil {
		t.Fatalf("postLabel: %v", err)
	}
	if gotURL != "/api/internal/investigations/abc/label" {
		t.Fatalf("path: %q", gotURL)
	}
	if gotAuth != "Bearer tkn" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"label":"OOMKilled in zeebe-broker"`) {
		t.Fatalf("body: %q", gotBody)
	}
}

func TestPostLabel_LauncherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	s := &Server{opts: Options{LauncherURL: srv.URL, LauncherToken: "t", TraceID: "x", HTTPClient: srv.Client()}}
	err := s.postLabel(context.Background(), "ok")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 surfaced, got %v", err)
	}
}

func TestPostLabel_NoLauncherURL(t *testing.T) {
	s := &Server{opts: Options{}}
	err := s.postLabel(context.Background(), "ok")
	if err == nil || !strings.Contains(err.Error(), "launcher unreachable") {
		t.Fatalf("expected unreachable error, got %v", err)
	}
}

// TestPostLabel_StripsTelemetryPath exercises the real-world env shape:
// the launcher passes the full telemetry endpoint
// (http://host:port/api/internal/tool-events) as TRIAGENT_MCP_TELEMETRY_URL
// because the env is shared across MCP kinds. The meta server must
// strip the path and POST against the launcher's origin.
func TestPostLabel_StripsTelemetryPath(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := &Server{opts: Options{
		LauncherURL:   srv.URL + "/api/internal/tool-events",
		LauncherToken: "t",
		TraceID:       "abc",
		HTTPClient:    srv.Client(),
	}}
	if err := s.postLabel(context.Background(), "ok"); err != nil {
		t.Fatalf("postLabel: %v", err)
	}
	if gotURL != "/api/internal/investigations/abc/label" {
		t.Fatalf("path: %q (got telemetry path concatenated instead of stripped)", gotURL)
	}
}

func TestLauncherOrigin(t *testing.T) {
	cases := []struct {
		in, want, wantErr string
	}{
		{in: "http://127.0.0.1:39509/api/internal/tool-events", want: "http://127.0.0.1:39509"},
		{in: "https://launcher.example.com:8443/foo/bar", want: "https://launcher.example.com:8443"},
		{in: "http://host", want: "http://host"},
		{in: "  http://host  ", want: "http://host"},
		{in: "/no/scheme", wantErr: "missing scheme"},
		{in: "", wantErr: "missing scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := launcherOrigin(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetSessionLabelHandler(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s, err := New(Options{LauncherURL: srv.URL, LauncherToken: "t", TraceID: "abc", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, out, err := s.handleSetLabel(context.Background(), nil, setSessionLabelIn{Label: "  rebuild zeebe broker after deploy  "})
	if err != nil {
		t.Fatalf("handleSetLabel: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if !out.OK {
		t.Fatalf("expected ok=true, got %+v", out)
	}
	if calls != 1 {
		t.Fatalf("expected 1 launcher call, got %d", calls)
	}
}

func TestSetSessionLabelHandler_RejectsEmpty(t *testing.T) {
	s, _ := New(Options{LauncherURL: "http://unused", LauncherToken: "t", TraceID: "abc"})
	res, _, err := s.handleSetLabel(context.Background(), nil, setSessionLabelIn{Label: "   "})
	if err != nil {
		t.Fatalf("handleSetLabel returned go error %v; want a tool-level error result", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected tool-level error result, got %+v", res)
	}
}
