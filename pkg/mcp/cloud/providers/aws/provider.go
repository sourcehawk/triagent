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
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

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

// Account is one configured aws account the agent may make active: the account
// id it selects by, and the read-only role_arn triagent generates an assume-role
// profile for, layered over the source's SSO base.
type Account struct {
	ID      string
	RoleARN string
}

// Options carries the multi-account config the launcher threads through from the
// profile's cloud source: the source alias (the generated profiles' namespace),
// the operator's SSO source_profile, and the account set. The zero value is the
// single-account legacy form — no generated profiles, the selectable set comes
// from inventory.
type Options struct {
	Alias         string
	SourceProfile string
	Accounts      []Account
}

// Provider is the AWS realization of cloud.Provider. binary is resolved once at
// construction (overridable in tests); allowlist is the parsed embedded default.
// alias and accounts carry the multi-account config: ConfiguredTargets surfaces
// the accounts as the selectable set, and ActiveTargetEnv names each account's
// generated profile.
type Provider struct {
	binary    string
	allowlist *cloud.CommandAllowlist
	alias     string
	accounts  []Account
}

// New constructs the AWS provider, resolving aws to an absolute path once via
// exec.LookPath so a poisoned PATH cannot redirect the binary at run time. A
// PATH with relative entries makes LookPath return a relative path (flagged with
// exec.ErrDot); the path is made absolute so a later subprocess env/PATH change
// cannot reinterpret it against a different working directory.
//
// When opts carries accounts, New generates the per-account assume-role profiles
// into ~/.aws/config (or $AWS_CONFIG_FILE) before returning, so the profiles
// exist for both the serve subprocess and any launcher-side probe that runs the
// CLI under AWS_PROFILE. Generation is idempotent: repeated construction (serve
// and launcher both build the provider) replaces the alias's managed block.
func New(opts ...Options) (*Provider, error) {
	bin, err := exec.LookPath("aws")
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return nil, fmt.Errorf("aws: resolve aws binary: %w", err)
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return nil, fmt.Errorf("aws: resolve aws binary to absolute path: %w", err)
	}
	return newWithBinary(abs, opts...)
}

// newWithBinary builds the provider against an already-resolved binary path. It
// is the seam tests inject a fixed path through, bypassing exec.LookPath. At most
// one Options is honored.
func newWithBinary(binary string, opts ...Options) (*Provider, error) {
	var list cloud.CommandAllowlist
	if err := json.Unmarshal(defaultCommandsJSON, &list); err != nil {
		return nil, fmt.Errorf("aws: parse default allowlist: %w", err)
	}
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if len(o.Accounts) > 0 {
		cfg := awsConfigPath()
		if cfg == "" {
			return nil, fmt.Errorf("aws: cannot resolve ~/.aws/config (no HOME and no AWS_CONFIG_FILE) to generate account profiles")
		}
		if err := writeManagedProfiles(cfg, o.Alias, o.SourceProfile, o.Accounts); err != nil {
			return nil, fmt.Errorf("aws: generate account profiles: %w", err)
		}
	}
	return &Provider{binary: binary, allowlist: &list, alias: o.Alias, accounts: o.Accounts}, nil
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

// ConfiguredTargets is the configured account set surfaced as the agent's
// selectable targets. A configured account's id is both the Target ID (what
// set_active_target receives) and its Name. A single-account source is a
// one-entry list, so the set is never derived from a live inventory query.
func (p *Provider) ConfiguredTargets() []cloud.Target {
	out := make([]cloud.Target, 0, len(p.accounts))
	for _, a := range p.accounts {
		out = append(out, cloud.Target{ID: a.ID, Name: a.ID})
	}
	return out
}

// ActiveTargetEnv pins the aws CLI to the active account via AWS_PROFILE, naming
// the generated assume-role profile (triagent-cloud-<alias>-<account_id>). The
// value is a profile name, not a credential: the CLI performs the assume-role
// from the operator's base.
func (p *Provider) ActiveTargetEnv(id string) []string {
	return []string{EnvProfile + "=" + profileName(p.alias, id)}
}

// ExpectedIdentity returns the read-only role ARN configured for the account
// id, so session_status validates the active account against its own role on
// switch rather than a single source-level identity. ok is false for an id not
// in the configured set, leaving the server to fall back to its pinned identity.
func (p *Provider) ExpectedIdentity(id string) (string, bool) {
	for _, a := range p.accounts {
		if a.ID == id {
			return a.RoleARN, true
		}
	}
	return "", false
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
