# Cloud providers

Triagent optionally gives the agent read-only context from the cloud the cluster sits on, GCP or AWS, so a Kubernetes investigation can follow a thread down into the cloud layer without a human leaving the loop. It is opt-in and configured entirely in the deployment profile: the core investigation flow (Kubernetes triage, playbooks, wiki) works without it.

## What the cloud-context MCP gives the agent

A managed-Kubernetes incident is often only explicable from cloud context. A Pod cannot reach a dependency because of a firewall rule or a security group. A workload is denied because an identity lost a binding. The GKE or EKS cluster behaves unexpectedly because of how its networking or workload identity is configured. The smoking gun is in cloud logs, and "what changed right before this broke?" lives in the cloud audit trail, not in the cluster.

When a cloud source is configured, the launcher registers a `triagent-cloud-<alias>` MCP server for each investigation session. The agent reads cloud context along six axes: inventory (which projects/accounts and resources it can see), reachability (VPCs, subnets, firewall rules, security groups, routes), permissions (IAM policies, roles, service accounts), cluster (GKE/EKS networking and node config), logs, and the audit trail.

The MCP is read-only by construction, not by convention. The agent supplies argument tokens to a fixed `gcloud` or `aws` binary that runs without a shell, against a positive command allowlist with a hardcoded deny floor underneath, as a pinned read-only identity it can neither select nor escalate. Three independent layers (the command allowlist, the deny floor, and the read-only IAM grant on the pinned identity) each have to hold for a read to go through, and none of them can be widened by the agent.

## The pinned identity

The cloud identity is a deployment-chosen, read-only principal pinned in the profile. The agent can read which identity is active (it has a `session_status` whoami tool) and, when the deployment configures more than one target, switch among that pinned set with `set_active_target` (see [Active target](#active-target-moving-across-projects-and-accounts)) — but it has no tool to name an arbitrary identity, escalate one, or authenticate one. The deployment grants each pinned identity read-only IAM, and that grant is the outermost floor: even a misconfigured-too-broad command allowlist cannot read secrets or exfiltrate, because the identity itself lacks the permission.

The operator authenticates as themselves through their own normal cloud tooling. The harness then pins impersonation (GCP) or assume-role (AWS) of the configured read-only identity through environment it controls, never through anything the agent can supply. Triagent stores no cloud credential. Re-authentication is the operator's own corporate flow, outside Triagent.

## GCP setup

The operator authenticates normally:

```sh
gcloud auth login
```

The deployment grants the operator `roles/iam.serviceAccountTokenCreator` on a read-only service account. This is a one-time admin step, and the price of not storing a secret: the operator's own login plus the impersonated service account gives a clean audit trail (human plus role).

That binding lets the operator *act as* the service account; it is separate from what the service account itself may *read*. The service account needs read-only access on each project in the source's scope. The minimal set of predefined roles covering the default tool surface (inventory, reachability, IAM read, GKE, logs, audit):

```sh
SA=triage-readonly@prod.iam.gserviceaccount.com
for role in \
  roles/browser \
  roles/compute.viewer \
  roles/container.viewer \
  roles/iam.securityReviewer \
  roles/logging.viewer \
  roles/monitoring.viewer; do
  gcloud projects add-iam-policy-binding prod-platform \
    --member="serviceAccount:$SA" --role="$role"
done
```

`roles/browser` lists and reads projects, `compute.viewer` and `container.viewer` cover networking and GKE, `iam.securityReviewer` reads IAM policies and service accounts, and the logging and monitoring viewers cover the logs and audit axes. If you would rather not curate, the single basic role `roles/viewer` is read-only across all of these and is the simpler, broader alternative. Role names are current as of writing; verify against GCP's IAM reference, which evolves.

The profile pins that service account as `assumed_identity`. The harness sets `CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=<pinned-sa>` on the cloud MCP subprocess, so every `gcloud` call runs as the pinned service account while authenticating from the operator's base credentials. The agent never picks the identity, and because the pin lives in environment rather than in argv, `--impersonate-service-account` stays on the agent's deny floor without contradiction.

The whoami probe reports the source valid when impersonation is pinned to the configured service account and a minimal impersonated token read succeeds, proving the pin took effect. Under impersonation the operator's own base account stays active, so the probe does not require the active `gcloud` account to equal the service account; it confirms the pin and the read instead.

## AWS setup

The operator authenticates normally, for example:

```sh
aws sso login
```

An AWS role lives in exactly one account, so a source names a list of `accounts` — one `{account_id, role_arn}` per account the agent may reach — plus the operator's SSO base as `source_profile`. A single-account source is simply a one-entry list; there is no separate single-account shape. You do not pre-create an `~/.aws/config` profile per account: triagent generates one read-only assume-role profile per entry at session start, layering each account's `role_arn` over `source_profile`.

```yaml
cloud:
  - alias: prod-aws
    provider: aws
    source_profile: default                                           # the operator's SSO base
    accounts:
      - {account_id: "123456789012", role_arn: "arn:aws:iam::123456789012:role/triage-readonly"}
```

An AWS source has no `assumed_identity` — its identity is per-account, in each `accounts` entry's `role_arn`. The harness sets `AWS_PROFILE` to the active account's generated profile on each `run_cli`, so the AWS CLI assumes that account's read-only role from the operator's base credentials. The pin lives in environment, so `--profile` stays on the agent's deny floor. The connections panel validates the default (first) account's role.

Each account's read-only role needs a permission policy and a trust policy. The minimal permission policy, scoped to exactly the default tool surface:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "TriageReadOnly",
      "Effect": "Allow",
      "Action": [
        "sts:GetCallerIdentity",
        "organizations:ListAccounts",
        "organizations:DescribeOrganization",
        "ec2:Describe*",
        "iam:GetRole", "iam:ListRoles",
        "iam:ListAttachedRolePolicies", "iam:ListRolePolicies", "iam:GetRolePolicy",
        "iam:GetPolicy", "iam:GetPolicyVersion", "iam:ListPolicies",
        "iam:SimulatePrincipalPolicy",
        "eks:ListClusters", "eks:DescribeCluster",
        "eks:ListNodegroups", "eks:DescribeNodegroup",
        "eks:ListFargateProfiles", "eks:DescribeFargateProfile",
        "logs:DescribeLogGroups", "logs:DescribeLogStreams",
        "logs:FilterLogEvents", "logs:GetLogEvents",
        "cloudtrail:LookupEvents", "cloudtrail:DescribeTrails", "cloudtrail:GetTrailStatus"
      ],
      "Resource": "*"
    }
  ]
}
```

The trust policy lets the operator's base principal assume the role:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "AWS": "arn:aws:iam::123456789012:root" },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

Scope the trust `Principal` to the specific operator users or SSO role rather than the whole account root where you can. If you would rather not curate the permission policy, the AWS-managed `ReadOnlyAccess` policy is the broader, simpler alternative. Action names are current as of writing; verify against AWS's service-authorization reference, which evolves.

The whoami probe resolves the active caller with `aws sts get-caller-identity`. It reports valid when the caller is an assumed-role ARN whose underlying role matches the active account's `role_arn`. A plain user or root ARN means the assume-role pin did not take effect and base credentials leaked through, so the source degrades.

### Spanning several AWS accounts

An IAM role lives in exactly one account, so reaching several accounts is just a longer `accounts` list — one read-only `role_arn` per account, each with the account id the agent selects by. The same `source_profile` (the operator's SSO base) backs every generated profile.

```yaml
cloud:
  - alias: prod-aws
    provider: aws
    source_profile: sso-admin            # the operator's SSO base profile
    accounts:
      - {account_id: "111111111111", role_arn: "arn:aws:iam::111111111111:role/triage-readonly"}
      - {account_id: "222222222222", role_arn: "arn:aws:iam::222222222222:role/triage-readonly"}
      - {account_id: "333333333333", role_arn: "arn:aws:iam::333333333333:role/triage-readonly"}
```

Triagent writes the generated profiles into a managed block in your `~/.aws/config` (or `$AWS_CONFIG_FILE`) delimited by `# BEGIN triagent-cloud-<alias>` / `# END triagent-cloud-<alias>` markers. The block is rewritten idempotently and never touches profiles you authored yourself or another alias's block; triagent stores no credential, the AWS CLI performs the assume-role from your SSO base.

Give every account's role the same read-only permission and trust policies shown above. The connections panel shows the default (first) account's validity; `session_status` re-probes the active account's own role when the agent switches, so a non-default account reports its own validity in-session.

## Active target: moving across projects and accounts

A source can span more than one target — several projects under one GCP identity, or several accounts under an AWS `accounts` list. The agent chooses which one subsequent `run_cli` commands run against with the `set_active_target` tool, naming a target id from `list_inventory` (a project id for GCP, an account id for AWS). The agent can select only among the deployment-configured targets; a target outside that set is rejected, and `session_status` reports the active target alongside the pinned identity.

The two clouds reach their target set by different mechanisms, which is why AWS needs the `accounts` list and GCP does not:

- **GCP — one identity, many projects.** A single impersonated read-only service account can be granted viewer on every in-scope project, so one identity already spans them. Switching target changes only `CLOUDSDK_CORE_PROJECT`; the identity is unchanged, and `session_status` reports the same service account throughout. The selectable set is the source's `scope.projects` (or, when that axis is empty, the projects `list_inventory` surfaces).
- **AWS — one account per role.** A role lives in one account, so each account is its own read-only role. The selectable set is the source's `accounts` list, and switching target sets `AWS_PROFILE` to that account's generated profile — a different identity per account, so `session_status` re-probes on switch.

When a source has exactly one target, it is active from session start and the agent need not choose. When it has several and the agent has not yet chosen, `run_cli` returns an actionable error naming `set_active_target` rather than running against an unintended default. This is also why omitting a target flag is safe under multiple targets: the active target is an in-scope pin, never the CLI's ambient default.

## The `cloud:` profile block

Cloud sources live under a top-level `cloud:` list in the profile. Each entry is one provider connection the launcher wires as a `triagent-cloud-<alias>` MCP.

```yaml
# Read-only cloud-context sources. Each entry attaches a
# triagent-cloud-<alias> MCP to every investigation session. Identities
# are pinned here, never entered in the connections panel — the agent can
# read the active identity but cannot select or escalate it.
cloud:
  - alias: prod-gcp                 # stable name; the MCP is aliased triagent-cloud-<alias>.
    provider: gcp                   # "gcp" | "aws".
    # The pinned read-only identity. For gcp, the service-account email the
    # harness impersonates via CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT.
    assumed_identity: triage-readonly@prod.iam.gserviceaccount.com
    # Project and region/zone targets enforced on run_cli argv. An empty axis
    # is unconstrained; a non-empty axis means the agent cannot pivot outside it.
    scope:
      projects: [prod-platform, prod-data]
      regions:  [us-central1, us-east1]
    # Optional run_cli allowlist override. Empty uses the provider's
    # embedded read-only default.
    # command_allowlist_path: gcp-commands.json

  - alias: prod-aws
    provider: aws
    # aws has no assumed_identity — its identity is per-account (each accounts
    # entry's role_arn). The connections panel validates the default account.
    # aws: the operator's SSO base profile the generated per-account assume-role
    # profiles layer their role over. Required for aws; gcp ignores it.
    source_profile: sso-admin
    # aws: the account set the agent selects among via set_active_target. Each
    # entry is {account_id, role_arn}; a single-account source is a one-entry list.
    accounts:
      - {account_id: "123456789012", role_arn: "arn:aws:iam::123456789012:role/triage-readonly"}
    scope:
      regions:  [eu-west-1]                 # enforced on run_cli argv.
      accounts: ["123456789012"]            # informational scope note; distinct from the source-level accounts list.
```

The fields:

- `alias` — stable name for the source; the MCP is aliased `triagent-cloud-<alias>` and the connections panel keys off it.
- `provider` — `gcp` or `aws`. Selects the concrete provider behind the shared MCP.
- `assumed_identity` — GCP only: the impersonated read-only service-account email, shown in the connections panel and impersonated directly. AWS has no `assumed_identity` (setting it on an AWS source is rejected); its identity is per-account, in each `accounts` entry's `role_arn`.
- `source_profile` — AWS only. The operator's SSO base profile the generated per-account assume-role profiles layer their role over. Required for AWS; GCP ignores it.
- `accounts` — AWS only. The account set the agent selects among via `set_active_target`; each entry is `{account_id, role_arn}`, and a single-account source is a one-entry list. See [Spanning several AWS accounts](#spanning-several-aws-accounts). This is the source-level selectable set, distinct from the informational `scope.accounts` note.
- `scope` — the target allowlist (see below).
- `command_allowlist_path` — an optional `run_cli` allowlist override (see below). Empty uses the provider's embedded default.

## Scope allowlist

`scope` constrains which cloud targets the agent may reach, so it cannot pivot to an un-allowlisted project or region. Region/zone is enforced against `run_cli` argv. Project is not an argv axis: `--project` is deny-floored and the project is chosen with `set_active_target`, validated against `scope.projects`. Account reach is governed by the pinned role or profile, not by argv.

```yaml
scope:
  projects: [prod-platform]    # gcp projects the agent may set_active_target to
  regions:  [us-central1]      # --region / --zone values the agent may use (argv-enforced)
  accounts: ["123456789012"]   # aws accounts reachable via the pinned role (informational)
```

An empty (or omitted) `projects` or `regions` axis is unconstrained on that axis. A non-empty `regions` is a closed set: a `--region` or `--zone` value outside it fails validation before the command runs. A non-empty `projects` is the closed set `set_active_target` will accept.

The active target is the effective default, so a command that omits the target flag runs against an in-scope target rather than an ambient one (`CLOUDSDK_CORE_PROJECT` for GCP, the active account's profile for AWS). Region has no active-target equivalent: an omitted `--region` falls back to the configured `AWS_REGION` / gcloud default, which scope does not police. Hard project confinement therefore comes from the pinned identity's IAM, not from scope: grant the read-only roles only on the in-scope projects, as the setup above does, so an out-of-scope project is unreachable whatever the argv. Treat region scope as a guardrail against explicit pivots rather than a hard limit.

`accounts` is informational and reserved: it documents which AWS accounts the source is expected to reach, but `run_cli` does not validate account ids on argv. What actually bounds account reach is the pinned assume-role profile, whose role can only see the accounts its trust policy and permissions allow. Treat `accounts` as a note to operators, not an enforced allowlist.

Identity- and target-selecting flags (`--account`, `--profile`, `--project`) never reach scope validation at all, because the deny floor rejects them first.

## Command allowlist

What the agent can run through `run_cli` is governed by a positive command allowlist of normalized subcommand paths, for example `compute firewall-rules list` for GCP or `ec2 describe-security-groups` for AWS. Each provider ships an embedded read-only default covering the six axes. Point `command_allowlist_path` at a file (relative to the profile.yaml) to override it; an empty value uses the embedded default. The allowlist is the single source of truth, so the discovery tool advertises exactly what is permitted.

Allowlist entries must be complete leaf verbs, for example `compute instances list` or `ec2 describe-security-groups`, never an intermediate group path like `compute instances` or `ec2`. The allowlist matches an entry as a prefix of the command, so an intermediate entry would also admit its sibling verbs, including mutating ones (`compute instances delete`, `ec2 terminate-instances`). The shipped defaults are all leaf read verbs. The guarantee that the agent cannot write, even under a careless override, is the read-only IAM grant on the pinned identity: a viewer-only principal's mutating call fails at the cloud. The allowlist and deny floor keep the agent to reads and exclude secret-read and exfil; the no-write property itself rests on the identity's permissions.

Underneath the allowlist sits a hardcoded deny floor the config can never re-enable, mirroring how the k8s MCP always filters Secret regardless of its kinds config. The floor covers dangerous subcommands (`secrets`, `ssh`, `scp`, `cp`, `sync`, `auth`, `config`), dangerous flags (`--impersonate-service-account`, `--account`, `--profile`, `--endpoint-url`, `--cli-input-json`, `--cli-input-yaml`, `--configuration`), and argument values beginning with `file://`, `fileb://`, `@`, `http://`, or `https://` (local-file read and SSRF vectors). A too-broad allowlist override cannot punch through it.

The command allowlist and the IAM grant are independent layers and must stay aligned. The recommended policies above are least-privilege for the default allowlist. Tightening the allowlist needs no IAM change; if you widen it with `command_allowlist_path`, widen the identity's read-only grant to match, or the added commands fail at the cloud rather than at the harness. Never widen either beyond read-only. The authoritative list of what a configured source permits is whatever the agent's `list_allowed_commands` tool returns, which reads the same allowlist `run_cli` enforces; each provider's shipped default lives in its `default_commands.json` under `pkg/mcp/cloud/providers/<provider>/`.

## Visible degrade

A stale or invalid cloud credential never blocks Kubernetes triage. Unlike the cluster-auth preflight, which gates the session, a failed cloud probe degrades only that cloud source. The connections panel shows the source unavailable with a re-auth hint, and the session starts with the source disabled and visibly marked unavailable. The Kubernetes investigation proceeds without the cloud axis.

Re-authentication is the operator's own cloud login (`gcloud auth login`, `aws sso login`), not anything entered in Triagent. The probe runs on connections-panel load so the operator can fix a stale credential before starting a session rather than discovering a degraded one mid-investigation.

## See also

- [Connections](/docs/connections). Slack and incident.io credential handling, and the read-only cloud pills the same panel surfaces.
- [Profiles](/docs/profiles). The deployment config bundle the `cloud:` block lives in.
- [MCP](/docs/mcp). The tool catalog the cloud source extends.
