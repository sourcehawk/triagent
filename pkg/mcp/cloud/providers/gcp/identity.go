package gcp

import (
	"context"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// Identity is the read-only whoami. It is called by cloud.Probe with an
// unvalidated RunFunc, so it may use the deny-floored `auth` subcommand
// directly. Validity means "impersonation is pinned to the expected SA and the
// pin actually works", not "logged in directly as the SA": gcloud impersonation
// keeps the operator's base account active while every call assumes the target
// SA, so comparing the base account to the target would mark a correctly
// configured session invalid.
//
// The probe reads the in-process impersonation env (the launcher sets it on the
// subprocess; the agent cannot reach it), confirms it is pinned to the expected
// target, then runs a minimal impersonated read to prove the pin took effect. A
// degraded auth state surfaces through Valid and Hint, never a Go error.
//
// NOTE: validated against gcloud's documented impersonation behavior; verify
// against a live gcloud before relying on the exact print-access-token shape.
func (p *Provider) Identity(ctx context.Context, run cloud.RunFunc, expected string) (cloud.IdentityStatus, error) {
	st := cloud.IdentityStatus{Provider: "gcp"}

	if expected == "" {
		st.Valid = false
		st.Hint = "no impersonation target pinned; set " + EnvImpersonate + " on the cloud MCP subprocess"
		return st, nil
	}

	pinned := os.Getenv(EnvImpersonate)
	if pinned != expected {
		st.Valid = false
		st.Hint = EnvImpersonate + " is not pinned to the expected identity " + expected +
			"; the launcher must set it on the cloud MCP subprocess"
		return st, nil
	}

	// Minimal impersonated read: succeeds only when the pinned SA can mint a
	// token, which proves the impersonation grant is in place and active.
	res, err := run(ctx, []string{"auth", "print-access-token", "--format=json"})
	if err != nil {
		st.Valid = false
		st.Hint = err.Error()
		return st, nil
	}
	if res.ExitCode != 0 {
		st.Valid = false
		hint := "impersonation failed; check the serviceAccountTokenCreator grant or re-auth: gcloud auth login"
		if res.Stderr != "" {
			hint = res.Stderr + " — " + hint
		}
		st.Hint = hint
		return st, nil
	}

	st.AssumedIdentity = expected
	st.Valid = true
	return st, nil
}
