package profile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sourcehawk/triagent/internal/profile"
)

const minimalYAML = `
name: example
description: test profile
auth:
  kind: teleport
  teleport:
    proxy: example.teleport.sh
    auth_connector: okta
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: org/playbooks
  playbooks_path: pb
  wiki_repo: org/wiki
  wiki_path: w
  sessions_repo: org/sessions
  sessions_path: s
  prometheus:
    service: default-prom
    namespace: monitoring
    port: 9090
slack:
  channel_prefix: inc-
linked_repos:
  - owner: org
    name: repo
    alias: repo
    description: example
extra_mcps:
  - alias: org-docs
    description: docs MCP
  - alias: prom-spawn
    description: spawn-mode example
    command: triagent-mcp
    args: ["serve", "--kind=prom"]
    env:
      FOO_URL: "${env:FOO_URL}"
    allowed_tools: ["mcp__prom-spawn__cpu_pressure"]
investigation_inputs:
  - id: cluster_id
    label: Cluster
    type: cluster_id
    optional: true
`

func TestParse(t *testing.T) {
	p, err := profile.Parse(strings.NewReader(minimalYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "example" {
		t.Errorf("Name=%q, want example", p.Name)
	}
	if p.Auth.Kind != "teleport" {
		t.Errorf("Auth.Kind=%q, want teleport", p.Auth.Kind)
	}
	if p.Auth.Teleport.Proxy != "example.teleport.sh" {
		t.Errorf("Auth.Teleport.Proxy=%q", p.Auth.Teleport.Proxy)
	}
	if p.Playbooks.Entrypoint != "investigation" {
		t.Errorf("Playbooks.Entrypoint=%q", p.Playbooks.Entrypoint)
	}
	if p.Defaults.Prometheus.Port != 9090 {
		t.Errorf("Defaults.Prometheus.Port=%d", p.Defaults.Prometheus.Port)
	}
	if p.Defaults.PlaybooksPath != "pb" {
		t.Errorf("Defaults.PlaybooksPath=%q, want pb", p.Defaults.PlaybooksPath)
	}
	if p.Defaults.WikiPath != "w" {
		t.Errorf("Defaults.WikiPath=%q, want w", p.Defaults.WikiPath)
	}
	if p.Defaults.SessionsPath != "s" {
		t.Errorf("Defaults.SessionsPath=%q, want s", p.Defaults.SessionsPath)
	}
	if p.Slack.ChannelPrefix != "inc-" {
		t.Errorf("Slack.ChannelPrefix=%q", p.Slack.ChannelPrefix)
	}
	if len(p.LinkedRepos) != 1 || p.LinkedRepos[0].Owner != "org" {
		t.Errorf("LinkedRepos parsing wrong: %+v", p.LinkedRepos)
	}
	if len(p.ExtraMCPs) != 2 {
		t.Fatalf("expected 2 ExtraMCPs, got %d", len(p.ExtraMCPs))
	}
	if p.ExtraMCPs[0].Alias != "org-docs" {
		t.Errorf("reference mode alias wrong: %+v", p.ExtraMCPs[0])
	}
	if p.ExtraMCPs[1].Alias != "prom-spawn" || p.ExtraMCPs[1].Command != "triagent-mcp" {
		t.Errorf("spawn mode parsing wrong: %+v", p.ExtraMCPs[1])
	}
	if len(p.ExtraMCPs[1].Args) != 2 || p.ExtraMCPs[1].Args[1] != "--kind=prom" {
		t.Errorf("spawn mode args wrong: %+v", p.ExtraMCPs[1])
	}
	if p.ExtraMCPs[1].Env["FOO_URL"] != "${env:FOO_URL}" {
		t.Errorf("spawn mode env wrong: %+v", p.ExtraMCPs[1].Env)
	}
	if len(p.ExtraMCPs[1].AllowedTools) != 1 {
		t.Errorf("allowed_tools wrong: %+v", p.ExtraMCPs[1].AllowedTools)
	}
	if len(p.InvestigationInputs) != 1 || p.InvestigationInputs[0].ID != "cluster_id" {
		t.Errorf("InvestigationInputs parsing wrong: %+v", p.InvestigationInputs)
	}
}

func TestLoadEmbeddedDefault(t *testing.T) {
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatalf("LoadEmbedded default: %v", err)
	}
	if p.Name != "default" {
		t.Errorf("Name=%q, want default", p.Name)
	}
}

func TestLoadEmbeddedUnknown(t *testing.T) {
	_, err := profile.LoadEmbedded("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestLoadPath(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: ext
description: external test
auth:
  kind: teleport
  teleport:
    proxy: ext.teleport.sh
    auth_connector: okta
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  prometheus:
    port: 9090
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "system.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := profile.LoadPath(dir)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if p.Name != "ext" {
		t.Errorf("Name=%q", p.Name)
	}
	if p.Prompts["system.md"] != "hello" {
		t.Errorf("Prompts[system.md]=%q", p.Prompts["system.md"])
	}
}

func TestLoadDispatchesPathVsName(t *testing.T) {
	p, err := profile.Load("default")
	if err != nil {
		t.Fatalf("Load(default): %v", err)
	}
	if p.Name != "default" {
		t.Errorf("Name=%q", p.Name)
	}
	// Path forms: contains "/" or starts with ".".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"),
		[]byte("name: x\nauth:\n  kind: teleport\nplaybooks:\n  entrypoint: a\n  closing: b\ndefaults:\n  prometheus:\n    port: 9090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p2, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load(path): %v", err)
	}
	if p2.Name != "x" {
		t.Errorf("path-load Name=%q", p2.Name)
	}
}

func TestValidateOK(t *testing.T) {
	p := &profile.Profile{
		Name:      "test",
		Auth:      profile.Auth{Kind: "teleport", Teleport: profile.TeleportConfig{Proxy: "proxy.example.com", AuthConnector: "okta"}},
		Playbooks: profile.Playbooks{Entrypoint: "investigation", Closing: "capture_offer"},
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateUnknownAuthKind(t *testing.T) {
	p := &profile.Profile{Name: "x", Auth: profile.Auth{Kind: "magic"}, Playbooks: profile.Playbooks{Entrypoint: "a", Closing: "b"}}
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "auth.kind") {
		t.Fatalf("want auth.kind error, got %v", err)
	}
}

func TestDefaultProfileValidates(t *testing.T) {
	// The default profile ships with auth.kind: kubeconfig and is runnable
	// out of the box on any machine with a working kubectl. It must pass
	// validation without any required fields missing.
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatalf("LoadEmbedded default: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("default profile must validate clean, got: %v", err)
	}
}

func TestValidateMissingTeleportFields(t *testing.T) {
	p := &profile.Profile{
		Name:      "x",
		Auth:      profile.Auth{Kind: "teleport"},
		Playbooks: profile.Playbooks{Entrypoint: "a", Closing: "b"},
	}
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "teleport.proxy") {
		t.Fatalf("want teleport.proxy error, got %v", err)
	}
}

func validCloudBase() *profile.Profile {
	return &profile.Profile{
		Name:      "x",
		Auth:      profile.Auth{Kind: "kubeconfig"},
		Playbooks: profile.Playbooks{Entrypoint: "a", Closing: "b"},
	}
}

func TestValidateCloudSourcesOK(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Alias: "prod-gcp", Provider: "gcp", AssumedIdentity: "ro@proj.iam.gserviceaccount.com"},
		{Alias: "prod-aws", Provider: "aws", AssumedIdentity: "arn:aws:iam::1:role/ro", Profile: "ro"},
	}
	assert.NoError(t, p.Validate(), "a valid multi-source cloud profile must validate clean")
}

func TestValidateCloudDuplicateAlias(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Alias: "dup", Provider: "gcp", AssumedIdentity: "ro@proj.iam.gserviceaccount.com"},
		{Alias: "dup", Provider: "aws", AssumedIdentity: "arn:aws:iam::1:role/ro", Profile: "ro"},
	}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
	assert.Contains(t, err.Error(), "dup")
}

func TestValidateCloudEmptyAlias(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Provider: "gcp", AssumedIdentity: "ro@proj.iam.gserviceaccount.com"},
	}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alias")
}

func TestValidateCloudUnknownProvider(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Alias: "x", Provider: "azure", AssumedIdentity: "whatever"},
	}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
	assert.Contains(t, err.Error(), "azure")
}

func TestValidateCloudMissingIdentity(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Alias: "x", Provider: "gcp"},
	}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assumed_identity")
}

func TestValidateCloudAWSMissingProfile(t *testing.T) {
	p := validCloudBase()
	p.Cloud = []profile.CloudSource{
		{Alias: "x", Provider: "aws", AssumedIdentity: "arn:aws:iam::1:role/ro"},
	}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profile")
}

func TestDefaultProfilePromptsPopulated(t *testing.T) {
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"system.md", "architecture.md", "strategies.md", "wiki_editor.md", "editor.md"} {
		body := p.Prompts[name]
		if body == "" {
			t.Errorf("Prompts[%q] empty", name)
			continue
		}
		// Default profile ships real, ready-to-use content. Leftover "TODO"
		// markers would mean someone reverted a prompt to a stub.
		if strings.Contains(body, "TODO") {
			t.Errorf("Prompts[%q] contains TODO marker — default profile must ship real content, not a stub", name)
		}
		// Real content is meaningfully longer than a placeholder. The
		// shortest real prompt today is architecture.md at ~1.5 KiB; 400
		// bytes is a generous floor that still catches accidental reverts.
		if len(body) < 400 {
			t.Errorf("Prompts[%q] too short (%d bytes) — likely a reverted stub", name, len(body))
		}
	}
}

func TestBaseMerge(t *testing.T) {
	// Child merges on top of the embedded "default" base. The default profile
	// has auth.kind=kubeconfig and empty defaults; the child overrides
	// defaults.playbooks_repo and description.
	dir := t.TempDir()
	yaml := `base: default
name: child
description: child overrides only
defaults:
  playbooks_repo: child/playbooks
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if p.Name != "child" {
		t.Errorf("Name=%q", p.Name)
	}
	// Description comes from child (non-zero → override wins).
	if p.Description != "child overrides only" {
		t.Errorf("Description=%q", p.Description)
	}
	// Override field replaces the (empty) base value.
	if p.Defaults.PlaybooksRepo != "child/playbooks" {
		t.Errorf("Defaults.PlaybooksRepo=%q (expected override)", p.Defaults.PlaybooksRepo)
	}
	// Playbooks entrypoint falls through from base.
	if p.Playbooks.Entrypoint != "investigation" {
		t.Errorf("Playbooks.Entrypoint=%q (expected base inherited)", p.Playbooks.Entrypoint)
	}
}

func TestDefaultProfileLinkedReposEmpty(t *testing.T) {
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatal(err)
	}
	// The default starter profile ships with no linked repos — operators
	// add their own via profile.yaml overrides.
	if len(p.LinkedRepos) != 0 {
		t.Errorf("default profile linked_repos not empty: %+v", p.LinkedRepos)
	}
}

func TestDefaultProfileSubpaths(t *testing.T) {
	// The default profile ships the conventional subpaths so an operator
	// can point all three *_repo defaults at one shared upstream and have
	// playbooks/wikis/sessions land under distinct top-level subdirs out
	// of the box.
	p, err := profile.LoadEmbedded("default")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Defaults.PlaybooksPath, "playbooks"; got != want {
		t.Errorf("Defaults.PlaybooksPath=%q, want %q", got, want)
	}
	if got, want := p.Defaults.WikiPath, "wikis"; got != want {
		t.Errorf("Defaults.WikiPath=%q, want %q", got, want)
	}
	if got, want := p.Defaults.SessionsPath, "sessions"; got != want {
		t.Errorf("Defaults.SessionsPath=%q, want %q", got, want)
	}
}

func TestBaseMergeRootSentinelClearsSubpath(t *testing.T) {
	// `playbooks_path: /` in a child profile means "use the repo root,
	// even though base ships a subpath". The literal `""` cannot carry
	// that intent through applyBase (it's indistinguishable from
	// "field absent → fall back to base"); `/` survives the merge and
	// is normalized to "" before runtime sees it.
	dir := t.TempDir()
	yaml := `base: default
name: flat-repos
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: org/playbooks
  playbooks_path: /
  wiki_repo: org/wiki
  wiki_path: /
  sessions_repo: org/sessions
  sessions_path: /
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Defaults.PlaybooksPath != "" {
		t.Errorf("PlaybooksPath=%q (want '' after `/` normalization)", p.Defaults.PlaybooksPath)
	}
	if p.Defaults.WikiPath != "" {
		t.Errorf("WikiPath=%q (want '')", p.Defaults.WikiPath)
	}
	if p.Defaults.SessionsPath != "" {
		t.Errorf("SessionsPath=%q (want '')", p.Defaults.SessionsPath)
	}
	// The repo fields the child set should also win.
	if p.Defaults.PlaybooksRepo != "org/playbooks" {
		t.Errorf("PlaybooksRepo=%q", p.Defaults.PlaybooksRepo)
	}
}

func TestBaseMergeInheritsSubpaths(t *testing.T) {
	// A child profile that doesn't restate the *_path defaults still gets
	// them from the embedded default base — so an operator who only wants
	// to set their repo slugs doesn't have to copy the subpath defaults.
	dir := t.TempDir()
	yaml := `base: default
name: child
defaults:
  playbooks_repo: child/shared
  wiki_repo: child/shared
  sessions_repo: child/shared
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Defaults.PlaybooksPath != "playbooks" {
		t.Errorf("Defaults.PlaybooksPath=%q (want inherited 'playbooks')", p.Defaults.PlaybooksPath)
	}
	if p.Defaults.WikiPath != "wikis" {
		t.Errorf("Defaults.WikiPath=%q (want inherited 'wikis')", p.Defaults.WikiPath)
	}
	if p.Defaults.SessionsPath != "sessions" {
		t.Errorf("Defaults.SessionsPath=%q (want inherited 'sessions')", p.Defaults.SessionsPath)
	}
}

func TestLoadPath_AcceptsYAMLFileRef(t *testing.T) {
	// LoadPath accepts a path to a yaml file (any name), not just a
	// directory containing profile.yaml. The yaml's siblings are
	// resolved against the file's parent dir.
	dir := t.TempDir()
	yaml := `name: from-file
description: loaded by file path
base: default
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
`
	yamlPath := filepath.Join(dir, "camunda.yaml")
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(yamlPath)
	if err != nil {
		t.Fatalf("Load(yaml file): %v", err)
	}
	if p.Name != "from-file" {
		t.Errorf("Name=%q", p.Name)
	}
	// Base merge still works.
	if p.Defaults.PlaybooksPath != "playbooks" {
		t.Errorf("Defaults.PlaybooksPath=%q (want inherited)", p.Defaults.PlaybooksPath)
	}
}

func TestLoadPath_PromptFilesAndKindsFile(t *testing.T) {
	// `prompt_files` and `kinds_file` in profile.yaml let operators keep
	// a flat profile dir: arch.md and kinds.json live next to the yaml,
	// no `prompts/` or `k8s/` subdir required.
	dir := t.TempDir()
	yaml := `name: flat
base: default
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
prompt_files:
  architecture.md: arch.md
kinds_file: kinds.json
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	const archBody = "# custom architecture\nThis cluster runs Foo."
	if err := os.WriteFile(filepath.Join(dir, "arch.md"), []byte(archBody), 0o600); err != nil {
		t.Fatal(err)
	}
	const kindsBody = `{"kinds":[{"group":"","version":"v1","kind":"Pod","description":"test"}]}`
	if err := os.WriteFile(filepath.Join(dir, "kinds.json"), []byte(kindsBody), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p.Prompts["architecture.md"]; got != archBody {
		t.Errorf("Prompts[architecture.md]=%q, want %q", got, archBody)
	}
	// Other prompts still fall back to the base.
	if p.Prompts["system.md"] == "" {
		t.Errorf("Prompts[system.md] empty — base fallback should fill it")
	}
	if p.KindsPath != filepath.Join(dir, "kinds.json") {
		t.Errorf("KindsPath=%q, want %q", p.KindsPath, filepath.Join(dir, "kinds.json"))
	}
}

func TestLoadPath_KindsPathAbsoluteFromRelativeRef(t *testing.T) {
	// triagent-mcp subprocesses run with a session-scoped cwd, so a
	// relative KindsPath bakes a path that can't be opened from the
	// child. Load must absolutize KindsPath regardless of whether the
	// profile ref was relative.
	root := t.TempDir()
	profDir := filepath.Join(root, "test-profile", "camunda")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `name: camunda
base: default
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
kinds_file: kinds.json
`
	if err := os.WriteFile(filepath.Join(profDir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	kindsBody := `{"kinds":[{"group":"","version":"v1","kind":"Pod","description":"test"}]}`
	if err := os.WriteFile(filepath.Join(profDir, "kinds.json"), []byte(kindsBody), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	p, err := profile.Load("test-profile/camunda/profile.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(p.KindsPath) {
		t.Errorf("KindsPath=%q must be absolute so subprocess cwd doesn't matter", p.KindsPath)
	}
	if _, err := os.Stat(p.KindsPath); err != nil {
		t.Errorf("KindsPath=%q must point to a real file: %v", p.KindsPath, err)
	}
}

func TestLoadPath_CommandAllowlistPathAbsoluteFromRelativeRef(t *testing.T) {
	// command_allowlist_path is documented as relative to profile.yaml, but the
	// cloud MCP subprocess os.ReadFiles it against a session-scoped cwd. Load
	// must absolutize a relative override so the injected env points at the file
	// regardless of the child's cwd; an absolute override passes through.
	root := t.TempDir()
	profDir := filepath.Join(root, "test-profile", "camunda")
	require.NoError(t, os.MkdirAll(profDir, 0o755))
	yaml := `name: camunda
base: default
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
cloud:
  - alias: prod-gcp
    provider: gcp
    assumed_identity: ro@proj.iam.gserviceaccount.com
    command_allowlist_path: allow/gcp.json
  - alias: prod-aws
    provider: aws
    assumed_identity: arn:aws:iam::111122223333:role/ro
    profile: ro
    command_allowlist_path: /etc/triagent/aws-allow.json
`
	require.NoError(t, os.WriteFile(filepath.Join(profDir, "profile.yaml"), []byte(yaml), 0o600))

	t.Chdir(root)
	p, err := profile.Load("test-profile/camunda/profile.yaml")
	require.NoError(t, err)
	require.Len(t, p.Cloud, 2)

	assert.Equal(t, filepath.Join(profDir, "allow", "gcp.json"), p.Cloud[0].CommandAllowlistPath,
		"a relative command_allowlist_path resolves against the profile dir")
	assert.True(t, filepath.IsAbs(p.Cloud[0].CommandAllowlistPath))
	assert.Equal(t, "/etc/triagent/aws-allow.json", p.Cloud[1].CommandAllowlistPath,
		"an absolute command_allowlist_path passes through unchanged")
}

func TestLoadPath_KindsFileMissingErrors(t *testing.T) {
	// Declaring a kinds_file that doesn't exist on disk is a hard error,
	// not a silent skip — operators should know their override didn't
	// take effect.
	dir := t.TempDir()
	yaml := `name: bad
base: default
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
kinds_file: missing.json
`
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := profile.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing kinds_file, got nil")
	}
	if !strings.Contains(err.Error(), "kinds_file") {
		t.Errorf("error %q doesn't mention kinds_file", err.Error())
	}
}

func TestDefaultProfileHasNoEmbeddedKinds(t *testing.T) {
	data, err := profile.EmbeddedKinds("default")
	if err != nil {
		t.Fatal(err)
	}
	// Default profile ships no k8s/kinds.json — the k8s MCP uses its
	// trimmed baseline list.
	if data != nil {
		t.Errorf("expected nil kinds for default profile, got %d bytes", len(data))
	}
}

func TestParse_NamespaceDerivation_Template(t *testing.T) {
	t.Parallel()
	src := `
name: t
description: t
auth:
  kind: none
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: ""
  wiki_repo: ""
  sessions_repo: ""
slack:
  channel_prefix: ""
linked_repos: []
extra_mcps: []
investigation_inputs: []
namespace_derivation:
  template: "${project_id}-zeebe"
`
	p, err := profile.Parse(strings.NewReader(src))
	require.NoError(t, err)
	assert.Equal(t, "${project_id}-zeebe", p.NamespaceDerivation.Template)
}

func TestParse_NamespaceDerivation_Rules(t *testing.T) {
	t.Parallel()
	src := `
name: t
description: t
auth:
  kind: none
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: ""
  wiki_repo: ""
  sessions_repo: ""
slack:
  channel_prefix: ""
linked_repos: []
extra_mcps: []
investigation_inputs: []
namespace_derivation:
  rules:
    - when: "${alert_kind} == 'X'"
      template: "x-system"
    - when: "${project_id} != ''"
      template: "${project_id}-zeebe"
`
	p, err := profile.Parse(strings.NewReader(src))
	require.NoError(t, err)
	require.Len(t, p.NamespaceDerivation.Rules, 2)
	assert.Equal(t, "${alert_kind} == 'X'", p.NamespaceDerivation.Rules[0].When)
	assert.Equal(t, "x-system", p.NamespaceDerivation.Rules[0].Template)
}

func TestParse_ModelsBlock_ExplicitOverrides(t *testing.T) {
	t.Parallel()
	src := `
name: t
description: t
auth:
  kind: none
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: ""
  wiki_repo: ""
  sessions_repo: ""
slack:
  channel_prefix: ""
linked_repos: []
extra_mcps: []
investigation_inputs: []
models:
  investigation: claude-opus-4-7
  subagent: claude-sonnet-4-6
`
	p, err := profile.Parse(strings.NewReader(src))
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-7", p.Models.Investigation)
	assert.Equal(t, "claude-sonnet-4-6", p.Models.Subagent)
}

func TestProfile_ApplyDefaults_FillsModelsWhenAbsent(t *testing.T) {
	t.Parallel()
	p := &profile.Profile{}
	p.ApplyDefaults()
	assert.Equal(t, "claude-sonnet-4-6", p.Models.Investigation)
	assert.Equal(t, "claude-haiku-4-5-20251001", p.Models.Subagent)
}

func TestProfile_ApplyDefaults_PreservesExplicitModels(t *testing.T) {
	t.Parallel()
	p := &profile.Profile{Models: profile.Models{Investigation: "x", Subagent: "y"}}
	p.ApplyDefaults()
	assert.Equal(t, "x", p.Models.Investigation)
	assert.Equal(t, "y", p.Models.Subagent)
}

func TestProfile_ParsesCloudBlock(t *testing.T) {
	t.Parallel()
	src := `
name: example
description: test profile
auth:
  kind: kubeconfig
cloud:
  - alias: prod-gcp
    provider: gcp
    assumed_identity: triage-ro@prod.iam.gserviceaccount.com
    scope:
      projects:
        - prod-a
        - prod-b
    command_allowlist_path: /etc/triagent/gcp-allow.json
  - alias: prod-aws
    provider: aws
    assumed_identity: arn:aws:iam::123456789012:role/triage-ro
    profile: triage-ro
    scope:
      regions:
        - us-east-1
`
	p, err := profile.Parse(strings.NewReader(src))
	require.NoError(t, err)
	require.Len(t, p.Cloud, 2)

	gcp := p.Cloud[0]
	assert.Equal(t, "prod-gcp", gcp.Alias)
	assert.Equal(t, "gcp", gcp.Provider)
	assert.Equal(t, "triage-ro@prod.iam.gserviceaccount.com", gcp.AssumedIdentity)
	assert.Equal(t, []string{"prod-a", "prod-b"}, gcp.Scope.Projects)
	assert.Equal(t, "/etc/triagent/gcp-allow.json", gcp.CommandAllowlistPath)

	aws := p.Cloud[1]
	assert.Equal(t, "prod-aws", aws.Alias)
	assert.Equal(t, "aws", aws.Provider)
	assert.Equal(t, "arn:aws:iam::123456789012:role/triage-ro", aws.AssumedIdentity)
	assert.Equal(t, "triage-ro", aws.Profile)
	assert.Equal(t, []string{"us-east-1"}, aws.Scope.Regions)
}
