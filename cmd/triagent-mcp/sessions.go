package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sourcehawk/triagent/pkg/mcp/sessions"
	"github.com/spf13/cobra"
)

const envSessionsProposalsPathCLI = "C1_SESSIONS_PROPOSALS_PATH"

func sessionsDeleteProposalCmd() *cobra.Command {
	var props string
	cmd := &cobra.Command{
		Use:   "sessions-delete-proposal <proposal_id>",
		Short: "Delete a session proposal draft.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			props = resolveSessionsProposalsPath(props)
			if err := sessions.DeleteProposal(props, args[0]); err != nil {
				out := map[string]any{"ok": false, "error": err.Error()}
				b, _ := json.Marshal(out)
				_, _ = fmt.Fprintln(os.Stdout, string(b))
				os.Exit(2)
			}
			_, _ = fmt.Fprintln(os.Stdout, `{"ok":true}`)
			return nil
		},
	}
	cmd.Flags().StringVar(&props, "proposals", "", "proposals dir (defaults to $"+envSessionsProposalsPathCLI+", then ~/.triagent/session-proposals)")
	return cmd
}

// resolveSessionsProposalsPath: --proposals flag wins, else env, else ~/.triagent/session-proposals.
func resolveSessionsProposalsPath(flag string) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv(envSessionsProposalsPathCLI); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".triagent", "session-proposals")
	}
	return ""
}

func sessionsDraftCmd() *cobra.Command {
	var metadataPath, eventsPath, outPath, claudeBin string
	cmd := &cobra.Command{
		Use:   "sessions-draft",
		Short: "Draft a session.md from metadata + events. One-shot wrapper around propose_session_draft.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if metadataPath == "" || eventsPath == "" || outPath == "" {
				return fmt.Errorf("--metadata, --events, --out are all required")
			}
			return sessions.RunOneShotDraft(cmd.Context(), sessions.OneShotDraftOptions{
				MetadataPath: metadataPath,
				EventsPath:   eventsPath,
				OutPath:      outPath,
				ClaudeBinary: claudeBin,
			})
		},
	}
	cmd.Flags().StringVar(&metadataPath, "metadata", "", "session metadata.json (required)")
	cmd.Flags().StringVar(&eventsPath, "events", "", "session events.jsonl (required)")
	cmd.Flags().StringVar(&outPath, "out", "", "output session.md path (required)")
	cmd.Flags().StringVar(&claudeBin, "claude-binary", "", "claude CLI (defaults to $PATH)")
	return cmd
}
