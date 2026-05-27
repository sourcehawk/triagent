package strategies

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadPlaybooks_LocalSeed confirms LoadPlaybooks works against the
// local seed dir — the launcher's strategies MCP gets the equivalent
// path from $TRIAGENT_MCP_SYSTEM_PLAYBOOKS_DIR.
func TestLoadPlaybooks_LocalSeed(t *testing.T) {
	t.Parallel()
	books, err := LoadPlaybooks(localTestPlaybooksDir)
	require.NoError(t, err, "LoadPlaybooks")
	_, ok := books["cluster_health"]
	require.True(t, ok, "expected cluster_health among local-seed playbooks; got %v", keys(books))
}

// TestLoadPlaybooksFrom_UserOverride exercises the override semantics: a
// user file with an existing system id wins. With the single-file layout
// the user writes <id>.yaml in the type subdir; no version stamp needed.
func TestLoadPlaybooksFrom_UserOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	override := `id: cluster_health
symptom: "USER OVERRIDE — symptom"
entrypoint: only
nodes:
  only:
    description: "user-supplied entry"
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "cluster_health.yaml"), []byte(override), 0o600), "write override")

	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", dir)
	require.NoError(t, err, "LoadPlaybooksFrom")
	pb := books["cluster_health"]
	require.NotNil(t, pb, "cluster_health missing")
	assert.Equal(t, "USER OVERRIDE — symptom", pb.Symptom, "override didn't win")
	_, ok := pb.Nodes["only"]
	assert.True(t, ok, "override's only node missing")
}

// TestLoadPlaybooksFrom_DisableViaActiveFalse confirms a user file with
// active=false suppresses the plugin version — the operator's way of
// disabling a system playbook they don't want. With the single-file
// layout the user writes <id>.yaml with active=false.
func TestLoadPlaybooksFrom_DisableViaActiveFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tombstone := `id: cluster_health
active: false
symptom: "(operator-disabled)"
entrypoint: a
nodes:
  a:
    description: "tombstone"
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "cluster_health.yaml"), []byte(tombstone), 0o600), "write tombstone")
	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", dir)
	require.NoError(t, err, "LoadPlaybooksFrom")
	_, ok := books["cluster_health"]
	assert.False(t, ok, "expected cluster_health to be suppressed by user tombstone")
}

// TestLoadPlaybooksFrom_ActiveUserFileWins confirms an active user file
// wins over the plugin-tier version. With the single-file layout there
// is exactly one file per id; active=true (or absent) means the user
// version loads.
func TestLoadPlaybooksFrom_ActiveUserFileWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: cluster_health
active: true
symptom: "USER version (active)"
entrypoint: only
nodes:
  only:
    description: "user node"
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "cluster_health.yaml"), []byte(body), 0o600), "write user file")
	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", dir)
	require.NoError(t, err, "LoadPlaybooksFrom")
	pb := books["cluster_health"]
	require.NotNil(t, pb, "cluster_health missing")
	assert.Equal(t, "USER version (active)", pb.Symptom, "user file should win")
}

// TestLoadPlaybooksFrom_UserAddsNew exercises the "fresh id" path: user
// files with brand-new ids merge into the embedded set.
func TestLoadPlaybooksFrom_UserAddsNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: my_custom
symptom: "operator-local playbook"
entrypoint: a
nodes:
  a:
    description: "step one"
    next:
      - condition: "always"
        goto: b
  b:
    description: "step two"
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "my_custom.yaml"), []byte(body), 0o600), "write")
	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", dir)
	require.NoError(t, err, "LoadPlaybooksFrom")
	_, ok := books["my_custom"]
	assert.True(t, ok, "my_custom missing from merged set")
	// Embedded playbooks must still be present.
	_, ok = books["cluster_health"]
	assert.True(t, ok, "cluster_health vanished after user merge")
}

// TestLoadPlaybooksFrom_ProposalsDirIgnored confirms drafts in the
// proposals/ subdir are NOT loaded into the running set — they're only
// promoted to versioned files when the operator approves. Drafts are
// nested under proposals/<type>/ in the new layout.
func TestLoadPlaybooksFrom_ProposalsDirIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proposals", "investigation"), 0o700), "mkdir proposals")
	draft := `id: should_not_load
version: v1
symptom: "draft — must not be loaded"
entrypoint: a
nodes:
  a:
    description: "x"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "proposals", "investigation", "should_not_load__abc123.yaml"), []byte(draft), 0o600), "write draft")
	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", dir)
	require.NoError(t, err, "LoadPlaybooksFrom")
	_, ok := books["should_not_load"]
	assert.False(t, ok, "draft from proposals/ got loaded into running set")
}

// TestLoadPlaybooksFrom_InvalidUserFile soft-skips a structurally
// broken user playbook with a stderr warning rather than failing the
// whole load. A single hand-edited junk file shouldn't take down the
// strategies MCP and block every other playbook from loading; the
// launcher's editor surfaces the broken file as a red-icon entry via
// its own user-dir walker.
func TestLoadPlaybooksFrom_InvalidUserFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := `id: broken
symptom: "x"
entrypoint: nope
nodes:
  a:
    description: "exists"
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "broken.yaml"), []byte(bad), 0o600), "write")
	books, err := LoadPlaybooksFrom("", "", dir)
	require.NoError(t, err, "load with one broken user file should not error")
	_, present := books["broken"]
	assert.False(t, present, "broken playbook should have been skipped")
}


// TestLoadPlaybooksFrom_IgnoresRepoArtefacts confirms README.md,
// LICENSE, dot-prefixed dirs (.git, .github) and other repo-management
// files at the system-dir root are skipped without error. The clone
// of the upstream playbooks repo always carries these alongside the
// type subdirs; they must not parse as playbooks or as types.
func TestLoadPlaybooksFrom_IgnoresRepoArtefacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// One real type dir with a real playbook so the loader has
	// something to load alongside the noise.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "investigation"), 0o700), "mkdir")
	yaml := `id: smoke
schema_version: 1
symptom: "smoke"
entrypoint: a
nodes:
  a:
    description: "x"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "investigation", "smoke.yaml"), []byte(yaml), 0o600), "write smoke")
	// Repo-management files + dot-dirs that should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# upstream\n"), 0o600), "write README")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("MIT\n"), 0o600), "write LICENSE")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp\n"), 0o600), "write .gitignore")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700), "mkdir .git")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o700), "mkdir .github")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("name: ci\n"), 0o600), "write workflow")

	books, err := LoadPlaybooks(dir)
	require.NoError(t, err, "LoadPlaybooks")
	_, ok := books["smoke"]
	require.True(t, ok, "expected smoke to load alongside the repo artefacts; got %v", keys(books))
	assert.Len(t, books, 1, "expected exactly 1 playbook; repo artefacts may have leaked in (got %v)", keys(books))

	types, err := LoadPlaybookTypes(dir)
	require.NoError(t, err, "LoadPlaybookTypes")
	require.Len(t, types, 1, "expected only the investigation type, got %+v", types)
	assert.Equal(t, "investigation", types[0].Name, "expected only the investigation type, got %+v", types)

	raw, err := SystemRawPlaybooks(dir)
	require.NoError(t, err, "SystemRawPlaybooks")
	_, ok = raw["smoke"]
	assert.True(t, ok, "expected smoke in raw playbooks")
	assert.Len(t, raw, 1, "expected only smoke in raw playbooks")
}

// TestLoadPlaybooksFrom_MissingUserDir is silent — fresh installs
// have no user playbooks dir yet and that shouldn't break startup.
// The system dir IS required to exist (the launcher manages the
// clone), so this only exercises the user-dir branch.
func TestLoadPlaybooksFrom_MissingUserDir(t *testing.T) {
	t.Parallel()
	books, err := LoadPlaybooksFrom(localTestPlaybooksDir, "", "/nonexistent/path/somewhere")
	require.NoError(t, err, "missing user dir should be silent")
	_, ok := books["cluster_health"]
	assert.True(t, ok, "system set should still load when user dir is missing")
}

// TestParseAndValidatePlaybookYAML covers the standalone validator the
// `triagent-mcp validate-playbook` subcommand wraps. Used by the launcher to
// check a user playbook before persisting it.
func TestParseAndValidatePlaybookYAML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		yaml      string
		wantOK    bool
		wantSubst []string
	}{
		{
			name: "ok",
			yaml: `id: x
symptom: "s"
entrypoint: a
nodes:
  a:
    description: "first"
    next:
      - condition: "always"
        goto: b
  b:
    description: "second"
`,
			wantOK: true,
		},
		{
			name: "missing symptom",
			yaml: `id: x
entrypoint: a
nodes:
  a:
    description: "x"
`,
			wantOK:    false,
			wantSubst: []string{"missing symptom"},
		},
		{
			name: "dangling goto",
			yaml: `id: x
symptom: "s"
entrypoint: a
nodes:
  a:
    description: "x"
    next:
      - condition: "c"
        goto: nowhere
`,
			wantOK:    false,
			wantSubst: []string{"goto", "does not resolve"},
		},
		{
			name: "node missing description",
			yaml: `id: x
symptom: "s"
entrypoint: a
nodes:
  a:
    description: ""
`,
			wantOK:    false,
			wantSubst: []string{"empty description"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := ParseAndValidatePlaybookYAML([]byte(tc.yaml))
			if tc.wantOK {
				assert.Empty(t, errs, "expected ok")
				return
			}
			require.NotEmpty(t, errs, "expected errors")
			joined := strings.Join(errs, "|")
			for _, want := range tc.wantSubst {
				assert.Contains(t, joined, want, "expected substring %q in errs: %v", want, errs)
			}
		})
	}
}

// TestWriteUserPlaybook_Overwrites confirms the new save path
// overwrites <type>/<id>.yaml in place — no v1/v2/vN bumping.
// Source-of-truth for the bug-fix-by-replacement model.
func TestWriteUserPlaybook_Overwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: backup
schema_version: 1
symptom: "first"
entrypoint: a
nodes:
  a:
    description: "step"
`
	errs, err := WriteUserPlaybook(dir, "investigation", "backup", body, true)
	require.NoError(t, err)
	require.Empty(t, errs)
	target := filepath.Join(dir, "investigation", "backup.yaml")
	first, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(first), "active: true")
	assert.Contains(t, string(first), "symptom: first")

	// Re-save with a different body — same file gets overwritten.
	body2 := strings.Replace(body, `"first"`, `"second"`, 1)
	errs, err = WriteUserPlaybook(dir, "investigation", "backup", body2, true)
	require.NoError(t, err)
	require.Empty(t, errs)
	second, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(second), "symptom: second")
	assert.NotContains(t, string(second), "symptom: first")
	// No siblings created.
	entries, _ := os.ReadDir(filepath.Join(dir, "investigation"))
	assert.Len(t, entries, 1, "expected exactly backup.yaml; got %v", entries)
}

// TestRenderPlaybookYAML_OmitsEmptyVersion prevents future regressions
// of the version-stripping invariant.
func TestRenderPlaybookYAML_OmitsEmptyVersion(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "x",
		Symptom:    "s",
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {Description: "step"},
		},
	}
	out, err := renderPlaybookYAML(pb)
	require.NoError(t, err)
	// Use "\nversion:" (line-start anchor) to avoid false-matching
	// "schema_version:" — consistent with TestWriteUserPlaybook_StripsVersion.
	// SchemaVersion is unset (0) so schema_version: is also omitted here,
	// but the anchor is retained for safety against future fixture changes.
	assert.NotContains(t, out, "\nversion:", "empty version should not be rendered")
}

// TestWriteUserPlaybook_StripsVersion drops the legacy version field
// from the body so upstream PRs and local saves stop carrying it.
func TestWriteUserPlaybook_StripsVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: backup
version: v3
schema_version: 1
symptom: "x"
entrypoint: a
nodes:
  a:
    description: "step"
`
	errs, err := WriteUserPlaybook(dir, "investigation", "backup", body, false)
	require.NoError(t, err)
	require.Empty(t, errs)
	out, _ := os.ReadFile(filepath.Join(dir, "investigation", "backup.yaml"))
	// Use "\nversion:" (line-start anchor) to avoid matching "schema_version:".
	assert.NotContains(t, string(out), "\nversion:", "version stamp must be stripped")
	assert.Contains(t, string(out), "active: false")
}

// TestWriteUserPlaybook_StampsActive overrides whatever active flag
// was in the body — the activate parameter is the source of truth.
func TestWriteUserPlaybook_StampsActive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: backup
active: false
schema_version: 1
symptom: "x"
entrypoint: a
nodes:
  a:
    description: "step"
`
	errs, err := WriteUserPlaybook(dir, "investigation", "backup", body, true)
	require.NoError(t, err)
	require.Empty(t, errs)
	out, _ := os.ReadFile(filepath.Join(dir, "investigation", "backup.yaml"))
	assert.Contains(t, string(out), "active: true",
		"activate=true should override the body's active: false")
}

// TestWriteUserPlaybook_RejectsInvalid confirms validation errors
// surface as the validationErrs return without writing the file.
func TestWriteUserPlaybook_RejectsInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `id: backup
symptom: ""
entrypoint: a
nodes:
  a:
    description: ""
`
	errs, err := WriteUserPlaybook(dir, "investigation", "backup", body, true)
	require.NoError(t, err)
	require.NotEmpty(t, errs, "expected validation errors")
	joined := strings.Join(errs, "|")
	assert.Contains(t, joined, "missing symptom",
		"expected the symptom-empty validator to fire; got %v", errs)
	_, statErr := os.Stat(filepath.Join(dir, "investigation", "backup.yaml"))
	assert.True(t, errors.Is(statErr, fs.ErrNotExist),
		"file must not be written when validation fails")
}

func TestValidate_DelegateTo_RequiresNext(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "delegate without next"
    delegate_to: child
  b:
    description: "fallback terminal"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.NotEmpty(t, errs, "expected validation error")
	joined := strings.Join(errs, "; ")
	assert.Contains(t, joined, "delegate_to", "error should name delegate_to")
	assert.Contains(t, joined, "next", "error should mention missing next")
}

func TestValidate_DelegateTo_RejectsTerminalAdvice(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "delegate + terminal — illegal"
    delegate_to: child
    terminal_advice: "nope"
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.NotEmpty(t, errs)
	joined := strings.Join(errs, "; ")
	assert.Contains(t, joined, "delegate_to")
	assert.Contains(t, joined, "terminal_advice", "error should name the conflicting terminal_advice field")
}

func TestValidate_DelegateTo_RejectsHandoff(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "delegate + handoff — illegal"
    delegate_to: child
    handoff: [other]
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.NotEmpty(t, errs)
	joined := strings.Join(errs, "; ")
	assert.Contains(t, joined, "delegate_to")
	assert.Contains(t, joined, "handoff", "error should name the conflicting handoff field")
}

func TestValidate_DelegateTo_RejectsSuggestedCalls(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "delegate + tools — illegal"
    delegate_to: child
    suggested_calls:
      - tool: triagent-k8s/list_resources
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.NotEmpty(t, errs)
	joined := strings.Join(errs, "; ")
	assert.Contains(t, joined, "delegate_to")
	assert.Contains(t, joined, "suggested_calls", "error should name the conflicting suggested_calls field")
}

func TestValidate_DelegateTo_RejectsBadIDPattern(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "bad id — has spaces"
    delegate_to: "bad id"
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.NotEmpty(t, errs)
	assert.Contains(t, strings.Join(errs, "; "), "delegate_to")
}

func TestValidate_DelegateTo_AcceptsValid(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
id: parent
schema_version: 1
symptom: "test"
entrypoint: a
nodes:
  a:
    description: "delegate to child, then continue to b"
    delegate_to: child
    next:
      - condition: "always"
        goto: b
  b:
    description: "term"
    terminal_advice: "done"
`)
	_, errs := ParseAndValidatePlaybookYAML(yaml)
	require.Empty(t, errs, "expected no validation errors: %v", errs)
}

// TestEmbeddedSystemPlaybooks_Validate runs full structural validation
// against every YAML file shipped via system, then checks
// that every cross-playbook id (delegate_to / handoff) resolves to
// another file in the embedded set. Catches dangling gotos, missing
// entrypoints, unreachable nodes, AND typo'd cross-playbook references
// at CI time rather than at launcher startup.
//
// Reads the files via relative path because the system playbooks are
// bundled under system/ at the repo root — we read them via relative
// path from the test file. The on-disk layout is stable within the
// single module.
func TestEmbeddedSystemPlaybooks_Validate(t *testing.T) {
	t.Parallel()
	const dir = "../../../system"
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read embedded system playbooks dir")

	loaded := map[string]*Playbook{}
	loadedFile := map[string]string{} // id → source filename for diagnostics
	fileCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		fileCount++
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		pb, errs := ParseAndValidatePlaybookYAML(data)
		assert.Empty(t, errs, "validation errors in %s: %v", name, errs)
		if pb == nil {
			continue
		}
		if prev, dup := loaded[pb.ID]; dup {
			t.Errorf("duplicate id %q across embedded system playbooks (%s and %s)", pb.ID, loadedFile[prev.ID], name)
			continue
		}
		loaded[pb.ID] = pb
		loadedFile[pb.ID] = name
	}
	require.NotZero(t, fileCount, "no embedded system playbooks found at %s", dir)

	// Cross-playbook id resolution. validateOne is single-file only —
	// "Cross-playbook id resolution happens at the editor + runtime
	// layer". For the system set we know the full graph at test time,
	// so a typo'd handoff/delegate id that would silently pass single-
	// file validation gets caught here.
	for id, pb := range loaded {
		for nodeID, node := range pb.Nodes {
			if node.DelegateTo != "" {
				if _, ok := loaded[node.DelegateTo]; !ok {
					t.Errorf("%s (file %s): node %q delegate_to %q does not resolve to any embedded system playbook", id, loadedFile[id], nodeID, node.DelegateTo)
				}
			}
			for _, h := range node.Handoff {
				if _, ok := loaded[h]; !ok {
					t.Errorf("%s (file %s): node %q handoff %q does not resolve to any embedded system playbook", id, loadedFile[id], nodeID, h)
				}
			}
		}
	}
}

func keys(m map[string]*Playbook) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestValidateOne_AcceptsEntityTags(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "x",
		Symptom:    "test",
		Entrypoint: "start",
		Nodes: map[string]Node{
			"start": {ID: "start", Description: "d", TerminalAdvice: "done"},
		},
		Services: []string{"zeebe-broker"},
		Errors:   []string{"oom-kill"},
		Symptoms: []string{"pod-restart-loop"},
	}
	errs := validateOne(pb, "test")
	require.Empty(t, errs, "well-formed tags should validate")
}

func TestValidateOne_RejectsMalformedEntityTag(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "x",
		Symptom:    "test",
		Entrypoint: "start",
		Nodes: map[string]Node{
			"start": {ID: "start", Description: "d", TerminalAdvice: "done"},
		},
		Services: []string{"Zeebe Broker"}, // spaces + uppercase
	}
	errs := validateOne(pb, "test")
	require.NotEmpty(t, errs, "expected validation error for malformed service name")
	joined := strings.Join(errs, "; ")
	assert.Contains(t, joined, "Zeebe Broker", "error should name the bad value")
	assert.Contains(t, joined, "zeebe-broker", "error should suggest the canonical form")
}

func TestValidateOne_AllowsEmptyEntityArrays(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "x",
		Symptom:    "test",
		Entrypoint: "start",
		Nodes: map[string]Node{
			"start": {ID: "start", Description: "d", TerminalAdvice: "done"},
		},
		// Services / Errors / Symptoms all nil — back-compat: untagged playbooks load fine.
	}
	errs := validateOne(pb, "test")
	require.Empty(t, errs, "untagged playbook should validate")
}

// TestSystemPlaybooks_NoDeletedToolReferences scans every system playbook YAML
// for references to tool names that have been deleted from the strategies MCP.
// Fails fast so that future additions can't accidentally reintroduce the old names.
func TestSystemPlaybooks_NoDeletedToolReferences(t *testing.T) {
	t.Parallel()
	deleted := []string{
		"triagent-strategies/record_finding",
		"triagent-strategies/advance",
	}
	// Resolve relative to the strategies package source path.
	// pkg/mcp/strategies/playbook_test.go → ../../../system
	dir := filepath.Join("..", "..", "..", "system")
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no system playbooks found at %s", dir)
	}
	for _, p := range matches {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range deleted {
			if bytes.Contains(body, []byte(needle)) {
				t.Errorf("%s: references deleted tool %q", p, needle)
			}
		}
	}
}

func TestParseAndValidatePlaybookYAML_AcceptsDispatchSubagent(t *testing.T) {
	t.Parallel()
	yaml := `id: testpb
schema_version: 1
symptom: t
dispatch: subagent
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	pb, errs := ParseAndValidatePlaybookYAML([]byte(yaml))
	require.Empty(t, errs)
	require.NotNil(t, pb)
	assert.Equal(t, DispatchSubagent, pb.Dispatch)
}

func TestParseAndValidatePlaybookYAML_RejectsUnknownDispatchValue(t *testing.T) {
	t.Parallel()
	yaml := `id: testpb
schema_version: 1
symptom: t
dispatch: bogus
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	_, errs := ParseAndValidatePlaybookYAML([]byte(yaml))
	require.NotEmpty(t, errs)
}

func TestParseAndValidatePlaybookYAML_EmptyDispatchIsDefault(t *testing.T) {
	t.Parallel()
	yaml := `id: testpb
schema_version: 1
symptom: t
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	pb, errs := ParseAndValidatePlaybookYAML([]byte(yaml))
	require.Empty(t, errs)
	assert.Equal(t, DispatchDefault, pb.Dispatch)
}
