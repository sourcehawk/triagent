// Package repos manages the launcher's view of GitHub repositories that
// can be linked to an investigation. Two sources back the model:
//
//   - defaults — read-only, sourced from the active profile's
//     linked_repos field. Always show up in the picker; removed from
//     the API surface (no DELETE).
//   - user_repos.yaml — user-managed list. Added/removed via the API.
//     Path is read from the profile's paths.user_repos_file.
//
// The resolved investigation repo list is the union of defaults and
// user repos, deduped by owner/name (defaults win).
package repos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sourcehawk/triagent/internal/profile"
)

// ProfileDefaults converts a profile's LinkedRepos list into the repos
// package's own LinkedRepo type. The field names are identical, so this is a
// mechanical copy. Returns nil when p is nil or has no linked_repos.
//
// This is the single source of "system / org defaults" — operators
// who want a different set fork the profile and override
// linked_repos there.
func ProfileDefaults(p *profile.Profile) []LinkedRepo {
	if p == nil || len(p.LinkedRepos) == 0 {
		return nil
	}
	out := make([]LinkedRepo, len(p.LinkedRepos))
	for i, r := range p.LinkedRepos {
		out[i] = LinkedRepo{
			Owner:       r.Owner,
			Name:        r.Name,
			Alias:       r.Alias,
			Description: r.Description,
		}
	}
	return out
}

// MinDescriptionLength is the floor we require for repo descriptions at
// add time. The description is rendered into the investigation prompt's
// Linked repositories section and serves as the first-line orientation
// the agent sees when an architecture summary hasn't yet been generated
// for this repo (or auto-gen on connection failed). One sentence ~30
// chars is the smallest hint that's actually useful — "z" or "a" don't
// help the agent at all. Enforced at add time only; existing entries
// in user_repos.yaml that predate this rule still load to keep launcher
// startup robust against historical data.
const MinDescriptionLength = 30

// LinkedRepo is one GitHub repository linked to an investigation.
type LinkedRepo struct {
	Owner       string    `yaml:"owner" json:"owner"`
	Name        string    `yaml:"name" json:"name"`
	Alias       string    `yaml:"alias,omitempty" json:"alias,omitempty"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	AddedAt     time.Time `yaml:"added_at,omitempty" json:"addedAt,omitempty"`
	// CodefixTimeoutMinutes overrides the default draft_pr wall-clock
	// cap for this repo. Useful for repos with very slow integration
	// test suites (raise) or pure-docs repos (lower). 0 = use default.
	CodefixTimeoutMinutes int `yaml:"codefix_timeout_minutes,omitempty" json:"codefixTimeoutMinutes,omitempty"`
}

// Key is the dedupe key.
func (r LinkedRepo) Key() string { return r.Owner + "/" + r.Name }

// EffectiveAlias returns the explicit alias when set, otherwise the repo
// name. Used to compute the MCP server alias `triagent-git-<alias>`.
func (r LinkedRepo) EffectiveAlias() string {
	if r.Alias != "" {
		return r.Alias
	}
	return r.Name
}

// fileShape is the YAML wire format for both files.
type fileShape struct {
	Repos []LinkedRepo `yaml:"repos"`
}

// LoadDefaults reads the read-only defaults file. Returns an empty list
// when the file doesn't exist (no defaults installed); other errors are
// surfaced.
func LoadDefaults(path string) ([]LinkedRepo, error) {
	return loadFile(path)
}

// LoadUserRepos reads the user-managed file. Returns an empty list when
// the file doesn't exist (no repos added yet).
func LoadUserRepos(path string) ([]LinkedRepo, error) {
	return loadFile(path)
}

func loadFile(path string) ([]LinkedRepo, error) {
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	var f fileShape
	if err := yaml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range f.Repos {
		if err := validateRepo(f.Repos[i]); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return f.Repos, nil
}

// AddUserRepo appends repo to the user file, creating the file (and any
// parent dirs) when needed. Returns an error if the file already contains
// a repo with the same owner/name. AddedAt is stamped at call time when
// caller hasn't set it.
//
// Add-time validation is strictly stronger than load-time validation:
// the description must meet MinDescriptionLength here, but loadFile
// still accepts existing entries with shorter descriptions (so launcher
// startup doesn't fail on historical user_repos.yaml files predating
// this rule).
func AddUserRepo(path string, repo LinkedRepo) error {
	if err := validateRepoForAdd(repo); err != nil {
		return err
	}
	if path == "" {
		return errors.New("user repos file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	existing, err := LoadUserRepos(path)
	if err != nil {
		return err
	}
	for _, r := range existing {
		if r.Key() == repo.Key() {
			return fmt.Errorf("repo %s already in user list", repo.Key())
		}
	}
	if repo.AddedAt.IsZero() {
		repo.AddedAt = time.Now().UTC()
	}
	updated := append(existing, repo)
	return writeFile(path, updated)
}

// RemoveUserRepo removes the entry matching owner/name from the user
// file. Returns an error if the file doesn't contain the repo.
func RemoveUserRepo(path, owner, name string) error {
	existing, err := LoadUserRepos(path)
	if err != nil {
		return err
	}
	key := owner + "/" + name
	out := make([]LinkedRepo, 0, len(existing))
	found := false
	for _, r := range existing {
		if r.Key() == key {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return fmt.Errorf("repo %s not in user list", key)
	}
	return writeFile(path, out)
}

func writeFile(path string, repos []LinkedRepo) error {
	body, err := yaml.Marshal(fileShape{Repos: repos})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(path, body, 0o600)
}

// Resolve merges profile defaults and user-managed repos into a deduped
// list. Order: defaults first (profile order), then user repos by
// AddedAt ascending. Duplicate keys keep the first occurrence so
// defaults always win.
func Resolve(defaults, user []LinkedRepo) []LinkedRepo {
	out := make([]LinkedRepo, 0, len(defaults)+len(user))
	seen := make(map[string]bool)
	add := func(r LinkedRepo) {
		if seen[r.Key()] {
			return
		}
		seen[r.Key()] = true
		out = append(out, r)
	}

	for _, r := range defaults {
		add(r)
	}

	sortedUser := make([]LinkedRepo, len(user))
	copy(sortedUser, user)
	sort.SliceStable(sortedUser, func(i, j int) bool {
		return sortedUser[i].AddedAt.Before(sortedUser[j].AddedAt)
	})
	for _, r := range sortedUser {
		add(r)
	}

	return out
}

// DefaultConfigDir returns the directory the launcher uses for repo
// state files: $XDG_CONFIG_HOME/triagent, falling back to
// ~/.config/triagent.
func DefaultConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "triagent"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "triagent"), nil
}

// DefaultsPath / UserReposPath return the conventional file paths under
// the default config dir.
func DefaultsPath(dir string) string  { return filepath.Join(dir, "defaults.yaml") }
func UserReposPath(dir string) string { return filepath.Join(dir, "user_repos.yaml") }

func validateRepo(r LinkedRepo) error {
	if r.Owner == "" {
		return errors.New("repo owner is empty")
	}
	if r.Name == "" {
		return errors.New("repo name is empty")
	}
	if strings.ContainsAny(r.Owner, "/ \t\n") || strings.ContainsAny(r.Name, "/ \t\n") {
		return fmt.Errorf("invalid repo identifier %s", r.Key())
	}
	if r.Alias != "" && strings.ContainsAny(r.Alias, "/ \t\n") {
		return fmt.Errorf("invalid alias %q for repo %s", r.Alias, r.Key())
	}
	return nil
}

// validateRepoForAdd is the strict variant used at add time (CLI / API /
// embedded defaults). Layered on top of validateRepo's structural checks
// with the description-required rule. Load-time callers stay on
// validateRepo so historical entries don't fail startup.
func validateRepoForAdd(r LinkedRepo) error {
	if err := validateRepo(r); err != nil {
		return err
	}
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		return fmt.Errorf("repo %s: description is required (min %d chars) — describe what's in the repo so the investigation agent has orientation when no architecture summary is cached yet", r.Key(), MinDescriptionLength)
	}
	if len([]rune(desc)) < MinDescriptionLength {
		return fmt.Errorf("repo %s: description must be at least %d characters (got %d) — write 1-2 sentences on what the repo contains and when to consult it", r.Key(), MinDescriptionLength, len([]rune(desc)))
	}
	return nil
}
