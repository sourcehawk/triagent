# Cloud providers

Triagent optionally gives the agent read-only context from the cloud the cluster sits on, GCP or AWS, so a Kubernetes investigation can follow a thread down into the cloud layer without a human leaving the loop. It is opt-in and configured entirely in the deployment profile: the core investigation flow (Kubernetes triage, playbooks, wiki) works without it.

## What the cloud-context MCP gives the agent

A managed-Kubernetes incident is often only explicable from cloud context. A Pod cannot reach a dependency because of a firewall rule or a security group. A workload is denied because an identity lost a binding. The GKE or EKS cluster behaves unexpectedly because of how its networking or workload identity is configured. The smoking gun is in cloud logs, and "what changed right before this broke?" lives in the cloud audit trail, not in the cluster.

When a cloud source is configured, the launcher registers a `triagent-cloud-<alias>` MCP server for each investigation session. The agent reads cloud context along six axes: inventory (which projects/accounts and resources it can see), reachability (VPCs, subnets, firewall rules, security groups, routes), permissions (IAM policies, roles, service accounts), cluster (GKE/EKS networking and node config), logs, and the audit trail.

The MCP is read-only by construction, not by convention. The agent supplies argument tokens to a fixed `gcloud` or `aws` binary that runs without a shell, against a positive command allowlist with a hardcoded deny floor underneath, as a pinned read-only identity it can neither select nor escalate. Three independent layers (the command allowlist, the deny floor, and the read-only IAM grant on the pinned identity) each have to hold for a read to go through, and none of them can be widened by the agent.

## The pinned identity

The cloud identity is a deployment-chosen, read-only principal pinned in the profile. The agent can read which identity is active (it has a `session_status` whoami tool) but has no tool to choose, change, or authenticate one. The deployment grants that identity read-only IAM, and that grant is the outermost floor: even a misconfigured-too-broad command allowlist cannot read secrets or exfiltrate, because the identity itself lacks the permission.

The operator authenticates as themselves through their own normal cloud tooling. The harness then pins impersonation (GCP) or assume-role (AWS) of the configured read-only identity through environment it controls, never through anything the agent can supply. Triagent stores no cloud credential. Re-authentication is the operator's own corporate flow, outside Triagent.

## GCP setup

The operator authenticates normally:

```sh
gcloud auth login
```

The deployment grants the operator `roles/iam.serviceAccountTokenCreator` on a read-only service account. This is a one-time admin step, and the price of not storing a secret: the operator's own login plus the impersonated service account gives a clean audit trail (human plus role).

The profile pins that service account as `assumed_identity`. The harness sets `CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=<pinned-sa>` on the cloud MCP subprocess, so every `gcloud` call runs as the pinned service account while authenticating from the operator's base credentials. The agent never picks the identity, and because the pin lives in environment rather than in argv, `--impersonate-service-account` stays on the agent's deny floor without contradiction.

The whoami probe reports the source valid only when the active `gcloud` account equals the pinned impersonation target.

## AWS setup

The operator authenticates normally, for example:

```sh
aws sso login
```

Configure an `~/.aws/config` profile whose `role_arn` is the read-only role, with the operator's base profile as `source_profile`:

```ini
[profile triage-readonly]
role_arn       = arn:aws:iam::123456789012:role/triage-readonly
source_profile = default
region         = eu-west-1
```

The profile's `profile:` field selects that assume-role profile via `AWS_PROFILE`, and `assumed_identity` is the expected role ARN. The harness sets `AWS_PROFILE=<pinned>` on the cloud MCP subprocess, so the AWS CLI assumes the read-only role from the operator's base credentials. As with GCP, the pin lives in environment, so `--profile` stays on the agent's deny floor.

The whoami probe resolves the active caller with `aws sts get-caller-identity`. It reports valid when the caller is an assumed-role ARN whose underlying role matches the pinned `assumed_identity`. A plain user or root ARN means the assume-role pin did not take effect and base credentials leaked through, so the source degrades.

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
    # Targets any run_cli argv may reference. An empty axis is unconstrained;
    # a non-empty axis means the agent cannot pivot outside it.
    scope:
      projects: [prod-platform, prod-data]
      regions:  [us-central1, us-east1]
    # Optional run_cli allowlist override. Empty uses the provider's
    # embedded read-only default.
    # command_allowlist_path: gcp-commands.json

  - alias: prod-aws
    provider: aws
    # For aws, the role ARN the assumed-role caller must resolve to. Validity
    # checks the resolved caller against this exact ARN.
    assumed_identity: arn:aws:iam::123456789012:role/triage-readonly
    # aws-only: the AWS_PROFILE the harness selects for credentials. Its
    # role_arn is the read-only role, with the operator's base as
    # source_profile. gcp ignores this field.
    profile: triage-readonly
    scope:
      accounts: ["123456789012"]
      regions:  [eu-west-1]
```

The fields:

- `alias` — stable name for the source; the MCP is aliased `triagent-cloud-<alias>` and the connections panel keys off it.
- `provider` — `gcp` or `aws`. Selects the concrete provider behind the shared MCP.
- `assumed_identity` — the canonical pinned identity shown in the connections panel: a service-account email for GCP, a role ARN for AWS. GCP impersonates it directly. AWS checks it as the expected role ARN for strict validity.
- `profile` — AWS only. The `AWS_PROFILE` selector for the assume-role profile that produces credentials. GCP ignores it.
- `scope` — the target allowlist (see below).
- `command_allowlist_path` — an optional `run_cli` allowlist override (see below). Empty uses the provider's embedded default.

## Scope allowlist

`scope` constrains which cloud targets any `run_cli` argument may reference, so the agent cannot pivot to an un-allowlisted project, account, or region. It has three axes:

```yaml
scope:
  projects: [prod-platform]    # gcp --project values the agent may use
  accounts: ["123456789012"]   # aws account ids the agent may use
  regions:  [us-central1]      # --region / --zone values the agent may use
```

An empty (or omitted) axis is unconstrained on that axis. A non-empty axis is a closed set: a `--project`, `--region`, or `--zone` value outside it fails validation before the command runs. Identity-selecting flags (`--account`, `--profile`) never reach scope validation at all, because the deny floor rejects them first.

## Command allowlist

What the agent can run through `run_cli` is governed by a positive command allowlist of normalized subcommand paths, for example `compute firewall-rules list` for GCP or `ec2 describe-security-groups` for AWS. Each provider ships an embedded read-only default covering the six axes. Point `command_allowlist_path` at a file (relative to the profile.yaml) to override it; an empty value uses the embedded default. The allowlist is the single source of truth, so the discovery tool advertises exactly what is permitted.

Underneath the allowlist sits a hardcoded deny floor the config can never re-enable, mirroring how the k8s MCP always filters Secret regardless of its kinds config. The floor covers dangerous subcommands (`secrets`, `ssh`, `scp`, `cp`, `sync`, `auth`, `config`), dangerous flags (`--impersonate-service-account`, `--account`, `--profile`, `--endpoint-url`, `--cli-input-json`, `--cli-input-yaml`, `--configuration`), and argument values beginning with `file://`, `fileb://`, `@`, `http://`, or `https://` (local-file read and SSRF vectors). A too-broad allowlist override cannot punch through it.

## Visible degrade

A stale or invalid cloud credential never blocks Kubernetes triage. Unlike the cluster-auth preflight, which gates the session, a failed cloud probe degrades only that cloud source. The connections panel shows the source unavailable with a re-auth hint, and the session starts with the source disabled and visibly marked unavailable. The Kubernetes investigation proceeds without the cloud axis.

Re-authentication is the operator's own cloud login (`gcloud auth login`, `aws sso login`), not anything entered in Triagent. The probe runs on connections-panel load so the operator can fix a stale credential before starting a session rather than discovering a degraded one mid-investigation.

## See also

- [Connections](/docs/connections). Slack and incident.io credential handling, and the read-only cloud pills the same panel surfaces.
- [Profiles](/docs/profiles). The deployment config bundle the `cloud:` block lives in.
- [MCP](/docs/mcp). The tool catalog the cloud source extends.
