package k8s

import (
	"fmt"
	"path"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// DefaultCrossplaneGroupPatterns is the fallback group-glob list used when
// the caller passes none. Covers the common Crossplane provider conventions
// (Upbound/Upjet and core Crossplane providers). Operators can extend with
// env / CLI overrides — e.g. to include legacy `*.gcp.crossplane.io`.
var DefaultCrossplaneGroupPatterns = []string{
	"*.upbound.io",
	"*.crossplane.io",
}

// ResolvedKind pairs an allow-list entry with the plural resource name and
// namespacing that the cluster actually reports at startup.
type ResolvedKind struct {
	Kind         Kind
	GVR          schema.GroupVersionResource
	Namespaced   bool
	Scope        string // "Namespaced" or "Cluster"
	ResourceName string // plural, e.g. "pods", "zeebeclusters"
}

// Resolver maps kind lookups used by MCP tool callers to the concrete GVR
// needed to drive the dynamic client.
type Resolver struct {
	byKey map[string]ResolvedKind // lower-cased "Kind" OR "group/Kind" OR "plural"
	order []string                // allow-list order, for stable listing
}

// BuildResolver queries cluster discovery to verify each allow-list entry
// and returns a resolver ready for tool handlers to use. Kinds the cluster
// does not report are dropped from the resolver and surfaced as warnings
// (they are not fatal — different workload versions ship different CRDs).
func BuildResolver(dc discovery.DiscoveryInterface, allow *Allowlist) (*Resolver, []string, error) {
	r := &Resolver{byKey: make(map[string]ResolvedKind)}
	var warnings []string

	for _, k := range allow.Kinds {
		gv := schema.GroupVersion{Group: k.Group, Version: k.Version}
		rl, err := dc.ServerResourcesForGroupVersion(gv.String())
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("discovery failed for %s: %v", k.GVK(), err))
			continue
		}

		var match *metav1.APIResource
		for i := range rl.APIResources {
			ar := &rl.APIResources[i]
			// Skip subresources like "pods/status", "pods/log".
			if strings.Contains(ar.Name, "/") {
				continue
			}
			if strings.EqualFold(ar.Kind, k.Kind) {
				match = ar
				break
			}
		}
		if match == nil {
			warnings = append(warnings, fmt.Sprintf("%s not found on cluster, skipping", k.GVK()))
			continue
		}

		scope := "Cluster"
		if match.Namespaced {
			scope = "Namespaced"
		}
		rk := ResolvedKind{
			Kind:         k,
			GVR:          schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: match.Name},
			Namespaced:   match.Namespaced,
			Scope:        scope,
			ResourceName: match.Name,
		}

		keyCanonical := strings.ToLower(k.Kind)
		r.byKey[keyCanonical] = rk
		if k.Group != "" {
			r.byKey[strings.ToLower(k.Group+"/"+k.Kind)] = rk
		}
		r.byKey[strings.ToLower(match.Name)] = rk
		r.order = append(r.order, k.GVK())
	}

	return r, warnings, nil
}

// DiscoverCrossplaneGVRs returns every namespaced-or-cluster-scoped GVR the
// cluster reports whose API group matches any of patterns (glob via
// path.Match). Subresources ("pods/log", etc.) are skipped. Failures on a
// single GroupVersion are reported as warnings and don't block the scan.
func DiscoverCrossplaneGVRs(dc discovery.DiscoveryInterface, patterns []string) ([]schema.GroupVersionResource, []string, error) {
	if len(patterns) == 0 {
		patterns = DefaultCrossplaneGroupPatterns
	}

	groupList, err := dc.ServerGroups()
	if err != nil {
		return nil, nil, fmt.Errorf("discover server groups: %w", err)
	}

	var gvrs []schema.GroupVersionResource
	var warnings []string
	for _, g := range groupList.Groups {
		if !groupMatches(g.Name, patterns) {
			continue
		}
		// Use the preferred version only — following every served version
		// for every group would balloon the fan-out with little benefit, and
		// Crossplane MRs are consistently reachable via their preferred one.
		gv := g.PreferredVersion.GroupVersion
		if gv == "" {
			continue
		}
		rl, err := dc.ServerResourcesForGroupVersion(gv)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("crossplane group %s: %v", gv, err))
			continue
		}
		for i := range rl.APIResources {
			ar := &rl.APIResources[i]
			if strings.Contains(ar.Name, "/") {
				continue
			}
			gvrs = append(gvrs, schema.GroupVersionResource{
				Group:    g.Name,
				Version:  g.PreferredVersion.Version,
				Resource: ar.Name,
			})
		}
	}
	return gvrs, warnings, nil
}

func groupMatches(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// Lookup finds the resolved entry for a kind by Kind name (e.g. "Pod"),
// group-qualified form ("apps/Deployment"), or plural resource name ("pods").
// Matching is case-insensitive.
func (r *Resolver) Lookup(name string) (ResolvedKind, bool) {
	rk, ok := r.byKey[strings.ToLower(name)]
	return rk, ok
}

// All returns resolved kinds in allow-list order (no duplicates).
func (r *Resolver) All() []ResolvedKind {
	seen := make(map[string]struct{}, len(r.order))
	out := make([]ResolvedKind, 0, len(r.order))
	for _, gvk := range r.order {
		for _, rk := range r.byKey {
			if rk.Kind.GVK() == gvk {
				if _, ok := seen[gvk]; ok {
					break
				}
				seen[gvk] = struct{}{}
				out = append(out, rk)
				break
			}
		}
	}
	return out
}
