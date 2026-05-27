package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths is the per-machine filesystem layout the launcher reads from
// the profile. Every field accepts the tokens ${XDG_CONFIG_HOME},
// ${XDG_CACHE_HOME}, and ${HOME}; Resolve() expands them. Operators
// who need a different layout fork the profile and override the
// fields they care about — there are no CLI flags for these.
//
// XDG fallbacks match the spec: ${XDG_CONFIG_HOME} → $HOME/.config,
// ${XDG_CACHE_HOME} → $HOME/.cache.
type Paths struct {
	// UpstreamPlaybooksDir is the launcher's clone of the upstream
	// playbooks repo. The strategies MCP loads YAML from here as the
	// "plugin" set; the editor's "push as PR" flow commits + pushes
	// from it.
	UpstreamPlaybooksDir string `yaml:"upstream_playbooks_dir"`

	// SystemPlaybooksDir is where the launcher extracts its embedded
	// system playbooks at startup. Re-extracted every boot; treated
	// as a one-way materialisation.
	SystemPlaybooksDir string `yaml:"system_playbooks_dir"`

	// UserPlaybooksDir holds operator-customised playbooks the editor
	// writes to. Layered on top of the system + plugin sets at load
	// time.
	UserPlaybooksDir string `yaml:"user_playbooks_dir"`

	// WikiDir is the launcher's clone of the upstream wiki repo.
	// "Promote to wiki" commits + pushes from here.
	WikiDir string `yaml:"wiki_dir"`

	// SessionsRoot is the parent dir under which per-investigation
	// session dirs are created (transcripts, mcp.json, etc.).
	SessionsRoot string `yaml:"sessions_root"`

	// UpstreamSessionsDir is the launcher's clone of the upstream
	// investigation-sessions repo. Empty disables the sessions feature.
	UpstreamSessionsDir string `yaml:"upstream_sessions_dir"`

	// SessionsProposalsDir is where session.md drafts land before
	// they're pushed.
	SessionsProposalsDir string `yaml:"sessions_proposals_dir"`

	// CodefixProposalsDir holds codefix proposal payloads + the
	// discard-ledger.
	CodefixProposalsDir string `yaml:"codefix_proposals_dir"`

	// WikiProposalsDir holds in-flight wiki drafts shared between the
	// MCP wiki server and the launcher backend.
	WikiProposalsDir string `yaml:"wiki_proposals_dir"`

	// GitCacheDir is forwarded to each triagent-mcp git server. Holds
	// per-repo clones used for architecture summaries / draft_pr.
	GitCacheDir string `yaml:"git_cache_dir"`

	// UserReposFile is the per-machine YAML the manage-repos UI
	// writes additions to. The launcher reads it at startup to
	// reconstruct the linked-repos list.
	UserReposFile string `yaml:"user_repos_file"`

	// UserWatchesFile is the per-profile YAML the signal-watches
	// subsystem reads/writes. Per-watch ingestion data lives in a
	// `watches/<id>/` sibling of this file.
	UserWatchesFile string `yaml:"user_watches_file"`
}

// defaultPathTemplates are the in-code fallbacks applied to any Paths
// field a profile leaves empty. They mirror the layout the embedded
// default profile uses (${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/<bucket>,
// or ${XDG_CACHE_HOME}/triagent-mcp/${PROFILE_NAME}/... for caches), so
// a profile that omits the `paths:` block entirely Just Works. Without
// these, a profile with no paths block resolves every dir to "" and the
// launcher silently fails to persist anything (codefix proposals, wiki
// drafts) — the activity panel stays empty even though the underlying
// MCP tool succeeded.
var defaultPathTemplates = Paths{
	UpstreamPlaybooksDir: "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/upstream-playbooks",
	SystemPlaybooksDir:   "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/system-playbooks",
	UserPlaybooksDir:     "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/playbooks",
	WikiDir:              "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/wiki",
	SessionsRoot:         "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/sessions",
	UpstreamSessionsDir:  "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/upstream-sessions",
	SessionsProposalsDir: "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/session-proposals",
	CodefixProposalsDir:  "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/codefix-proposals",
	WikiProposalsDir:     "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/wiki-proposals",
	GitCacheDir:          "${XDG_CACHE_HOME}/triagent-mcp/${PROFILE_NAME}/git",
	UserReposFile:        "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/user_repos.yaml",
	UserWatchesFile:      "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/user_watches.yaml",
}

// Resolve returns a new Paths with ${XDG_CONFIG_HOME}, ${XDG_CACHE_HOME},
// ${HOME}, and ${PROFILE_NAME} expanded. Empty fields are filled from
// defaultPathTemplates before expansion, so a profile with no `paths:`
// block (or with only some fields set) gets the standard XDG layout.
// Absolute paths pass through unchanged.
//
// profileName isolates per-profile state when paths bake in
// ${PROFILE_NAME} (the default templates do this for every dir). A path
// referencing ${PROFILE_NAME} with profileName=="" is rejected — every
// runtime profile carries a name, so the empty case is a programming
// error worth surfacing rather than silently producing a doubled slash.
func (p Paths) Resolve(profileName string) (Paths, error) {
	p = mergePaths(defaultPathTemplates, p)
	xdgConfig, err := xdgDir("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return Paths{}, err
	}
	xdgCache, err := xdgDir("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return Paths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// HOME is required for the fallbacks above; if we got here it
		// means XDG_* was set explicitly. ${HOME} expansion below will
		// produce an empty string in that case, which the caller will
		// catch when it tries to mkdir.
		home = ""
	}
	for _, s := range []string{
		p.UpstreamPlaybooksDir, p.SystemPlaybooksDir, p.UserPlaybooksDir,
		p.WikiDir, p.SessionsRoot, p.UpstreamSessionsDir,
		p.SessionsProposalsDir, p.CodefixProposalsDir, p.WikiProposalsDir,
		p.GitCacheDir, p.UserReposFile, p.UserWatchesFile,
	} {
		if strings.Contains(s, "${PROFILE_NAME}") && profileName == "" {
			return Paths{}, fmt.Errorf("path %q references ${PROFILE_NAME} but the loaded profile has no name", s)
		}
	}
	expand := func(s string) string {
		s = strings.ReplaceAll(s, "${XDG_CONFIG_HOME}", xdgConfig)
		s = strings.ReplaceAll(s, "${XDG_CACHE_HOME}", xdgCache)
		s = strings.ReplaceAll(s, "${HOME}", home)
		s = strings.ReplaceAll(s, "${PROFILE_NAME}", profileName)
		return s
	}
	return Paths{
		UpstreamPlaybooksDir: expand(p.UpstreamPlaybooksDir),
		SystemPlaybooksDir:   expand(p.SystemPlaybooksDir),
		UserPlaybooksDir:     expand(p.UserPlaybooksDir),
		WikiDir:              expand(p.WikiDir),
		SessionsRoot:         expand(p.SessionsRoot),
		UpstreamSessionsDir:  expand(p.UpstreamSessionsDir),
		SessionsProposalsDir: expand(p.SessionsProposalsDir),
		CodefixProposalsDir:  expand(p.CodefixProposalsDir),
		WikiProposalsDir:     expand(p.WikiProposalsDir),
		GitCacheDir:          expand(p.GitCacheDir),
		UserReposFile:        expand(p.UserReposFile),
		UserWatchesFile:      expand(p.UserWatchesFile),
	}, nil
}

// mergePaths returns base with override fields applied per-field
// (override wins when non-empty). Used by applyBase so a child profile
// can override just one path without restating the rest.
func mergePaths(base, override Paths) Paths {
	pick := func(o, b string) string {
		if o != "" {
			return o
		}
		return b
	}
	return Paths{
		UpstreamPlaybooksDir: pick(override.UpstreamPlaybooksDir, base.UpstreamPlaybooksDir),
		SystemPlaybooksDir:   pick(override.SystemPlaybooksDir, base.SystemPlaybooksDir),
		UserPlaybooksDir:     pick(override.UserPlaybooksDir, base.UserPlaybooksDir),
		WikiDir:              pick(override.WikiDir, base.WikiDir),
		SessionsRoot:         pick(override.SessionsRoot, base.SessionsRoot),
		UpstreamSessionsDir:  pick(override.UpstreamSessionsDir, base.UpstreamSessionsDir),
		SessionsProposalsDir: pick(override.SessionsProposalsDir, base.SessionsProposalsDir),
		CodefixProposalsDir:  pick(override.CodefixProposalsDir, base.CodefixProposalsDir),
		WikiProposalsDir:     pick(override.WikiProposalsDir, base.WikiProposalsDir),
		GitCacheDir:          pick(override.GitCacheDir, base.GitCacheDir),
		UserReposFile:        pick(override.UserReposFile, base.UserReposFile),
		UserWatchesFile:      pick(override.UserWatchesFile, base.UserWatchesFile),
	}
}

func xdgDir(envVar, homeSubdir string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir for %s fallback: %w", envVar, err)
	}
	return filepath.Join(home, homeSubdir), nil
}
