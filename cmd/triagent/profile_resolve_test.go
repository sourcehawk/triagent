package main

import (
	"testing"
)

func TestProfileFlagDefaultsToDefault(t *testing.T) {
	t.Setenv("TRIAGENT_PROFILE", "")
	got := ResolveProfileRef("")
	if got != "default" {
		t.Errorf("default profile ref = %q, want default", got)
	}
}

func TestProfileEnvOverridesDefault(t *testing.T) {
	t.Setenv("TRIAGENT_PROFILE", "default")
	got := ResolveProfileRef("")
	if got != "default" {
		t.Errorf("env profile ref = %q, want default", got)
	}
}

func TestProfileFlagOverridesEnv(t *testing.T) {
	t.Setenv("TRIAGENT_PROFILE", "default")
	got := ResolveProfileRef("/tmp/custom")
	if got != "/tmp/custom" {
		t.Errorf("flag profile ref = %q, want /tmp/custom", got)
	}
}

