package providers

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
)

// probeTimeout bounds a single identity probe so a hung CLI (a stale SSO flow,
// a slow network, a wedged gcloud/aws) cannot block /api/connections or session
// preflight indefinitely — the "degrade, never block" contract. A normal whoami
// returns in 1-3s; 15s sits comfortably above that yet well below anything that
// would stall a request. On deadline the CLI exec is killed, the provider
// surfaces the context error, and the probe degrades to Valid:false with a hint
// rather than hanging. A package var, not a const, so tests can shorten it.
var probeTimeout = 15 * time.Second

// baseEnvPassthrough is the minimal env every provider CLI needs regardless of
// cloud: PATH so the resolved binary can find its own dependencies, HOME so it
// can locate per-user config. It mirrors the launcher-side serve harness; the
// per-source credential vars are overlaid on top.
var baseEnvPassthrough = []string{"PATH", "HOME"}

// Source is a neutral description of one cloud connection to probe: the provider
// name, the pinned identity, and the aws credential config. It carries exactly
// what ProbeSource needs without coupling this package to the launcher's profile
// type.
//
// AWS has two forms. The single-account form sets Profile (the operator's
// AWS_PROFILE selector). The multi-account form sets Alias, SourceProfile, and
// Accounts; ProbeSource generates the per-account profiles and probes the default
// (first) account's generated profile — per-account validity is out of scope for
// v1, so the panel reflects the source's default-target validity. gcp ignores
// all four.
type Source struct {
	Provider        string
	AssumedIdentity string
	Profile         string // aws single-account AWS_PROFILE selector; ignored by gcp
	Alias           string // aws multi-account: the generated profiles' namespace
	SourceProfile   string // aws multi-account: the operator's SSO base profile
	Accounts        []aws.Account
}

// ProbeSource constructs the source's provider and runs the read-only identity
// probe, threading the source's pinned identity and the subprocess credential
// env explicitly so concurrent probes for different sources never share state.
// It degrades, never blocks: a provider construction error (e.g. a missing CLI
// binary) returns an invalid status with the error as the hint, exactly like a
// failed probe.
func ProbeSource(ctx context.Context, src Source) cloud.IdentityStatus {
	p, err := New(src.Provider, Options{
		AWSAlias:         src.Alias,
		AWSSourceProfile: src.SourceProfile,
		AWSAccounts:      src.Accounts,
	})
	if err != nil {
		return cloud.IdentityStatus{
			Provider:        src.Provider,
			AssumedIdentity: src.AssumedIdentity,
			Valid:           false,
			Hint:            err.Error(),
		}
	}
	return probeProvider(ctx, p, src.AssumedIdentity, sourceEnvFor(p, src))
}

// probeProvider runs the identity probe for an already-constructed provider
// under a bounded timeout, so a hung CLI degrades to an invalid status instead
// of blocking the caller. The deadline cancels the CLI exec; cloud.Probe
// surfaces the resulting context error as a Valid:false status with a hint.
func probeProvider(ctx context.Context, p cloud.Provider, expected string, env []string) cloud.IdentityStatus {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	st, _ := cloud.Probe(ctx, p, expected, env)
	return st
}

// passthroughLister is the slice of the cloud.Provider contract sourceEnvFor
// needs: which env names the provider's CLI carries from the parent process.
type passthroughLister interface {
	EnvPassthrough() []string
}

// sourceEnvFor builds the explicit subprocess env for one source: the base
// PATH/HOME plus the provider's declared config-dir passthrough names, carried
// from the launcher process env, with the per-source credential var overlaid.
// The launcher process itself does not hold the pinned credential env (that is
// injected only into the serve subprocess), so ProbeSource supplies it here
// rather than reading it from os.Environ.
func sourceEnvFor(p passthroughLister, src Source) []string {
	keep := make(map[string]bool, len(baseEnvPassthrough)+len(p.EnvPassthrough()))
	for _, name := range baseEnvPassthrough {
		keep[name] = true
	}
	for _, name := range p.EnvPassthrough() {
		keep[name] = true
	}

	overlay := credentialEnv(src)
	var env []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !keep[name] {
			continue
		}
		if _, overridden := overlay[name]; overridden {
			continue
		}
		env = append(env, kv)
	}
	for name, val := range overlay {
		env = append(env, name+"="+val)
	}
	return env
}

// credentialEnv is the per-provider credential the CLI authenticates with for
// the source: gcp impersonates the assumed identity directly; aws selects the
// assume-role profile. For a multi-account aws source the profile is the default
// (first) account's generated profile name — the same name aws.New wrote into
// ~/.aws/config — so the launcher-side probe reflects the source's default
// target. The env-name constants come from the provider packages, never raw
// literals.
func credentialEnv(src Source) map[string]string {
	switch src.Provider {
	case "gcp":
		return map[string]string{gcp.EnvImpersonate: src.AssumedIdentity}
	case "aws":
		return map[string]string{aws.EnvProfile: awsProbeProfile(src)}
	default:
		return nil
	}
}

// awsProbeProfile is the AWS_PROFILE the launcher-side probe authenticates with:
// the default (first) account's generated profile for a multi-account source,
// else the operator's single-account profile selector.
func awsProbeProfile(src Source) string {
	if len(src.Accounts) > 0 {
		return aws.ProfileName(src.Alias, src.Accounts[0].ID)
	}
	return src.Profile
}
