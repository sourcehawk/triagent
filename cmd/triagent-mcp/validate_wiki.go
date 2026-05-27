package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/wiki"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// validateWikiCmd is the hidden `triagent-mcp validate-wiki` subcommand.
// Reads a wiki markdown file from stdin, autodetects whether it's an
// incident note or an entity stub from the frontmatter shape, runs the
// matching schema validation, and writes a JSON result to stdout:
//
//	{"ok": true, "kind": "incident"}
//	{"ok": false, "kind": "incident", "errors": ["..."]}
//
// Exit code 0 = ok, 1 = validation errors, 2 = read/parse problem.
//
// Used by the upstream wiki repo's CI to validate every incident /
// entity stub on PR. Mirrors validate-playbook's contract so the
// reusable workflow shape stays uniform.
func validateWikiCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "validate-wiki",
		Short:  "Validate a wiki incident note or entity stub read from stdin",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				writeWikiValidateResult("unknown", false, []string{fmt.Sprintf("read stdin: %v", err)})
				os.Exit(2)
				return nil
			}
			if len(data) == 0 {
				writeWikiValidateResult("unknown", false, []string{"empty input"})
				os.Exit(1)
				return nil
			}
			fmBytes, body, err := wiki.SplitFrontmatter(data)
			if err != nil {
				writeWikiValidateResult("unknown", false, []string{fmt.Sprintf("frontmatter: %v", err)})
				os.Exit(1)
				return nil
			}

			// Probe the frontmatter shape to decide which schema to run.
			var probe map[string]any
			if err := yaml.Unmarshal(fmBytes, &probe); err != nil {
				writeWikiValidateResult("unknown", false, []string{fmt.Sprintf("frontmatter parse: %v", err)})
				os.Exit(1)
				return nil
			}

			switch {
			case hasField(probe, "id"):
				validateIncident(fmBytes, body)
			case hasField(probe, "type") && hasField(probe, "name"):
				validateEntity(fmBytes)
			default:
				writeWikiValidateResult("unknown", false, []string{"frontmatter has neither 'id:' (incident) nor 'type:'+'name:' (entity stub)"})
				os.Exit(1)
			}
			return nil
		},
	}
}

func validateIncident(fmBytes, body []byte) {
	var fm wiki.Frontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		writeWikiValidateResult("incident", false, []string{fmt.Sprintf("incident frontmatter parse: %v", err)})
		os.Exit(1)
	}
	var errs []string
	errs = append(errs, wiki.ValidateFrontmatter(fm)...)
	errs = append(errs, wiki.ValidateBodyHeaders(string(body))...)
	if len(errs) > 0 {
		writeWikiValidateResult("incident", false, errs)
		os.Exit(1)
	}
	writeWikiValidateResult("incident", true, nil)
}

func validateEntity(fmBytes []byte) {
	var ef wiki.EntityFrontmatter
	if err := yaml.Unmarshal(fmBytes, &ef); err != nil {
		writeWikiValidateResult("entity", false, []string{fmt.Sprintf("entity frontmatter parse: %v", err)})
		os.Exit(1)
	}
	if errs := wiki.ValidateEntityFrontmatter(ef); len(errs) > 0 {
		writeWikiValidateResult("entity", false, errs)
		os.Exit(1)
	}
	writeWikiValidateResult("entity", true, nil)
}

func hasField(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return v != nil
}

type wikiValidateResult struct {
	OK     bool     `json:"ok"`
	Kind   string   `json:"kind"`
	Errors []string `json:"errors,omitempty"`
}

func writeWikiValidateResult(kind string, ok bool, errs []string) {
	out := wikiValidateResult{OK: ok, Kind: kind, Errors: errs}
	b, _ := json.Marshal(out)
	_, _ = fmt.Fprintln(os.Stdout, string(b))
}
