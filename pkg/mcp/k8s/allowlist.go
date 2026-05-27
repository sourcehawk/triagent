// Package k8s implements the read-only, namespace-scoped Kubernetes MCP server
// that the triagent-mcp binary exposes to Claude. It surfaces a small set of generic
// tools (list_resource_kinds, list_resources, get_resource, get_logs, list_events,
// trace_crossplane) bounded by an allow-list and tenant-isolated by namespace.
package k8s

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed default_kinds.json
var defaultKindsJSON []byte

// Kind is one entry in the resource allow-list. The group+version+kind triple
// is required; description and redact are optional.
type Kind struct {
	Group       string `json:"group"`
	Version     string `json:"version"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	Redact      bool   `json:"redact,omitempty"`
}

// GVK returns the canonical "group/version:Kind" form (or "version:Kind" for
// core resources) used for diagnostics and map keys.
func (k Kind) GVK() string {
	if k.Group == "" {
		return k.Version + ":" + k.Kind
	}
	return k.Group + "/" + k.Version + ":" + k.Kind
}

// Allowlist is the decoded allow-list document.
type Allowlist struct {
	Kinds []Kind `json:"kinds"`
}

// LoadAllowlist returns the allow-list from path, or the embedded default
// if path is empty. Secret is always filtered out regardless of what the
// input asks for — its contents are never safe to serve to an LLM.
func LoadAllowlist(path string) (*Allowlist, error) {
	data := defaultKindsJSON
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read allow-list %q: %w", path, err)
		}
		data = b
	}

	var list Allowlist
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse allow-list: %w", err)
	}

	filtered := make([]Kind, 0, len(list.Kinds))
	for _, k := range list.Kinds {
		if k.Version == "" || k.Kind == "" {
			return nil, fmt.Errorf("allow-list entry missing version/kind: %+v", k)
		}
		if strings.EqualFold(k.Kind, "Secret") && (k.Group == "" || strings.EqualFold(k.Group, "core")) {
			// Secret.v1.core is always dropped; its contents are never exposed.
			continue
		}
		filtered = append(filtered, k)
	}
	list.Kinds = filtered
	return &list, nil
}
