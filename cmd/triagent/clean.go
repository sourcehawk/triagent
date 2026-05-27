package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/spf13/cobra"
)

// Stable category identifiers for the --only-* flag set. Keep these
// strings stable — they're how the operator addresses each subsystem
// from the CLI.
const (
	categorySessions      = "sessions"
	categoryPlaybooks     = "playbooks"
	categoryWiki          = "wiki"
	categoryWatches       = "watches"
	categorySlackCache    = "slack-cache"
	categoryUserPlaybooks = "user-playbooks"
)

// clean returns the `triagent clean` subcommand: removes the
// launcher's on-disk caches (sessions, upstream-playbooks clone,
// extracted system-playbooks, wiki clone). Operator-authored content
// (the user playbooks dir) is opt-in via --include-user.
//
// Use cases:
//   - Reset session state during dev / debugging
//   - Force a fresh upstream-playbooks clone (e.g. after the upstream
//     repo url changed, or to recover from a corrupted .git)
//   - Drop a stuck system-playbooks extract that survived a binary
//     downgrade (re-extracted on next start anyway)
//
// Default behaviour is destructive-but-confirmed: the command lists
// every path it would delete, asks for an explicit "yes", and only
// then unlinks. --yes skips the prompt; --dry-run just lists.
func clean() *cobra.Command {
	var (
		yes            bool
		dryRun         bool
		includeUser    bool
		onlySessions   bool
		onlyPlaybooks  bool
		onlyWiki       bool
		onlyWatches    bool
		onlySlackCache bool
	)
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove the launcher's on-disk caches (sessions, clones, extracts, watches)",
		Long: `Clean the launcher's storage directories. By default removes:

  - sessions root (per-investigation transcripts + per-session MCP configs)
  - upstream playbooks clone (re-cloned on next start)
  - extracted bundled system-playbooks (re-extracted on next start)
  - upstream wiki clone (re-cloned on next start)
  - watches ingestion data (items + signals + queue + state per watch — user_watches.yaml config preserved)
  - slack channel caches (recreated on next slack-aware tool call)

User-authored playbooks are NOT touched by default — those are
operator content. Pass --include-user to nuke them too.

To scope cleanup to one subsystem pass --only-<name>. Multiple flags
are additive (union of the listed categories):

  triagent clean --only-watches
  triagent clean --only-watches --only-sessions --yes

Note: --only-playbooks is a full playbook reset — it includes the
user playbooks dir alongside the upstream clone + bundled extract,
because the latter two are recovered on next start anyway and the
user dir is the only thing worth protecting. Use it when you want a
clean slate after upstream playbook changes land. The broad
"triagent clean" still protects the user dir by default.

The command lists every path it would delete and asks for confirmation
before unlinking. --yes skips the prompt; --dry-run only lists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClean(cleanFlags{
				Yes:            yes,
				DryRun:         dryRun,
				IncludeUser:    includeUser,
				OnlySessions:   onlySessions,
				OnlyPlaybooks:  onlyPlaybooks,
				OnlyWiki:       onlyWiki,
				OnlyWatches:    onlyWatches,
				OnlySlackCache: onlySlackCache,
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt and delete immediately")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list paths that would be removed without deleting them")
	cmd.Flags().BoolVar(&includeUser, "include-user", false, "ALSO delete the user playbooks dir (operator-authored content); off by default")
	cmd.Flags().BoolVar(&onlySessions, "only-sessions", false, "only clean the sessions root")
	cmd.Flags().BoolVar(&onlyPlaybooks, "only-playbooks", false, "only clean playbook content (upstream clone, bundled extract, user dir)")
	cmd.Flags().BoolVar(&onlyWiki, "only-wiki", false, "only clean the upstream wiki clone")
	cmd.Flags().BoolVar(&onlyWatches, "only-watches", false, "only clean per-watch ingestion data (user_watches.yaml config preserved)")
	cmd.Flags().BoolVar(&onlySlackCache, "only-slack-cache", false, "only clean the slack channel caches")
	return cmd
}

type cleanFlags struct {
	Yes            bool
	DryRun         bool
	IncludeUser    bool
	OnlySessions   bool
	OnlyPlaybooks  bool
	OnlyWiki       bool
	OnlyWatches    bool
	OnlySlackCache bool
}

// selectedCategories returns the set of category identifiers the
// operator opted into via --only-* flags. Empty when none are set —
// callers should interpret that as "keep all default targets".
func (f cleanFlags) selectedCategories() map[string]bool {
	out := map[string]bool{}
	if f.OnlySessions {
		out[categorySessions] = true
	}
	if f.OnlyPlaybooks {
		out[categoryPlaybooks] = true
		// User-dir playbooks live in their own category so the broad
		// clean can protect them by default; --only-playbooks is the
		// explicit "all playbook content" scope, so it includes them.
		out[categoryUserPlaybooks] = true
	}
	if f.OnlyWiki {
		out[categoryWiki] = true
	}
	if f.OnlyWatches {
		out[categoryWatches] = true
	}
	if f.OnlySlackCache {
		out[categorySlackCache] = true
	}
	return out
}

func runClean(f cleanFlags) error {
	// --only-playbooks implies user-dir inclusion: the upstream clone
	// and bundled extract are recoverable on next start, so the only
	// playbook content worth protecting is the user dir — and if the
	// operator explicitly scoped to playbooks, they're asking for a
	// full reset of that content. The broad-clean default still
	// protects user playbooks (only --include-user surfaces them).
	includeUser := f.IncludeUser || f.OnlyPlaybooks
	targets, err := cleanTargets(includeUser)
	if err != nil {
		return err
	}

	// If any --only-* flag is set, filter to those categories only.
	if selected := f.selectedCategories(); len(selected) > 0 {
		filtered := targets[:0]
		for _, t := range targets {
			if selected[t.Category] {
				filtered = append(filtered, t)
			}
		}
		targets = filtered
	}

	// Filter to paths that actually exist — listing dangling targets
	// in the confirmation prompt would just be noise.
	var present []cleanTarget
	for _, t := range targets {
		info, err := os.Stat(t.Path)
		if err != nil {
			continue
		}
		t.size = approxDirSize(t.Path, info)
		present = append(present, t)
	}

	if len(present) == 0 {
		fmt.Println("nothing to clean — every target directory is already absent.")
		return nil
	}

	fmt.Println("the following paths will be removed:")
	for _, t := range present {
		fmt.Printf("  - %s  (%s)  %s\n", t.Path, t.Label, humanSize(t.size))
	}
	fmt.Println()

	if f.DryRun {
		fmt.Println("--dry-run: no files were deleted.")
		return nil
	}

	if !f.Yes {
		fmt.Print("delete these paths? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("aborted; nothing deleted.")
			return nil
		}
	}

	for _, t := range present {
		if err := os.RemoveAll(t.Path); err != nil {
			log.Error("failed to remove", "path", t.Path, "err", err)
			// Continue — partial cleanup is more useful than refusing
			// to remove anything because one path failed (e.g. a
			// busy file from a still-running launcher).
			continue
		}
		log.Info("removed", "path", t.Path, "label", t.Label)
	}
	return nil
}

type cleanTarget struct {
	Path     string
	Label    string // human-readable hint for the confirmation prompt
	Category string // stable identifier the --only-* flags filter on
	size     int64  // populated post-stat; not from the resolver
}

// slackCacheDir returns the slack MCP's default cache root —
// <UserCacheDir>/triagent-mcp/slack. Each per-channel cache lives under it as
// <root>/<channelID>/. Mirrors mcp/internal/slack/server.go's defaulting
// logic; if the operator overrides the slack MCP's cache dir via env or
// flag, this resolver will not reflect that — the configured path lives
// inside the running MCP process and isn't surfaced to the launcher
// outside its config-write step.
func slackCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "triagent-mcp", "slack"), nil
}

// cleanTargets builds the path list. Pulls every cleanable directory
// out of the active profile's Paths block so the clean command reflects
// whatever layout the launcher actually uses.
func cleanTargets(includeUser bool) ([]cleanTarget, error) {
	prof, err := profile.Load(ResolveProfileRef(""))
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	paths, err := prof.Paths.Resolve(prof.Name)
	if err != nil {
		return nil, fmt.Errorf("resolve profile paths: %w", err)
	}

	var out []cleanTarget

	if paths.SessionsRoot != "" {
		out = append(out, cleanTarget{Path: paths.SessionsRoot, Label: "sessions", Category: categorySessions})
	}
	if paths.UpstreamPlaybooksDir != "" {
		out = append(out, cleanTarget{Path: paths.UpstreamPlaybooksDir, Label: "upstream playbooks clone", Category: categoryPlaybooks})
	}
	if paths.SystemPlaybooksDir != "" {
		out = append(out, cleanTarget{Path: paths.SystemPlaybooksDir, Label: "bundled system-playbooks extract", Category: categoryPlaybooks})
	}
	if paths.WikiDir != "" {
		out = append(out, cleanTarget{Path: paths.WikiDir, Label: "upstream wiki clone", Category: categoryWiki})
	}

	// Watches: only the data dir (items.jsonl + signals.jsonl + queue.json
	// + state.json per watch). The yaml config (user_watches.yaml) is
	// preserved so toggling --only-watches resets ingestion state without
	// nuking the operator's watch list — they can re-poll from scratch.
	// Operators who want to delete watches use the per-watch UI delete
	// or edit user_watches.yaml directly.
	if paths.UserWatchesFile != "" {
		out = append(out, cleanTarget{
			Path:     filepath.Join(filepath.Dir(paths.UserWatchesFile), "watches"),
			Label:    "watches ingestion data (items, signals, queue, state per watch — configs preserved)",
			Category: categoryWatches,
		})
	}

	if dir, err := slackCacheDir(); err == nil {
		out = append(out, cleanTarget{Path: dir, Label: "slack channel caches", Category: categorySlackCache})
	}

	if includeUser && paths.UserPlaybooksDir != "" {
		out = append(out, cleanTarget{
			Path:     paths.UserPlaybooksDir,
			Label:    "USER PLAYBOOKS — your authored content!",
			Category: categoryUserPlaybooks,
		})
	}

	return out, nil
}

// approxDirSize returns the on-disk byte total for a path. Best-
// effort: errors mid-walk just stop the count and we return what we
// had — the size is purely advisory for the confirmation prompt.
// Symlinks are not followed (consistent with `du -hs`).
func approxDirSize(path string, info os.FileInfo) int64 {
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip what we can't read; carry on
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		total += fi.Size()
		return nil
	})
	return total
}

// humanSize formats a byte count as a tight human-readable string
// for the confirmation prompt. Goes up to GiB; the launcher's caches
// rarely exceed that.
func humanSize(n int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	switch {
	case n < KiB:
		return fmt.Sprintf("%d B", n)
	case n < MiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(KiB))
	case n < GiB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(MiB))
	default:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(GiB))
	}
}
