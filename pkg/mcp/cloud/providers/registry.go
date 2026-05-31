// Package providers is the single construction site for a cloud.Provider. It
// imports the concrete gcp and aws packages and resolves a provider name to a
// constructed value, so every consumer — the triagent-mcp serve arm, the
// session preflight, and the connections panel — obtains a provider the same
// way. This mirrors how the launcher builds an auth.Provider from
// pkg/auth/teleport and pkg/auth/kubeconfig: a neutral package that imports the
// implementations the cloud package itself cannot import without a cycle.
package providers

import (
	"fmt"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
)

// Options carries the multi-account config the aws provider needs from the
// profile's cloud source: the source alias (the generated profiles' namespace),
// the operator's SSO source_profile, and the account set. gcp ignores it. The
// zero value is the single-account / single-identity form, so callers that do
// not configure accounts call New(name) unchanged.
type Options struct {
	AWSAlias         string
	AWSSourceProfile string
	AWSAccounts      []aws.Account
}

// New constructs the cloud.Provider for the named provider ("gcp" | "aws"),
// threading the aws multi-account config through when present. The concrete
// New() resolves the provider's CLI binary to an absolute path and, for an aws
// source with accounts, generates the per-account assume-role profiles; a
// missing binary surfaces as a construction error, which the launcher degrades
// to an unavailable cloud source rather than a fatal failure. An unknown name is
// named in the error. At most one Options is honored.
func New(name string, opts ...Options) (cloud.Provider, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	switch name {
	case "gcp":
		return gcp.New()
	case "aws":
		return aws.New(aws.Options{
			Alias:         o.AWSAlias,
			SourceProfile: o.AWSSourceProfile,
			Accounts:      o.AWSAccounts,
		})
	default:
		return nil, fmt.Errorf("unknown cloud provider %q (want gcp or aws)", name)
	}
}
