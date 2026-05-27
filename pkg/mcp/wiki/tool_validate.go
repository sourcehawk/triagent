package wiki

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// validate_wiki: agent-callable schema validator for wiki markdown.
//
// Lets the investigation agent check a draft (or an existing entry it's
// reading via wiki_get) against the canonical schema before calling
// propose_wiki_draft. Mirrors the strategies package's validate_playbook
// contract: returns {ok, errors[]} with structured error strings the
// agent can act on.
//
// Auto-detects whether the input is an incident note (frontmatter has
// `id:`) or an entity stub (`type:` + `name:`). Same detection the CLI
// subcommand `validate-wiki` uses.

type validateWikiIn struct {
	Markdown string `json:"markdown" jsonschema:"the full wiki markdown to validate (frontmatter + body, including the leading and closing --- delimiters)"`
}

type validateWikiOut struct {
	OK     bool     `json:"ok"`
	Kind   string   `json:"kind"` // "incident" | "entity" | "unknown"
	Errors []string `json:"errors,omitempty"`
}

func (s *Server) validateWiki(ctx context.Context, _ *mcp.CallToolRequest, in validateWikiIn) (*mcp.CallToolResult, validateWikiOut, error) {
	if strings.TrimSpace(in.Markdown) == "" {
		return nil, validateWikiOut{OK: false, Kind: "unknown", Errors: []string{"markdown is required"}}, nil
	}
	fmBytes, body, err := SplitFrontmatter([]byte(in.Markdown))
	if err != nil {
		return nil, validateWikiOut{OK: false, Kind: "unknown", Errors: []string{"frontmatter: " + err.Error()}}, nil
	}

	// Probe shape to pick the right validator.
	var probe map[string]any
	if err := yaml.Unmarshal(fmBytes, &probe); err != nil {
		return nil, validateWikiOut{OK: false, Kind: "unknown", Errors: []string{"frontmatter parse: " + err.Error()}}, nil
	}

	hasID := probeHasField(probe, "id")
	hasType := probeHasField(probe, "type")
	hasName := probeHasField(probe, "name")

	switch {
	case hasID:
		var fm Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return nil, validateWikiOut{OK: false, Kind: "incident", Errors: []string{"incident frontmatter parse: " + err.Error()}}, nil
		}
		var errs []string
		errs = append(errs, ValidateFrontmatter(fm)...)
		errs = append(errs, ValidateBodyHeaders(string(body))...)
		if len(errs) > 0 {
			return nil, validateWikiOut{OK: false, Kind: "incident", Errors: errs}, nil
		}
		return nil, validateWikiOut{OK: true, Kind: "incident"}, nil

	case hasType && hasName:
		var ef EntityFrontmatter
		if err := yaml.Unmarshal(fmBytes, &ef); err != nil {
			return nil, validateWikiOut{OK: false, Kind: "entity", Errors: []string{"entity frontmatter parse: " + err.Error()}}, nil
		}
		if errs := ValidateEntityFrontmatter(ef); len(errs) > 0 {
			return nil, validateWikiOut{OK: false, Kind: "entity", Errors: errs}, nil
		}
		return nil, validateWikiOut{OK: true, Kind: "entity"}, nil
	}

	return nil, validateWikiOut{
		OK:     false,
		Kind:   "unknown",
		Errors: []string{"frontmatter has neither 'id:' (incident) nor 'type:'+'name:' (entity stub)"},
	}, nil
}

func probeHasField(m map[string]any, key string) bool {
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
