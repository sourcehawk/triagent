package signalingest

import "testing"

func TestNewServer(t *testing.T) {
	_, err := New(Options{LauncherURL: "http://127.0.0.1:1", LauncherToken: "tok", TraceID: "w1"})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
}
