# Cloud-triage playbook

## Problem

The cloud-context MCP gives the agent read-only capability across six axes (inventory, reachability, permissions, cluster, logs, audit) but no *discipline*. Nothing tells the agent when cloud triage is warranted versus a rabbit hole, and nothing orients it before it starts running `run_cli` reads. Two failure modes follow:

1. **Cloud-spelunking with no signal.** The agent has cloud tools, so it reaches for them even when the symptom is plainly cluster-internal (app bug, k8s misconfig, image, OOM). A read-only identity makes this harmless but wasteful and distracting.
2. **Querying before orienting.** The agent runs cloud reads without first pinning which project/account/region it is even looking at, so the reads target the wrong (or an ambient) scope.

A guided-flow playbook can encode the judgment a good operator applies: do not enter the cloud without a cloud-shaped signal, and when you do, pin the target before you read.

## Design

A new `type: general` sub-flow, `system/cloud_triage.yaml`, modeled on `system/prom_lookup.yaml`. It is binary-embedded (picked up by `system/embed.go`'s `*.yaml` glob), provider-agnostic (the cloud MCP's tools — `list_inventory`, `set_active_target`, `session_status`, `run_cli`, `list_allowed_commands` — are neutral across GCP and AWS), and read-only by construction (the cloud MCP's three gates already bound it).

### Reachability

Not wired into the locked `investigation.yaml`. Like `prom_lookup`, the agent reaches it by its own judgment via `list_playbooks` / `playbook_correlate` when it recognizes a cloud-shaped signal, walks it as a sub-flow, and returns. Entry is disciplined by two things: a sharp `symptom`/`description` so `playbook_correlate` only surfaces it for cloud-shaped cases, and an explicit gate node inside.

### Node graph (6 nodes)

1. **`gate`** (entrypoint). The restraint mechanism. A cloud-shaped signal is one of: an account id, a role or service-account ARN, a cloud resource name surfaced in evidence; a cloud permission / quota / throttle error; or an explicit user request to check the cloud. If none is present, the symptom is cluster-internal and the playbook does not query the cloud.
   - → `orient` when a concrete signal is present (or the user asked).
   - → `terminal_no_signal` otherwise.
2. **`orient`**. Pin project/account/region before any read. Derive coordinates from the cluster first: the workload's node carries the cloud account/project and region (`spec.providerID`, `topology.kubernetes.io/region` and `/zone` labels), and the workload's ServiceAccount workload-identity annotation names the cloud identity, all read via `triagent-k8s`. Then `session_status` (what target is pinned) and `list_inventory` (configured targets and tags), and `set_active_target` to the match.
   - → `investigate` when the right target is active and the region is known.
   - → `terminal_blocked` when no configured target matches the signal's account/project.
3. **`investigate`**. One lean node. Pick the cheapest read along the axis the signal points to: reachability (security groups / firewall rules / routes), permissions (IAM reads, simulate), cluster config (GKE/EKS), or logs/audit (what changed before it broke). Use `list_allowed_commands` if unsure what is permitted; `run_cli` for the read; correlate the finding back to the cluster symptom and timeline.
   - → `terminal_done` when a finding explains or cleanly rules out the cluster symptom.
   - → `terminal_blocked` when the needed read is outside the allowlist or the identity's grant.
4. **`terminal_done`**. Hand a short citable bullet back to the parent (resource + finding + time, or "cloud ruled out"). It is a sub-flow, so it does not call `summarize`.
5. **`terminal_no_signal`**. Return without searching, stating explicitly that no cloud signal surfaced and the investigation stays in the cluster, so the parent does not re-enter. This node encodes the core restraint principle.
6. **`terminal_blocked`**. Could not complete: either no configured target matches, or the read is outside the allowlist / identity grant. Name which, advise against trying to widen, continue in-cluster.

### Tool references

The cloud MCP's wire alias is per-source (`triagent-cloud-<alias>`), so unlike `prom_lookup` the cloud tools cannot be hardcoded in `suggested_calls`. Only the stable `triagent-k8s` node read goes in `suggested_calls`; the cloud tools are named in node prose.

### Entity tags

`errors` / `symptoms` tags drawn from the `^[a-z0-9-]+$` vocabulary (e.g. `permission-denied`, `forbidden`, `timeout`) so `playbook_correlate` ranks it for cloud-shaped queries.

## Testing

`system/embed_test.go` already loads and validates every embedded playbook (parse, terminal nodes carry advice, branch `goto`s resolve within the document). Extend it to assert `cloud_triage` loads and its graph is well-formed (entrypoint `gate` present, every `goto` resolves, terminals carry `terminal_advice`).

## Out of scope

- No edit to `investigation.yaml` (locked system meta-playbook; the agent reaches this sub-flow by judgment).
- No per-provider variants; one provider-agnostic playbook.
- No per-axis node branching in the investigate phase (one lean node, by design).
- No new MCP tools or code paths; this is content over the existing cloud MCP surface.
