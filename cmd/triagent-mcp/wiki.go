package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/wiki"
	"github.com/spf13/cobra"
)

func wikiDeleteProposalCmd() *cobra.Command {
	var props string
	cmd := &cobra.Command{
		Use:   "wiki-delete-proposal <proposal_id>",
		Short: "Delete a wiki proposal draft from the local proposals dir.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if props == "" {
				return fmt.Errorf("--proposals is required (the launcher passes it from the active profile's paths.wiki_proposals_dir)")
			}
			if err := wiki.DeleteProposal(props, args[0]); err != nil {
				out := map[string]any{"ok": false, "error": err.Error()}
				b, _ := json.Marshal(out)
				_, _ = fmt.Fprintln(os.Stdout, string(b))
				os.Exit(2)
			}
			_, _ = fmt.Fprintln(os.Stdout, `{"ok":true}`)
			return nil
		},
	}
	cmd.Flags().StringVar(&props, "proposals", "", "proposals dir (required; the launcher passes it from the active profile's paths.wiki_proposals_dir)")
	return cmd
}
