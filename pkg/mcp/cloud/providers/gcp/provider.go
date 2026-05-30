// Package gcp implements the cloud.Provider contract over the gcloud CLI. It is
// selected by --provider=gcp and plugged into the cloud-context MCP behind the
// Provider interface (the teleport DI pattern); it never reaches into the parent
// cloud package's harness. All cloud access shells gcloud through the injected
// cloud.RunFunc — there is no cloud.google.com/go SDK dependency.
package gcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// defaultCommandsJSON is the embedded read-only gcloud command allowlist. Each
// entry's description names the investigative axis it serves. The exact-match
// allowlist requires the complete invariant verb chain per entry.
//
//go:embed default_commands.json
var defaultCommandsJSON []byte

// impersonationEnv is the env var the launcher sets to pin the read-only
// service account gcloud impersonates. The provider reads it (never sets it) to
// learn which identity Identity must resolve to; it is on the agent deny floor
// as a flag, so the agent can never select it.
const impersonationEnv = "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"

// Provider implements cloud.Provider over the gcloud CLI.
type Provider struct {
	binary    string
	allowlist *cloud.CommandAllowlist
}

// New constructs the gcp provider, resolving gcloud to an absolute path once via
// exec.LookPath so a poisoned PATH cannot redirect the binary at run time.
func New() (*Provider, error) {
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		return nil, fmt.Errorf("gcp: resolve gcloud binary: %w", err)
	}
	return newWithBinary(bin)
}

// newWithBinary builds the provider against an already-resolved binary path. It
// is the seam tests inject a fixed path through, bypassing exec.LookPath.
func newWithBinary(binary string) (*Provider, error) {
	var list cloud.CommandAllowlist
	if err := json.Unmarshal(defaultCommandsJSON, &list); err != nil {
		return nil, fmt.Errorf("gcp: parse embedded default_commands.json: %w", err)
	}
	return &Provider{binary: binary, allowlist: &list}, nil
}

// Name reports the provider identifier.
func (p *Provider) Name() string { return "gcp" }

// Binary is the resolved absolute path to gcloud.
func (p *Provider) Binary() string { return p.binary }

// DefaultAllowlist is the embedded read-only command allowlist.
func (p *Provider) DefaultAllowlist() *cloud.CommandAllowlist { return p.allowlist }

// DenyFloorAdditions contributes gcp-specific subcommands that read credentials,
// shell into instances, or mutate by side effect, on top of the base floor.
func (p *Provider) DenyFloorAdditions() cloud.DenyFloor {
	return cloud.DenyFloor{
		Subcommands: []string{
			"compute ssh",
			"compute scp",
			"compute reset-windows-password",
			"functions call",
		},
	}
}

// EnvPassthrough names the gcloud env vars the subprocess needs: the pinned
// impersonation target plus the config and active-project locations. PATH and
// HOME are forwarded by the harness base set, so they are absent here.
func (p *Provider) EnvPassthrough() []string {
	return []string{
		impersonationEnv,
		"CLOUDSDK_CONFIG",
		"CLOUDSDK_CORE_PROJECT",
	}
}
