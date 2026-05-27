# Playbook Entity Correlation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `services` / `errors` / `symptoms` entity tags to playbooks plus a `playbook_correlate` MCP tool with one-hop entity lifting; wire the tool into `investigation.yaml`'s `route` node; surface "related playbooks" + entity-chip inputs in the editor.

**Architecture:** Extract the wiki's pure entity helpers into a new `mcp/internal/entities/` package shared by wiki + strategies. Add three optional top-level entity arrays to the `Playbook` struct. New tool `strategies/playbook_correlate` ranks playbooks by entity overlap, score = `3 × direct_hits + 1 × lifted_hits` (lifted = entities tagged on one-hop children via `delegate_to` / `handoff`). Result is metadata-only (id, symptom, description, type, score, match_path) — the body is only loaded when the agent picks a candidate via existing tools. Walker integration is additive (correlate first, `list_playbooks` as fallback); editor adds a related-playbooks panel and chip inputs for the new fields.

**Tech Stack:** Go (`mcp/internal/...`), YAML (`gopkg.in/yaml.v3`), MCP go-sdk, Next.js + React (`investigate/frontend/`), Vitest (node mode).

**Reference spec:** `docs/superpowers/specs/2026-05-14-playbook-correlation-design.md`.

---

## Task 1: Extract `mcp/internal/entities/` package

**Files:**
- Create: `mcp/internal/entities/entities.go`
- Create: `mcp/internal/entities/entities_test.go`
- Modify: `mcp/internal/wiki/entity_match.go` (remove the pure helpers; keep vault-specific `loadKnownEntitiesByType`)
- Modify: `mcp/internal/wiki/entity_match_test.go` (remove the moved tests)
- Modify: `mcp/internal/wiki/tool_correlate.go:71-85` (call `entities.ValidateNames` / `entities.Resolve`)
- Modify: `mcp/internal/wiki/tools_read.go:57-83` (same call-site changes)

This is a pure refactor — no behaviour change. Wiki tests must stay green.

- [ ] **Step 1: Create the new package by copying the wiki helpers, renaming for export**

Create `mcp/internal/entities/entities.go` with the contents of `mcp/internal/wiki/entity_match.go` lines 1–185 (the `EntityResolution`, `resolveKeywords`, `resolveOne`, `nearMatches`, `levenshtein` block) and lines 187–225 (`validateEntityNames`, `canonicaliseHint`), with these renames:

```go
package entities

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Field constants for keyword resolution. The string values surface in
// tool error messages and JSON output; keep them stable across consumers.
const (
	FieldServices = "services"
	FieldErrors   = "errors"
	FieldSymptoms = "symptoms"
)

// NamePattern is the canonical entity-name shape: lowercase, hyphens,
// no spaces/underscores/capitals. Mirrors the wiki schema regex.
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Resolution explains how one input keyword mapped against a
// known-entity set. Same JSON shape as the wiki's previous
// EntityResolution; consumers serialise it directly.
type Resolution struct {
	Field string   `json:"field"`
	Input string   `json:"input"`
	Exact bool     `json:"exact"`
	Near  []string `json:"near,omitempty"`
}

// ResolveKeywords runs every input keyword (across services / errors /
// symptoms) against the known set per type and returns one Resolution
// per (field, input). knownByType keys are "service" / "error" /
// "symptom" (singular) to match the wiki vault layout.
func ResolveKeywords(services, errors, symptoms []string, knownByType map[string][]string) []Resolution {
	out := make([]Resolution, 0, len(services)+len(errors)+len(symptoms))
	for _, s := range services {
		out = append(out, ResolveOne(FieldServices, s, knownByType["service"]))
	}
	for _, e := range errors {
		out = append(out, ResolveOne(FieldErrors, e, knownByType["error"]))
	}
	for _, sym := range symptoms {
		out = append(out, ResolveOne(FieldSymptoms, sym, knownByType["symptom"]))
	}
	return out
}

// ResolveOne checks a single keyword against a known-name list.
// Exact matches short-circuit; otherwise we surface up to 5 near hits.
func ResolveOne(field, input string, known []string) Resolution {
	for _, n := range known {
		if n == input {
			return Resolution{Field: field, Input: input, Exact: true}
		}
	}
	return Resolution{
		Field: field,
		Input: input,
		Exact: false,
		Near:  NearMatches(input, known, 5),
	}
}

// NearMatches returns up to max entries from candidates ranked
// closest-first to input. Ranking blends substring containment with
// Levenshtein distance (≤ max(3, len/3) edits). Substring beats
// pure edit-distance; ties break alphabetically.
func NearMatches(input string, candidates []string, max int) []string {
	if input == "" || len(candidates) == 0 {
		return nil
	}
	type scored struct {
		name   string
		subset int
		dist   int
	}
	threshold := 3
	if t := len(input) / 3; t > threshold {
		threshold = t
	}
	var hits []scored
	for _, c := range candidates {
		if c == input {
			continue
		}
		s := scored{name: c}
		if strings.Contains(c, input) {
			s.subset = 2
		} else if strings.Contains(input, c) {
			s.subset = 1
		}
		s.dist = levenshtein(input, c)
		if s.subset == 0 && s.dist > threshold {
			continue
		}
		hits = append(hits, s)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].subset != hits[j].subset {
			return hits[i].subset > hits[j].subset
		}
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// ValidateNames rejects malformed entity names with an actionable
// error. Returns nil when every value matches NamePattern.
// The error message names the field, the bad value, the shape rule,
// and (when canonicalisable) a "did you mean" hint.
//
// listSourceTool is the tool consumers should call to enumerate
// canonical names (e.g. "wiki_list_entities"). Empty omits the
// "use … to enumerate" suffix.
func ValidateNames(field, listSourceTool string, names []string) error {
	for _, n := range names {
		if NamePattern.MatchString(n) {
			continue
		}
		hint := CanonicaliseHint(n)
		var hintClause string
		if hint != "" && hint != n {
			hintClause = fmt.Sprintf(" (did you mean %q?)", hint)
		}
		suffix := ""
		if listSourceTool != "" {
			suffix = fmt.Sprintf(" Use %s to enumerate canonical names; pass them verbatim", listSourceTool)
		}
		return fmt.Errorf("%s contains %q which is not a valid entity name%s — names must match ^[a-z0-9][a-z0-9-]*$ (lowercase, hyphens only, no spaces / underscores / capitals).%s", field, n, hintClause, suffix)
	}
	return nil
}

// CanonicaliseHint converts an obviously-non-canonical input into the
// shape ValidateNames would accept, for inclusion in error messages.
// Returns "" when the result still wouldn't pass (e.g. leading
// hyphen, punctuation).
func CanonicaliseHint(in string) string {
	out := strings.ToLower(in)
	out = strings.ReplaceAll(out, " ", "-")
	out = strings.ReplaceAll(out, "_", "-")
	out = strings.Trim(out, "-")
	if !NamePattern.MatchString(out) {
		return ""
	}
	return out
}
```

- [ ] **Step 2: Write the package-level tests by moving the wiki ones**

Create `mcp/internal/entities/entities_test.go` with the contents of `mcp/internal/wiki/entity_match_test.go` adjusted for the renamed surface:

```go
package entities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateNames_AcceptsCanonical(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateNames("services", "wiki_list_entities", []string{"zeebe-broker", "elasticsearch", "operate"}))
}

func TestValidateNames_RejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		hint  string
	}{
		{"spaces", "Zeebe Broker", "zeebe-broker"},
		{"underscore", "zeebe_broker", "zeebe-broker"},
		{"uppercase", "Operate", "operate"},
		{"mixed", "OOM_kill", "oom-kill"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNames("services", "wiki_list_entities", []string{tc.input})
			require.Error(t, err, "expected error for %q", tc.input)
			msg := err.Error()
			assert.Contains(t, msg, tc.hint, "error message missing canonicalisation hint")
			assert.Contains(t, msg, "wiki_list_entities", "error message should mention listSourceTool when provided")
		})
	}
}

func TestValidateNames_NoHintWhenUnsalvageable(t *testing.T) {
	t.Parallel()
	err := ValidateNames("services", "wiki_list_entities", []string{"-broker"})
	require.Error(t, err, "expected error for leading hyphen")
}

func TestValidateNames_OmitsListSourceWhenEmpty(t *testing.T) {
	t.Parallel()
	err := ValidateNames("services", "", []string{"Bad Name"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Use  to enumerate", "empty listSourceTool must not produce a half-rendered hint")
}

func TestNearMatches_PrefersSubstringHits(t *testing.T) {
	t.Parallel()
	got := NearMatches("broker", []string{"zeebe-broker", "operate", "tasklist", "broker-controller"}, 3)
	want := []string{"zeebe-broker", "broker-controller"}
	assert.Equal(t, want, got)
}

func TestNearMatches_LevenshteinFallback(t *testing.T) {
	t.Parallel()
	got := NearMatches("oom-kil", []string{"oom-kill", "elasticsearch", "zeebe-broker"}, 3)
	require.NotEmpty(t, got, "expected oom-kill first")
	assert.Equal(t, "oom-kill", got[0], "expected oom-kill first")
}

func TestNearMatches_DropsUnrelated(t *testing.T) {
	t.Parallel()
	got := NearMatches("shard-stuck", []string{"zeebe-broker", "operate"}, 3)
	assert.Empty(t, got, "expected empty (unrelated)")
}

func TestResolveKeywords_ExactAndNear(t *testing.T) {
	t.Parallel()
	known := map[string][]string{
		"service": {"zeebe-broker", "elasticsearch"},
		"error":   {"oom-kill"},
		"symptom": {"stuck-reconciliation"},
	}
	got := ResolveKeywords(
		[]string{"zeebe-broker", "broker"},
		[]string{"oom-kill"},
		[]string{"completely-unrelated-thing"},
		known,
	)
	require.Len(t, got, 4, "expected 4 resolution entries")
	assert.True(t, got[0].Exact, "entry 0 wrong: %+v", got[0])
	assert.Equal(t, "services", got[0].Field, "entry 0 wrong")
	assert.Equal(t, "zeebe-broker", got[0].Input, "entry 0 wrong")
	assert.False(t, got[1].Exact, "entry 1 should have near=[zeebe-broker]: %+v", got[1])
	assert.Equal(t, "broker", got[1].Input, "entry 1 should have near=[zeebe-broker]")
	require.NotEmpty(t, got[1].Near, "entry 1 should have near=[zeebe-broker]")
	assert.Equal(t, "zeebe-broker", got[1].Near[0], "entry 1 should have near=[zeebe-broker]")
	assert.True(t, got[2].Exact, "entry 2 wrong: %+v", got[2])
	assert.Equal(t, "errors", got[2].Field, "entry 2 wrong")
	assert.False(t, got[3].Exact, "entry 3 should have empty near: %+v", got[3])
	assert.Equal(t, "symptoms", got[3].Field, "entry 3 should have empty near")
	assert.Empty(t, got[3].Near, "entry 3 should have empty near")
}
```

- [ ] **Step 3: Run the new package's tests — they should pass**

```
go test -race -count=1 ./mcp/internal/entities/...
```

Expected: all tests PASS. (We copied working code.)

- [ ] **Step 4: Switch wiki to import the new package; delete duplicated code**

In `mcp/internal/wiki/entity_match.go`, **delete** lines 1–225 (everything from `package wiki` through the end of `canonicaliseHint`). Replace with:

```go
package wiki

import (
	"fmt"
	"strings"

	"github.com/camunda/c1-plugins/mcp/internal/entities"
)

// Re-export for callers that still type wiki.EntityResolution. Drop
// these once external callers migrate to the entities package.
type EntityResolution = entities.Resolution
```

(Leave lines 226–360 — `loadKnownEntitiesByType`, `validateEntityType`, `validateStatusFilter`, `validateSeverityFilter`, `validateNotePath` — intact; they stay wiki-local.)

Then update the in-package callsites that referenced the deleted names:

In `mcp/internal/wiki/tool_correlate.go` lines 71-85, replace:

```go
if err := validateEntityNames("services", in.Services); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
if err := validateEntityNames("errors", in.Errors); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
if err := validateEntityNames("symptoms", in.Symptoms); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}

known, err := loadKnownEntitiesByType(s.vaultPath)
if err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
resolution := resolveKeywords(in.Services, in.Errors, in.Symptoms, known)
```

with:

```go
if err := entities.ValidateNames("services", "wiki_list_entities", in.Services); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
if err := entities.ValidateNames("errors", "wiki_list_entities", in.Errors); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
if err := entities.ValidateNames("symptoms", "wiki_list_entities", in.Symptoms); err != nil {
    return errorResult(err.Error()), emptyOut, nil
}

known, err := loadKnownEntitiesByType(s.vaultPath)
if err != nil {
    return errorResult(err.Error()), emptyOut, nil
}
resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)
```

Add `"github.com/camunda/c1-plugins/mcp/internal/entities"` to the imports.

In `mcp/internal/wiki/tools_read.go` lines 57-83, apply the same rename (every `validateEntityNames(field, …)` → `entities.ValidateNames(field, "wiki_list_entities", …)`; `resolveKeywords(…)` → `entities.ResolveKeywords(…)`; add the import).

Also update the `Resolution []EntityResolution` field at line 46 of `tools_read.go` if it's referenced as a local type — `EntityResolution` is now a type alias for `entities.Resolution`, so the field will still compile. Leave it for now.

- [ ] **Step 5: Delete the moved tests from wiki**

Open `mcp/internal/wiki/entity_match_test.go` and delete the tests now duplicated in `entities_test.go`: `TestValidateEntityNames_*`, `TestNearMatches_*`, `TestResolveKeywords_ExactAndNear`. If the file becomes empty (no remaining tests), delete the file entirely.

- [ ] **Step 6: Run the wiki tests to prove behaviour is preserved**

```
go test -race -count=1 ./mcp/internal/wiki/...
```

Expected: all tests PASS. Any compile error means a callsite still references the deleted names — find and update.

- [ ] **Step 7: Run the full backend test suite**

```
go test -race -count=1 ./...
```

Expected: all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add mcp/internal/entities/ mcp/internal/wiki/entity_match.go mcp/internal/wiki/entity_match_test.go mcp/internal/wiki/tool_correlate.go mcp/internal/wiki/tools_read.go
git commit -m "$(cat <<'EOF'
refactor(mcp/entities): extract entity helpers from wiki for cross-MCP reuse

Pure helpers (ValidateNames, ResolveKeywords, NearMatches, levenshtein,
Resolution type) move into a new mcp/internal/entities package so the
strategies MCP can use the same vocabulary for playbook tag correlation.
loadKnownEntitiesByType stays wiki-local — it reads vault frontmatter.

No behaviour change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Playbook frontmatter fields + validation

**Files:**
- Modify: `mcp/internal/strategies/playbook.go:18-61` (add three fields to `Playbook` struct)
- Modify: `mcp/internal/strategies/playbook.go:596-765` (extend `validateOne` to check the new fields)
- Modify: `mcp/internal/strategies/playbook_test.go` (add validation cases)

- [ ] **Step 1: Write failing tests for the new validation cases**

Append to `mcp/internal/strategies/playbook_test.go`:

```go
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
```

Ensure `strings`, `require`, and `assert` are already imported in the file; add them if not.

- [ ] **Step 2: Run the tests to verify they fail**

```
go test -race -count=1 ./mcp/internal/strategies/ -run TestValidateOne_
```

Expected: `TestValidateOne_AcceptsEntityTags` and `TestValidateOne_RejectsMalformedEntityTag` FAIL with "undefined: Services" (or similar), because the struct doesn't have the fields yet.

- [ ] **Step 3: Add the three fields to the `Playbook` struct**

In `mcp/internal/strategies/playbook.go`, modify the `Playbook` struct (currently lines 18-61). Add three new fields anywhere after `Description`:

```go
// Services / Errors / Symptoms are canonical entity tags used by
// playbook_correlate to rank candidate playbooks against a query
// set lifted from findings. All three are optional; empty arrays
// = "not tagged" (the playbook is invisible to correlate as a
// direct match but can still surface via lifting from a tagged
// child playbook). Names must match the shape enforced by
// entities.NamePattern (^[a-z0-9][a-z0-9-]*$) — same vocabulary
// the wiki uses, so an agent that lifted entities for wiki_correlate
// can pass them straight to playbook_correlate.
Services []string `yaml:"services,omitempty" json:"services,omitempty"`
Errors   []string `yaml:"errors,omitempty"   json:"errors,omitempty"`
Symptoms []string `yaml:"symptoms,omitempty" json:"symptoms,omitempty"`
```

- [ ] **Step 4: Extend `validateOne` to check the new fields**

In `mcp/internal/strategies/playbook.go`, find the end of the per-node validation loop (just before `if len(pb.Nodes) > 0 && terminalCount == 0 {` — around line 726). Add this block immediately after the node loop closes:

```go
// Entity tag validation: each entry must be a canonical name
// (^[a-z0-9][a-z0-9-]*$). Reuses entities.ValidateNames so the
// error shape — including the "did you mean" hint — is identical
// to what wiki_correlate surfaces.
if err := entities.ValidateNames("services", "", pb.Services); err != nil {
    errs = append(errs, fmt.Sprintf("%s: %v", label, err))
}
if err := entities.ValidateNames("errors", "", pb.Errors); err != nil {
    errs = append(errs, fmt.Sprintf("%s: %v", label, err))
}
if err := entities.ValidateNames("symptoms", "", pb.Symptoms); err != nil {
    errs = append(errs, fmt.Sprintf("%s: %v", label, err))
}
```

Add `"github.com/camunda/c1-plugins/mcp/internal/entities"` to the imports at the top of the file.

We pass empty `listSourceTool` because there's no equivalent of `wiki_list_entities` on the strategies side — the canonical-name source is whatever the operator authored. The error still names the bad value and the shape rule.

- [ ] **Step 5: Run the tests to verify they pass**

```
go test -race -count=1 ./mcp/internal/strategies/ -run TestValidateOne_
```

Expected: all three new tests PASS.

- [ ] **Step 6: Run the full backend test suite to confirm no regressions**

```
go test -race -count=1 ./...
```

Expected: all tests PASS. Pay attention to `mcp/internal/strategies/playbook_test.go`'s existing validation tests and the embed test in `investigate/system/embed_test.go` — adding optional fields shouldn't break either, but verify.

- [ ] **Step 7: Commit**

```bash
git add mcp/internal/strategies/playbook.go mcp/internal/strategies/playbook_test.go
git commit -m "$(cat <<'EOF'
feat(mcp/strategies): playbook entity tags (services/errors/symptoms)

Adds three optional top-level entity arrays to the Playbook struct,
validated against entities.NamePattern. Untagged playbooks load
unchanged (additive, back-compat preserved; no schema_version bump).

The fields are inputs for the upcoming playbook_correlate tool,
which ranks playbooks by entity overlap with a query set lifted
from findings.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `playbook_correlate` tool

**Files:**
- Create: `mcp/internal/strategies/tool_correlate.go`
- Create: `mcp/internal/strategies/tool_correlate_test.go`
- Modify: `mcp/internal/strategies/server.go:82-142` (register handler)
- Modify: `mcp/internal/strategies/specs.go:13-82` (add ToolSpec entry)

- [ ] **Step 1: Write failing tests covering the full surface**

Create `mcp/internal/strategies/tool_correlate_test.go`:

```go
package strategies

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixturePlaybook builds a Playbook with sensible defaults for use in
// correlate tests. Override fields by mutating the returned pointer.
func fixturePlaybook(id string) *Playbook {
	return &Playbook{
		ID:         id,
		Symptom:    "test symptom for " + id,
		Entrypoint: "start",
		Nodes: map[string]Node{
			"start": {ID: "start", Description: "d", TerminalAdvice: "done"},
		},
	}
}

func newServerWithPlaybooks(t *testing.T, books ...*Playbook) *Server {
	t.Helper()
	m := make(map[string]*Playbook, len(books))
	for _, b := range books {
		m[b.ID] = b
	}
	return &Server{playbooks: m}
}

func TestPlaybookCorrelate_DirectMatch(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("zeebe_oom")
	pb.Services = []string{"zeebe-broker"}
	pb.Errors = []string{"oom-kill"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 1, "expected one match")
	m := out.Correlations[0]
	assert.Equal(t, "zeebe_oom", m.ID)
	assert.Equal(t, 3, m.Score, "direct hit = 3")
	assert.Equal(t, []string{"zeebe-broker"}, m.MatchPath.Direct)
	assert.Empty(t, m.MatchPath.Lifted)
}

func TestPlaybookCorrelate_LiftedViaDelegateTo(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("elasticsearch")
	child.Errors = []string{"shard-failure"}

	parent := fixturePlaybook("cluster_health")
	parent.Nodes = map[string]Node{
		"start":     {ID: "start", Description: "d", DelegateTo: "elasticsearch", Next: []Branch{{Condition: "always", Goto: "done"}}},
		"done":      {ID: "done", Description: "d", TerminalAdvice: "ok"},
	}
	parent.Entrypoint = "start"

	s := newServerWithPlaybooks(t, parent, child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Errors: []string{"shard-failure"},
	})
	require.NoError(t, err)

	byID := map[string]playbookCorrelateMatch{}
	for _, m := range out.Correlations {
		byID[m.ID] = m
	}
	require.Contains(t, byID, "elasticsearch", "child should match directly")
	require.Contains(t, byID, "cluster_health", "parent should match via lift")
	assert.Equal(t, 3, byID["elasticsearch"].Score, "direct hit")
	assert.Equal(t, 1, byID["cluster_health"].Score, "lifted hit")
	require.Len(t, byID["cluster_health"].MatchPath.Lifted, 1)
	assert.Equal(t, "shard-failure", byID["cluster_health"].MatchPath.Lifted[0].Entity)
	assert.Equal(t, "elasticsearch", byID["cluster_health"].MatchPath.Lifted[0].Via)
}

func TestPlaybookCorrelate_LiftedViaHandoff(t *testing.T) {
	t.Parallel()
	target := fixturePlaybook("network")
	target.Symptoms = []string{"connectivity-loss"}

	source := fixturePlaybook("triage")
	source.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "hand off", Handoff: []string{"network"}},
	}
	source.Entrypoint = "start"

	s := newServerWithPlaybooks(t, source, target)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Symptoms: []string{"connectivity-loss"},
	})
	require.NoError(t, err)

	byID := map[string]playbookCorrelateMatch{}
	for _, m := range out.Correlations {
		byID[m.ID] = m
	}
	require.Contains(t, byID, "triage", "handoff source should lift the target's tags")
	assert.Equal(t, 1, byID["triage"].Score)
	require.Len(t, byID["triage"].MatchPath.Lifted, 1)
	assert.Equal(t, "network", byID["triage"].MatchPath.Lifted[0].Via)
}

func TestPlaybookCorrelate_DirectBeatsLifted(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("child")
	child.Services = []string{"zeebe-broker"}

	parent := fixturePlaybook("parent")
	parent.Services = []string{"zeebe-broker"} // also directly tagged
	parent.Nodes = map[string]Node{
		"start":   {ID: "start", Description: "d", DelegateTo: "child", Next: []Branch{{Condition: "always", Goto: "done"}}},
		"done":    {ID: "done", Description: "d", TerminalAdvice: "ok"},
	}
	parent.Entrypoint = "start"

	s := newServerWithPlaybooks(t, parent, child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)

	var parentMatch *playbookCorrelateMatch
	for i := range out.Correlations {
		if out.Correlations[i].ID == "parent" {
			parentMatch = &out.Correlations[i]
		}
	}
	require.NotNil(t, parentMatch)
	assert.Equal(t, 3, parentMatch.Score, "direct hit wins; entity not double-counted as lifted")
	assert.Equal(t, []string{"zeebe-broker"}, parentMatch.MatchPath.Direct)
	assert.Empty(t, parentMatch.MatchPath.Lifted)
}

func TestPlaybookCorrelate_ManyParentsLiftSameChildOnce(t *testing.T) {
	t.Parallel()
	child := fixturePlaybook("conn")
	child.Symptoms = []string{"connectivity-loss"}

	mkParent := func(id string) *Playbook {
		p := fixturePlaybook(id)
		p.Nodes = map[string]Node{
			"start":   {ID: "start", Description: "d", DelegateTo: "conn", Next: []Branch{{Condition: "always", Goto: "done"}}},
			"done":    {ID: "done", Description: "d", TerminalAdvice: "ok"},
		}
		p.Entrypoint = "start"
		return p
	}
	s := newServerWithPlaybooks(t, mkParent("alpha"), mkParent("beta"), mkParent("gamma"), child)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Symptoms: []string{"connectivity-loss"},
	})
	require.NoError(t, err)

	scoreByID := map[string]int{}
	for _, m := range out.Correlations {
		scoreByID[m.ID] = m.Score
	}
	assert.Equal(t, 3, scoreByID["conn"], "child direct")
	assert.Equal(t, 1, scoreByID["alpha"], "lift counted once per parent")
	assert.Equal(t, 1, scoreByID["beta"])
	assert.Equal(t, 1, scoreByID["gamma"])
}

func TestPlaybookCorrelate_CycleSafe(t *testing.T) {
	t.Parallel()
	// A → B → A cycle through handoff. Lifting must terminate.
	a := fixturePlaybook("a")
	a.Services = []string{"svc-a"}
	a.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "go b", Handoff: []string{"b"}},
	}
	a.Entrypoint = "start"

	b := fixturePlaybook("b")
	b.Services = []string{"svc-b"}
	b.Nodes = map[string]Node{
		"start": {ID: "start", Description: "d", TerminalAdvice: "go a", Handoff: []string{"a"}},
	}
	b.Entrypoint = "start"

	s := newServerWithPlaybooks(t, a, b)

	_, _, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"svc-a"},
	})
	require.NoError(t, err, "cycle must not infinite-loop")
}

func TestPlaybookCorrelate_TypeFilter(t *testing.T) {
	t.Parallel()
	investigationPB := fixturePlaybook("inv")
	investigationPB.Type = "investigation"
	investigationPB.Services = []string{"zeebe-broker"}

	generalPB := fixturePlaybook("gen")
	generalPB.Type = "general"
	generalPB.Services = []string{"zeebe-broker"}

	s := newServerWithPlaybooks(t, investigationPB, generalPB)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
		Type:     "investigation",
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 1, "type filter should drop non-matching")
	assert.Equal(t, "inv", out.Correlations[0].ID)
}

func TestPlaybookCorrelate_Limit(t *testing.T) {
	t.Parallel()
	books := make([]*Playbook, 0, 10)
	for i := 0; i < 10; i++ {
		p := fixturePlaybook(string(rune('a'+i)))
		p.Services = []string{"zeebe-broker"}
		books = append(books, p)
	}
	s := newServerWithPlaybooks(t, books...)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
		Limit:    3,
	})
	require.NoError(t, err)
	assert.Len(t, out.Correlations, 3)
}

func TestPlaybookCorrelate_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("anything")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{})
	require.NoError(t, err)
	assert.NotNil(t, out.Correlations, "must be [] not null")
	assert.Empty(t, out.Correlations)
}

func TestPlaybookCorrelate_MalformedInputErrors(t *testing.T) {
	t.Parallel()
	s := newServerWithPlaybooks(t)
	res, _, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"Bad Name"},
	})
	require.NoError(t, err, "tool-level error, not transport error")
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestPlaybookCorrelate_SurfacesNearMatches(t *testing.T) {
	t.Parallel()
	pb := fixturePlaybook("anything")
	pb.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, pb)

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"broker"}, // near match: should resolve to zeebe-broker
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.Resolution, "near match should surface in Resolution")
	r := out.Resolution[0]
	assert.False(t, r.Exact)
	assert.Equal(t, "broker", r.Input)
	require.NotEmpty(t, r.Near)
	assert.Equal(t, "zeebe-broker", r.Near[0])
}

func TestPlaybookCorrelate_DeterministicTieBreak(t *testing.T) {
	t.Parallel()
	a := fixturePlaybook("alpha")
	a.Services = []string{"zeebe-broker"}
	b := fixturePlaybook("beta")
	b.Services = []string{"zeebe-broker"}
	s := newServerWithPlaybooks(t, b, a) // construction order shouldn't matter

	_, out, err := s.playbookCorrelate(context.Background(), nil, playbookCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err)
	require.Len(t, out.Correlations, 2)

	// Both have score 3 → tie-break alphabetical
	ids := []string{out.Correlations[0].ID, out.Correlations[1].ID}
	sortedIDs := make([]string, len(ids))
	copy(sortedIDs, ids)
	sort.Strings(sortedIDs)
	assert.Equal(t, sortedIDs, ids, "ties break alphabetical by id")
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

```
go test -race -count=1 ./mcp/internal/strategies/ -run TestPlaybookCorrelate_
```

Expected: every test FAILs with "undefined: playbookCorrelateIn" / "undefined: playbookCorrelate" (or similar) — the tool doesn't exist yet.

- [ ] **Step 3: Implement the tool**

Create `mcp/internal/strategies/tool_correlate.go`:

```go
package strategies

import (
	"context"
	"sort"

	"github.com/camunda/c1-plugins/mcp/internal/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type playbookCorrelateIn struct {
	Services []string `json:"services,omitempty" jsonschema:"canonical service entity names lifted from findings, e.g. ['zeebe-broker']; ^[a-z0-9][a-z0-9-]*$"`
	Errors   []string `json:"errors,omitempty"   jsonschema:"canonical error entity names from findings, e.g. ['oom-kill']; ^[a-z0-9][a-z0-9-]*$"`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"canonical symptom entity names from findings, e.g. ['stuck-reconciliation']; ^[a-z0-9][a-z0-9-]*$"`
	Type     string   `json:"type,omitempty"     jsonschema:"optional type filter — 'investigation' / 'general'. Same semantics as list_playbooks. Omit for all."`
	Limit    int      `json:"limit,omitempty"    jsonschema:"max correlations to return; default 5"`
}

type playbookCorrelateMatch struct {
	ID          string            `json:"id"`
	Symptom     string            `json:"symptom"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"`
	Score       int               `json:"score"`
	MatchPath   playbookMatchPath `json:"match_path"`
}

type playbookMatchPath struct {
	Direct []string         `json:"direct,omitempty"`
	Lifted []playbookLifted `json:"lifted,omitempty"`
}

type playbookLifted struct {
	Entity string `json:"entity"`
	Via    string `json:"via"`
}

type playbookCorrelateOut struct {
	Correlations []playbookCorrelateMatch `json:"correlations"`
	Resolution   []entities.Resolution    `json:"resolution,omitempty"`
}

// playbookCorrelate ranks the loaded playbook set by entity overlap
// with the query set. Score = 3 * |direct hits| + 1 * |lifted hits
// not already direct|. Lifting walks one hop via delegate_to /
// handoff[*] only (next.goto is intra-playbook, validated by
// validateOne). Body is never returned — metadata only.
//
// Naive O(N) scan; the loaded set is dozens, not thousands. If the
// library grows large enough to hurt, swap in an inverted index
// (entityName → []playbookID) built once at New().
func (s *Server) playbookCorrelate(ctx context.Context, _ *mcp.CallToolRequest, in playbookCorrelateIn) (*mcp.CallToolResult, playbookCorrelateOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	emptyOut := playbookCorrelateOut{Correlations: []playbookCorrelateMatch{}}

	if len(in.Services) == 0 && len(in.Errors) == 0 && len(in.Symptoms) == 0 {
		return nil, emptyOut, nil
	}

	if err := entities.ValidateNames("services", "", in.Services); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("errors", "", in.Errors); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("symptoms", "", in.Symptoms); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}

	// Build the known-set lazily from the union of tags across all loaded
	// playbooks for resolution / near-match hints. Cheap (dozens of items).
	known := buildKnownEntities(s.playbooks)
	resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)

	querySvc := makeStringSet(in.Services)
	queryErr := makeStringSet(in.Errors)
	querySym := makeStringSet(in.Symptoms)

	matches := []playbookCorrelateMatch{}
	for id, pb := range s.playbooks {
		if in.Type != "" && pb.EffectiveType() != in.Type {
			continue
		}
		direct := directHits(pb, querySvc, queryErr, querySym)
		lifted := liftedHits(pb, s.playbooks, querySvc, queryErr, querySym, direct)
		score := 3*len(direct) + len(lifted)
		if score == 0 {
			continue
		}
		matches = append(matches, playbookCorrelateMatch{
			ID:          id,
			Symptom:     pb.Symptom,
			Description: pb.Description,
			Type:        pb.EffectiveType(),
			Score:       score,
			MatchPath: playbookMatchPath{
				Direct: direct,
				Lifted: lifted,
			},
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return nil, playbookCorrelateOut{Correlations: matches, Resolution: resolution}, nil
}

// buildKnownEntities collects canonical names from every loaded
// playbook's services / errors / symptoms arrays, grouped by type
// (keys "service" / "error" / "symptom" — matches wiki vault layout).
// Duplicates collapse via the map; output is sorted for determinism.
func buildKnownEntities(books map[string]*Playbook) map[string][]string {
	seenSvc, seenErr, seenSym := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pb := range books {
		for _, s := range pb.Services {
			seenSvc[s] = true
		}
		for _, e := range pb.Errors {
			seenErr[e] = true
		}
		for _, sym := range pb.Symptoms {
			seenSym[sym] = true
		}
	}
	out := map[string][]string{
		"service": sortedKeys(seenSvc),
		"error":   sortedKeys(seenErr),
		"symptom": sortedKeys(seenSym),
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func makeStringSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, v := range in {
		out[v] = true
	}
	return out
}

// directHits returns entities from the playbook's own services / errors
// / symptoms arrays that also appear in the query sets, deduped (each
// entity once per playbook even if it's tagged in multiple arrays).
// Order is preserved relative to the playbook's arrays.
func directHits(pb *Playbook, qSvc, qErr, qSym map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range pb.Services {
		if qSvc[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, e := range pb.Errors {
		if qErr[e] && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, sym := range pb.Symptoms {
		if qSym[sym] && !seen[sym] {
			seen[sym] = true
			out = append(out, sym)
		}
	}
	return out
}

// liftedHits walks the playbook's one-hop cross-playbook references
// (delegate_to / handoff[*]) and returns entities on those children
// that match the query sets, EXCLUDING entities already counted as
// direct hits on the parent. Per (entity, parent) we attribute one
// `via` — the first child encountered in deterministic-id order that
// supplied the entity. Cycle-safe via a visited set; depth = 1 only.
func liftedHits(parent *Playbook, books map[string]*Playbook, qSvc, qErr, qSym map[string]bool, directOnParent []string) []playbookLifted {
	directSet := map[string]bool{}
	for _, e := range directOnParent {
		directSet[e] = true
	}

	// Collect one-hop child ids in deterministic order.
	childIDs := collectOneHopChildren(parent)
	sort.Strings(childIDs)

	seenEntity := map[string]bool{}
	var out []playbookLifted
	for _, cid := range childIDs {
		child, ok := books[cid]
		if !ok {
			continue
		}
		for _, s := range child.Services {
			if !qSvc[s] || directSet[s] || seenEntity[s] {
				continue
			}
			seenEntity[s] = true
			out = append(out, playbookLifted{Entity: s, Via: cid})
		}
		for _, e := range child.Errors {
			if !qErr[e] || directSet[e] || seenEntity[e] {
				continue
			}
			seenEntity[e] = true
			out = append(out, playbookLifted{Entity: e, Via: cid})
		}
		for _, sym := range child.Symptoms {
			if !qSym[sym] || directSet[sym] || seenEntity[sym] {
				continue
			}
			seenEntity[sym] = true
			out = append(out, playbookLifted{Entity: sym, Via: cid})
		}
	}
	return out
}

// collectOneHopChildren returns the unique set of OTHER playbook ids
// referenced by any node of `pb` via delegate_to or handoff[*]. Excludes
// self-references (the parent's own id) so a playbook can't lift from
// itself.
func collectOneHopChildren(pb *Playbook) []string {
	seen := map[string]bool{}
	for _, node := range pb.Nodes {
		if node.DelegateTo != "" && node.DelegateTo != pb.ID {
			seen[node.DelegateTo] = true
		}
		for _, h := range node.Handoff {
			if h != "" && h != pb.ID {
				seen[h] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 4: Run the tool tests to verify they pass**

```
go test -race -count=1 ./mcp/internal/strategies/ -run TestPlaybookCorrelate_
```

Expected: all tests PASS. Any failure points at a scoring/lifting bug — debug before moving on.

- [ ] **Step 5: Register the tool with the MCP server**

In `mcp/internal/strategies/server.go`, find the `register()` function (starts at line 82). After the `playbook_proposal_draft` registration (line 138-141), add:

```go
mcp.AddTool(s.impl, &mcp.Tool{
    Name:        "playbook_correlate",
    Description: "Rank the loaded playbooks by entity overlap with a query set of canonical services / errors / symptoms (the same vocabulary wiki_correlate uses; pass entities you already lifted from findings). Returns metadata-only matches (id, symptom, description, type, score, match_path) — not the body, so this is cheap to call before deciding which playbook to walk. Score = 3 * direct hits + 1 * lifted hits, where 'lifted' means an entity tagged on a one-hop child (delegate_to / handoff target). The match_path tells you why a playbook scored: direct entities + per-lifted-entity 'via' child id. Empty input returns empty results — pass at least one entity. Use this BEFORE list_playbooks when you have entities; fall back to list_playbooks only when correlate returns nothing or you want the full menu.",
}, telemetry.Wrap("playbook_correlate", s.playbookCorrelate))
```

- [ ] **Step 6: Add the ToolSpec entry**

In `mcp/internal/strategies/specs.go`, after the `playbook_proposal_draft` entry (line 76-80), add:

```go
{
    Server:      "c1-strategies",
    Name:        "playbook_correlate",
    Description: "Rank playbooks by entity overlap (services/errors/symptoms) with a query set, with one-hop lifting through delegate_to/handoff.",
    Inputs:      toolspec.FromStruct(playbookCorrelateIn{}),
},
```

- [ ] **Step 7: Run the full backend test suite — including the wire test**

```
go test -race -count=1 ./...
```

Expected: all tests PASS, including any test in `mcp/cmd/` that cross-checks the `ToolSpecs()` catalog against the registered handlers (the "wire test" referenced in CLAUDE.md). If a wire test fails with "playbook_correlate registered but not in ToolSpecs" or the reverse, fix the missing side.

- [ ] **Step 8: Commit**

```bash
git add mcp/internal/strategies/tool_correlate.go mcp/internal/strategies/tool_correlate_test.go mcp/internal/strategies/server.go mcp/internal/strategies/specs.go
git commit -m "$(cat <<'EOF'
feat(mcp/strategies): playbook_correlate tool with one-hop entity lifting

Ranks loaded playbooks by entity overlap with a query set. Score is
3 * direct + 1 * lifted-not-already-direct, where lifting walks
one-hop cross-playbook references via delegate_to / handoff[*].
Returns metadata only (id, symptom, description, type, score,
match_path) plus a Resolution[] for near-match hints — keeps the
context-loading cost low compared to list_playbooks at scale.

Cycle-safe (visited-set in lift traversal). Many-parents dedup is
per-entity-per-parent so a popular child doesn't inflate gateways.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Walker integration — `investigation.yaml` `route` node

**Files:**
- Modify: `investigate/system/investigation.yaml:157-188` (the `route` node)

The change is YAML-only. The system embed test (`investigate/system/embed_test.go`) re-validates every system playbook on every build, so structural correctness is automatically checked.

- [ ] **Step 1: Replace the `route` node body**

In `investigate/system/investigation.yaml`, find the `route:` node (currently lines 157-188). Replace the entire node (from `route:` through the closing of its `next:` block, i.e. the line `goto: terminal_novel_case`) with:

```yaml
  route:
    description: |
      Pick the domain playbook whose entity tags best match the
      symptom + findings. Two-step:

      1. **First**, call playbook_correlate with the canonical
         entities you lifted from the gather sub-flows (the same
         services / errors / symptoms you passed to wiki_correlate
         in walk_wiki_recall). The top match is almost always your
         candidate — its match_path shows you *why* it matched
         (direct entity hits vs lifted from a child via
         delegate_to / handoff). A direct hit beats a pure-gateway
         lift; trust the score.

      2. **If correlate returns no matches** (or you genuinely
         want to scan the full library to confirm nothing else
         fits), fall back to list_playbooks with type=investigation
         and pick by reading the symptom prose. Untagged playbooks
         are only discoverable this way until they're tagged, so
         the fallback stays load-bearing.

      Picking heuristics for the fallback list:
        - Prefer the more *specific* playbook when two look
          applicable.
        - If a playbook's symptom names a specific component / CRD
          / condition string that appears in the operator's notes
          OR an incidentio_affected_service finding, that's almost
          always the right pick.
        - Don't open a playbook whose symptom is only tangentially
          related.

      Record the chosen playbook (and a one-line why — cite the
      correlate match_path if applicable) via record_finding before
      advancing.
    suggested_calls:
      - tool: c1-strategies/playbook_correlate
        args:
          services: []
          errors: []
          symptoms: []
          type: investigation
      - tool: c1-strategies/list_playbooks
        args:
          type: investigation
    expected_findings:
      - candidate_playbooks
      - chosen_playbook
    next:
      - condition: a single playbook clearly matches the symptom
        goto: terminal_handoff_domain
      - condition: no playbook in the library matches — this is a genuinely novel case
        goto: terminal_novel_case
```

The empty arrays in the `playbook_correlate` suggested args are intentional: the walker prompt explains the agent fills them in with the entities it already lifted. The fixed shape just shows the agent which args exist.

- [ ] **Step 2: Run the system embed test**

```
go test -race -count=1 ./investigate/system/...
```

Expected: PASS. If the YAML is malformed or violates structural rules, this surfaces here.

- [ ] **Step 3: Run the full backend test suite**

```
go test -race -count=1 ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add investigate/system/investigation.yaml
git commit -m "$(cat <<'EOF'
feat(investigate/system): route via playbook_correlate, fall back to list_playbooks

The investigation playbook's `route` node now suggests playbook_correlate
first with the entities lifted earlier in the gather flow, falling back
to list_playbooks only when correlate returns nothing. Untagged playbooks
remain discoverable via the fallback path; operators have no obligation
to backfill tags for the walker to keep working.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Editor — related-playbooks panel (launcher endpoint + frontend)

**Files:**
- Modify: `investigate/internal/server/handlers.go:140-160` (register new route)
- Create: `investigate/internal/server/playbooks_related.go` (handler)
- Create: `investigate/internal/server/playbooks_related_test.go`
- Modify: `investigate/frontend/lib/api.ts` (add client function — verify exact signature pattern in file)
- Create: `investigate/frontend/components/RelatedPlaybooks.tsx`
- Modify: `investigate/frontend/components/PlaybookEditor.tsx` (mount the panel)

The launcher exposes `GET /api/playbooks/{id}/related`. It computes the playbook's own entity tags and calls into the strategies in-process API (the launcher holds the `*strategies.Server` instance) to run correlate, then filters out the queried id and returns up to 5 matches.

- [ ] **Step 1: Discover the existing in-process strategies surface the launcher uses**

Before writing the handler, run:

```
grep -rn "strategies\." /home/aegir/Documents/camunda/c1-plugins/investigate/internal/server/ | head -20
grep -rn "playbookCorrelate\|playbook_correlate\|PlaybookCorrelate" /home/aegir/Documents/camunda/c1-plugins/investigate/internal/server/ 2>/dev/null
```

The launcher likely holds a `*strategies.Server` (from `mcp/internal/strategies`). To call the tool in-process, we need an exported method that matches the handler's shape. Currently `playbookCorrelate` is unexported. Either:
- **(a)** Add an exported wrapper `func (s *strategies.Server) Correlate(in CorrelateIn) (CorrelateOut, error)` in `mcp/internal/strategies/tool_correlate.go` for direct in-process callers.
- **(b)** Have the launcher exec the strategies MCP over stdio (already done for normal MCP traffic; reuse).

Pick **(a)** — same-process call, no marshalling round-trip. Add to `tool_correlate.go`:

```go
// CorrelateIn / CorrelateOut / Correlate are the exported shape for
// in-process callers (the launcher's editor surface). Same semantics
// as the MCP tool; returns a typed error instead of an MCP error result.
type CorrelateIn = playbookCorrelateIn
type CorrelateOut = playbookCorrelateOut

func (s *Server) Correlate(ctx context.Context, in CorrelateIn) (CorrelateOut, error) {
    res, out, err := s.playbookCorrelate(ctx, nil, in)
    if err != nil {
        return out, err
    }
    if res != nil && res.IsError {
        // Promote MCP-formatted error back to a Go error for in-process callers.
        if len(res.Content) > 0 {
            if t, ok := res.Content[0].(*mcp.TextContent); ok {
                return out, fmt.Errorf("%s", t.Text)
            }
        }
        return out, fmt.Errorf("playbook_correlate failed")
    }
    return out, nil
}
```

Add the `"fmt"` import if not already present.

- [ ] **Step 2: Write the failing handler test**

Create `investigate/internal/server/playbooks_related_test.go` (consult an existing handler test in `playbooks_test.go` for the harness pattern — `httptest.NewRecorder`, the launcher's auth wrapper, etc.). Pseudocode shape:

```go
package server

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// TestRelatedPlaybooks_ReturnsCorrelatedMatches sets up a tiny
// in-process strategies server with two playbooks sharing a tag,
// hits GET /api/playbooks/{id}/related, expects the other playbook
// in the response.
func TestRelatedPlaybooks_ReturnsCorrelatedMatches(t *testing.T) {
    a := newTestAppWithPlaybooks(t, // helper modeled on existing playbooks_test.go harness
        &Playbook{ID: "a", Services: []string{"zeebe-broker"}, /* ... minimal fields */},
        &Playbook{ID: "b", Services: []string{"zeebe-broker"}, /* ... */},
    )

    req := httptest.NewRequest(http.MethodGet, "/api/playbooks/a/related", nil)
    w := httptest.NewRecorder()
    a.mux.ServeHTTP(w, req)

    require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
    var resp struct {
        Related []struct {
            ID    string `json:"id"`
            Score int    `json:"score"`
        } `json:"related"`
    }
    require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
    require.Len(t, resp.Related, 1, "should exclude self")
    assert.Equal(t, "b", resp.Related[0].ID)
}

// TestRelatedPlaybooks_UntaggedPlaybookReturnsEmpty
// covers an untagged playbook id — empty `related` array, 200 OK.
func TestRelatedPlaybooks_UntaggedPlaybookReturnsEmpty(t *testing.T) {
    a := newTestAppWithPlaybooks(t,
        &Playbook{ID: "untagged" /* ... no Services/Errors/Symptoms */},
    )
    req := httptest.NewRequest(http.MethodGet, "/api/playbooks/untagged/related", nil)
    w := httptest.NewRecorder()
    a.mux.ServeHTTP(w, req)
    require.Equal(t, http.StatusOK, w.Code)
    var resp struct {
        Related []json.RawMessage `json:"related"`
    }
    require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
    assert.Empty(t, resp.Related)
}

// TestRelatedPlaybooks_UnknownIDReturns404
// covers a playbook id that doesn't exist in the loaded set.
func TestRelatedPlaybooks_UnknownIDReturns404(t *testing.T) {
    a := newTestAppWithPlaybooks(t)
    req := httptest.NewRequest(http.MethodGet, "/api/playbooks/nope/related", nil)
    w := httptest.NewRecorder()
    a.mux.ServeHTTP(w, req)
    assert.Equal(t, http.StatusNotFound, w.Code)
}
```

The `newTestAppWithPlaybooks` helper does not exist yet — model it on whatever harness `playbooks_test.go` uses (likely a `newTestApp` that wires the launcher's `App` struct with an in-memory store + a `*strategies.Server` built from an in-memory playbook map). Open `playbooks_test.go` and adapt. If no helper exists for setting playbooks directly, add one in `playbooks_test.go` (small, reusable across this and future tests).

- [ ] **Step 3: Run the test to verify it fails**

```
go test -race -count=1 ./investigate/internal/server/ -run TestRelatedPlaybooks_
```

Expected: FAIL — 404 from the mux (route not registered), or compile error if `newTestAppWithPlaybooks` is missing.

- [ ] **Step 4: Implement the handler**

Create `investigate/internal/server/playbooks_related.go`:

```go
package server

import (
    "encoding/json"
    "net/http"

    "github.com/camunda/c1-plugins/mcp/internal/strategies"
)

// handleRelatedPlaybooks proxies the strategies MCP's playbook_correlate
// tool, using the queried playbook's own tags as the query set. Returns
// up to 5 OTHER playbooks (the queried id is filtered out — a playbook
// is not "related to" itself).
func (a *App) handleRelatedPlaybooks(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    pb, ok := a.strategies.Playbook(id) // see step 5 — add an exported accessor
    if !ok {
        http.Error(w, "playbook not found", http.StatusNotFound)
        return
    }
    out, err := a.strategies.Correlate(r.Context(), strategies.CorrelateIn{
        Services: pb.Services,
        Errors:   pb.Errors,
        Symptoms: pb.Symptoms,
        Limit:    6, // request one extra; self-filter trims back to ≤5
    })
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    related := make([]strategies.CorrelateMatch, 0, len(out.Correlations))
    for _, m := range out.Correlations {
        if m.ID == id {
            continue
        }
        related = append(related, m)
        if len(related) >= 5 {
            break
        }
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"related": related})
}
```

Note: the response uses `strategies.CorrelateMatch` — add that as a type alias in `tool_correlate.go` next to `CorrelateIn` / `CorrelateOut`:

```go
type CorrelateMatch = playbookCorrelateMatch
```

- [ ] **Step 5: Add exported accessors needed by the handler**

In `mcp/internal/strategies/server.go`, add an exported lookup method:

```go
// Playbook returns the loaded playbook with the given id and a
// "found" boolean. Lets in-process callers (the launcher) look up
// a playbook without holding a reference to the internal map.
func (s *Server) Playbook(id string) (*Playbook, bool) {
    pb, ok := s.playbooks[id]
    return pb, ok
}
```

- [ ] **Step 6: Register the route**

In `investigate/internal/server/handlers.go`, find the playbook route registrations (lines 140-160). After `mux.HandleFunc("GET /api/playbooks/{id}/commits/{sha}", …)` (line 153), add:

```go
mux.HandleFunc("GET /api/playbooks/{id}/related", a.handleRelatedPlaybooks)
```

- [ ] **Step 7: Run the handler tests to verify they pass**

```
go test -race -count=1 ./investigate/internal/server/ -run TestRelatedPlaybooks_
```

Expected: all PASS. If 404 still fires, double-check the route literal matches the request literal; if 500 fires, check that `a.strategies` is populated in the test harness.

- [ ] **Step 8: Add the frontend API client function**

Open `investigate/frontend/lib/api.ts`. Find an existing GET helper that hits an `/api/playbooks/{id}/...` endpoint (e.g. `getPlaybook`) and add a sibling:

```ts
export type RelatedPlaybookMatch = {
  id: string;
  symptom: string;
  description?: string;
  type: string;
  score: number;
  match_path: {
    direct?: string[];
    lifted?: { entity: string; via: string }[];
  };
};

export async function getRelatedPlaybooks(id: string): Promise<RelatedPlaybookMatch[]> {
  const res = await fetch(`/api/playbooks/${encodeURIComponent(id)}/related`);
  if (!res.ok) throw new Error(`getRelatedPlaybooks ${id}: ${res.status}`);
  const data = (await res.json()) as { related: RelatedPlaybookMatch[] };
  return data.related ?? [];
}
```

Match the surrounding style (existing helpers may use a different fetch wrapper — adopt whatever pattern is local).

- [ ] **Step 9: Create the `RelatedPlaybooks` panel component**

Create `investigate/frontend/components/RelatedPlaybooks.tsx`:

```tsx
"use client";

import { useEffect, useState } from "react";
import { getRelatedPlaybooks, type RelatedPlaybookMatch } from "@/lib/api";

type Props = {
  playbookID: string;
  onSelect?: (id: string) => void;
};

export function RelatedPlaybooks({ playbookID, onSelect }: Props) {
  const [matches, setMatches] = useState<RelatedPlaybookMatch[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setMatches(null);
    setError(null);
    getRelatedPlaybooks(playbookID)
      .then((m) => { if (alive) setMatches(m); })
      .catch((e) => { if (alive) setError(String(e)); });
    return () => { alive = false; };
  }, [playbookID]);

  if (error) {
    return <div className="text-sm text-rose-600">Related playbooks unavailable: {error}</div>;
  }
  if (matches === null) {
    return <div className="text-sm text-slate-500">Loading related playbooks…</div>;
  }
  if (matches.length === 0) {
    return <div className="text-sm text-slate-500">No related playbooks (tag this one with services/errors/symptoms to surface neighbours).</div>;
  }
  return (
    <ul className="space-y-1">
      {matches.map((m) => (
        <li key={m.id}>
          <button
            type="button"
            className="text-left text-sm text-sky-700 hover:underline"
            onClick={() => onSelect?.(m.id)}
          >
            <span className="font-mono">{m.id}</span>
            <span className="text-slate-500"> · {m.symptom}</span>
            <span className="ml-2 text-xs text-slate-400">score {m.score}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
```

- [ ] **Step 10: Mount the panel in the playbook editor**

Open `investigate/frontend/components/PlaybookEditor.tsx`. Find the layout for the playbook detail view (likely a side panel or footer area). Import and render:

```tsx
import { RelatedPlaybooks } from "@/components/RelatedPlaybooks";

// inside the JSX, e.g. as a section below the graph:
<section aria-labelledby="related-heading" className="mt-4">
  <h2 id="related-heading" className="text-sm font-semibold text-slate-700">Related playbooks</h2>
  <RelatedPlaybooks playbookID={playbook.id} onSelect={onOpenPlaybook} />
</section>
```

`onOpenPlaybook` is whatever existing handler the editor uses to switch playbooks (likely a router push to `/playbooks?playbook=<id>` per the CLAUDE.md URL-as-source-of-truth rule). If no such handler exists at this layer, plumb it from the parent or use `useRouter().push`.

- [ ] **Step 11: Run the frontend test + build**

```
cd investigate/frontend && npm test -- --run
npm run typecheck
npm run build
cd ../..
```

Expected: PASS. The component has no dedicated test (per CLAUDE.md, smoke-via-build for component changes); typecheck + build are the bar.

- [ ] **Step 12: Smoke-test in a dev server**

Per CLAUDE.md (frontend changes): start the dev server, navigate to the playbook editor, confirm:
- The "Related playbooks" section renders.
- For a playbook with tagged neighbours, links appear and clicking one switches view.
- For an untagged playbook, the "no related" hint renders without errors.

```
cd investigate/frontend && npm run dev
# in another shell or your browser, navigate to /playbooks?playbook=<some-id>
```

Stop the dev server (`Ctrl-C`) once verified.

- [ ] **Step 13: Commit**

```bash
git add mcp/internal/strategies/tool_correlate.go mcp/internal/strategies/server.go investigate/internal/server/handlers.go investigate/internal/server/playbooks_related.go investigate/internal/server/playbooks_related_test.go investigate/frontend/lib/api.ts investigate/frontend/components/RelatedPlaybooks.tsx investigate/frontend/components/PlaybookEditor.tsx
git commit -m "$(cat <<'EOF'
feat(investigate/frontend): related playbooks panel on playbook detail

GET /api/playbooks/{id}/related proxies playbook_correlate using the
playbook's own tags as the query, filters out self, returns ≤5. The
PlaybookEditor mounts a small panel rendering the matches as clickable
links. Lets operators navigate by entity affinity instead of just by id.

Exports a strategies.Server.Correlate / .Playbook surface for the
launcher's in-process calls.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Editor — entity chip inputs

**Files:**
- Investigate: `investigate/frontend/components/wiki/WikiNodeEditor.tsx` (look for an *editable* chip input variant; the `ChipGroup` we already saw is read-only display)
- Decide path: extract a shared `EntityChipsInput` component, OR write a thin new one
- Create or modify: `investigate/frontend/components/EntityChipsInput.tsx` (shared)
- Modify: `investigate/frontend/components/PlaybookEditor.tsx` (mount the inputs)
- Possibly modify: wiki editor consumer (if migrating to the shared component)

- [ ] **Step 1: Inspect the wiki editor for an existing editable chip input**

```
grep -n "input\|onKeyDown\|onChange\|setServices\|push" /home/aegir/Documents/camunda/c1-plugins/investigate/frontend/components/wiki/WikiNodeEditor.tsx | head -40
```

If the file has an editable chip pattern (an input that adds to `services` on Enter), the extraction path is preferable: lift it into a shared `EntityChipsInput` component, migrate wiki to it, then reuse in PlaybookEditor. If only the read-only `ChipGroup` exists, build a new `EntityChipsInput` from scratch — small (input + chip list + add/remove handlers).

- [ ] **Step 2: Write a failing vitest unit test for the component (TDD: test before implementation)**

Create `investigate/frontend/components/EntityChipsInput.test.tsx`:

```tsx
/* @vitest-environment jsdom */

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { EntityChipsInput } from "./EntityChipsInput";

describe("EntityChipsInput", () => {
  it("adds a valid entity on Enter and clears the input", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "zeebe-broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith(["zeebe-broker"]);
  });

  it("rejects malformed names and shows an error", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Zeebe Broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText(/must be lowercase \+ hyphens/)).toBeTruthy();
  });

  it("rejects duplicates", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={["zeebe-broker"]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "zeebe-broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText(/already in the list/)).toBeTruthy();
  });

  it("removes an entity when the × button is clicked", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={["a", "b"]} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText("Remove a"));
    expect(onChange).toHaveBeenCalledWith(["b"]);
  });
});
```

If `@testing-library/react` isn't already a dev dep, you may need to install it (`npm i -D @testing-library/react`) — check existing frontend tests under `investigate/frontend/components/` for the convention.

- [ ] **Step 3: Run the test to verify it fails**

```
cd investigate/frontend && npm test -- --run EntityChipsInput
```

Expected: FAIL with "Cannot find module './EntityChipsInput'" (component doesn't exist yet).

- [ ] **Step 4: Implement the shared `EntityChipsInput` component**

Create `investigate/frontend/components/EntityChipsInput.tsx`:

```tsx
"use client";

import { useState } from "react";

const NAME_PATTERN = /^[a-z0-9][a-z0-9-]*$/;

type Props = {
  label: string;
  values: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
};

export function EntityChipsInput({ label, values, onChange, placeholder }: Props) {
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);

  const tryAdd = () => {
    const v = draft.trim();
    if (!v) return;
    if (!NAME_PATTERN.test(v)) {
      setError(`"${v}" must be lowercase + hyphens (^[a-z0-9][a-z0-9-]*$)`);
      return;
    }
    if (values.includes(v)) {
      setError(`"${v}" is already in the list`);
      return;
    }
    onChange([...values, v]);
    setDraft("");
    setError(null);
  };

  const remove = (v: string) => onChange(values.filter((x) => x !== v));

  return (
    <div className="space-y-1">
      <label className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</label>
      <div className="flex flex-wrap gap-1">
        {values.map((v) => (
          <span key={v} className="inline-flex items-center gap-1 rounded bg-slate-100 px-2 py-0.5 text-xs">
            <span className="font-mono">{v}</span>
            <button
              type="button"
              aria-label={`Remove ${v}`}
              className="text-slate-500 hover:text-slate-900"
              onClick={() => remove(v)}
            >×</button>
          </span>
        ))}
      </div>
      <input
        type="text"
        value={draft}
        placeholder={placeholder ?? "add entity…"}
        onChange={(e) => { setDraft(e.target.value); setError(null); }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            tryAdd();
          }
        }}
        onBlur={tryAdd}
        className="w-full rounded border border-slate-300 px-2 py-1 text-sm font-mono"
      />
      {error && <div className="text-xs text-rose-600">{error}</div>}
    </div>
  );
}
```

- [ ] **Step 5: Run the test to verify it passes**

```
cd investigate/frontend && npm test -- --run EntityChipsInput
```

Expected: all four cases PASS.

- [ ] **Step 6: Mount the inputs in `PlaybookEditor.tsx`**

Open `investigate/frontend/components/PlaybookEditor.tsx`. Find where the playbook's top-level fields are edited (id, symptom prose, description). Add a "Tags" section using the new component, wired to the playbook's `services` / `errors` / `symptoms` arrays via the editor's existing state setter (whatever pattern is local — likely a `useState<Playbook>` or a controlled form):

```tsx
import { EntityChipsInput } from "@/components/EntityChipsInput";

// inside the JSX, near the other top-level field editors:
<section aria-labelledby="tags-heading" className="space-y-2">
  <h3 id="tags-heading" className="text-sm font-semibold text-slate-700">Entity tags</h3>
  <p className="text-xs text-slate-500">
    Canonical service / error / symptom names used by playbook_correlate. Use the same vocabulary
    as the wiki vault (lowercase, hyphens, e.g. <code className="font-mono">zeebe-broker</code>).
  </p>
  <EntityChipsInput
    label="services"
    values={playbook.services ?? []}
    onChange={(next) => setPlaybook({ ...playbook, services: next })}
  />
  <EntityChipsInput
    label="errors"
    values={playbook.errors ?? []}
    onChange={(next) => setPlaybook({ ...playbook, errors: next })}
  />
  <EntityChipsInput
    label="symptoms"
    values={playbook.symptoms ?? []}
    onChange={(next) => setPlaybook({ ...playbook, symptoms: next })}
  />
</section>
```

The local state-setter shape (`setPlaybook` here) may differ in the actual editor — adapt to whatever the existing top-level setters look like. The `Playbook` TS type may need extending to include the three optional arrays; if so, modify it (likely in `lib/api.ts` or a shared types file).

- [ ] **Step 7: Verify save path round-trips the new fields**

The launcher's existing `POST /api/playbooks/{id}` (line 142 of `handlers.go`) writes the playbook body via `WriteUserPlaybook`, which calls `renderPlaybookYAML(pb)`. Because we added `services` / `errors` / `symptoms` to the `Playbook` struct with `yaml:"...,omitempty"` tags, the marshal preserves them automatically. No backend change needed — but verify by adding an integration test or manual smoke:

Run the dev server, open a playbook in the editor, add an entity chip, save, then `cat ~/.config/c1/plugins/investigate/user-playbooks/<type>/<id>.yaml` (or your local equivalent) and confirm the tag appears in the YAML. (Path may differ — check `investigate/internal/server/playbooks.go` for the configured user dir.)

- [ ] **Step 8: If extracting from wiki — migrate the wiki editor to use the shared component**

If step 1 found an editable chip variant in `WikiNodeEditor.tsx`, replace that local component with `<EntityChipsInput …>` in the same PR. This is the "second consumer paid the extraction cost" rule from CLAUDE.md. If the wiki only had `ChipGroup` (read-only), there's no migration to do — note in the commit message and move on.

- [ ] **Step 9: Run frontend tests + typecheck + build**

```
cd investigate/frontend && npm test -- --run
npm run typecheck
npm run build
cd ../..
```

Expected: all PASS.

- [ ] **Step 10: Smoke-test in dev server**

Per CLAUDE.md: start dev server, open the playbook editor, exercise the entity inputs (add valid, add malformed → see error, add duplicate → see error, remove via ×). Save and confirm the YAML round-trip.

```
cd investigate/frontend && npm run dev
# navigate to /playbooks?playbook=<some-id>
```

- [ ] **Step 11: Commit**

```bash
git add investigate/frontend/components/EntityChipsInput.tsx investigate/frontend/components/EntityChipsInput.test.tsx investigate/frontend/components/PlaybookEditor.tsx investigate/frontend/lib/api.ts
# also stage the wiki editor change if migrated
git commit -m "$(cat <<'EOF'
feat(investigate/frontend): entity chip inputs for playbook tags

New shared EntityChipsInput component (validates names client-side
against ^[a-z0-9][a-z0-9-]*$, mirroring the backend regex). Mounted
in PlaybookEditor for services / errors / symptoms. YAML round-trip
falls out for free via the existing playbook save path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review (run before opening a PR)

1. **Tag the existing system playbooks** (optional but valuable) — once the tool ships, walk `investigate/system/*.yaml` and the upstream domain playbooks and add tags where the playbook clearly addresses a known canonical entity. This is *not* a code change — it's authoring work. Skip in this plan; do it incrementally per playbook touch.

2. **Spec coverage sanity check** — every section of the spec maps to a task:
   - Frontmatter additions → Task 2
   - Shared entity package → Task 1
   - Scoring & lifting → Task 3
   - Tool surface → Task 3
   - Walker integration → Task 4
   - Editor: related panel → Task 5
   - Editor: chip inputs → Task 6
   - Testing — interleaved per task (TDD)

3. **Verify the wire test name** — Task 3 step 7 mentions a "wire test" in `mcp/cmd/`. If your repo's wire test lives elsewhere (e.g. `mcp/internal/toolspec/`), adapt the command. Run `go test ./...` regardless; the test will surface itself.

4. **Run the full backend + frontend gauntlet before the final commit:**

```
go test -race -count=1 ./...
cd investigate/frontend && npm test -- --run && npm run typecheck && npm run build && cd ../..
```
