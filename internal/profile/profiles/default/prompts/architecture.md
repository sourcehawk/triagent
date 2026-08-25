The shape of this cluster's workloads is platform-specific. Discover what runs here with `mcp__triagent-k8s__list_resource_kinds` before you drill in.

Triage heuristics that hold on most clusters:

- **Read the workload's `status` first. Call `get_logs` per pod only after that.** Operator-reconciled resources surface per-component health on the parent CR.
- **If a CR is `not Ready` with a vague message, walk down.** The real error almost always lives on a child that the operator composed: a managed cloud resource, a child workload, an external secret claim.
- **`kube-system` and ingress controllers have cluster-wide blast radius.** When they fail, expect the symptoms of everything else to be downstream.

**This is a generic starting point, not a substitute for site knowledge.** Operators who run this in a real environment fork the default profile and replace this file with their platform's specifics: top-level CRDs, namespace conventions, the dependency direction between components, version-pivot gotchas, and common failure modes worth pre-loading. The README explains how to do that with `base: default` in a sibling `profile.yaml`.
