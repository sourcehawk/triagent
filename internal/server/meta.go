package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/sourcehawk/triagent/pkg/mcp/git"
	"github.com/sourcehawk/triagent/pkg/mcp/incidentio"
	"github.com/sourcehawk/triagent/pkg/mcp/k8s"
	mcpmeta "github.com/sourcehawk/triagent/pkg/mcp/meta"
	"github.com/sourcehawk/triagent/pkg/mcp/parallel"
	"github.com/sourcehawk/triagent/pkg/mcp/slack"
	"github.com/sourcehawk/triagent/pkg/mcp/strategies"
	"github.com/sourcehawk/triagent/pkg/mcp/teleport"
	"github.com/sourcehawk/triagent/pkg/mcp/toolspec"
	"github.com/sourcehawk/triagent/pkg/mcp/wiki"
)

// MetaPlaybook is the launcher-side projection of one embedded playbook
// loaded from the system + plugin playbook dirs. Cached at startup so
// the playbook editor frontend doesn't pay a directory walk per request.
type MetaPlaybook struct {
	YAML   string `json:"yaml"`
	Source string `json:"source"` // "plugin" (overridable upstream library) or "system" (locked launcher metas)
	Type   string `json:"type"`   // type slot the playbook lives in (directory name)
	Locked bool   `json:"locked,omitempty"`
}

// MetaTool is the launcher-side projection of one MCP tool's catalog
// entry. Inputs reflect each tool's Go input struct (+ jsonschema tags)
// so the editor can offer input pickers + the MCP catalog view can
// surface per-input documentation on expand.
type MetaTool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Inputs      []MetaToolInput `json:"inputs,omitempty"`
}

// MetaToolInput is one entry in MetaTool.Inputs — name, coarse type
// label ("string"/"integer"/etc.), human description from the
// jsonschema tag, and required flag derived from `omitempty` on
// the Go json tag.
type MetaToolInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Meta holds everything the playbook editor needs about the triagent-mcp side.
// Loaded once at server start; nil values mean "loadMeta failed" — the
// editor degrades gracefully (list says no playbooks, dropdown is empty).
type Meta struct {
	Playbooks            map[string]MetaPlaybook `json:"playbooks"`
	Tools                []MetaTool              `json:"tools"`
	CurrentSchemaVersion int                     `json:"currentSchemaVersion"`
}

// loadMeta builds the launcher's meta payload in-process by walking the
// plugin + system playbook dirs and aggregating every MCP package's
// ToolSpecs(). Both packages live in the same Go module as the launcher
// since the pkg/mcp/ lift, so we don't need a subprocess shim. System
// wins on id collision, matching the runtime loader's merge semantics.
func loadMeta(_ context.Context, pluginPlaybooksDir, systemPlaybooksDir string) (*Meta, error) {
	out := &Meta{
		Playbooks:            make(map[string]MetaPlaybook),
		Tools:                toolCatalog(),
		CurrentSchemaVersion: strategies.CurrentSchemaVersion,
	}
	if pluginPlaybooksDir != "" {
		plugin, err := strategies.SystemRawPlaybooks(pluginPlaybooksDir)
		if err != nil {
			return nil, fmt.Errorf("load plugin playbooks: %w", err)
		}
		for id, raw := range plugin {
			out.Playbooks[id] = MetaPlaybook{YAML: raw.YAML, Source: "plugin", Type: raw.Type}
		}
	}
	if systemPlaybooksDir != "" {
		system, err := strategies.SystemRawPlaybooks(systemPlaybooksDir)
		if err != nil {
			return nil, fmt.Errorf("load system playbooks: %w", err)
		}
		for id, raw := range system {
			out.Playbooks[id] = MetaPlaybook{YAML: raw.YAML, Source: "system", Type: raw.Type, Locked: true}
		}
	}
	return out, nil
}

// toolCatalog aggregates each MCP package's ToolSpecs() into one
// ordered launcher-side catalog. Order matches the historical
// dump-meta ordering so the editor's tool dropdown groups render
// in the same sequence operators see elsewhere.
func toolCatalog() []MetaTool {
	specs := make([]toolspec.ToolSpec, 0, 64)
	specs = append(specs, k8s.ToolSpecs()...)
	specs = append(specs, strategies.ToolSpecs()...)
	specs = append(specs, mcpmeta.ToolSpecs()...)
	specs = append(specs, git.ToolSpecs()...)
	specs = append(specs, wiki.ToolSpecs()...)
	specs = append(specs, slack.ToolSpecs()...)
	specs = append(specs, incidentio.ToolSpecs()...)
	specs = append(specs, parallel.ToolSpecs()...)
	specs = append(specs, teleport.ToolSpecs()...)
	out := make([]MetaTool, 0, len(specs))
	for _, s := range specs {
		ins := make([]MetaToolInput, 0, len(s.Inputs))
		for _, in := range s.Inputs {
			ins = append(ins, MetaToolInput{
				Name:        in.Name,
				Type:        in.Type,
				Description: in.Description,
				Required:    in.Required,
			})
		}
		out = append(out, MetaTool{
			Server:      s.Server,
			Name:        s.Name,
			Description: s.Description,
			Inputs:      ins,
		})
	}
	return out
}

// metaCache wraps a *Meta with a mutex so future hot-reload (e.g. watching
// the embedded playbooks dir during development) is a one-line swap.
// Today's load is one-shot at startup, but the cache type is cheap.
type metaCache struct {
	mu   sync.RWMutex
	meta *Meta
}

func (c *metaCache) set(m *Meta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.meta = m
}

func (c *metaCache) get() *Meta {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.meta
}
