package teleport

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the teleport server's tool catalog with each
// tool's input shape introspected from its Go struct (and its
// jsonschema tags).
//
// Order mirrors the registration order in server.go's registerOn().
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server:      "triagent-teleport",
			Name:        "list_clusters",
			Description: "List Kubernetes clusters reachable via Teleport. Marks clusters that already have a kubeconfig context provisioned.",
			Inputs:      toolspec.FromStruct(ListClustersInput{}),
		},
		{
			Server:      "triagent-teleport",
			Name:        "login",
			Description: "Provision kubeconfig credentials for a Teleport-managed cluster. Returns the kubeconfig context name; pair with triagent-k8s.switch_context to start using it.",
			Inputs:      toolspec.FromStruct(LoginInput{}),
		},
	}
}
