# Per-profile AWS managed config file

## Problem

triagent generates the AWS assume-role profiles by editing the operator's shared `~/.aws/config` in place, inside `# BEGIN/END triagent-cloud-<alias>` comment markers. The blast radius is the whole file: a single stray line (observed in the field) makes the aws CLI reject `~/.aws/config` entirely, so *every* profile fails, including the operator's own base SSO. Renaming a source alias also leaves an orphaned managed block behind. A fail-closed write guard now prevents triagent from landing unparseable content, but it does not address the root concern: triagent should not be mutating a file it does not own.

## Design

triagent writes the generated profiles into a **triagent-owned, per-profile** config file and points the cloud MCP at it with `AWS_CONFIG_FILE`. The operator's `~/.aws/config` becomes read-only input.

### Location

`${XDG_CACHE_HOME}/triagent-mcp/${PROFILE_NAME}/aws/config`, a new `Paths.CloudCacheDir` field resolved like the existing `GitCacheDir` (`${XDG_CACHE_HOME}/triagent-mcp/${PROFILE_NAME}/git`). Per-profile, so two deployment profiles never clobber each other; overridable in a profile's `paths:` block; `${PROFILE_NAME}`-required validation already guards it.

### File content

`<stripped copy of the operator's ~/.aws/config>` + a sentinel line + `<managed region>`. The managed region holds the `# BEGIN/END triagent-cloud-<alias>` blocks. On each generation (per AWS source, flock-serialized):

1. Read the operator config fresh and strip any `# BEGIN/END triagent-cloud-*` blocks from it (so a pre-migration operator config does not duplicate ours). This is the base.
2. Read the existing target's managed region (everything after the sentinel).
3. Splice this alias's block into the managed region (`replaceBlock`, as today).
4. Assemble `base + sentinel + managed region`, fail-closed validate, atomic write.

Copying the whole operator config makes the target self-contained, so `source_profile` resolves whatever its type (SSO session, static creds, chained). `AWS_SHARED_CREDENTIALS_FILE` is left at its default, so static source creds still resolve from `~/.aws/credentials`; SSO works because the `[sso-session]` is in the copy and the token cache (`~/.aws/sso/cache`) is shared.

### Threading

- `writeManagedProfiles`/`aws.New` take explicit source and target paths via `Options` instead of reading `os.Getenv` ambiently (so the launcher-side probe, which runs in a process without these env vars, can pass them directly).
- `mcpconfig` sets `AWS_CONFIG_FILE=<target>` (so the aws CLI the MCP runs reads it) and a new `TRIAGENT_CLOUD_AWS_SOURCE_CONFIG=<operator config>` (the provider copies from it) on the cloud subprocess; the serve command reads them into `Options`.
- `providers.Options`/`Source` carry the two paths so `ProbeSource` threads them on the launcher side.

### Migration

On launcher start, strip any `# BEGIN/END triagent-cloud-*` blocks triagent previously wrote into the operator's `~/.aws/config`, cleaning up cruft (including orphaned alias blocks) from the old in-place approach.

### Freshness

The copy is a snapshot taken at generation time (launcher start / probe). SSO *re-login* is picked up (shared token cache); operator config *edits* need a relaunch. This matches today's behavior.

## Out of scope

- No change to the "triagent stores no cloud credential" property: the aws CLI still performs the assume-role from `source_profile`; triagent never holds credentials.
- No change to assume-role session length (the 1h/`duration_seconds` knob is separate, tracked elsewhere).
- No pruning of stale alias blocks inside the managed region beyond what a wholesale rebuild covers (the file is triagent-owned and regenerable).
