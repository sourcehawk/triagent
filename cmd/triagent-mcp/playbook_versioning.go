package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/strategies"
	"github.com/spf13/cobra"
)

// The launcher (triagent) is a separate Go module from triagent-mcp,
// so it can't import strategies' Go API directly. These hidden CLI
// subcommands are the cross-module bridge: the launcher execs triagent-mcp
// with the appropriate verb whenever it needs single-file user playbook
// I/O. Same pattern as the existing `validate-playbook` subcommand
// the launcher already invokes.
//
// All three print a JSON result on stdout. Exit codes:
//   0 = success (operation completed; structured result on stdout)
//   1 = validation failure (errors carried in JSON; not a hard error)
//   2 = setup / I/O failure (couldn't read stdin, dir missing, etc.)

// writeUserPlaybookCmd reads YAML from stdin, validates + writes
// <dir>/<type>/<id>.yaml, stamping active per the flag. No version
// bumping. JSON result on stdout.
//
// Used by the launcher's editor save flow — when an operator clicks
// Save in the editor, this is what produces the file. The launcher
// wraps the call with git add + git commit in the user dir.
func writeUserPlaybookCmd() *cobra.Command {
	var activate bool
	cmd := &cobra.Command{
		Use:    "write-user-playbook <dir> <type> <id>",
		Short:  "Validate + write a single-file user playbook (YAML on stdin) to <dir>/<type>/<id>.yaml",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, typeName, id := args[0], args[1], args[2]
			body, err := io.ReadAll(os.Stdin)
			if err != nil {
				writeJSONExit(map[string]any{"ok": false, "error": fmt.Sprintf("read stdin: %v", err)}, 2)
				return nil
			}
			validationErrs, err := strategies.WriteUserPlaybook(dir, typeName, id, string(body), activate)
			if err != nil {
				writeJSONExit(map[string]any{"ok": false, "error": err.Error()}, 2)
				return nil
			}
			if len(validationErrs) > 0 {
				writeJSONExit(map[string]any{"ok": false, "errors": validationErrs}, 1)
				return nil
			}
			writeJSONExit(map[string]any{"ok": true}, 0)
			return nil
		},
	}
	cmd.Flags().BoolVar(&activate, "activate", true, "stamp active=true on the new file (default true; --activate=false stamps inactive)")
	return cmd
}

func promoteProposalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "promote-proposal <dir> <proposal_id>",
		Short:  "Promote a draft proposal to a versioned user playbook",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, proposalID := args[0], args[1]
			id, typeName, body, err := strategies.LoadProposalDraft(dir, proposalID)
			if err != nil {
				writeJSONExit(map[string]any{"ok": false, "error": err.Error()}, 2)
				return nil
			}
			validationErrs, err := strategies.WriteUserPlaybook(dir, typeName, id, body, true)
			if err != nil {
				writeJSONExit(map[string]any{"ok": false, "error": err.Error()}, 2)
				return nil
			}
			if len(validationErrs) > 0 {
				writeJSONExit(map[string]any{"ok": false, "errors": validationErrs}, 1)
				return nil
			}
			deleteErr := strategies.DeleteProposalDraft(dir, proposalID)
			result := map[string]any{
				"ok":        true,
				"id":        id,
				"type":      typeName,
				"activated": true,
			}
			if deleteErr != nil {
				result["draft_cleanup_warning"] = deleteErr.Error()
			}
			writeJSONExit(result, 0)
			return nil
		},
	}
}

// deleteProposalCmd drops a draft without promoting it. Used by the
// decline path. JSON result: {ok}.
func deleteProposalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "delete-proposal <dir> <proposal_id>",
		Short:  "Delete a draft proposal (decline / refine path)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, proposalID := args[0], args[1]
			if err := strategies.DeleteProposalDraft(dir, proposalID); err != nil {
				writeJSONExit(map[string]any{"ok": false, "error": err.Error()}, 2)
				return nil
			}
			writeJSONExit(map[string]any{"ok": true}, 0)
			return nil
		},
	}
}

// writeJSONExit prints the body as JSON and exits with the given code.
// Centralised so all four subcommands have the same shape; the
// launcher's wrapper code only needs to decode one JSON shape per
// verb.
func writeJSONExit(body map[string]any, code int) {
	out, _ := json.MarshalIndent(body, "", "  ")
	_, _ = os.Stdout.Write(out)
	_, _ = os.Stdout.Write([]byte("\n"))
	if code != 0 {
		os.Exit(code)
	}
}
