package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// listAccountsResult is the projection of `aws organizations list-accounts
// --output json`. Only the fields inventory needs are decoded.
type listAccountsResult struct {
	Accounts []organizationsAccount `json:"Accounts"`
}

type organizationsAccount struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Status string `json:"Status"`
}

// Inventory projects the AWS accounts the pinned identity can read.
//
// When the source is configured with an accounts list, the reachable set is
// exactly those accounts: each is its own read-only role, so the configured set
// already describes what run_cli can reach. Inventory returns them directly and
// shells nothing — an org-wide list-accounts would over-advertise accounts the
// role cannot enter.
//
// Without a configured accounts list (the single-account form), the primary
// source is `aws organizations list-accounts`; only when that fails with an
// Organizations-unavailable condition (AccessDenied or the account not being a
// member of an organization) does it fall back to the single account the caller
// is in, derived from `aws sts get-caller-identity`. Any other failure — a
// transport error, throttling, a network fault — is surfaced rather than masked
// behind the single-account fallback. Both commands are allowlisted so the
// projection works under the validated run core.
func (p *Provider) Inventory(ctx context.Context, run cloud.RunFunc) (cloud.Inventory, error) {
	if len(p.accounts) > 0 {
		scopes := make([]cloud.Scope, 0, len(p.accounts))
		for _, a := range p.accounts {
			scopes = append(scopes, cloud.Scope{ID: a.ID, Name: a.ID})
		}
		return cloud.Inventory{Scopes: scopes}, nil
	}

	res, err := run(ctx, []string{"organizations", "list-accounts", "--output", "json"})
	if err != nil {
		return cloud.Inventory{}, fmt.Errorf("aws organizations list-accounts: %w", err)
	}
	if res.ExitCode != 0 {
		if isOrgsUnavailable(res.Stderr) {
			return p.callerAccountInventory(ctx, run)
		}
		return cloud.Inventory{}, fmt.Errorf("aws organizations list-accounts failed (exit %d): %s", res.ExitCode, res.Stderr)
	}

	var parsed listAccountsResult
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return cloud.Inventory{}, fmt.Errorf("parse organizations list-accounts: %w", err)
	}

	scopes := make([]cloud.Scope, 0, len(parsed.Accounts))
	for _, a := range parsed.Accounts {
		if a.Status != "ACTIVE" {
			continue
		}
		scopes = append(scopes, cloud.Scope{ID: a.ID, Name: a.Name})
	}
	return cloud.Inventory{Scopes: scopes}, nil
}

// isOrgsUnavailable reports whether a non-zero `organizations list-accounts`
// stderr is the benign "Organizations is not available to this identity"
// condition — the only failure the single-account fallback is correct for. AWS
// returns AccessDenied when the role lacks Organizations permissions and
// AWSOrganizationsNotInUseException (or "not a member of an organization") when
// the account is standalone.
func isOrgsUnavailable(stderr string) bool {
	for _, marker := range []string{
		"AccessDenied",
		"not a member of an organization",
		"AWSOrganizationsNotInUseException",
	} {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}

// callerAccountInventory derives the single-account inventory from the caller
// identity, the fallback when Organizations access is denied.
func (p *Provider) callerAccountInventory(ctx context.Context, run cloud.RunFunc) (cloud.Inventory, error) {
	res, err := run(ctx, []string{"sts", "get-caller-identity", "--output", "json"})
	if err != nil {
		return cloud.Inventory{}, fmt.Errorf("caller identity for inventory fallback: %w", err)
	}
	if res.ExitCode != 0 {
		return cloud.Inventory{}, fmt.Errorf("aws sts get-caller-identity failed (exit %d)", res.ExitCode)
	}
	var caller callerIdentity
	if err := json.Unmarshal([]byte(res.Stdout), &caller); err != nil {
		return cloud.Inventory{}, fmt.Errorf("parse caller identity for inventory fallback: %w", err)
	}
	if caller.Account == "" {
		return cloud.Inventory{}, fmt.Errorf("caller identity has no account")
	}
	return cloud.Inventory{Scopes: []cloud.Scope{{ID: caller.Account, Name: caller.Account}}}, nil
}
