package cloud

// Environment variable names the cloud-context MCP subprocess reads. The
// launcher sets these in the subprocess env (never argv); the agent supplies
// argv only and cannot reach them. Provider impersonation env vars
// (CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT, AWS_PROFILE) are contributed by
// the provider packages, not here.
const (
	// EnvProvider selects the concrete provider ("gcp" | "aws").
	EnvProvider = "TRIAGENT_CLOUD_PROVIDER"
	// EnvAllowlistPath points at a command-allowlist override file; empty uses
	// the provider's embedded default.
	EnvAllowlistPath = "TRIAGENT_CLOUD_ALLOWLIST_PATH"
	// EnvScope carries the target scope allowlist the launcher froze for this
	// session, as JSON the cloud package decodes into ScopeAllowlist.
	EnvScope = "TRIAGENT_CLOUD_SCOPE"
	// EnvExpectedIdentity carries the identity the launcher pinned for this
	// session, uniform across providers: the impersonation target for gcp, the
	// expected role ARN for aws. The serve subprocess reads it once at startup and
	// threads it into the identity probe; the provider validates the resolved
	// identity against it.
	EnvExpectedIdentity = "TRIAGENT_CLOUD_EXPECTED_IDENTITY"
)
