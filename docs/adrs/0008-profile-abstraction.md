# ADR-0008: Profile abstraction for deployment-specific defaults

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

All deployment-specific defaults, repo lists, namespace derivation, prompt content, investigation input fields, and external-MCP references load at runtime from a **profile** (`internal/profile/`) — not from baked-in Go constants or `//go:embed` strings.

`pkg/mcp/k8s/default_kinds.json` is platform-neutral. Custom CRDs belong in a deployment-specific profile, not in the MCP binary.

Profiles load by name (embedded) or by path on disk. Profiles support a `base:` key for merging.

`investigation.yaml` and other system playbooks use semantic-role keys (e.g. two-bucket scope) rather than deployment-specific terminology. The profile supplies the deployment-specific terms.

`atomic.Pointer[snapshot]` is the standard for hot-swappable client state (k8s ToolKit, prom URL). It lets `switch_context` / lazy-prom-attach work without locking and without restarting the MCP.

Each subprocess we spawn carries explicit `KUBECONFIG` (and any other context env) — never inherit ambient operator shell state. Operator's mid-session `kubectx` switch must not leak into running investigations.

## Context

Baking deployment-specific things into the binary (a Camunda namespace pattern, a specific repo list, a particular Prom service name) makes the binary unusable for the next deployment. Even with build tags, every fork would need a parallel release pipeline. Loading at runtime from a profile means the same binary serves N deployments, and a new deployment is a YAML file, not a Go change.

`base:` merging lets profiles compose: a Camunda-prod profile inherits from a Camunda-base profile inherits from a default. Each layer overrides only what differs.

Semantic-role keys in system playbooks (rather than deployment-specific terminology) keep the playbook portable: the walker doesn't know what "data-layer team" means, it knows the semantic role, and the profile maps it.

`atomic.Pointer[snapshot]` is the standard for hot-swap because it lets a single reader CAS a new snapshot in without locking readers or restarting subscribers. Used by `switch_context` (k8s tools) and lazy-prom-attach (prom MCP).

Explicit `KUBECONFIG` per subprocess is the safety net: the operator can switch context in their shell mid-investigation, and the running investigation MUST NOT silently follow. The subprocess has its own pinned config.

## Consequences

- No deployment-specific constants in Go code. New deployment = new profile YAML, not a new build.
- `default_kinds.json` stays platform-neutral; custom CRDs are profile-side.
- New hot-swappable client state uses `atomic.Pointer[snapshot]`; never a mutex around a pointer.
- Every spawned subprocess gets explicit context env. Never inherit ambient shell state for `KUBECONFIG` or equivalent.
- System playbook authors use semantic-role keys; profile maintainers supply the deployment-specific terms.
