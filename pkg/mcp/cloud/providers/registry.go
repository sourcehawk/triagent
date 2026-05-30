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

// New constructs the cloud.Provider for the named provider ("gcp" | "aws"). The
// concrete New() resolves the provider's CLI binary to an absolute path; a
// missing binary surfaces as a construction error, which the launcher degrades
// to an unavailable cloud source rather than a fatal failure. An unknown name is
// named in the error.
func New(name string) (cloud.Provider, error) {
	switch name {
	case "gcp":
		return gcp.New()
	case "aws":
		return aws.New()
	default:
		return nil, fmt.Errorf("unknown cloud provider %q (want gcp or aws)", name)
	}
}
