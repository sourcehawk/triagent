package main

import "testing"

// TestStartCmd_LaunchBrowserFlag pins the --launch-browser contract: it is
// registered and defaults to true (the launcher opens a browser unless told
// not to). The e2e harness relies on --launch-browser=false to stay headless;
// runWeb gates openBrowser on this flag.
func TestStartCmd_LaunchBrowserFlag(t *testing.T) {
	f := start().Flags().Lookup("launch-browser")
	if f == nil {
		t.Fatal("--launch-browser flag is not registered on `start`")
	}
	if f.DefValue != "true" {
		t.Errorf("--launch-browser default = %q, want %q", f.DefValue, "true")
	}
}
