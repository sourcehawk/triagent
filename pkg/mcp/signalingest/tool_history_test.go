package signalingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuerySignalHistoryHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"signalId":"S1","outcome":"investigation_started"}]}`))
	}))
	defer srv.Close()
	s, _ := New(Options{LauncherURL: srv.URL, LauncherToken: "tok", TraceID: "w1"})
	out, err := s.queryHistory(context.Background(), querySignalHistoryIn{SinceHours: 72})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].SignalID != "S1" {
		t.Fatalf("unexpected: %+v", out)
	}
	_, _ = json.Marshal(out)
}
