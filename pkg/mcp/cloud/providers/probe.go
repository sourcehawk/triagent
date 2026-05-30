package providers

import (
	"context"
	"os"
	"sync"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
)

// Source is a neutral description of one cloud connection to probe: the
// provider name, the pinned identity, and (aws only) the assume-role profile.
// It carries exactly what ProbeSource needs without coupling this package to
// the launcher's profile type.
type Source struct {
	Provider        string
	AssumedIdentity string
	Profile         string // aws AWS_PROFILE selector; ignored by gcp
}

// probeEnvMu serializes the env pinning ProbeSource does. The whoami probe runs
// in the launcher process, where the provider reads its expected-identity env
// via os.Getenv; ProbeSource pins that env around the probe and restores it, so
// concurrent probes for different sources cannot read each other's pin.
var probeEnvMu sync.Mutex

// ProbeSource constructs the source's provider and runs the read-only identity
// probe, pinning the per-provider expected-identity env for the duration so the
// probe validates against this source's pinned identity. It degrades, never
// blocks: a provider construction error (e.g. a missing CLI binary) returns an
// invalid status with the error as the hint, exactly like a failed probe.
func ProbeSource(ctx context.Context, src Source) cloud.IdentityStatus {
	p, err := New(src.Provider)
	if err != nil {
		return cloud.IdentityStatus{Provider: src.Provider, Valid: false, Hint: err.Error()}
	}

	probeEnvMu.Lock()
	defer probeEnvMu.Unlock()
	defer pinIdentityEnv(src)()

	st, _ := cloud.Probe(ctx, p)
	return st
}

// pinIdentityEnv sets the per-provider expected-identity env for src and returns
// a restore func. gcp impersonates the assumed identity directly; aws selects an
// assume-role profile and checks the expected role ARN. The names come from the
// provider packages, never raw literals.
func pinIdentityEnv(src Source) func() {
	switch src.Provider {
	case "gcp":
		return setEnv(map[string]string{gcp.EnvImpersonate: src.AssumedIdentity})
	case "aws":
		return setEnv(map[string]string{
			aws.EnvProfile:         src.Profile,
			aws.EnvExpectedRoleARN: src.AssumedIdentity,
		})
	default:
		return func() {}
	}
}

// setEnv sets each name to its value and returns a func restoring the prior
// values (including unset for names that were absent).
func setEnv(vals map[string]string) func() {
	prior := make(map[string]*string, len(vals))
	for name, val := range vals {
		if old, ok := os.LookupEnv(name); ok {
			prior[name] = &old
		} else {
			prior[name] = nil
		}
		_ = os.Setenv(name, val)
	}
	return func() {
		for name, old := range prior {
			if old == nil {
				_ = os.Unsetenv(name)
			} else {
				_ = os.Setenv(name, *old)
			}
		}
	}
}
