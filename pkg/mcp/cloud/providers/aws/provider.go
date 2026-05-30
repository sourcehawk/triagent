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

// EnvProfile is the env var the launcher sets to select the assume-role profile
// whose role_arn is the deployment's read-only role (with the operator's base
// credentials as source_profile). The provider reads it through the CLI, never
// sets it; the --profile flag stays on the agent deny floor so the agent can
// never select the profile itself.
const EnvProfile = "AWS_PROFILE"

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

// New constructs the AWS provider, resolving aws to an absolute path once via
// exec.LookPath so a poisoned PATH cannot redirect the binary at run time.
func New() (*Provider, error) {
	bin, err := exec.LookPath("aws")
	if err != nil {
		return nil, fmt.Errorf("aws: resolve aws binary: %w", err)
	}
	return newWithBinary(bin)
}

// newWithBinary builds the provider against an already-resolved binary path. It
// is the seam tests inject a fixed path through, bypassing exec.LookPath.
func newWithBinary(binary string) (*Provider, error) {
	var list cloud.CommandAllowlist
	if err := json.Unmarshal(defaultCommandsJSON, &list); err != nil {
		return nil, fmt.Errorf("aws: parse default allowlist: %w", err)
	}
	return &Provider{binary: binary, allowlist: &list}, nil
}

// Name reports the provider identifier.
func (p *Provider) Name() string { return "aws" }

// Binary is the resolved absolute path to the aws CLI.
func (p *Provider) Binary() string { return p.binary }

// DefaultAllowlist is the embedded default command allowlist: read-only
// describe/get/list/lookup verbs across the investigative axes (inventory,
// reachability, permissions, cluster, logs, audit).
func (p *Provider) DefaultAllowlist() *cloud.CommandAllowlist { return p.allowlist }

// DenyFloorAdditions contributes the AWS-specific subcommands that return secret
// material, object contents, decrypted plaintext, or shell access beyond the base
// floor. The base floor prefix-matches top-level tokens, so it never reaches
// these nested verbs; each is listed by its full token-wise path. Metadata reads
// under the same services (describe-secret, list-secrets, head-object,
// describe-parameters, describe-key) are deliberately absent: the floor targets
// secret VALUES, object CONTENTS, and decryption, not listing or describing.
func (p *Provider) DenyFloorAdditions() cloud.DenyFloor {
	return cloud.DenyFloor{
		Subcommands: []string{
			"ec2 get-password-data",
			"ec2-instance-connect send-ssh-public-key",
			"ec2-instance-connect send-serial-console-ssh-public-key",
			"sts get-session-token",
			"sts get-federation-token",
			"secretsmanager get-secret-value",
			"s3 cp",
			"s3 mv",
			"s3 sync",
			"s3api get-object",
			"s3api get-object-attributes",
			"s3api get-object-torrent",
			"kms decrypt",
			"ssm get-parameter",
			"ssm get-parameters",
			"ssm get-parameters-by-path",
		},
	}
}

// EnvPassthrough lists the env var NAMES the aws subprocess needs forwarded:
// AWS_PROFILE pins the assume-role identity; the region and config-file names
// let the launcher point the CLI at the right account/config without the agent
// supplying them as argv. PATH and HOME are forwarded by the harness base set.
func (p *Provider) EnvPassthrough() []string {
	return []string{
		EnvProfile,
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
	}
}
