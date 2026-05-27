package profile_test

import (
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
)

func TestParsePaths(t *testing.T) {
	yamlBody := `name: ext
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  auto: true
  offline: true
paths:
  upstream_playbooks_dir: ${XDG_CONFIG_HOME}/triagent/upstream-playbooks
  system_playbooks_dir: ${XDG_CONFIG_HOME}/triagent/system-playbooks
  user_playbooks_dir: ${XDG_CONFIG_HOME}/triagent/playbooks
  wiki_dir: ${XDG_CONFIG_HOME}/triagent/wiki
  sessions_root: ${XDG_CONFIG_HOME}/triagent/sessions
  upstream_sessions_dir: ${XDG_CONFIG_HOME}/triagent/upstream-sessions
  sessions_proposals_dir: ${XDG_CONFIG_HOME}/triagent/session-proposals
  codefix_proposals_dir: ${XDG_CONFIG_HOME}/triagent/codefix-proposals
  wiki_proposals_dir: ${XDG_CONFIG_HOME}/triagent/wiki-proposals
  git_cache_dir: ${XDG_CACHE_HOME}/triagent-mcp/git
  user_repos_file: ${XDG_CONFIG_HOME}/triagent/user_repos.yaml
`
	p, err := profile.Parse(strings.NewReader(yamlBody))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !p.Defaults.Auto {
		t.Errorf("Defaults.Auto: want true")
	}
	if !p.Defaults.Offline {
		t.Errorf("Defaults.Offline: want true")
	}
	if p.Paths.UpstreamPlaybooksDir != "${XDG_CONFIG_HOME}/triagent/upstream-playbooks" {
		t.Errorf("UpstreamPlaybooksDir=%q", p.Paths.UpstreamPlaybooksDir)
	}
	if p.Paths.GitCacheDir != "${XDG_CACHE_HOME}/triagent-mcp/git" {
		t.Errorf("GitCacheDir=%q", p.Paths.GitCacheDir)
	}
}

func TestPathsResolveExpandsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/me")

	in := profile.Paths{
		UpstreamPlaybooksDir: "${XDG_CONFIG_HOME}/triagent/upstream-playbooks",
		GitCacheDir:          "${XDG_CACHE_HOME}/triagent-mcp/git",
		WikiDir:              "${HOME}/wiki",
		UserReposFile:        "${XDG_CONFIG_HOME}/triagent/user_repos.yaml",
	}
	out, err := in.Resolve("any-profile")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.UpstreamPlaybooksDir != "/cfg/triagent/upstream-playbooks" {
		t.Errorf("UpstreamPlaybooksDir=%q", out.UpstreamPlaybooksDir)
	}
	if out.GitCacheDir != "/cache/triagent-mcp/git" {
		t.Errorf("GitCacheDir=%q", out.GitCacheDir)
	}
	if out.WikiDir != "/home/me/wiki" {
		t.Errorf("WikiDir=%q", out.WikiDir)
	}
	if out.UserReposFile != "/cfg/triagent/user_repos.yaml" {
		t.Errorf("UserReposFile=%q", out.UserReposFile)
	}
}

// Switching profiles must not contaminate state: paths using
// ${PROFILE_NAME} should expand to the active profile's name. The
// default profile bakes this token into every path so forked
// profiles get isolation without restating paths.
func TestPathsResolveExpandsProfileName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/me")

	in := profile.Paths{
		UpstreamPlaybooksDir: "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/upstream-playbooks",
		GitCacheDir:          "${XDG_CACHE_HOME}/triagent-mcp/${PROFILE_NAME}/git",
	}
	out, err := in.Resolve("camunda")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.UpstreamPlaybooksDir != "/cfg/triagent/camunda/upstream-playbooks" {
		t.Errorf("UpstreamPlaybooksDir=%q", out.UpstreamPlaybooksDir)
	}
	if out.GitCacheDir != "/cache/triagent-mcp/camunda/git" {
		t.Errorf("GitCacheDir=%q", out.GitCacheDir)
	}
}

// An empty profile name with a ${PROFILE_NAME} token is a load-time
// programming error (every profile carries a name). Surface it loudly
// instead of silently producing a path with a doubled separator.
func TestPathsResolveRequiresProfileNameWhenTokenPresent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	in := profile.Paths{
		UpstreamPlaybooksDir: "${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/upstream-playbooks",
	}
	if _, err := in.Resolve(""); err == nil {
		t.Fatal("expected error when ${PROFILE_NAME} is referenced with empty profile name")
	}
}

func TestPathsResolveFallsBackToHomeWhenXDGUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/me")

	in := profile.Paths{
		UpstreamPlaybooksDir: "${XDG_CONFIG_HOME}/triagent/upstream-playbooks",
		GitCacheDir:          "${XDG_CACHE_HOME}/triagent-mcp/git",
	}
	out, err := in.Resolve("any-profile")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.UpstreamPlaybooksDir != "/home/me/.config/triagent/upstream-playbooks" {
		t.Errorf("UpstreamPlaybooksDir=%q (XDG_CONFIG_HOME unset should fall back to $HOME/.config)", out.UpstreamPlaybooksDir)
	}
	if out.GitCacheDir != "/home/me/.cache/triagent-mcp/git" {
		t.Errorf("GitCacheDir=%q (XDG_CACHE_HOME unset should fall back to $HOME/.cache)", out.GitCacheDir)
	}
}

func TestPathsResolvePassesThroughLiteralValues(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/me")
	in := profile.Paths{
		UpstreamPlaybooksDir: "/absolute/path",
		GitCacheDir:          "",
	}
	out, err := in.Resolve("any-profile")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.UpstreamPlaybooksDir != "/absolute/path" {
		t.Errorf("absolute path mangled: %q", out.UpstreamPlaybooksDir)
	}
	// Empty inputs now resolve to the standard XDG default rather than
	// staying empty — the old "" semantic silently disabled features
	// (e.g. codefix proposal persistence) for any profile that omitted
	// the field. See TestPathsResolveFillsDefaultsWhenEmpty.
	if out.GitCacheDir != "/cache/triagent-mcp/any-profile/git" {
		t.Errorf("empty path should fall back to the standard default, got %q", out.GitCacheDir)
	}
}

// TestPathsResolveFillsDefaultsWhenEmpty: a Paths{} with no fields set —
// the shape produced when a profile YAML omits the `paths:` block entirely
// — must resolve to the standard ${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}
// layout, not to empty strings. Without this the launcher silently fails to
// persist codefix proposals, wiki drafts, etc.: writeCodefixProposal calls
// os.MkdirAll(""), the error logs to stderr, the operator never sees a
// signal that anything went wrong, and the repo activity panel stays empty
// even after a successful create_github_issue / draft_pr.
func TestPathsResolveFillsDefaultsWhenEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/me")

	out, err := profile.Paths{}.Resolve("camunda")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := profile.Paths{
		UpstreamPlaybooksDir: "/cfg/triagent/camunda/upstream-playbooks",
		SystemPlaybooksDir:   "/cfg/triagent/camunda/system-playbooks",
		UserPlaybooksDir:     "/cfg/triagent/camunda/playbooks",
		WikiDir:              "/cfg/triagent/camunda/wiki",
		SessionsRoot:         "/cfg/triagent/camunda/sessions",
		UpstreamSessionsDir:  "/cfg/triagent/camunda/upstream-sessions",
		SessionsProposalsDir: "/cfg/triagent/camunda/session-proposals",
		CodefixProposalsDir:  "/cfg/triagent/camunda/codefix-proposals",
		WikiProposalsDir:     "/cfg/triagent/camunda/wiki-proposals",
		GitCacheDir:          "/cache/triagent-mcp/camunda/git",
		UserReposFile:        "/cfg/triagent/camunda/user_repos.yaml",
	}
	if out != want {
		t.Errorf("Resolve filled-defaults mismatch:\n got:  %+v\n want: %+v", out, want)
	}
}

// TestPathsResolveExplicitOverrideWinsOverDefault: when a profile specifies
// a field, the explicit value wins over the in-code default — the fallback
// must only fill empty fields, not clobber explicit ones.
func TestPathsResolveExplicitOverrideWinsOverDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/me")

	in := profile.Paths{
		CodefixProposalsDir: "/custom/codefix",
	}
	out, err := in.Resolve("camunda")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.CodefixProposalsDir != "/custom/codefix" {
		t.Errorf("explicit override clobbered: got %q, want /custom/codefix", out.CodefixProposalsDir)
	}
	// Sibling fields still get the default.
	if out.WikiProposalsDir != "/cfg/triagent/camunda/wiki-proposals" {
		t.Errorf("sibling default missing: got %q", out.WikiProposalsDir)
	}
}

func TestBaseMergePathsAndDefaults(t *testing.T) {
	// Smoke: child profile inherits Paths and Defaults.Auto/Offline from
	// the base when the child leaves them blank.
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatalf("LoadEmbedded default: %v", err)
	}
	if p.Paths.UpstreamPlaybooksDir == "" {
		t.Fatalf("default profile is missing paths.upstream_playbooks_dir; the cleanup must populate Paths on the embedded default")
	}
	if p.Paths.GitCacheDir == "" {
		t.Fatalf("default profile is missing paths.git_cache_dir")
	}
}
