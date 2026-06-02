package gcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// project is one entry of `gcloud projects list --format=json`. Only the fields
// the inventory projection surfaces are decoded.
type project struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

// Inventory lists the projects the agent may reach. When the deployment
// configured a project set, that set (with its tags) is the inventory and
// nothing is shelled — it is exactly what the agent can select among. Only when
// no projects are configured (the unconstrained form) does it list projects live
// via `gcloud projects list` (untagged). A live run error is a real failure of
// the inventory tool and is returned, unlike the identity probe which degrades.
func (p *Provider) Inventory(ctx context.Context, run cloud.RunFunc) (cloud.Inventory, error) {
	if len(p.projects) > 0 {
		scopes := make([]cloud.Scope, 0, len(p.projects))
		for _, pr := range p.projects {
			scopes = append(scopes, cloud.Scope{ID: pr.ID, Name: pr.ID, Tags: pr.Tags})
		}
		return cloud.Inventory{Scopes: scopes}, nil
	}

	res, err := run(ctx, []string{"projects", "list", "--format=json"})
	if err != nil {
		return cloud.Inventory{}, fmt.Errorf("gcloud projects list: %w", err)
	}
	if res.ExitCode != 0 {
		return cloud.Inventory{}, fmt.Errorf("gcloud projects list failed (exit %d): %s", res.ExitCode, res.Stderr)
	}

	var projects []project
	if err := json.Unmarshal([]byte(res.Stdout), &projects); err != nil {
		return cloud.Inventory{}, fmt.Errorf("parse gcloud projects list output: %w", err)
	}

	inv := cloud.Inventory{Scopes: make([]cloud.Scope, 0, len(projects))}
	for _, pr := range projects {
		inv.Scopes = append(inv.Scopes, cloud.Scope{ID: pr.ProjectID, Name: pr.Name})
	}
	return inv, nil
}
