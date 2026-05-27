// Package cmd defines the CLI commands exposed by the investigate plugin.
package main

import (
	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/spf13/cobra"
)

// provider is the cluster-access provider injected at startup.
// It must be set via SetProvider before any command executes.
var provider auth.Provider

// SetProvider configures the cluster-access provider used by the
// investigate plugin. Call this from main() before plugin.Run.
func SetProvider(p auth.Provider) {
	provider = p
}

// Commands returns all commands exposed by the plugin root.
//
// The MCP servers (k8s, strategies, prom) are not hosted by investigate/
// itself — they live in the triagent-mcp plugin. The launcher writes a per-session
// MCP config that invokes `triagent-mcp serve --kind=...` for each one.
func Commands() []*cobra.Command {
	return []*cobra.Command{
		start(),
		clean(),
		createProfileCmd(),
	}
}
