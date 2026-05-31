# Cloud active-target selection (extends the cloud-context MCP)

This design extends the read-only cloud-context MCP defined in [2026-05-30-cloud-context-mcp-design.md](2026-05-30-cloud-context-mcp-design.md). That base MCP (the `Provider` interface, the no-shell harness, the command allowlist and deny floor, the identity probe, and the GCP/AWS providers) ships unchanged. This document specifies only the addition: letting the agent operate across more than one project or account, by selecting an active target from a deployment-pinned set.

## Problem

The base MCP pins one target per cloud source and the agent cannot change it. That fits GCP, where a single impersonated service account can be granted read-only on many projects, so one identity already spans them. It does not fit AWS: an IAM role lives in exactly one account, so a single assumed role can only read one account's resources. A responder investigating an incident that crosses accounts (or that lives in a different account than the one the source happens to pin) has no way to follow the thread without a second source and a human switch.

Two consequences in the base MCP make this concrete. The AWS `list_inventory` runs `organizations list-accounts` and can advertise the whole org, yet `run_cli` only works in the role's own account, so inventory over-promises reachability. And scope only constrains an explicit `--project`/`--region`; omitting the flag falls back to the CLI's ambient default, which scope does not police.

## Goals

- Let the agent operate across the deployment's set of projects (GCP) and accounts (AWS) within one cloud source, by selecting which target subsequent `run_cli` commands run against.
- Keep the selection bounded: the agent may choose only among targets the deployment configured, and can neither name an arbitrary target nor escalate within the set.
- Make the active target the effective default, so a command that omits the target flag runs against an in-scope target rather than an ambient one. This closes the scope-by-omission gap.
- Hold no credential. The mechanism stays env injection, the same as the base MCP's pinned identity.

## Non-goals

- Runtime credential brokering. triagent does not call `sts:AssumeRole` itself or hold temporary credentials (see Alternatives).
- Region switching. Region/zone stays a scope-validated explicit argv flag as in the base MCP; this feature is about project/account only.
- Clouds beyond GCP and AWS, and any write path. Unchanged from the base MCP.
- Choosing a target the deployment did not configure. The selectable set is the profile's, not the agent's.

## Design

### The `set_active_target` tool

One tool is added to the provider-agnostic surface in `specs.go`:

- `set_active_target` — input `target`: an ID from `list_inventory` (a project id for GCP, an account id for AWS). It sets the session's active target and returns the new target's `session_status` so the agent immediately sees whether it is valid. A `target` outside the pinned set is rejected before anything changes.

`session_status` is extended to report the active target alongside the identity. `list_inventory` already surfaces the selectable set as its `Scopes`.

### The selectable set

- **GCP**: the profile's `scope.projects`. When that axis is unconstrained (empty), the set is the projects `list_inventory` surfaces (those the impersonated service account can see).
- **AWS**: a new per-source `accounts` list, each entry `{account_id, role_arn}`. The single-account deployment is a one-entry list, which reproduces today's behavior.

### Applying the selection (the leak-safe rule)

The active target is in-memory state on the cloud MCP `Server`, applied through one MCP-controlled environment variable built fresh into each `run_cli` child process, and **never** through a process-global `os.Setenv` (the same discipline the identity probe already follows, so there is no cross-request or cross-session bleed):

- **GCP**: `CLOUDSDK_CORE_PROJECT=<active project>`. gcloud uses it as the default project for every command; commands that take no project ignore it.
- **AWS**: `AWS_PROFILE=<active account's profile>`.

The values are non-secret identifiers (a project id, a profile name), not credentials. `--project`, `--account`, `--profile`, and `--impersonate-service-account` stay on the deny floor, so the agent cannot override the MCP's pin; `set_active_target` is the only way to change the target.

### GCP versus AWS, by mechanism

- **GCP — one identity, many projects.** A single impersonated read-only service account spans the allowlisted projects. Switching changes only `CLOUDSDK_CORE_PROJECT`; the identity is unchanged. `session_status` reports the same service account throughout, with the active project alongside.
- **AWS — one account per role.** Each configured account is its own read-only role. triagent generates a `~/.aws/config` profile per `accounts` entry at startup, each layering the entry's `role_arn` over the operator's SSO `source_profile`; switching sets `AWS_PROFILE` to the active account's generated profile. The identity changes per account, so `session_status` re-probes the active role on switch. triagent still stores no credential: it sets a profile name and the AWS CLI performs the assume-role from the operator's base.

### Default active target

If the set has exactly one entry, it is the active target from session start (today's behavior). If it has several, `run_cli` returns an actionable error until the agent calls `set_active_target`, so a command can never run against an unintended default.

## Security model

This preserves the base MCP's two-layer model and tightens one part of it. The agent still cannot run a forbidden command (the harness, allowlist, and deny floor are unchanged). The identity layer changes from "the agent cannot select the target" to "the agent selects within a deployment-pinned set, never beyond it": the selectable set loads server-side from the profile, the agent has a tool to choose among its members but none to mutate it or add to it, and the target-selecting flags remain deny-floored so the choice can only be applied through the MCP's controlled env. The read-only IAM grant on each pinned identity remains the outermost floor.

The scope-by-omission gap the base MCP documents is closed here: because the active target is an MCP-pinned in-scope value rather than the CLI's ambient default, a command that omits the flag still runs against an allowlisted target.

## Builds on (unchanged)

The harness, command allowlist, deny floor, identity probe, output truncation, provider interface, and the visible-degrade preflight from the base MCP are unchanged. This feature adds the `set_active_target` tool, the AWS `accounts` config plus its startup profile generation, the active-target session state, and the per-exec env application. The AWS `list_inventory` is adjusted so it reflects the reachable target set rather than over-advertising the whole organization.

## Alternatives considered

- **Runtime `AssumeRole` brokering for AWS.** The MCP would call `sts:AssumeRole` on switch and use the returned temporary credentials for `run_cli`. Rejected: it removes the per-account profile setup but reverses the base MCP's defining invariant (triagent would hold and refresh live credentials), enlarges the leak surface, and diverges from GCP. Generating per-account profiles and switching `AWS_PROFILE` gives the same ergonomics with no credential in triagent's custody.
- **Injecting the GCP `--project` flag.** The MCP would append `--project=<active>` to each `run_cli`. Rejected for asymmetry and fragility: it needs per-command knowledge of which commands accept `--project`, where the env var (`CLOUDSDK_CORE_PROJECT`) applies uniformly and matches the AWS shape.

## Vocabulary

- The project (GCP) or account (AWS) the agent currently operates in is the **active target**, chosen from the deployment-pinned set via **`set_active_target`** and applied through the MCP's controlled env, never agent argv.
- The base MCP's terms (pinned identity, realizations, provider, axes, the gated `run_cli`) carry over unchanged.
