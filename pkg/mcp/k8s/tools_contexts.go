package k8s

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/tools/clientcmd"
)

// --- list_contexts ---------------------------------------------------------

type ListContextsInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Optional case-sensitive substring matched against the context name."`
}

type ContextInfo struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Current   bool   `json:"current" jsonschema:"True when this is the kubeconfig's current-context."`
	Active    bool   `json:"active" jsonschema:"True when this MCP is currently bound to this context."`
}

type ListContextsOutput struct {
	Items []ContextInfo `json:"items"`
}

func (t *ToolKit) listContexts(_ context.Context, _ *mcp.CallToolRequest, in ListContextsInput) (*mcp.CallToolResult, ListContextsOutput, error) {
	cfg, err := clientcmd.LoadFromFile(t.kubeconfigPath)
	if err != nil {
		return errorResult("load kubeconfig: " + err.Error()), ListContextsOutput{}, nil
	}
	activeName := ""
	if snap := t.snapshot.Load(); snap != nil {
		activeName = snap.contextName
	}
	out := ListContextsOutput{Items: make([]ContextInfo, 0, len(cfg.Contexts))}
	for name, c := range cfg.Contexts {
		if in.Filter != "" && !strings.Contains(name, in.Filter) {
			continue
		}
		out.Items = append(out.Items, ContextInfo{
			Name:      name,
			Cluster:   c.Cluster,
			Namespace: c.Namespace,
			Current:   name == cfg.CurrentContext,
			Active:    name == activeName,
		})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Name < out.Items[j].Name })
	return nil, out, nil
}

// --- get_current_context ---------------------------------------------------

type GetCurrentContextInput struct{}

type GetCurrentContextOutput struct {
	Name         string   `json:"name"`
	Cluster      string   `json:"cluster"`
	Namespace    string   `json:"namespace,omitempty"`
	Warnings     []string `json:"warnings,omitempty" jsonschema:"Non-fatal issues from the most recent context bind (e.g. allow-listed kinds not installed)."`
	AllowedKinds int      `json:"allowedKindsAvailable" jsonschema:"How many allow-listed kinds the cluster actually exposes."`
}

func (t *ToolKit) getCurrentContext(_ context.Context, _ *mcp.CallToolRequest, _ GetCurrentContextInput) (*mcp.CallToolResult, GetCurrentContextOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, GetCurrentContextOutput{}, nil
	}
	cfg, err := clientcmd.LoadFromFile(t.kubeconfigPath)
	if err != nil {
		return errorResult("load kubeconfig: " + err.Error()), GetCurrentContextOutput{}, nil
	}
	c := cfg.Contexts[snap.contextName]
	out := GetCurrentContextOutput{
		Name:         snap.contextName,
		Warnings:     snap.warnings,
		AllowedKinds: len(snap.resolver.All()),
	}
	if c != nil {
		out.Cluster = c.Cluster
		out.Namespace = c.Namespace
	}
	return nil, out, nil
}

// --- switch_context --------------------------------------------------------

type SwitchContextInput struct {
	Name string `json:"name" jsonschema:"Kubeconfig context to bind to. Use triagent-teleport.login to provision a context for a Teleport-managed cluster first."`
}

type SwitchContextOutput struct {
	PreviousContext string   `json:"previousContext,omitempty"`
	ActiveContext   string   `json:"activeContext"`
	Cluster         string   `json:"cluster,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

func (t *ToolKit) switchContext(_ context.Context, _ *mcp.CallToolRequest, in SwitchContextInput) (*mcp.CallToolResult, SwitchContextOutput, error) {
	if in.Name == "" {
		return errorResult("name is required (the kubeconfig context to bind to)"), SwitchContextOutput{}, nil
	}
	newSnap, err := t.buildSnapshot(in.Name)
	if err != nil {
		return errorResult("switch_context " + in.Name + ": " + err.Error()), SwitchContextOutput{}, nil
	}
	prev := ""
	if old := t.snapshot.Load(); old != nil {
		prev = old.contextName
	}
	t.snapshot.Store(newSnap)

	if t.sessionDir != "" {
		// Best-effort: triagent's launcher reads this file to drive prom
		// port-forwards. Failure to write is non-fatal — the snapshot
		// swap already succeeded, the agent's tool calls work, only the
		// launcher's prom resolver loses visibility into the new context.
		if err := writeActiveContextFile(t.sessionDir, newSnap.contextName); err != nil {
			fmt.Fprintf(os.Stderr, "k8s: write active-context: %v\n", err)
		}
	}

	out := SwitchContextOutput{
		PreviousContext: prev,
		ActiveContext:   newSnap.contextName,
		Warnings:        newSnap.warnings,
	}
	if cfg, err := clientcmd.LoadFromFile(t.kubeconfigPath); err == nil {
		if c := cfg.Contexts[newSnap.contextName]; c != nil {
			out.Cluster = c.Cluster
		}
	}
	return nil, out, nil
}
