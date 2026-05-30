package aws

import (
	"context"
	"encoding/json"
	"fmt"

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

// Inventory projects the AWS accounts the pinned identity can read. The primary
// source is `aws organizations list-accounts`; when the identity lacks
// Organizations access (AccessDenied, surfaced as a non-zero exit or a transport
// error) it falls back to the single account the caller is in, derived from `aws
// sts get-caller-identity`. Both commands are allowlisted so the projection works
// under the validated run core.
func (p *Provider) Inventory(ctx context.Context, run cloud.RunFunc) (cloud.Inventory, error) {
	res, err := run(ctx, []string{"organizations", "list-accounts", "--output", "json"})
	if err != nil || res.ExitCode != 0 {
		return p.callerAccountInventory(ctx, run)
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
