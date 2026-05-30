package profile

import (
	"errors"
	"fmt"
	"strings"
)

// Validate reports configuration errors that would prevent the launcher
// from starting. All errors are joined; callers see the full list at
// once rather than one-at-a-time.
func (p *Profile) Validate() error {
	var errs []string

	if p.Name == "" {
		errs = append(errs, "name: required")
	}
	if p.Playbooks.Entrypoint == "" {
		errs = append(errs, "playbooks.entrypoint: required")
	}
	if p.Playbooks.Closing == "" {
		errs = append(errs, "playbooks.closing: required")
	}

	switch p.Auth.Kind {
	case "kubeconfig":
		// No additional fields required.
	case "teleport":
		if p.Auth.Teleport.Proxy == "" {
			errs = append(errs, "auth.teleport.proxy: required when auth.kind=teleport")
		}
		if p.Auth.Teleport.AuthConnector == "" {
			errs = append(errs, "auth.teleport.auth_connector: required when auth.kind=teleport")
		}
	case "":
		errs = append(errs, "auth.kind: required")
	default:
		errs = append(errs, fmt.Sprintf("auth.kind: unknown value %q (supported: kubeconfig, teleport)", p.Auth.Kind))
	}

	// Investigation-input shape validation (light; deeper template
	// validation lives in Milestone 4).
	seenIDs := map[string]bool{}
	for i, in := range p.InvestigationInputs {
		if in.ID == "" {
			errs = append(errs, fmt.Sprintf("investigation_inputs[%d].id: required", i))
			continue
		}
		if seenIDs[in.ID] {
			errs = append(errs, fmt.Sprintf("investigation_inputs[%d].id: duplicate %q", i, in.ID))
		}
		seenIDs[in.ID] = true
		switch in.Type {
		case "text", "url", "textarea", "cluster_id", "slack_channel":
		case "":
			errs = append(errs, fmt.Sprintf("investigation_inputs[%d].type: required", i))
		default:
			errs = append(errs, fmt.Sprintf("investigation_inputs[%d].type: unknown %q (supported: text, url, textarea, cluster_id, slack_channel)", i, in.Type))
		}
	}

	// Cloud sources are wired per session as triagent-cloud-<alias> MCP servers
	// keyed by alias, so a duplicate or empty alias silently overwrites another
	// server's entry; an unknown provider, missing identity, or aws source
	// without a profile reaches preflight as a broken connection. Catch all of
	// it here.
	seenAliases := map[string]bool{}
	for i, c := range p.Cloud {
		if c.Alias == "" {
			errs = append(errs, fmt.Sprintf("cloud[%d].alias: required", i))
		} else if seenAliases[c.Alias] {
			errs = append(errs, fmt.Sprintf("cloud[%d].alias: duplicate %q", i, c.Alias))
		}
		seenAliases[c.Alias] = true

		switch c.Provider {
		case "gcp", "aws":
		case "":
			errs = append(errs, fmt.Sprintf("cloud[%d].provider: required (supported: gcp, aws)", i))
		default:
			errs = append(errs, fmt.Sprintf("cloud[%d].provider: unknown %q (supported: gcp, aws)", i, c.Provider))
		}

		if c.AssumedIdentity == "" {
			errs = append(errs, fmt.Sprintf("cloud[%d].assumed_identity: required", i))
		}
		if c.Provider == "aws" && c.Profile == "" {
			errs = append(errs, fmt.Sprintf("cloud[%d].profile: required when provider=aws", i))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("profile " + p.Name + " invalid:\n  - " + strings.Join(errs, "\n  - "))
}
