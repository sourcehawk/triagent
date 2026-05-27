package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sourcehawk/triagent/pkg/mcp/sessions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// validateSessionCmd is the hidden `triagent-mcp validate-session` subcommand.
// Reads a session.md file from stdin, runs frontmatter + body-header
// schema validation, and writes a JSON result to stdout:
//
//	{"ok": true}
//	{"ok": false, "errors": ["..."]}
//
// Exit code 0 = ok, 1 = validation errors, 2 = read/parse problem.
//
// Used by the upstream c1-investigation-sessions repo's CI to validate
// every pushed session.md on PR. Mirrors validate-wiki / validate-playbook
// so the reusable workflow shape stays uniform across the three
// repo-backed surfaces.
func validateSessionCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "validate-session",
		Short:  "Validate a session markdown file read from stdin",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				writeSessionValidateResult(false, []string{fmt.Sprintf("read stdin: %v", err)})
				os.Exit(2)
				return nil
			}
			if len(data) == 0 {
				writeSessionValidateResult(false, []string{"empty input"})
				os.Exit(1)
				return nil
			}
			fmBytes, body, err := sessions.SplitFrontmatter(data)
			if err != nil {
				writeSessionValidateResult(false, []string{fmt.Sprintf("frontmatter: %v", err)})
				os.Exit(1)
				return nil
			}
			var fm sessions.Frontmatter
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				writeSessionValidateResult(false, []string{fmt.Sprintf("frontmatter parse: %v", err)})
				os.Exit(1)
				return nil
			}
			var errs []string
			errs = append(errs, sessions.ValidateFrontmatter(fm)...)
			errs = append(errs, sessions.ValidateBodyHeaders(string(body))...)
			if len(errs) > 0 {
				writeSessionValidateResult(false, errs)
				os.Exit(1)
				return nil
			}
			writeSessionValidateResult(true, nil)
			return nil
		},
	}
}

type sessionValidateResult struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

func writeSessionValidateResult(ok bool, errs []string) {
	out := sessionValidateResult{OK: ok, Errors: errs}
	b, _ := json.Marshal(out)
	_, _ = fmt.Fprintln(os.Stdout, string(b))
}
