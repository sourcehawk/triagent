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
	// server's entry; an unknown provider or a malformed identity shape reaches
	// preflight as a broken connection. Catch all of it here. The identity shape
	// is provider-specific: gcp pins one impersonated service account
	// (assumed_identity); aws pins a per-account role set (accounts +
	// source_profile) and has no single assumed identity.
	seenAliases := map[string]bool{}
	for i, c := range p.Cloud {
		if c.Alias == "" {
			errs = append(errs, fmt.Sprintf("cloud[%d].alias: required", i))
		} else if seenAliases[c.Alias] {
			errs = append(errs, fmt.Sprintf("cloud[%d].alias: duplicate %q", i, c.Alias))
		}
		seenAliases[c.Alias] = true

		switch c.Provider {
		case "gcp":
			if c.AssumedIdentity == "" {
				errs = append(errs, fmt.Sprintf("cloud[%d].assumed_identity: required when provider=gcp", i))
			}
			if len(c.Accounts) > 0 || c.SourceProfile != "" {
				errs = append(errs, fmt.Sprintf("cloud[%d]: accounts/source_profile are aws-only; gcp selects projects", i))
			}
			errs = append(errs, validateGCPProjects(i, c)...)
		case "aws":
			if c.AssumedIdentity != "" {
				errs = append(errs, fmt.Sprintf("cloud[%d].assumed_identity: must be empty when provider=aws (each account pins its own role_arn)", i))
			}
			if len(c.Projects) > 0 {
				errs = append(errs, fmt.Sprintf("cloud[%d].projects: gcp-only; an aws source selects accounts", i))
			}
			errs = append(errs, validateAWSCredentials(i, c)...)
		case "":
			errs = append(errs, fmt.Sprintf("cloud[%d].provider: required (supported: gcp, aws)", i))
		default:
			errs = append(errs, fmt.Sprintf("cloud[%d].provider: unknown %q (supported: gcp, aws)", i, c.Provider))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("profile " + p.Name + " invalid:\n  - " + strings.Join(errs, "\n  - "))
}

// validateGCPProjects checks the optional gcp project set: each entry needs a
// non-empty id, unique across the source. Tags are free-form and unchecked. An
// empty set is valid — the agent selects among the live-listed projects instead.
func validateGCPProjects(i int, c CloudSource) []string {
	var errs []string
	seen := map[string]bool{}
	for j, pr := range c.Projects {
		switch {
		case pr.ID == "":
			errs = append(errs, fmt.Sprintf("cloud[%d].projects[%d].id: required", i, j))
		case seen[pr.ID]:
			errs = append(errs, fmt.Sprintf("cloud[%d].projects[%d].id: duplicate %q", i, j, pr.ID))
		}
		seen[pr.ID] = true
	}
	return errs
}

// validateAWSCredentials checks the aws credential shape: an accounts list (a
// single-account source is a one-entry list) plus the operator's source_profile
// the generated per-account assume-role profiles layer over. Account ids and
// role_arns are each required and unique across the source.
func validateAWSCredentials(i int, c CloudSource) []string {
	var errs []string
	if len(c.Accounts) == 0 {
		errs = append(errs, fmt.Sprintf("cloud[%d].accounts: required when provider=aws (a single-account source is a one-entry list)", i))
	}
	if c.SourceProfile == "" {
		errs = append(errs, fmt.Sprintf("cloud[%d].source_profile: required when provider=aws", i))
	}
	seenIDs := map[string]bool{}
	seenARNs := map[string]bool{}
	for j, a := range c.Accounts {
		switch {
		case a.AccountID == "":
			errs = append(errs, fmt.Sprintf("cloud[%d].accounts[%d].account_id: required", i, j))
		case seenIDs[a.AccountID]:
			errs = append(errs, fmt.Sprintf("cloud[%d].accounts[%d].account_id: duplicate %q", i, j, a.AccountID))
		}
		seenIDs[a.AccountID] = true

		switch {
		case a.RoleARN == "":
			errs = append(errs, fmt.Sprintf("cloud[%d].accounts[%d].role_arn: required", i, j))
		case seenARNs[a.RoleARN]:
			errs = append(errs, fmt.Sprintf("cloud[%d].accounts[%d].role_arn: duplicate %q", i, j, a.RoleARN))
		}
		seenARNs[a.RoleARN] = true
	}
	return errs
}
