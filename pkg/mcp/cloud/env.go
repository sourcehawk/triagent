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
	// EnvGCPProjects carries the gcp project set as a JSON array of {id, tags}
	// objects: the deployment-configured selectable projects and their free-form
	// tags. The serve subprocess decodes it into the gcp provider's configured
	// targets. Empty means unconstrained — the provider lists projects live
	// instead. gcp-only.
	EnvGCPProjects = "TRIAGENT_CLOUD_GCP_PROJECTS"
	// EnvAWSAccounts carries the aws account set as a JSON array of
	// {account_id, role_arn, tags} objects. The serve subprocess decodes it and
	// builds the aws provider's configured targets and generated assume-role
	// profiles. A single-account source is a one-entry array. gcp leaves it empty.
	EnvAWSAccounts = "TRIAGENT_CLOUD_AWS_ACCOUNTS"
	// EnvAWSSourceProfile carries the operator's SSO base profile the generated
	// multi-account assume-role profiles layer their role over. aws-only.
	EnvAWSSourceProfile = "TRIAGENT_CLOUD_AWS_SOURCE_PROFILE"
	// EnvAWSAlias carries the cloud source's alias, the namespace for the
	// generated assume-role profile names (triagent-cloud-<alias>-<account_id>).
	// The launcher-side probe and the serve subprocess both build the provider
	// with it, so they name and generate the same profiles. aws-only.
	EnvAWSAlias = "TRIAGENT_CLOUD_AWS_ALIAS"
	// EnvAWSConfigFile is the standard aws CLI config-file selector. The launcher
	// sets it to the triagent-owned per-profile config (under CloudCacheDir): the
	// aws CLI reads it, and the provider generates into it. aws-only.
	EnvAWSConfigFile = "AWS_CONFIG_FILE"
	// EnvAWSSourceConfig is the operator's own aws config the provider copies into
	// EnvAWSConfigFile so source_profile resolves. Distinct from EnvAWSConfigFile
	// (the generated target) because the subprocess sees the latter as
	// AWS_CONFIG_FILE and cannot infer the operator's original. aws-only.
	EnvAWSSourceConfig = "TRIAGENT_CLOUD_AWS_SOURCE_CONFIG"
)
