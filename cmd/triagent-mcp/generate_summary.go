package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/git"
	"github.com/spf13/cobra"
)

// generateSummaryCmd builds the `generate-architecture-summary` subcommand.
// Mirrors the constructor-style other subcommands in this package use —
// no globals, no init() side effects.
func generateSummaryCmd() *cobra.Command {
	var (
		repo              string
		cacheDir          string
		kind              string
		description       string
		focus             string
		operatorEditsFile string
		claudeBinary      string
		model             string
		timeout           time.Duration
	)

	cmd := &cobra.Command{
		Use:   "generate-architecture-summary",
		Short: "Generate (or refresh) the cached architecture summary for a linked repo",
		Args:  cobra.NoArgs,
		Long: `Spawns a focused sub-agent in the repo's clone and writes a Markdown
architecture digest to <cache-dir>/<owner>/<name>/architecture_summary.md
(same layout the triagent-git server's EnsureClone uses for the clone itself).
Invoked by the launcher's auto-gen-on-add flow and by manual refresh from the
repo management page. Not exposed as an MCP tool — agents call the sibling
get_repo_architecture_summary tool to read what this command produces.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			owner, name, err := parseOwnerName(repo)
			if err != nil {
				return err
			}

			// Load the operator-edits diff from file (the launcher
			// writes a temp file rather than passing kilobytes of
			// unified-diff text through a CLI flag value). Empty
			// flag = no edits to consider.
			var operatorEditsDiff string
			if operatorEditsFile != "" {
				data, err := os.ReadFile(operatorEditsFile)
				if err != nil {
					return fmt.Errorf("read operator-edits file %s: %w", operatorEditsFile, err)
				}
				operatorEditsDiff = string(data)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			srv, err := git.New(git.Options{
				Repo:         repo,
				CacheDir:     cacheDir,
				ClaudeBinary: claudeBinary,
			})
			if err != nil {
				return fmt.Errorf("init git server: %w", err)
			}
			repoPath, err := srv.EnsureClone(ctx)
			if err != nil {
				return fmt.Errorf("ensure clone: %w", err)
			}

			res, err := git.GenerateArchitectureSummary(ctx, git.GenerateOptions{
				GitCacheDir:       cacheDir,
				RepoPath:          repoPath,
				Owner:             owner,
				Name:              name,
				Kind:              kind,
				Description:       description,
				Focus:             focus,
				OperatorEditsDiff: operatorEditsDiff,
				Model:             model,
				Timeout:           timeout,
				ClaudeBinary:      claudeBinary,
			})
			if err != nil {
				return fmt.Errorf("generate: %w", err)
			}

			// Print the cache file path so callers can pick it up from stdout.
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Path)
			if res.TimedOut {
				// Non-zero exit so the launcher can distinguish timeout from
				// success. The cache file is still written (with error
				// frontmatter); the caller reads it for the user-facing
				// error message.
				return fmt.Errorf("sub-agent timed out after %s", timeout)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "owner/name (required)")
	f.StringVar(&cacheDir, "cache-dir", "", "root for git clones + summary cache (required); same value the triagent-git server uses for its --cache-dir")
	f.StringVar(&kind, "kind", "freeform", "summary kind: freeform (v1)")
	f.StringVar(&description, "description", "", "operator-authored description of the repo (the connection-time description from user_repos.yaml). Surfaced to the sub-agent so it can self-allocate emphasis without per-kind prompt templates.")
	f.StringVar(&focus, "focus", "", "optional operator hint, narrower than --description, e.g. 'concentrate on the reconcile loop'")
	f.StringVar(&operatorEditsFile, "operator-edits-file", "", "path to a file containing a unified diff of the operator's hand-edits on the prior baseline. Surfaced to the sub-agent as advisory context so it can incorporate preserved intent into the fresh output. Empty = no prior edits to consider.")
	f.StringVar(&claudeBinary, "claude-binary", "", "path to claude CLI (defaults to $PATH)")
	f.StringVar(&model, "model", "", "model name written to the cache file's frontmatter (informational)")
	f.DurationVar(&timeout, "timeout", git.DefaultGenerateTimeout, "sub-agent timeout cap")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("cache-dir")
	return cmd
}

func parseOwnerName(repo string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(repo), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid --repo %q: expected owner/name", repo)
	}
	return parts[0], parts[1], nil
}
