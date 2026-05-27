package signalingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartInvestigationHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/start-investigation") {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"signalId":"S1","queued":true,"position":1}`))
	}))
	defer srv.Close()
	s, _ := New(Options{LauncherURL: srv.URL, LauncherToken: "t", TraceID: "w1"})
	out, err := s.startInvestigation(context.Background(), startInvestigationIn{Briefing: "b", CitedItemIDs: []string{"I1"}, AutoMode: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Queued || out.SignalID != "S1" {
		t.Fatalf("unexpected: %+v", out)
	}
}
