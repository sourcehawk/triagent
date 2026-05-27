package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/repos"
)

// MCPProbeResult is one per-alias readiness check outcome.
type MCPProbeResult struct {
	Alias     string `json:"alias"`
	Kind      string `json:"kind"`             // logical kind: k8s | strategies | git | wiki | docs
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"` // populated when !OK; short human-readable
	LatencyMS int64  `json:"latencyMs"`
	CheckedAt string `json:"checkedAt"` // RFC3339
}

// runMCPProbes runs all probes for a given investigation in parallel
// (bounded). Returns one result per registered MCP alias for that session.
//
// Total wallclock cap: ~5s. Each individual probe has its own ~3s deadline
// derived from ctx so a single misbehaving check doesn't block the rest.
func runMCPProbes(ctx context.Context, inv *Investigation, opts Options) []MCPProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	type job struct {
		alias string
		kind  string
		probe func(context.Context) error
	}

	jobs := []job{
		// strategies — verify playbook dirs are readable.
		{
			alias: preflight.MCPAliasStrategies,
			kind:  "strategies",
			probe: func(c context.Context) error {
				return probeStrategies(c, opts.UserPlaybooksDir, opts.SystemPlaybooksDir)
			},
		},
	}

	// per-repo git instances.
	for _, r := range inv.LinkedRepos {
		alias := preflight.MCPAliasGitPrefix + r.EffectiveAlias()
		repo := r // capture
		jobs = append(jobs, job{
			alias: alias,
			kind:  "git",
			probe: func(c context.Context) error {
				return probeGitCache(c, opts.GitCacheDir, repo)
			},
		})
	}

	// wiki — only when configured.
	if opts.WikiPath != "" {
		wikiPath := opts.WikiPath
		jobs = append(jobs, job{
			alias: preflight.MCPAliasWiki,
			kind:  "wiki",
			probe: func(c context.Context) error {
				return probeWikiVault(c, wikiPath)
			},
		})
	}

	// docs — best-effort; we don't have a structured probe today.
	if inv.DocsPrefix != "" {
		alias := strings.TrimSuffix(strings.TrimPrefix(inv.DocsPrefix, "mcp__"), "__")
		jobs = append(jobs, job{
			alias: alias,
			kind:  "docs",
			probe: func(c context.Context) error {
				// We can't probe an arbitrary user-configured docs MCP
				// without re-implementing claude's MCP-config plumbing.
				// Treat as "unknown" by reporting OK; the passive
				// telemetry-driven view still surfaces actual usage state.
				return nil
			},
		})
	}

	// Run probes concurrently. Each gets a bounded sub-context.
	results := make([]MCPProbeResult, len(jobs))
	done := make(chan struct{}, len(jobs))
	for i, j := range jobs {
		i, j := i, j
		go func() {
			start := time.Now()
			pCtx, pCancel := context.WithTimeout(ctx, 3*time.Second)
			defer pCancel()
			err := j.probe(pCtx)
			ok := err == nil
			reason := ""
			if !ok {
				reason = err.Error()
				if len(reason) > 200 {
					reason = reason[:200] + "…"
				}
			}
			results[i] = MCPProbeResult{
				Alias:     j.alias,
				Kind:      j.kind,
				OK:        ok,
				Reason:    reason,
				LatencyMS: time.Since(start).Milliseconds(),
				CheckedAt: time.Now().UTC().Format(time.RFC3339),
			}
			done <- struct{}{}
		}()
	}
	for range jobs {
		<-done
	}
	return results
}

// ── individual probes ────────────────────────────────────────────────────────

// Note: probeKubectl was removed. The k8s MCP (triagent-k8s) boots context-less
// since E2: the agent picks a context via switch_context, so the launcher
// never has a meaningful kube context to probe at start time. A broken
// kubeconfig surfaces a clear error at the moment the agent calls
// switch_context — no silent pre-check is needed here.

func probeStrategies(_ context.Context, userDir, systemDir string) error {
	for _, d := range []string{systemDir, userDir} {
		if d == "" {
			continue
		}
		if _, err := os.Stat(d); err != nil {
			return fmt.Errorf("playbooks dir %s: %w", d, err)
		}
	}
	return nil
}

func probeGitCache(_ context.Context, cacheDir string, repo repos.LinkedRepo) error {
	if cacheDir == "" {
		// triagent-mcp git uses XDG_CACHE_HOME default; skip probe rather than
		// false-fail on the resolved path.
		return nil
	}
	dir := filepath.Join(cacheDir, repo.Owner, repo.Name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("repo cache %s/%s missing — first tool call will clone", repo.Owner, repo.Name)
	}
	return nil
}

func probeWikiVault(_ context.Context, vaultPath string) error {
	if vaultPath == "" {
		return errors.New("wiki vault not configured")
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".git")); err != nil {
		return fmt.Errorf("wiki vault %s is not a git checkout", vaultPath)
	}
	return nil
}
