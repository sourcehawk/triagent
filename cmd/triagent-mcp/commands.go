// Package cmd defines the CLI commands for the triagent-mcp plugin.
//
// The plugin exposes a single `serve` subcommand that dispatches to one of
// three MCP servers via --kind: k8s (read-only Kubernetes tools), strategies
// (guided investigation walker), or prom (curated Prometheus saturation
// queries). Each launcher writes a per-session MCP config that invokes
// triagent-mcp once per kind.
package main

import (
	"github.com/spf13/cobra"
)

// Commands returns all CLI commands exposed by this plugin.
func Commands() []*cobra.Command {
	return []*cobra.Command{
		serveCmd(),
		validatePlaybookCmd(),
		writeUserPlaybookCmd(),
		promoteProposalCmd(),
		deleteProposalCmd(),
		wikiDeleteProposalCmd(),
		validateWikiCmd(),
		sessionsDeleteProposalCmd(),
		sessionsDraftCmd(),
		validateSessionCmd(),
		generateSummaryCmd(),
	}
}
