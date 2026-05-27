package signalingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReportUnclearAndDismissHTTP(t *testing.T) {
	called := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called[r.URL.Path] = true
		_, _ = w.Write([]byte(`{"signalId":"S2"}`))
	}))
	defer srv.Close()
	s, _ := New(Options{LauncherURL: srv.URL, LauncherToken: "t", TraceID: "w1"})
	if _, err := s.reportUnclear(context.Background(), reportUnclearIn{CitedItemIDs: []string{"I1"}, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dismissItems(context.Background(), dismissItemsIn{ItemIDs: []string{"I1"}, Reason: "noise", DismissedWikiSlugs: []string{"keda-cooldown"}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/watches/w1/ingest/report-unclear", "/api/watches/w1/ingest/dismiss-items"} {
		if !called[p] {
			t.Errorf("expected POST to %s", p)
		}
	}
}
