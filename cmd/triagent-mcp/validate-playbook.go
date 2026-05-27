package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/strategies"
	"github.com/spf13/cobra"
)

// validatePlaybookCmd is the hidden `triagent-mcp validate-playbook` subcommand.
// Reads YAML from stdin, runs structural validation through the same code
// path strategies.LoadPlaybooksFrom uses, and writes a JSON result to
// stdout: {"ok": true} on success or {"ok": false, "errors": ["..."]}
// on failure. Exit code 0 = ok, 1 = validation errors, 2 = read/parse
// problem (so the launcher can distinguish "user playbook is malformed"
// from "validate-playbook itself broke").
//
// Used by the launcher before persisting a user playbook to disk so we
// don't end up with a broken file that fails to load on the next session.
func validatePlaybookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "validate-playbook",
		Short:  "Validate a playbook YAML read from stdin",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				writeValidateResult(false, []string{fmt.Sprintf("read stdin: %v", err)})
				os.Exit(2)
				return nil
			}
			if len(data) == 0 {
				writeValidateResult(false, []string{"empty input"})
				os.Exit(1)
				return nil
			}
			_, errs := strategies.ParseAndValidatePlaybookYAML(data)
			if len(errs) == 0 {
				writeValidateResult(true, nil)
				return nil
			}
			writeValidateResult(false, errs)
			os.Exit(1)
			return nil
		},
	}
}

func writeValidateResult(ok bool, errs []string) {
	body, _ := json.MarshalIndent(map[string]any{
		"ok":     ok,
		"errors": errs,
	}, "", "  ")
	_, _ = os.Stdout.Write(body)
	_, _ = os.Stdout.Write([]byte("\n"))
}
