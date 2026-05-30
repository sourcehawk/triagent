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

// Inventory lists the projects the pinned identity can read, projected to id +
// name. It is called with the server's validated RunFunc, so the argv must match
// the allowlisted `projects list` verb chain exactly. A run error here is a real
// failure of the inventory tool and is returned to the caller, unlike the
// identity probe which degrades.
func (p *Provider) Inventory(ctx context.Context, run cloud.RunFunc) (cloud.Inventory, error) {
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
