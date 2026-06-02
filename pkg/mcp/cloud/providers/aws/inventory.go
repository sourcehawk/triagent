package aws

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// Inventory projects the AWS accounts the agent may reach: exactly the
// configured account set. Each account is its own read-only role, so the
// configured list already describes what run_cli can reach; Inventory returns it
// directly and shells nothing. It never queries `organizations list-accounts` —
// an org-wide listing would over-advertise accounts the roles cannot enter.
func (p *Provider) Inventory(_ context.Context, _ cloud.RunFunc) (cloud.Inventory, error) {
	scopes := make([]cloud.Scope, 0, len(p.accounts))
	for _, a := range p.accounts {
		scopes = append(scopes, cloud.Scope{ID: a.ID, Name: a.ID, Tags: a.Tags})
	}
	return cloud.Inventory{Scopes: scopes}, nil
}
