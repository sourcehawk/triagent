The shape of this cluster's workloads is platform-specific. Discover what's running here via
`mcp__triagent-k8s__list_resource_kinds` before drilling in.

Triage heuristics that hold on most clusters:

- **Read the workload's `status` first; only `get_logs` per-pod after.** Operator-reconciled resources surface
  per-component health on the parent CR.
- **CRs `not Ready` with a vague message — walk down.** The real error almost always lives on a child the
  operator composed (a managed cloud resource, a child workload, an external secret claim).
- **`kube-system` and ingress controllers have cluster-wide blast radius.** When they fail, expect everything
  else's symptoms to be downstream.

**This is a generic starting point, not a substitute for site knowledge.** Operators running this in a real
environment should fork the default profile and replace this file with their platform's specifics — top-level
CRDs, namespace conventions, dependency direction between components, version-pivot gotchas, and common failure
modes worth pre-loading. See the README for how to do that with `base: default` in a sibling `profile.yaml`.
