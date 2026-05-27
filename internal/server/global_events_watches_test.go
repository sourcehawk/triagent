package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWatchStatusEnvelopeShape(t *testing.T) {
	ev := WatchStatusEvent{WatchID: "w1", Status: "healthy", Running: 1, Queued: 0}
	b, _ := json.Marshal(ev)
	s := string(b)
	for _, want := range []string{`"watchID":"w1"`, `"status":"healthy"`, `"running":1`, `"queued":0`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}
