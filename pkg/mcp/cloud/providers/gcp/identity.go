package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// authAccount is one entry of `gcloud auth list --format=json`.
type authAccount struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

// Identity is the read-only whoami. It is called by cloud.Probe with an
// unvalidated RunFunc, so it may use the deny-floored `auth` subcommand
// directly: it reads the active account and reports the session valid only when
// that account equals the pinned impersonation target the launcher set in
// CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT. A degraded auth state surfaces
// through Valid and Hint, never a Go error.
func (p *Provider) Identity(ctx context.Context, run cloud.RunFunc) (cloud.IdentityStatus, error) {
	target := os.Getenv(EnvImpersonate)

	res, err := run(ctx, []string{"auth", "list", "--filter=status:ACTIVE", "--format=json"})
	if err != nil {
		return cloud.IdentityStatus{Provider: "gcp", Valid: false, Hint: err.Error()}, nil
	}

	var accounts []authAccount
	if err := json.Unmarshal([]byte(res.Stdout), &accounts); err != nil {
		return cloud.IdentityStatus{
			Provider: "gcp",
			Valid:    false,
			Hint:     fmt.Sprintf("parse gcloud auth list output: %v", err),
		}, nil
	}

	active := activeAccount(accounts)
	st := cloud.IdentityStatus{Provider: "gcp", AssumedIdentity: active}

	switch {
	case target == "":
		st.Valid = false
		st.Hint = "no impersonation target pinned; set " + EnvImpersonate + " on the cloud MCP subprocess"
	case active == "":
		st.Valid = false
		st.Hint = "no active gcloud account; run: gcloud auth login"
	case active != target:
		st.Valid = false
		st.Hint = fmt.Sprintf("active account %q is not the pinned identity %q", active, target)
	default:
		st.Valid = true
	}
	return st, nil
}

// activeAccount returns the first account marked ACTIVE, or "" when none is. The
// --filter=status:ACTIVE argv already narrows this server-side; the status check
// is the belt to that braces.
func activeAccount(accounts []authAccount) string {
	for _, a := range accounts {
		if a.Status == "ACTIVE" {
			return a.Account
		}
	}
	return ""
}
