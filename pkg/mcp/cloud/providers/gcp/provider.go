// Package gcp implements the cloud.Provider contract over the gcloud CLI. It is
// selected by --provider=gcp and plugged into the cloud-context MCP behind the
// Provider interface (the teleport DI pattern); it never reaches into the parent
// cloud package's harness. All cloud access shells gcloud through the injected
// cloud.RunFunc — there is no cloud.google.com/go SDK dependency.
package gcp

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// defaultCommandsJSON is the embedded read-only gcloud command allowlist. Each
// entry's description names the investigative axis it serves. The exact-match
// allowlist requires the complete invariant verb chain per entry.
//
//go:embed default_commands.json
var defaultCommandsJSON []byte

// EnvImpersonate is the env var the launcher sets to pin the read-only
// service account gcloud impersonates. The provider reads it (never sets it) to
// learn which identity Identity must resolve to; it is on the agent deny floor
// as a flag, so the agent can never select it.
const EnvImpersonate = "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"

var _ cloud.Provider = (*Provider)(nil)

// Project is one deployment-configured selectable project: the project id the
// agent selects by, and the free-form tags surfaced by list_inventory so it can
// judge relevance.
type Project struct {
	ID   string
	Tags []string
}

// Options carries the gcp config the launcher threads from the profile: the
// deployment's selectable projects and their tags. The zero value (no projects)
// is the unconstrained form — the provider lists projects live instead.
type Options struct {
	Projects []Project
}

// Provider implements cloud.Provider over the gcloud CLI. projects is the
// deployment-configured selectable set (with tags); empty means unconstrained
// (Inventory lists projects live).
type Provider struct {
	binary    string
	allowlist *cloud.CommandAllowlist
	projects  []Project
}

// New constructs the gcp provider, resolving gcloud to an absolute path once via
// exec.LookPath so a poisoned PATH cannot redirect the binary at run time. A
// PATH with relative entries makes LookPath return a relative path (flagged with
// exec.ErrDot); the path is made absolute so a later subprocess env/PATH change
// cannot reinterpret it against a different working directory.
func New(opts ...Options) (*Provider, error) {
	bin, err := exec.LookPath("gcloud")
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return nil, fmt.Errorf("gcp: resolve gcloud binary: %w", err)
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return nil, fmt.Errorf("gcp: resolve gcloud binary to absolute path: %w", err)
	}
	return newWithBinary(abs, opts...)
}

// newWithBinary builds the provider against an already-resolved binary path. It
// is the seam tests inject a fixed path through, bypassing exec.LookPath. At most
// one Options is honored.
func newWithBinary(binary string, opts ...Options) (*Provider, error) {
	var list cloud.CommandAllowlist
	if err := json.Unmarshal(defaultCommandsJSON, &list); err != nil {
		return nil, fmt.Errorf("gcp: parse embedded default_commands.json: %w", err)
	}
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Provider{binary: binary, allowlist: &list, projects: o.Projects}, nil
}

// Name reports the provider identifier.
func (p *Provider) Name() string { return "gcp" }

// Binary is the resolved absolute path to gcloud.
func (p *Provider) Binary() string { return p.binary }

// DefaultAllowlist is the embedded read-only command allowlist.
func (p *Provider) DefaultAllowlist() *cloud.CommandAllowlist { return p.allowlist }

// DenyFloorAdditions contributes gcp-specific subcommands that read credentials,
// shell into instances, exfiltrate or read object contents, decrypt, or mutate by
// side effect, on top of the base floor. The base floor prefix-matches top-level
// tokens, so it never reaches the nested storage/kms verbs; each is listed by its
// full token-wise path. `gcloud secrets versions access` is already covered by
// the base `secrets` prefix. Metadata reads (`storage ls`, `storage buckets
// describe`, `kms keys list`) are deliberately absent: the floor targets object
// CONTENTS and decryption, not listing or describing.
func (p *Provider) DenyFloorAdditions() cloud.DenyFloor {
	return cloud.DenyFloor{
		Subcommands: []string{
			"compute ssh",
			"compute scp",
			"compute reset-windows-password",
			"functions call",
			"storage cp",
			"storage mv",
			"storage rsync",
			"storage cat",
			"kms decrypt",
		},
	}
}

// ConfiguredTargets is the deployment-configured project set surfaced as the
// agent's selectable targets, with each project's tags. Empty when the source
// configured no projects (the unconstrained form), so the server falls back to
// the live inventory instead.
func (p *Provider) ConfiguredTargets() []cloud.Target {
	out := make([]cloud.Target, 0, len(p.projects))
	for _, pr := range p.projects {
		out = append(out, cloud.Target{ID: pr.ID, Name: pr.ID, Tags: pr.Tags})
	}
	return out
}

// ActiveTargetEnv pins gcloud to the active project via CLOUDSDK_CORE_PROJECT,
// the default project for every command that takes one. One impersonated
// identity spans the allowlisted projects, so switching changes only the
// project, never the identity.
func (p *Provider) ActiveTargetEnv(id string) []string {
	return []string{"CLOUDSDK_CORE_PROJECT=" + id}
}

// ExpectedIdentity reports no per-target identity: one impersonated service
// account spans every allowlisted project, so switching projects never changes
// the identity. The server validates against the session's pinned identity.
func (p *Provider) ExpectedIdentity(string) (string, bool) { return "", false }

// EnvPassthrough names the gcloud env vars the subprocess needs: the pinned
// impersonation target plus the config and active-project locations. PATH and
// HOME are forwarded by the harness base set, so they are absent here.
func (p *Provider) EnvPassthrough() []string {
	return []string{
		EnvImpersonate,
		"CLOUDSDK_CONFIG",
		"CLOUDSDK_CORE_PROJECT",
	}
}
