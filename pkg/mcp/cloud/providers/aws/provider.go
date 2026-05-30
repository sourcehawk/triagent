// Package aws implements the cloud.Provider contract over the read-only aws CLI.
// It ships the AWS default command allowlist, the AWS-specific deny-floor
// additions, the env names the aws subprocess needs, and the projection parsers
// for identity and inventory. It never shells the CLI directly: every invocation
// goes through the cloud.RunFunc the harness injects.
//
// The pinned identity is realized by the launcher through AWS_PROFILE: a profile
// whose role_arn is the deployment's read-only role, with the operator's base
// credentials as source_profile. The provider never selects the profile; the
// --profile flag stays on the agent deny floor.
package aws

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

//go:embed default_commands.json
var defaultCommandsJSON []byte

// Provider satisfies the cloud.Provider contract.
var _ cloud.Provider = (*Provider)(nil)

// AWS account scoping decision (bubble-up from #45): the cloud package's
// ScopeAllowlist.Accounts field is not enforced in validateArgv, and AWS has no
// single --account flag to scope on. In the operator-ambient model the account
// is fixed by the assume-role profile (AWS_PROFILE): the pinned identity can only
// act in the account(s) its role grants, so the identity itself constrains the
// account and argv-level account scoping is unnecessary here. Region scoping
// (the --region/--zone axis) is still enforced by validateArgv against
// ScopeAllowlist.Regions. If a future deployment needs sub-account argv scoping,
// it belongs in the shared validateArgv, not in this provider.

// Provider is the AWS realization of cloud.Provider. binary is resolved once at
// construction (overridable in tests); allowlist is the parsed embedded default.
type Provider struct {
	binary    string
	allowlist *cloud.CommandAllowlist
}

// New constructs the AWS provider, resolving the aws binary on PATH and parsing
// the embedded default allowlist. A missing aws binary is not a construction
// error: Binary falls back to the literal "aws" and the exec surfaces the
// failure as a visible degrade through the identity probe's Hint, rather than
// failing the launcher at startup. New errors only when the embedded allowlist
// is malformed (a build-time defect).
func New() (*Provider, error) {
	bin := "aws"
	if resolved, err := exec.LookPath("aws"); err == nil {
		bin = resolved
	}
	allow, err := parseAllowlist()
	if err != nil {
		return nil, err
	}
	return &Provider{binary: bin, allowlist: allow}, nil
}

// parseAllowlist decodes the embedded default command allowlist.
func parseAllowlist() (*cloud.CommandAllowlist, error) {
	var list cloud.CommandAllowlist
	if err := json.Unmarshal(defaultCommandsJSON, &list); err != nil {
		return nil, fmt.Errorf("aws: parse default allowlist: %w", err)
	}
	return &list, nil
}

// Name reports the provider identifier.
func (p *Provider) Name() string { return "aws" }

// Binary is the resolved absolute path to the aws CLI.
func (p *Provider) Binary() string { return p.binary }

// DefaultAllowlist is the embedded default command allowlist: read-only
// describe/get/list/lookup verbs across the investigative axes (inventory,
// reachability, permissions, cluster, logs, audit).
func (p *Provider) DefaultAllowlist() *cloud.CommandAllowlist { return p.allowlist }

// DenyFloorAdditions contributes the AWS-specific subcommands that return
// credential material or shell access beyond the base floor. The base floor
// already covers the secrets/ssh/auth/config families and identity flags; these
// add the credential-returning reads unique to AWS.
func (p *Provider) DenyFloorAdditions() cloud.DenyFloor {
	return cloud.DenyFloor{
		Subcommands: []string{
			"ec2 get-password-data",
			"ec2-instance-connect send-ssh-public-key",
			"ec2-instance-connect send-serial-console-ssh-public-key",
			"sts get-session-token",
			"sts get-federation-token",
		},
	}
}

// EnvPassthrough lists the env var NAMES the aws subprocess needs forwarded:
// AWS_PROFILE pins the assume-role identity; the region and config-file names
// let the launcher point the CLI at the right account/config without the agent
// supplying them as argv. PATH and HOME are forwarded by the harness base set.
func (p *Provider) EnvPassthrough() []string {
	return []string{
		"AWS_PROFILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
	}
}
