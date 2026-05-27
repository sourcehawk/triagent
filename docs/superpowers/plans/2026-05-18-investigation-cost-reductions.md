# Investigation Cost Reductions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the four cost reductions from `docs/superpowers/specs/2026-05-18-investigation-cost-reductions-design.md` — `list_namespaces` trim, capture-tail trim + Haiku sub-agent dispatch, `list_playbooks` trim + filter, walker auto-advance — plus the cross-cutting profile-configurable model defaults that makes Haiku dispatch possible.

**Architecture:** Each task lands one green-on-its-own commit. Tasks 1–5 are independent and could ship in any order. Tasks 6–9 form the profile-configurable-models bundle (6 lands the schema, 7+8 land the pass-throughs, 9 wires the launcher). Tasks 10–14 land the capture-tail dispatch (10 enables single-shot validation, 11 trims the system playbooks so the existing walker path is shorter, 12+13+14 introduce dispatch-mode playbooks).

**Tech Stack:** Go 1.x (single module `github.com/sourcehawk/triagent`); `gopkg.in/yaml.v3` for YAML; `github.com/modelcontextprotocol/go-sdk` for MCP servers; `github.com/stretchr/testify` for assertions; `k8s.io/client-go` (fake) for k8s tests. Frontend untouched — pure backend work.

**Common conventions (apply to every task):**
- Run `go test -race -count=1 ./<package>/...` after every implementation step. The race flag is non-negotiable per CLAUDE.md.
- Commits use `feat(<area>): ...` / `test(<area>): ...` / `refactor(<area>): ...`. Areas mirror module paths (`pkg/mcp/k8s`, `internal/profile`, etc.).
- Stage files by exact name. **Never** `git add -A` or `git add .`. **Never** `--no-verify`.
- Commit message format: HEREDOC with the `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` trailer.

---

## File Structure

**New files (created during this plan):**
- `internal/profile/namespace_derivation.go` — render a namespace-hint string from alert payload fields per the profile's template or rules.
- `internal/profile/namespace_derivation_test.go`
- `pkg/mcp/strategies/dispatch.go` — sub-agent dispatch entry point invoked by `walk_playbook` when the playbook is `dispatch: subagent`.
- `pkg/mcp/strategies/dispatch_test.go`
- `pkg/mcp/strategies/dispatch_prompt.go` — assemble the sub-agent prompt from a playbook + parent session findings + summary + operator refinement.
- `pkg/mcp/strategies/dispatch_prompt_test.go`

**Modified files:**
- `pkg/mcp/k8s/tools_namespaces.go` — trim default response.
- `pkg/mcp/k8s/tools_namespaces_test.go` — new cases for the trim.
- `internal/profile/profile.go` — add `NamespaceDerivation` and `Models` structs and fields.
- `internal/profile/profile_test.go` — load tests for both new blocks.
- `internal/sessions/session.go` — render namespace hint into `AppendSystemPrompt`; pass `Models.Investigation` to `claude.SessionOpts.Model`.
- `internal/server/manager.go` — pass `Models.Investigation` to operator-agent claude session (auto-mode).
- `internal/claude/session.go` — add `Model` to `SessionOpts` and pass `--model` in `baseArgs`.
- `internal/claude/session_test.go` (created if missing) — assert `--model` lands in argv.
- `pkg/mcp/subagent/subagent.go` — add `Model` to `Options` and pass `--model`.
- `pkg/mcp/subagent/subagent_test.go` — assert `--model` reaches the spawned argv via a stub.
- `pkg/mcp/strategies/server.go` — `listPlaybooks` filter + trim; `applyAdvance` auto-advance; `walkPlaybook` dispatch branch.
- `pkg/mcp/strategies/playbook.go` — `PlaybookSummary` trim; `Playbook.Dispatch` field; parser accepts `dispatch: subagent`.
- `pkg/mcp/strategies/walker.go` — `isPureTransition` helper.
- `pkg/mcp/strategies/walker_test.go` — auto-advance tests.
- `pkg/mcp/strategies/server_step_complete_test.go` — auto-advance through step_complete.
- `pkg/mcp/strategies/tools_proposal.go` — add `ValidationErrors []string` to `proposePlaybookDraftOut`, populate on validation failure (don't error the call).
- `system/wiki_proposal.yaml` — remove standalone `wiki_list_entities` / `wiki_get` micro-nodes whose content gets folded into the dispatch prompt; add `dispatch: subagent` (Task 14).
- `system/playbook_proposal.yaml` — remove `read_schema` and `draft_and_validate`'s `validate_playbook` call; fold guidance into adjacent nodes' descriptions; add `dispatch: subagent` (Task 14).

---

### Task 1: `list_namespaces` trim default response

**Files:**
- Modify: `pkg/mcp/k8s/tools_namespaces.go`
- Modify: `pkg/mcp/k8s/tools_namespaces_test.go`

The default response drops `Labels` and `CreationTimestamp` (~80 KB on prod clusters). New `IncludeLabels bool` input gates the full shape. Existing `Filter` + `Limit` behavior unchanged.

- [ ] **Step 1: Add failing test for default-trim behaviour**

Open `pkg/mcp/k8s/tools_namespaces_test.go`. After the existing `TestListNamespaces_DefaultLimit` add:

```go
func TestListNamespaces_DefaultOmitsLabelsAndTimestamp(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKitWithLabels(t, map[string]map[string]string{
		"abc-zeebe": {"team": "data", "env": "prod"},
	})
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Empty(t, out.Items[0].Labels, "labels must be omitted by default")
	assert.Empty(t, out.Items[0].CreationTimestamp, "creationTimestamp must be omitted by default")
	assert.Equal(t, "abc-zeebe", out.Items[0].Name)
}

func TestListNamespaces_IncludeLabelsReturnsLabelsAndTimestamp(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKitWithLabels(t, map[string]map[string]string{
		"abc-zeebe": {"team": "data"},
	})
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{IncludeLabels: true})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, map[string]string{"team": "data"}, out.Items[0].Labels)
	assert.NotEmpty(t, out.Items[0].CreationTimestamp, "creationTimestamp must be set when labels requested")
}
```

Then add the helper at the bottom of the file:

```go
func newNamespacesKitWithLabels(t *testing.T, labels map[string]map[string]string) *ToolKit {
	t.Helper()
	objs := make([]runtime.Object, 0, len(labels))
	for name, lbls := range labels {
		objs = append(objs, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Labels:            lbls,
				CreationTimestamp: metav1.Now(),
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})
	}
	cs := fake.NewSimpleClientset(objs...)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})
	return kit
}
```

- [ ] **Step 2: Run tests; expect compile error on `IncludeLabels`**

```bash
go test -race -count=1 ./pkg/mcp/k8s/... -run TestListNamespaces_DefaultOmitsLabelsAndTimestamp
```

Expected: build failure — `ListNamespacesInput` has no field `IncludeLabels`.

- [ ] **Step 3: Add `IncludeLabels` to the input and gate label population**

Edit `pkg/mcp/k8s/tools_namespaces.go`. Replace the input struct definition:

```go
type ListNamespacesInput struct {
	Filter        string `json:"filter,omitempty" jsonschema:"Optional case-sensitive substring matched against the namespace name. Empty returns the first <limit> namespaces."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Max results; defaults to 100, hard cap 500."`
	IncludeLabels bool   `json:"include_labels,omitempty" jsonschema:"Optional. When true, return labels and creationTimestamp for each namespace. Default false — names + phase only, which is ~10x smaller on prod clusters."`
}
```

Inside `listNamespaces`, replace the per-namespace `info` construction:

```go
		info := NamespaceInfo{
			Name:  ns.Name,
			Phase: string(ns.Status.Phase),
		}
		if in.IncludeLabels {
			info.Labels = ns.Labels
			if ts := ns.CreationTimestamp; !ts.IsZero() {
				info.CreationTimestamp = ts.UTC().Format("2006-01-02T15:04:05Z")
			}
		}
```

- [ ] **Step 4: Run the new tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/k8s/... -run 'TestListNamespaces'
```

Expected: all `TestListNamespaces_*` cases pass.

- [ ] **Step 5: Run the full k8s package; confirm no regression**

```bash
go test -race -count=1 ./pkg/mcp/k8s/...
```

Expected: PASS across the package.

- [ ] **Step 6: Commit**

```bash
git add pkg/mcp/k8s/tools_namespaces.go pkg/mcp/k8s/tools_namespaces_test.go
git commit -m "$(cat <<'EOF'
feat(k8s): trim list_namespaces default response

Drops labels + creationTimestamp from the default shape and gates them
behind a new include_labels input. On prod clusters with hundreds of
namespaces this cuts the response from ~84 KB to ~5 KB. Once cached
into the prompt, the saving compounds across every subsequent turn.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Profile `namespace_derivation` field + render helper

**Files:**
- Create: `internal/profile/namespace_derivation.go`
- Create: `internal/profile/namespace_derivation_test.go`
- Modify: `internal/profile/profile.go`
- Modify: `internal/profile/profile_test.go`

Adds an optional `namespace_derivation` block to `profile.yaml` with two shapes: `template` (simple ${field-name} substitution) and `rules` (list of when/template pairs evaluated in order). Helper renders zero-or-more namespace strings from an alert-payload field map. Server wiring lands in Task 3.

- [ ] **Step 1: Create failing render-helper tests**

Create `internal/profile/namespace_derivation_test.go`:

```go
package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderNamespaceHints_EmptyConfigReturnsNil(t *testing.T) {
	t.Parallel()
	got := RenderNamespaceHints(NamespaceDerivation{}, map[string]string{"cluster": "x"})
	assert.Empty(t, got)
}

func TestRenderNamespaceHints_TemplateSubstitutes(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{Template: "${project_id}-${component}"}
	got := RenderNamespaceHints(cfg, map[string]string{
		"project_id": "saas-platform-aws",
		"component":  "zeebe",
	})
	assert.Equal(t, []string{"saas-platform-aws-zeebe"}, got)
}

func TestRenderNamespaceHints_TemplateMissingFieldYieldsNothing(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{Template: "${project_id}-${component}"}
	got := RenderNamespaceHints(cfg, map[string]string{"project_id": "x"})
	// component missing -> template leaves ${component} in the output, which we treat as a failed render
	assert.Empty(t, got)
}

func TestRenderNamespaceHints_RulesFirstMatchWins(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'CrossplaneHighNumberOfManagedResourceNotReady'", Template: "crossplane-system"},
			{When: "${project_id} != ''", Template: "${project_id}-zeebe"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{
		"alert_kind": "CrossplaneHighNumberOfManagedResourceNotReady",
		"project_id": "p",
	})
	assert.Equal(t, []string{"crossplane-system"}, got)
}

func TestRenderNamespaceHints_RulesFallthroughToSecond(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'NoMatch'", Template: "nope"},
			{When: "${project_id} != ''", Template: "${project_id}-zeebe"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{
		"alert_kind": "Other",
		"project_id": "saas",
	})
	assert.Equal(t, []string{"saas-zeebe"}, got)
}

func TestRenderNamespaceHints_RulesNoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'NoMatch'", Template: "nope"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{"alert_kind": "Other"})
	assert.Empty(t, got)
}
```

- [ ] **Step 2: Run tests; expect compile error on missing types**

```bash
go test -race -count=1 ./internal/profile/... -run TestRenderNamespaceHints
```

Expected: build failure — `NamespaceDerivation`, `NamespaceRule`, `RenderNamespaceHints` not defined.

- [ ] **Step 3: Add the types to `internal/profile/profile.go`**

After the `Models` struct location (or near the bottom of the type declarations) — add to the `Profile` struct:

```go
	// NamespaceDerivation, when set, lets the launcher pre-compute namespace
	// hint(s) from the alert payload and inject them into the session's
	// system prompt at preflight, sparing the agent from calling
	// list_namespaces in the common case. See namespace_derivation.go for
	// the rendering semantics.
	NamespaceDerivation NamespaceDerivation `yaml:"namespace_derivation,omitempty"`
```

Then add the type declarations near the bottom (before the `Parse` function):

```go
// NamespaceDerivation configures alert-payload → namespace string rendering.
// Either Template (simple form) or Rules (branchy form) is honoured;
// Rules takes precedence when both are set.
type NamespaceDerivation struct {
	Template string          `yaml:"template,omitempty"`
	Rules    []NamespaceRule `yaml:"rules,omitempty"`
}

// NamespaceRule is one when/template pair evaluated top-to-bottom; the first
// rule whose `when` predicate matches the alert-payload field map renders
// `template`. Supported predicate shapes (kept deliberately small):
//   - `${field} == 'literal'`
//   - `${field} != ''`
type NamespaceRule struct {
	When     string `yaml:"when"`
	Template string `yaml:"template"`
}
```

- [ ] **Step 4: Create `internal/profile/namespace_derivation.go` with the renderer**

```go
package profile

import (
	"strings"
)

// RenderNamespaceHints applies the profile's NamespaceDerivation against a
// flat string-map of alert-payload fields. Returns zero or more hint
// strings. Behaviour:
//   - Empty config → nil.
//   - Rules takes precedence over Template when both are set.
//   - Rules: top-to-bottom; first matching `when` wins. Unsupported predicate
//     shapes silently skip (don't match). Missing fields in a template render
//     leave the placeholder intact, which the caller treats as a failed render
//     and drops.
//   - Template alone: rendered once.
func RenderNamespaceHints(cfg NamespaceDerivation, fields map[string]string) []string {
	if len(cfg.Rules) > 0 {
		for _, r := range cfg.Rules {
			if !matchesWhen(r.When, fields) {
				continue
			}
			if out, ok := renderTemplate(r.Template, fields); ok {
				return []string{out}
			}
			return nil
		}
		return nil
	}
	if cfg.Template == "" {
		return nil
	}
	out, ok := renderTemplate(cfg.Template, fields)
	if !ok {
		return nil
	}
	return []string{out}
}

// renderTemplate substitutes ${field-name} placeholders. Returns (rendered,
// true) when every placeholder resolved to a non-empty value; otherwise
// (zero-value, false). Leaving placeholders intact would be worse than
// emitting no hint.
func renderTemplate(tmpl string, fields map[string]string) (string, bool) {
	out := tmpl
	for k, v := range fields {
		out = strings.ReplaceAll(out, "${"+k+"}", v)
	}
	if strings.Contains(out, "${") {
		return "", false
	}
	return out, true
}

// matchesWhen is a minimal predicate evaluator. Supports two shapes:
//   - "${field} == 'literal'"
//   - "${field} != ''"
// Anything else is treated as non-matching. Kept small on purpose;
// profiles needing richer logic should compose multiple rules.
func matchesWhen(when string, fields map[string]string) bool {
	when = strings.TrimSpace(when)
	// `${field} == 'literal'`
	if i := strings.Index(when, " == "); i > 0 {
		left := strings.TrimSpace(when[:i])
		right := strings.TrimSpace(when[i+len(" == "):])
		if !strings.HasPrefix(left, "${") || !strings.HasSuffix(left, "}") {
			return false
		}
		if !strings.HasPrefix(right, "'") || !strings.HasSuffix(right, "'") {
			return false
		}
		field := left[2 : len(left)-1]
		lit := right[1 : len(right)-1]
		return fields[field] == lit
	}
	// `${field} != ''`
	if i := strings.Index(when, " != "); i > 0 {
		left := strings.TrimSpace(when[:i])
		right := strings.TrimSpace(when[i+len(" != "):])
		if !strings.HasPrefix(left, "${") || !strings.HasSuffix(left, "}") {
			return false
		}
		if right != "''" {
			return false
		}
		field := left[2 : len(left)-1]
		return fields[field] != ""
	}
	return false
}
```

- [ ] **Step 5: Run helper tests; expect PASS**

```bash
go test -race -count=1 ./internal/profile/... -run TestRenderNamespaceHints
```

Expected: all six render tests pass.

- [ ] **Step 6: Add a load test to `internal/profile/profile_test.go`**

Append:

```go
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
	p, err := Parse(strings.NewReader(src))
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
	p, err := Parse(strings.NewReader(src))
	require.NoError(t, err)
	require.Len(t, p.NamespaceDerivation.Rules, 2)
	assert.Equal(t, "${alert_kind} == 'X'", p.NamespaceDerivation.Rules[0].When)
	assert.Equal(t, "x-system", p.NamespaceDerivation.Rules[0].Template)
}
```

Add a `"strings"` import to the test file if not already present.

- [ ] **Step 7: Run profile tests; expect PASS**

```bash
go test -race -count=1 ./internal/profile/...
```

Expected: PASS — both load tests and all six render tests.

- [ ] **Step 8: Commit**

```bash
git add internal/profile/namespace_derivation.go internal/profile/namespace_derivation_test.go internal/profile/profile.go internal/profile/profile_test.go
git commit -m "$(cat <<'EOF'
feat(profile): add namespace_derivation block + renderer

Lets a profile pre-compute namespace hint(s) from the alert payload
(template form, or rules with a small when-predicate vocabulary).
Server wiring lands in the next commit; this one is the schema +
the standalone renderer with full test coverage.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Render namespace hint into the investigation session prompt

**Files:**
- Modify: `internal/sessions/session.go`
- Modify: `internal/sessions/session_test.go` (create if missing)

When the launcher constructs the investigation `claude.Session`, render `profile.NamespaceDerivation` against the alert-payload fields parsed from `opts.Notes` and append `Suggested namespace(s): <one>, <two>` to the `AppendSystemPrompt`. Behaviour is no-op when the profile has no derivation block or no fields render successfully.

- [ ] **Step 1: Check whether `internal/sessions/session_test.go` exists**

```bash
ls internal/sessions/session_test.go 2>/dev/null || echo "MISSING"
```

If MISSING, create the file with a package declaration and stdlib imports as the tests below use.

- [ ] **Step 2: Add failing test for namespace-hint suffix**

Append (or create with header) `internal/sessions/session_test.go`:

```go
package sessions

import (
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/stretchr/testify/assert"
)

func TestRenderSystemPromptAddendum_NoProfileHintAppendsNothingExtra(t *testing.T) {
	t.Parallel()
	got := renderSystemPromptAddendum(systemPromptAddendum, profile.NamespaceDerivation{}, nil)
	assert.Equal(t, systemPromptAddendum, got)
}

func TestRenderSystemPromptAddendum_TemplateRendersOneHint(t *testing.T) {
	t.Parallel()
	cfg := profile.NamespaceDerivation{Template: "${project_id}-zeebe"}
	fields := map[string]string{"project_id": "saas"}
	got := renderSystemPromptAddendum(systemPromptAddendum, cfg, fields)
	assert.True(t, strings.Contains(got, systemPromptAddendum), "must preserve the base addendum")
	assert.True(t, strings.Contains(got, "Suggested namespace(s): saas-zeebe"), "must append the rendered hint")
}

func TestRenderSystemPromptAddendum_NoMatchAppendsNothing(t *testing.T) {
	t.Parallel()
	cfg := profile.NamespaceDerivation{Template: "${absent}-zeebe"}
	got := renderSystemPromptAddendum(systemPromptAddendum, cfg, map[string]string{"other": "x"})
	assert.Equal(t, systemPromptAddendum, got)
}
```

- [ ] **Step 3: Run test; expect compile error**

```bash
go test -race -count=1 ./internal/sessions/... -run TestRenderSystemPromptAddendum
```

Expected: failure — `renderSystemPromptAddendum` does not exist.

- [ ] **Step 4: Implement the renderer + wire into `New`**

Edit `internal/sessions/session.go`. Add after the existing `systemPromptAddendum` constant:

```go
// renderSystemPromptAddendum appends namespace hints from the profile to the
// base addendum. Empty / no-match cases return the base unchanged. The
// rendered line uses a compact format that the investigation agent reads as
// a starting point — when present, the agent should skip list_namespaces
// in the common case.
func renderSystemPromptAddendum(base string, cfg profile.NamespaceDerivation, fields map[string]string) string {
	hints := profile.RenderNamespaceHints(cfg, fields)
	if len(hints) == 0 {
		return base
	}
	return base + "\n\nSuggested namespace(s): " + strings.Join(hints, ", ")
}
```

Add `"strings"` to the import block if not already present.

Then locate the `claude.NewSession(...)` call inside `New(opts Options)`. Replace the `AppendSystemPrompt: systemPromptAddendum,` line with:

```go
			AppendSystemPrompt: renderSystemPromptAddendum(systemPromptAddendum, opts.Profile.NamespaceDerivation, opts.alertPayloadFields()),
```

Find the `Options` struct in the same file (or wherever it's declared). Add a method on it:

```go
// alertPayloadFields extracts alert-payload fields from the operator's notes
// for namespace_derivation. The minimal v1 implementation reads two keys
// the launcher already parses upstream — alert_kind and project_id. Empty
// when notes are unstructured. Future signal MCPs can grow this map.
func (o Options) alertPayloadFields() map[string]string {
	out := map[string]string{}
	if o.AlertKind != "" {
		out["alert_kind"] = o.AlertKind
	}
	if o.ProjectID != "" {
		out["project_id"] = o.ProjectID
	}
	if o.Cluster != "" {
		out["cluster"] = o.Cluster
	}
	return out
}
```

If `Options` does not yet carry `AlertKind`, `ProjectID`, or `Cluster` fields, add them as `string`:

```go
	// AlertKind is the canonical alert title (when known from the source MCP).
	AlertKind string
	// ProjectID is the deployment / tenant identifier (when known).
	ProjectID string
	// Cluster is the kube-context / cluster name (when known).
	Cluster string
```

Tests don't construct an `Options` value yet — they call the renderer directly — so this struct addition won't break tests, but it does need callers to set the fields. The caller is `internal/server` constructing `sessions.Options`; existing call sites compile fine with these new zero-valued fields.

- [ ] **Step 5: Run sessions tests; expect PASS**

```bash
go test -race -count=1 ./internal/sessions/... -run TestRenderSystemPromptAddendum
```

Expected: PASS for all three.

- [ ] **Step 6: Run the whole sessions + server packages; confirm callers still compile**

```bash
go test -race -count=1 ./internal/sessions/... ./internal/server/...
```

Expected: PASS. If `server` fails because the `Options` literal expects new fields, that's *not* an error — Go struct literals with field names tolerate missing optional strings.

- [ ] **Step 7: Commit**

```bash
git add internal/sessions/session.go internal/sessions/session_test.go
git commit -m "$(cat <<'EOF'
feat(sessions): render namespace_derivation hint into session prompt

When the profile has a namespace_derivation block and the alert payload
exposes the required fields, append a "Suggested namespace(s): …" line
to the investigation agent's system prompt. The agent reads this as a
starting point and skips list_namespaces in the common case.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `list_playbooks` trim + `filter`

**Files:**
- Modify: `pkg/mcp/strategies/server.go`
- Modify: `pkg/mcp/strategies/playbook.go`
- Modify: `pkg/mcp/strategies/walker_test.go`

Adds `filter` (case-insensitive substring on id+symptom) and `include_description` to `list_playbooks`. Default response now omits the longer `description:` prose. Tool description gets a tightened summary.

- [ ] **Step 1: Add failing tests**

Open `pkg/mcp/strategies/walker_test.go` (or whichever file already exercises `listPlaybooks`; if it's `walker_test.go`, append). Add:

```go
func TestListPlaybooks_FilterMatchesIDOrSymptomCaseInsensitive(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{Filter: "CROSSPLANE"})
	require.NoError(t, err)
	require.NotEmpty(t, out.Playbooks)
	for _, pb := range out.Playbooks {
		assert.True(t,
			strings.Contains(strings.ToLower(pb.ID), "crossplane") ||
				strings.Contains(strings.ToLower(pb.Symptom), "crossplane"),
			"filtered entry %q/%q should match", pb.ID, pb.Symptom,
		)
	}
}

func TestListPlaybooks_DefaultOmitsDescription(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{})
	require.NoError(t, err)
	require.NotEmpty(t, out.Playbooks)
	for _, pb := range out.Playbooks {
		assert.Empty(t, pb.Description, "description must be omitted by default for %q", pb.ID)
	}
}

func TestListPlaybooks_IncludeDescriptionReturnsDescription(t *testing.T) {
	t.Parallel()
	srv := newServerWithFixtures(t)
	ctx := context.Background()
	_, out, err := srv.listPlaybooks(ctx, nil, listPlaybooksIn{IncludeDescription: true})
	require.NoError(t, err)
	// At least one fixture playbook should carry a description.
	found := false
	for _, pb := range out.Playbooks {
		if pb.Description != "" {
			found = true
			break
		}
	}
	assert.True(t, found, "at least one fixture playbook should have a non-empty description when include_description=true")
}
```

If `newServerWithFixtures` does not exist, identify the test helper already used in the file (likely a function that constructs a `*Server` with a fixed playbook set). Reuse it verbatim. If the existing test file uses a one-off harness, factor it into `newServerWithFixtures(t *testing.T) *Server` so the new cases can share it.

Add `"strings"` to the test file imports if missing.

- [ ] **Step 2: Run tests; expect compile failure on missing fields**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run 'TestListPlaybooks_'
```

Expected: build failure — `listPlaybooksIn` has no `Filter` or `IncludeDescription`; `PlaybookSummary` has no `Description` field (it does, just confirm).

- [ ] **Step 3: Extend the input + handler**

Edit `pkg/mcp/strategies/server.go`. Replace the `listPlaybooksIn` type:

```go
type listPlaybooksIn struct {
	Type               string `json:"type,omitempty" jsonschema:"optional filter — 'investigation' (diagnostic playbooks for operator symptoms) or 'general' (meta/workflow). Omit for all."`
	Filter             string `json:"filter,omitempty" jsonschema:"optional case-insensitive substring matched against id + symptom. Empty returns the full set (after type filter)."`
	IncludeDescription bool   `json:"include_description,omitempty" jsonschema:"optional. When true, include the longer description prose for each entry. Default false — id + symptom + type only, which is ~5x smaller."`
}
```

Replace the body of `listPlaybooks`:

```go
func (s *Server) listPlaybooks(ctx context.Context, req *mcp.CallToolRequest, in listPlaybooksIn) (*mcp.CallToolResult, listPlaybooksOut, error) {
	return nil, listPlaybooksOut{
		Playbooks: summariesFiltered(s.playbooks, strings.TrimSpace(in.Type), strings.TrimSpace(in.Filter), in.IncludeDescription),
	}, nil
}
```

Tighten the registered tool description (replace the `Description:` value on the `list_playbooks` `mcp.AddTool` call):

```go
		Description: "Return the available playbooks. Each entry has an id (use it for walk_playbook), a one-line symptom (the entry-point trigger), and a type. Pass type=\"investigation\" when triaging an operator-reported symptom or type=\"general\" for meta/workflow playbooks. Pass filter=\"<substring>\" to narrow by id or symptom (case-insensitive). Pass include_description=true when you need the longer prose (rarely needed for routing). Defaults are small — prefer narrow filters over full listings.",
```

- [ ] **Step 4: Extend `summariesFiltered` in `playbook.go`**

Edit `pkg/mcp/strategies/playbook.go`. Replace `summariesFiltered`:

```go
// summariesFiltered returns playbook summaries sorted by ID, narrowed by
// optional type and substring filters. When includeDescription is false the
// Description field is left empty in every returned summary.
func summariesFiltered(books map[string]*Playbook, typeFilter, nameFilter string, includeDescription bool) []PlaybookSummary {
	want := strings.ToLower(strings.TrimSpace(nameFilter))
	out := make([]PlaybookSummary, 0, len(books))
	for _, pb := range books {
		if typeFilter != "" && pb.EffectiveType() != typeFilter {
			continue
		}
		if want != "" {
			if !strings.Contains(strings.ToLower(pb.ID), want) &&
				!strings.Contains(strings.ToLower(pb.Symptom), want) {
				continue
			}
		}
		summary := PlaybookSummary{
			ID:      pb.ID,
			Symptom: pb.Symptom,
			Type:    pb.EffectiveType(),
		}
		if includeDescription {
			summary.Description = pb.Description
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
```

Add `"strings"` to the imports of `playbook.go` if not already present.

- [ ] **Step 5: Run the new and existing list_playbooks tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run 'TestListPlaybooks|listPlaybooks'
```

Expected: PASS.

- [ ] **Step 6: Run the whole strategies package; confirm no regression**

```bash
go test -race -count=1 ./pkg/mcp/strategies/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/mcp/strategies/server.go pkg/mcp/strategies/playbook.go pkg/mcp/strategies/walker_test.go
git commit -m "$(cat <<'EOF'
feat(strategies): list_playbooks filter + trim default

Adds a case-insensitive id+symptom substring filter and gates the
longer description prose behind include_description. Default response
shrinks from ~29 KB to ~5 KB on a 15-playbook catalog; filtered calls
return ~500 bytes when the agent knows roughly what it's looking for.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Walker auto-advance through pure-transition nodes

**Files:**
- Modify: `pkg/mcp/strategies/walker.go`
- Modify: `pkg/mcp/strategies/server.go`
- Modify: `pkg/mcp/strategies/server_step_complete_test.go`

When the walker advances into a node with no `expected_findings`, no `suggested_calls`, exactly one `next` branch, no `delegate_to`/`handoff`/`terminal_advice`, it transparently continues to the single next target. Bounded at 10 hops to surface malformed loops. Response gains an `auto_advanced_through` array listing the skipped node ids.

- [ ] **Step 1: Add the `isPureTransition` helper to `walker.go`**

This step is a refactor of intent (no behaviour change yet). At the bottom of `pkg/mcp/strategies/walker.go`:

```go
// isPureTransition reports whether a node contributes no agent-facing work:
// no expected findings, no suggested calls, exactly one next branch, no
// delegate_to / handoff / terminal_advice. The walker can transparently
// advance past such nodes — the agent's only legal action on reaching one
// was step_complete{goto: <single_next>}, so skipping the round-trip is
// correctness-preserving.
func isPureTransition(n Node) bool {
	return len(n.ExpectedFindings) == 0 &&
		len(n.SuggestedCalls) == 0 &&
		len(n.Next) == 1 &&
		n.DelegateTo == "" &&
		len(n.Handoff) == 0 &&
		n.TerminalAdvice == ""
}
```

(If the `Node` struct fields differ slightly — e.g. the project uses a pointer slice for `Handoff` — adjust the predicate to match the canonical zero-value check pattern used in `applyAdvance`.)

- [ ] **Step 2: Add the failing test for `step_complete` auto-advance**

Edit `pkg/mcp/strategies/server_step_complete_test.go`. Add:

```go
func TestStepComplete_AutoAdvancesThroughPureTransitionNode(t *testing.T) {
	t.Parallel()
	srv := newServerWithAutoAdvanceFixture(t)
	ctx := context.Background()

	// Walk into the fixture; entrypoint is "A" with one branch to "B".
	_, walkOut, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: "auto_advance_fixture"})
	require.NoError(t, err)
	require.Equal(t, "A", walkOut.Step.NodeID)
	sessID := walkOut.SessionID

	// step_complete{goto: B}. B is pure-transition (one next to C); the walker
	// should auto-advance to C and report B in auto_advanced_through.
	_, scOut, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: sessID,
		Goto:      "B",
	})
	require.NoError(t, err)
	assert.Equal(t, "C", scOut.Step.NodeID, "walker should auto-advance past B to C")
	assert.Equal(t, []string{"B"}, scOut.AutoAdvancedThrough, "should report B as skipped")
}

func TestStepComplete_AutoAdvanceLoopGuard(t *testing.T) {
	t.Parallel()
	srv := newServerWithLoopingFixture(t)
	ctx := context.Background()

	_, walkOut, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: "auto_advance_loop_fixture"})
	require.NoError(t, err)
	sessID := walkOut.SessionID

	// First step_complete trips the loop guard. The call returns a result
	// rather than spinning; the response carries the last-reached node id.
	res, _, err := srv.stepComplete(ctx, nil, stepCompleteIn{
		SessionID: sessID,
		Goto:      "L1",
	})
	require.NoError(t, err)
	require.NotNil(t, res, "loop guard should surface an error result")
	// Body shape: any error result the server already uses; assertion above is
	// the load-bearing one (we just must not loop forever).
}
```

Append fixture helpers (after the test functions or in a `_fixtures` helper block):

```go
func newServerWithAutoAdvanceFixture(t *testing.T) *Server {
	t.Helper()
	pb := &Playbook{
		ID:         "auto_advance_fixture",
		Symptom:    "auto-advance fixture",
		Entrypoint: "A",
		Nodes: map[string]Node{
			"A": {ID: "A", Description: "real work", ExpectedFindings: []string{"k"}, Next: []Branch{{Condition: "always", Goto: "B"}}},
			"B": {ID: "B", Description: "transient", Next: []Branch{{Condition: "always", Goto: "C"}}},
			"C": {ID: "C", Description: "real work again", ExpectedFindings: []string{"j"}, Next: []Branch{{Condition: "done", Goto: "D"}}},
			"D": {ID: "D", Description: "terminal", TerminalAdvice: "done"},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	return srv
}

func newServerWithLoopingFixture(t *testing.T) *Server {
	t.Helper()
	pb := &Playbook{
		ID:         "auto_advance_loop_fixture",
		Symptom:    "loop fixture",
		Entrypoint: "ENTRY",
		Nodes: map[string]Node{
			"ENTRY": {ID: "ENTRY", Description: "entry", ExpectedFindings: []string{"k"}, Next: []Branch{{Condition: "always", Goto: "L1"}}},
			"L1":    {ID: "L1", Description: "loop", Next: []Branch{{Condition: "always", Goto: "L2"}}},
			"L2":    {ID: "L2", Description: "loop", Next: []Branch{{Condition: "always", Goto: "L1"}}},
		},
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	return srv
}
```

`newEmptyServer(t)` should already exist; if not, replicate whatever harness the existing `server_step_complete_test.go` uses to build a `*Server` with no playbooks and an in-memory session store. The harness is small (literal struct fill); reuse the existing pattern verbatim.

- [ ] **Step 3: Run tests; expect failure (auto-advance not implemented)**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run 'TestStepComplete_AutoAdvance'
```

Expected: failure. `TestStepComplete_AutoAdvancesThroughPureTransitionNode` returns `step.node_id == "B"` instead of `"C"`. `TestStepComplete_AutoAdvanceLoopGuard` may hang or also fail.

If the hang risk is real, gate with a Go test timeout:

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run 'TestStepComplete_AutoAdvance' -timeout 30s
```

- [ ] **Step 4: Implement auto-advance in `applyAdvance`**

Edit `pkg/mcp/strategies/server.go`. Add the const:

```go
const autoAdvanceMaxHops = 10
```

Replace `stepCompleteOut`:

```go
type stepCompleteOut struct {
	Step                stepView        `json:"step"`
	DelegateReturned    *DelegateReturn `json:"delegate_returned,omitempty"`
	AutoAdvancedThrough []string        `json:"auto_advanced_through,omitempty"`
}
```

The cleanest place to thread the skipped-ids list is `applyAdvance` itself, which `recordAndAdvance` (and via it `stepComplete`) and `walkPlaybook` both call. Change its signature so callers receive the skipped slice:

```go
func (s *Server) applyAdvance(sess *Session, pb *Playbook, gotoID string) (*mcp.CallToolResult, stepView, *DelegateReturn, []string, error) {
```

Update the function body so every existing `return …` returns a four-value form. After the body's existing logic produces `node` (the rendered post-advance node), wrap it with the auto-advance loop:

```go
	// Auto-advance through pure-transition nodes. Done after the initial
	// store.update has committed the advance to gotoID, so the visited
	// audit trail records every node the walker passed through.
	var skipped []string
	for hops := 0; hops < autoAdvanceMaxHops; hops++ {
		current, err := findNode(pb, sess.CurrentNode)
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
		if !isPureTransition(current) {
			return nil, renderStep(current, sess), nil, skipped, nil
		}
		skipped = append(skipped, current.ID)
		nextID := current.Next[0].Goto
		next, err := findNode(pb, nextID)
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
		// Disallow auto-advancing INTO delegate_to / handoff terminals — those
		// require the structured handling in the non-auto path.
		if next.DelegateTo != "" || len(next.Handoff) > 0 {
			// Advance one step the regular way so the structured handling
			// runs, then stop.
			return s.applyAdvance(sess, pb, nextID)
		}
		sess, err = s.store.update(sess.ID, func(sn *Session) {
			sn.CurrentNode = nextID
			sn.Visited = append(sn.Visited, nextID)
		})
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
	}
	return errorResult(fmt.Sprintf("walker exceeded auto-advance hop limit (%d) starting from %q in playbook %q — possible loop in pure-transition nodes",
		autoAdvanceMaxHops, sess.CurrentNode, pb.ID)), stepView{}, nil, skipped, nil
```

Replace the two callers (`recordAndAdvance` and `walkPlaybook`) so they accept and forward the new slice. For `recordAndAdvance`, change its signature similarly:

```go
func (s *Server) recordAndAdvance(sessionID string, findings []FindingEntry, gotoID string) (*mcp.CallToolResult, stepView, *DelegateReturn, []string, error) {
```

…and update the early-return paths to `return errResult, stepView{}, nil, nil, nil` (the `nil` slice is the new fourth value).

Update `stepComplete` to populate the new output field:

```go
func (s *Server) stepComplete(ctx context.Context, _ *mcp.CallToolRequest, in stepCompleteIn) (*mcp.CallToolResult, stepCompleteOut, error) {
	if strings.TrimSpace(in.Goto) == "" {
		return errorResult("step_complete requires goto; use get_state to list next_options"), stepCompleteOut{}, nil
	}
	res, step, deleg, skipped, err := s.recordAndAdvance(in.SessionID, in.Findings, in.Goto)
	return res, stepCompleteOut{Step: step, DelegateReturned: deleg, AutoAdvancedThrough: skipped}, err
}
```

For `walk_playbook`, also surface the skipped list. Find its output struct (`walkPlaybookOut`) and add:

```go
	AutoAdvancedThrough []string `json:"auto_advanced_through,omitempty"`
```

Update its handler to forward the value.

Finally, tighten the `step_complete` tool description in `register()` to mention auto-advance:

```go
		Description: "Record findings and advance to the next node in one call. An unknown goto target rejects the whole call (no findings recorded). Pass findings: [] for a pure transition. The walker may transparently advance past nodes that have no agent-facing work (no expected_findings / suggested_calls / branches); the returned step.node_id is always your current position, and auto_advanced_through lists any skipped intermediate ids.",
```

- [ ] **Step 5: Run auto-advance tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run 'TestStepComplete_AutoAdvance' -timeout 30s
```

Expected: PASS.

- [ ] **Step 6: Run the full strategies package**

```bash
go test -race -count=1 ./pkg/mcp/strategies/...
```

Expected: PASS. Existing tests that asserted on specific `step_complete` chains may now see fewer turns; if any assertion compares an exact `Visited` list or call count, update the expectation to reflect the auto-advanced path (the visited list still contains every node, including auto-advanced ones).

- [ ] **Step 7: Commit**

```bash
git add pkg/mcp/strategies/walker.go pkg/mcp/strategies/server.go pkg/mcp/strategies/server_step_complete_test.go
git commit -m "$(cat <<'EOF'
feat(strategies): walker auto-advance through pure-transition nodes

When step_complete lands on a node with no agent-facing work (no
expected_findings, no suggested_calls, one next branch, no
delegate/handoff/terminal_advice), the walker transparently continues
to the single next target. Bounded at 10 hops to surface malformed
loops. Response now carries auto_advanced_through listing the skipped
ids so the audit trail stays complete.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Profile `models:` block + defaults

**Files:**
- Modify: `internal/profile/profile.go`
- Modify: `internal/profile/profile_test.go`

Adds a `Models` block to the profile schema with two fields and default values applied at load time. No consumers wired yet — that's Tasks 7–9.

- [ ] **Step 1: Add failing tests for parse + defaults**

Edit `internal/profile/profile_test.go`. Append:

```go
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
	p, err := Parse(strings.NewReader(src))
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-7", p.Models.Investigation)
	assert.Equal(t, "claude-sonnet-4-6", p.Models.Subagent)
}

func TestProfile_ApplyDefaults_FillsModelsWhenAbsent(t *testing.T) {
	t.Parallel()
	p := &Profile{}
	p.applyDefaults()
	assert.Equal(t, "claude-sonnet-4-6", p.Models.Investigation)
	assert.Equal(t, "claude-haiku-4-5-20251001", p.Models.Subagent)
}

func TestProfile_ApplyDefaults_PreservesExplicitModels(t *testing.T) {
	t.Parallel()
	p := &Profile{Models: Models{Investigation: "x", Subagent: "y"}}
	p.applyDefaults()
	assert.Equal(t, "x", p.Models.Investigation)
	assert.Equal(t, "y", p.Models.Subagent)
}
```

- [ ] **Step 2: Run tests; expect compile failure**

```bash
go test -race -count=1 ./internal/profile/... -run 'TestParse_ModelsBlock|TestProfile_ApplyDefaults'
```

Expected: build failure — `Models` and `applyDefaults` not defined.

- [ ] **Step 3: Add the `Models` type + struct field**

Edit `internal/profile/profile.go`. Add to the `Profile` struct (near the `NamespaceDerivation` field from Task 2):

```go
	// Models picks the LLM models for the main investigation session and
	// for sub-agent dispatches. Empty fields get defaults at load time —
	// Sonnet for investigation, Haiku for subagents. Per-call overrides
	// in subagent.Options.Model remain available for legitimately
	// Sonnet-grade sub-agent work (e.g. draft_pr).
	Models Models `yaml:"models,omitempty"`
```

Add the type declaration near the other small types:

```go
type Models struct {
	Investigation string `yaml:"investigation,omitempty"`
	Subagent      string `yaml:"subagent,omitempty"`
}
```

Add an `applyDefaults` method:

```go
// applyDefaults fills in zero-valued profile fields that need a baked-in
// default. Called from the loader after Parse but before the profile is
// handed to the launcher.
func (p *Profile) applyDefaults() {
	if p.Models.Investigation == "" {
		p.Models.Investigation = "claude-sonnet-4-6"
	}
	if p.Models.Subagent == "" {
		p.Models.Subagent = "claude-haiku-4-5-20251001"
	}
}
```

- [ ] **Step 4: Wire `applyDefaults` into the loader**

Find the profile loader (likely in `internal/profile/embed.go` or `internal/profile/profile.go`'s `Load` / `LoadDir` functions). At the point where parsing completes and the loader returns the profile to its caller, insert one call:

```go
	p.applyDefaults()
```

If multiple load paths exist (embedded vs disk vs base-merge), call `applyDefaults` only at the outermost return point so a `base:` merge doesn't double-default. If unsure, grep for callers of `Parse` and ensure each calls `applyDefaults()` once before returning the profile to non-test consumers.

- [ ] **Step 5: Run tests; expect PASS**

```bash
go test -race -count=1 ./internal/profile/...
```

Expected: PASS for the three new tests + all existing tests.

- [ ] **Step 6: Commit**

```bash
git add internal/profile/profile.go internal/profile/profile_test.go
git commit -m "$(cat <<'EOF'
feat(profile): add models block with sonnet/haiku defaults

Schema + defaults only. Models.Investigation defaults to Sonnet for
the main investigation session; Models.Subagent defaults to Haiku for
sub-agent dispatches. Consumers (claude.SessionOpts, subagent.Options)
land in the next two commits; this one is self-contained and tested.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: `claude.SessionOpts.Model` + `--model` passthrough

**Files:**
- Modify: `internal/claude/session.go`
- Modify: `internal/claude/session_test.go` (create if missing)

Adds `Model string` to `SessionOpts` and forwards as `--model <id>` in `baseArgs`. No-op when empty.

- [ ] **Step 1: Add a failing test**

If `internal/claude/session_test.go` does not exist, create it with:

```go
package claude

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaseArgs_NoModelByDefault(t *testing.T) {
	t.Parallel()
	s := &Session{mcpConfigPath: "/tmp/mcp.json", allowedTools: []string{"foo"}}
	args := s.baseArgs()
	for _, a := range args {
		assert.NotEqual(t, "--model", a, "--model must not appear when model is empty")
	}
}

func TestBaseArgs_ModelAppendsFlag(t *testing.T) {
	t.Parallel()
	s := &Session{mcpConfigPath: "/tmp/mcp.json", allowedTools: []string{"foo"}, model: "claude-opus-4-7"}
	args := s.baseArgs()
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "--model claude-opus-4-7")
}
```

If the file exists, append the two test functions to it.

- [ ] **Step 2: Run test; expect compile failure (no `model` field)**

```bash
go test -race -count=1 ./internal/claude/... -run TestBaseArgs
```

Expected: build failure — `Session` has no `model` field; `SessionOpts` has no `Model`.

- [ ] **Step 3: Add `Model` to the struct and `SessionOpts`**

Edit `internal/claude/session.go`. Add to the `Session` struct fields:

```go
	// model passes through as --model to the claude CLI. Empty inherits
	// claude's default. Populated from SessionOpts.Model at NewSession time.
	model string
```

Add to `SessionOpts`:

```go
	// Model passes through as --model to the claude CLI. Empty (the
	// default) inherits claude's default. Used by the launcher to route
	// auxiliary sessions (e.g. operator-agent) to a different model from
	// the investigation main session if needed.
	Model string
```

In `NewSession`, after the existing field assignments from opts, add:

```go
		s.model = opts[0].Model
```

In `baseArgs`, after the `--allowedTools` loop and before the `--append-system-prompt` branch:

```go
	if s.model != "" {
		args = append(args, "--model", s.model)
	}
```

- [ ] **Step 4: Run tests; expect PASS**

```bash
go test -race -count=1 ./internal/claude/... -run TestBaseArgs
```

Expected: PASS.

- [ ] **Step 5: Run the whole claude + dependent packages**

```bash
go test -race -count=1 ./internal/claude/... ./internal/sessions/... ./internal/server/...
```

Expected: PASS. The existing call sites pass `claude.SessionOpts{...}` literally — the new `Model` field is a zero-valued string, so callers compile unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/claude/session.go internal/claude/session_test.go
git commit -m "$(cat <<'EOF'
feat(claude): SessionOpts.Model passes through as --model

Empty preserves today's behaviour (claude picks its default). The
launcher wires profile.Models.Investigation into this in a follow-up
commit; this one is the plumbing + tests.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: `subagent.Options.Model` + `--model` passthrough

**Files:**
- Modify: `pkg/mcp/subagent/subagent.go`
- Modify: `pkg/mcp/subagent/subagent_test.go`

Adds `Model string` to `subagent.Options`. The `Run` function passes `--model <id>` to the spawned `claude` subprocess. Empty inherits the parent.

- [ ] **Step 1: Add a failing test**

Edit `pkg/mcp/subagent/subagent_test.go`. Append:

```go
func TestRun_PassesModelFlagWhenSet(t *testing.T) {
	t.Parallel()
	// Use a fake claude binary that just echoes its argv to stdout as JSON-ish.
	// existing test helpers in this file likely already shim claude via a
	// test-local PATH or a runner indirection — reuse that pattern.
	dir := t.TempDir()
	fake := writeArgvDumperBinary(t, dir)
	res, err := Run(context.Background(), Options{
		ClaudeBinary: fake,
		AllowedTools: "Bash(echo)",
		Prompt:       "hi",
		Model:        "claude-haiku-4-5-20251001",
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	assert.Contains(t, res.Summary, "--model")
	assert.Contains(t, res.Summary, "claude-haiku-4-5-20251001")
}

func TestRun_OmitsModelFlagWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := writeArgvDumperBinary(t, dir)
	res, err := Run(context.Background(), Options{
		ClaudeBinary: fake,
		AllowedTools: "Bash(echo)",
		Prompt:       "hi",
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	assert.NotContains(t, res.Summary, "--model")
}

// writeArgvDumperBinary creates a tiny shell script that prints its own argv
// in stream-json shape, so Run() can pick it up via its normal parser. The
// script writes a single "result" event with the argv joined.
func writeArgvDumperBinary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	body := `#!/usr/bin/env bash
printf '{"type":"result","subtype":"success","result":"%s"}\n' "$*"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}
```

Add the necessary imports to the test file: `"context"`, `"os"`, `"path/filepath"`, `"time"`, `"github.com/stretchr/testify/assert"`, `"github.com/stretchr/testify/require"`.

If the existing subagent tests already use a different fake-binary pattern, prefer the existing helper verbatim instead of `writeArgvDumperBinary`.

- [ ] **Step 2: Run tests; expect compile failure**

```bash
go test -race -count=1 ./pkg/mcp/subagent/... -run 'TestRun_.*Model'
```

Expected: build failure — `Options` has no `Model` field.

- [ ] **Step 3: Add `Model` and wire into the spawned argv**

Edit `pkg/mcp/subagent/subagent.go`. Add to `Options`:

```go
	// Model passes through as --model to the claude subprocess. Empty
	// inherits the parent. Used by callers (and per-profile defaults)
	// to route lighter structured-output work to Haiku.
	Model string
```

Inside `Run`, after the existing `args` slice is constructed and before `ResumeSessionID` handling, add:

```go
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
```

(Insert before the `if opts.ResumeSessionID != "" { ... }` block.)

- [ ] **Step 4: Run tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/subagent/... -run 'TestRun_.*Model'
```

Expected: PASS.

- [ ] **Step 5: Run the full subagent package**

```bash
go test -race -count=1 ./pkg/mcp/subagent/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/mcp/subagent/subagent.go pkg/mcp/subagent/subagent_test.go
git commit -m "$(cat <<'EOF'
feat(subagent): Options.Model passes through as --model

Empty inherits the parent's model. Callers that want the profile
default look it up via the launcher's profile accessor and pass it
in; the profile is the floor, per-call Model is the ceiling.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Wire `profile.Models` into the investigation + operator sessions

**Files:**
- Modify: `internal/sessions/session.go`
- Modify: `internal/server/manager.go`

The two `claude.NewSession(...)` call sites currently leave `Model` empty. This task fills in `profile.Models.Investigation` for both. Sub-agent callers that should follow the profile default (the proposal dispatch in Task 14) read `profile.Models.Subagent` at dispatch time — wiring there happens in Task 14.

- [ ] **Step 1: Add a failing test (sessions)**

Edit `internal/sessions/session_test.go`. Append:

```go
func TestNew_ForwardsProfileInvestigationModel(t *testing.T) {
	t.Parallel()
	// New() validates the `claude` binary exists on PATH; on CI this passes via
	// the test harness's fake. Skip if missing locally.
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not on PATH; skipping")
	}
	opts := Options{
		Profile: profile.Profile{Models: profile.Models{Investigation: "claude-opus-4-7"}},
	}
	sess, err := New(opts)
	require.NoError(t, err)
	// inner is unexported; assert via the public knobs that survive into the
	// struct. Use a helper that exposes the model for testing.
	assert.Equal(t, "claude-opus-4-7", sess.inner.Model())
}
```

The assertion above calls `sess.inner.Model()` — a new `Model() string` method on `claude.Session` returning the captured `s.model`. Add the method in `internal/claude/session.go`:

```go
// Model returns the configured model id (empty inherits claude's default).
// Exposed for tests; production code does not need to read it back.
func (s *Session) Model() string { return s.model }
```

- [ ] **Step 2: Run; expect failure (Model isn't being forwarded)**

```bash
go test -race -count=1 ./internal/sessions/... -run TestNew_ForwardsProfileInvestigationModel
```

Expected: FAIL — `inner.Model()` returns "" because `New()` doesn't pass `opts.Profile.Models.Investigation`.

- [ ] **Step 3: Wire `Models.Investigation` into the investigation session**

Edit `internal/sessions/session.go`. In `New(opts Options)`, update the `claude.SessionOpts{...}` literal to include `Model`:

```go
		claude.SessionOpts{
			Cwd:                opts.LaunchCwd,
			Env:                opts.childEnv(),
			AppendSystemPrompt: renderSystemPromptAddendum(systemPromptAddendum, opts.Profile.NamespaceDerivation, opts.alertPayloadFields()),
			Model:              opts.Profile.Models.Investigation,
		},
```

- [ ] **Step 4: Run; expect PASS**

```bash
go test -race -count=1 ./internal/sessions/... -run TestNew_ForwardsProfileInvestigationModel
```

Expected: PASS.

- [ ] **Step 5: Wire the operator-agent session (auto mode)**

Edit `internal/server/manager.go`. Locate `defaultAutoBackendFactory`. Update the `claude.SessionOpts{...}` literal:

```go
	}, claude.SessionOpts{
		Cwd:                opts.OperatorCwd,
		Env:                opts.Env,
		AppendSystemPrompt: system,
		Model:              opts.Profile.Models.Investigation,
	})
```

If `AutoOptions` does not yet carry a `Profile` field, add it:

```go
	// Profile is the active deployment profile. Used to thread
	// Models.Investigation to the operator-agent claude session.
	Profile profile.Profile
```

…and ensure callers populate it from the launcher's profile accessor. Grep for `AutoOptions{` in `internal/server` and add `Profile: <launcher-profile>` to each construction.

- [ ] **Step 6: Run server tests**

```bash
go test -race -count=1 ./internal/server/...
```

Expected: PASS. If any existing test constructs `AutoOptions{...}` and now expects the new `Profile` field, it'll still compile (zero-valued profile is fine for unit tests that don't care).

- [ ] **Step 7: Commit**

```bash
git add internal/sessions/session.go internal/server/manager.go internal/claude/session.go
git commit -m "$(cat <<'EOF'
feat(sessions,server): wire profile.Models.Investigation into claude sessions

The main investigation session and the operator-agent auto session
now both pick up the profile's investigation-model setting. Empty
preserves today's behaviour. Exposes Session.Model() for test
introspection.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Surface validation errors in `playbook_proposal_draft` response

**Files:**
- Modify: `pkg/mcp/strategies/tools_proposal.go`
- Modify: `pkg/mcp/strategies/tools_proposal_test.go` (create if missing)

Currently `playbook_proposal_draft` parses + validates the YAML and returns an error result on failure. This task changes failures into a structured `ValidationErrors []string` field on the success-path response, so a single sub-agent can iterate on errors without needing a separate `validate_playbook` round-trip. The standalone `validate_playbook` tool stays — it's still useful in the playbook editor — but the proposal flow no longer needs to call it.

- [ ] **Step 1: Add failing tests**

Create or extend `pkg/mcp/strategies/tools_proposal_test.go`:

```go
package strategies

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProposePlaybookDraft_ReturnsValidationErrorsInsteadOfErrorResult(t *testing.T) {
	t.Parallel()
	srv := newEmptyServer(t) // reuses the helper from server_step_complete_test.go
	ctx := context.Background()
	// Malformed YAML: missing entrypoint.
	_, out, err := srv.proposePlaybookDraft(ctx, nil, proposePlaybookDraftIn{
		YAML: "id: x\nschema_version: 1\nsymptom: foo\nnodes:\n  a:\n    description: a\n",
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.ValidationErrors, "validation errors must surface in the response, not as an error")
	assert.Empty(t, out.ProposalID, "no proposal_id when validation failed")
}

func TestProposePlaybookDraft_ValidYAMLProducesProposalNoValidationErrors(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	ctx := context.Background()
	yaml := `id: testpb
schema_version: 1
symptom: test
entrypoint: a
nodes:
  a:
    description: a
    terminal_advice: done
`
	_, out, err := srv.proposePlaybookDraft(ctx, nil, proposePlaybookDraftIn{
		YAML: yaml,
		Type: "investigation",
		Why:  "test",
	})
	require.NoError(t, err)
	assert.Empty(t, out.ValidationErrors)
	assert.NotEmpty(t, out.ProposalID)
}
```

`newServerWithUserPlaybooksDir(t)` should set `srv.userPlaybooksDir` to a `t.TempDir()` so the proposal file write succeeds. If a helper for this doesn't exist, write a minimal one matching the existing harness pattern.

- [ ] **Step 2: Run tests; expect failure**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestProposePlaybookDraft
```

Expected: FAIL. The malformed-YAML case returns an error result rather than populated `ValidationErrors`.

- [ ] **Step 3: Add `ValidationErrors` to the output struct and rewire the handler**

Edit `pkg/mcp/strategies/tools_proposal.go`. Add to `proposePlaybookDraftOut`:

```go
	// ValidationErrors lists structural validator failures when the supplied
	// YAML is malformed. Populated alongside an empty ProposalID; valid
	// proposals leave this nil. The agent inspects this field on the same
	// response shape rather than calling validate_playbook separately.
	ValidationErrors []string `json:"validation_errors,omitempty"`
```

In the handler `proposePlaybookDraft`, locate the call to `ParseAndValidatePlaybookYAML`. Replace the error-result branch with:

```go
	pb, errs := ParseAndValidatePlaybookYAML([]byte(in.YAML))
	if len(errs) > 0 {
		return nil, proposePlaybookDraftOut{ValidationErrors: errs}, nil
	}
```

(Keep the existing variable name conventions — the project may use `playbook` instead of `pb`.)

Tighten the registered tool description for `playbook_proposal_draft` to advertise the new validation-errors path:

```go
		Description: "Submit a draft playbook for the operator's inline review. Validates structurally; on failure returns validation_errors in the response (no need to call validate_playbook first). On success writes the YAML to a draft store; the launcher renders a diff card. Returns proposal_id (operator-facing UI uses it) + base_yaml/new_yaml (for the diff view).",
```

- [ ] **Step 4: Run tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestProposePlaybookDraft
```

Expected: PASS.

- [ ] **Step 5: Run the strategies package; check for regressions**

```bash
go test -race -count=1 ./pkg/mcp/strategies/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/mcp/strategies/tools_proposal.go pkg/mcp/strategies/tools_proposal_test.go
git commit -m "$(cat <<'EOF'
feat(strategies): surface playbook_proposal_draft validation errors inline

Validator failures now populate proposePlaybookDraftOut.ValidationErrors
instead of returning a tool-error result. A single sub-agent can iterate
on errors without a separate validate_playbook round-trip. The standalone
validate_playbook tool stays for the playbook editor's direct flow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: Trim `wiki_proposal.yaml` and `playbook_proposal.yaml`

**Files:**
- Modify: `system/wiki_proposal.yaml`
- Modify: `system/playbook_proposal.yaml`

Remove playbook nodes whose entire purpose is to instruct calls to `wiki_list_entities`, `playbook_schema`, or `validate_playbook` as standalone steps. Fold their content into adjacent substantive node descriptions. After this task the main-agent-walked path is shorter; Task 14 converts both YAMLs to dispatch mode and the trimmed prose becomes the dispatch prompt source.

- [ ] **Step 1: Trim `wiki_proposal.yaml`**

Edit `system/wiki_proposal.yaml`. The `ground_in_existing_entities` and `ground_in_existing_entities_for_edit` nodes' descriptions reference `wiki_list_entities`. Keep the *content* (it describes entity-grounding semantics) but the standalone `suggested_calls` invitation is no longer needed once dispatch lands. For this commit, fold them into the adjacent `find_similar_entries` / `check_for_existing_entry` nodes' descriptions and remove the standalone nodes. Concretely, replace `ground_in_existing_entities` with a leading paragraph in `find_similar_entries`'s description:

Replace the `ground_in_existing_entities` node block (lines around `ground_in_existing_entities:` through its `next:` block) so it no longer exists, then replace the entry into `find_similar_entries` from the original `gather_inputs.next` branch to point to `find_similar_entries` directly:

Find in `gather_inputs.next`:

```yaml
      - condition: "we have a valid slug and at least one summary (default new-draft path)"
        goto: ground_in_existing_entities
```

Replace with:

```yaml
      - condition: "we have a valid slug and at least one summary (default new-draft path)"
        goto: find_similar_entries
```

Same change for the edit branch:

```yaml
      - condition: "operator explicitly asked to EDIT / iterate on an existing wiki entry AND we have a valid slug"
        goto: ground_in_existing_entities_for_edit
```

Replace with:

```yaml
      - condition: "operator explicitly asked to EDIT / iterate on an existing wiki entry AND we have a valid slug"
        goto: load_existing_for_edit
```

Then prepend a paragraph to `find_similar_entries`'s description:

```yaml
  find_similar_entries:
    description: |
      Before searching, recall that the wiki vault canonicalises service /
      error / symptom names. As you read each hit and as you draft, reuse
      existing entity names rather than coining new ones. The propose tool
      will reject unknown entity references on submit — better to align
      now than retry then.

      Look for similar prior wiki entries to model the new draft after.
      Use `wiki_search` ...
```

Delete the old `ground_in_existing_entities` and `ground_in_existing_entities_for_edit` blocks entirely (they are no longer referenced).

- [ ] **Step 2: Trim `playbook_proposal.yaml`**

Edit `system/playbook_proposal.yaml`. Remove the standalone `read_schema` node and the `validate_playbook` invocation in `draft_and_validate`.

Replace the `read_schema` node block. Find:

```yaml
  read_schema:
    description: |
      Fetch the playbook YAML schema + authoring conventions. Read it
      once before drafting so the YAML you produce passes
      validate_playbook on the first try. Particular things to absorb:
        - placeholders ${cluster_id} / ${namespace} in suggested_calls.args
        - the convention that branch conditions are PROSE (the agent
          reads them; the walker doesn't evaluate)
        - terminal nodes carry terminal_advice and have no `next`
        - cross-playbook handoffs go via the structured `handoff` field
          on terminal nodes (a list of target playbook ids); the prose
          in `terminal_advice` carries the *why*
    suggested_calls:
      - tool: triagent-strategies/playbook_schema
    expected_findings:
      - schema_understood
    next:
      - condition: "schema in hand — draft the YAML"
        goto: draft_and_validate
```

Delete the node. Update everywhere `read_schema` is referenced as a `goto` target to point at `draft_and_validate` instead. (Search for `goto: read_schema` and replace; there should be three or four call sites — in `assess_novelty.next`, `scope_split.next`, `scope_multi_proposals.next`, and `read_existing_for_revision.next`.)

Prepend the schema-authoring guidance to `draft_and_validate`'s description so the agent still has it:

```yaml
  draft_and_validate:
    description: |
      Schema reminders the YAML you produce must satisfy:
        - placeholders ${cluster_id} / ${namespace} in suggested_calls.args
        - branch conditions are PROSE (the agent reads them; the walker
          doesn't evaluate them)
        - terminal nodes carry terminal_advice and have no `next`
        - cross-playbook handoffs go via the structured `handoff` field
          on terminal nodes (a list of target playbook ids); the prose
          in `terminal_advice` carries the *why*

      Draft the playbook YAML. Submit it via playbook_proposal_draft —
      the tool validates structurally and, on failure, returns
      validation_errors in the response. If you get validation_errors,
      fix the YAML and submit again. **Do not** call validate_playbook
      separately; the proposal tool does it inline.

      Common mistakes the validator catches:
        - dangling `goto`: branch points at a node id that doesn't exist
        - missing `entrypoint` — or one that points at a non-existent node
        - empty descriptions: every node needs prose the operator reads
        - missing `symptom` — the operator-facing one-line trigger
```

Then update `draft_and_validate.suggested_calls` and `next` — remove the `validate_playbook` call, change the success branch to submit_draft directly:

```yaml
    suggested_calls:
      - tool: triagent-strategies/playbook_proposal_draft
        args:
          yaml: "<your-draft-yaml>"
          why: "<one-sentence justification>"
    expected_findings:
      - draft_submitted_or_validation_errors
    next:
      - condition: "proposal_draft returned a proposal_id (no validation_errors)"
        goto: submit_draft
      - condition: "proposal_draft returned validation_errors — fix the YAML and re-run THIS node"
        goto: draft_and_validate
      - condition: "validation errors are unfixable (e.g. the playbook concept turns out not to fit the schema cleanly)"
        goto: terminal_no_proposal
```

Update `draft_parent_handoff.suggested_calls` similarly — remove the `validate_playbook` entry; keep the `playbook_proposal_draft` entry.

- [ ] **Step 3: Run embed + walker tests**

```bash
go test -race -count=1 ./internal/profile/... ./pkg/mcp/strategies/...
```

Expected: PASS. The `embed.go` tests verify the YAML files parse; the walker tests verify the playbooks load. Any test asserting the existence of `read_schema` or `ground_in_existing_entities` nodes needs updating — adjust expected node ids to the new layout.

- [ ] **Step 4: Quick sanity grep — no orphan goto targets**

```bash
grep -n 'goto: read_schema\|goto: ground_in_existing_entities' system/*.yaml
```

Expected: no output. Any remaining hits are dangling references — fix them before commit.

- [ ] **Step 5: Commit**

```bash
git add system/wiki_proposal.yaml system/playbook_proposal.yaml
git commit -m "$(cat <<'EOF'
feat(strategies): trim wiki_proposal and playbook_proposal flows

Removes the standalone read_schema and ground_in_existing_entities
nodes whose only job was to instruct schema / entity-list lookups.
Their guidance moves into adjacent substantive nodes. Validate is
inlined into playbook_proposal_draft per the prior commit — the
draft_and_validate loop now talks only to the proposal tool. Sub-agent
dispatch in Task 14 reuses the trimmed prose as the dispatch prompt.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Add `Playbook.Dispatch` field + parser support

**Files:**
- Modify: `pkg/mcp/strategies/playbook.go`
- Modify: `pkg/mcp/strategies/playbook_test.go`

Adds a top-level `dispatch: subagent` field on `Playbook`. Parser accepts it; load validation accepts only known values. No runtime behaviour change yet (dispatch handling lands in Task 14).

- [ ] **Step 1: Add failing tests**

Edit `pkg/mcp/strategies/playbook_test.go` (or create a new test file in the package if needed):

```go
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
```

- [ ] **Step 2: Run; expect compile failure**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestParseAndValidatePlaybookYAML
```

Expected: FAIL — `DispatchSubagent`, `DispatchDefault`, `Playbook.Dispatch` undefined.

- [ ] **Step 3: Add the field + constants**

Edit `pkg/mcp/strategies/playbook.go`. Near the existing `Playbook` type, add:

```go
// DispatchMode picks how walk_playbook should treat this playbook. Default
// walks the agent through nodes one step at a time (today's behaviour).
// "subagent" handles the entire playbook in a single sub-agent run on the
// profile's Models.Subagent default — used for structured-output proposals
// (wiki_proposal, playbook_proposal) where the multi-turn walker is wasteful.
type DispatchMode string

const (
	DispatchDefault  DispatchMode = ""
	DispatchSubagent DispatchMode = "subagent"
)
```

Add to the `Playbook` struct:

```go
	// Dispatch picks how walk_playbook handles this playbook. Empty (the
	// default) walks the agent through nodes one step at a time. "subagent"
	// hands the whole playbook to a single sub-agent run.
	Dispatch DispatchMode `yaml:"dispatch,omitempty"`
```

In the validator (likely `ParseAndValidatePlaybookYAML` or a function it calls), add:

```go
	switch pb.Dispatch {
	case DispatchDefault, DispatchSubagent:
		// ok
	default:
		errs = append(errs, fmt.Sprintf("unknown dispatch mode %q (allowed: \"\", \"subagent\")", pb.Dispatch))
	}
```

- [ ] **Step 4: Run; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestParseAndValidatePlaybookYAML
```

Expected: PASS.

- [ ] **Step 5: Run the full strategies package**

```bash
go test -race -count=1 ./pkg/mcp/strategies/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/mcp/strategies/playbook.go pkg/mcp/strategies/playbook_test.go
git commit -m "$(cat <<'EOF'
feat(strategies): add Playbook.Dispatch field (default | subagent)

Schema-only change. Parser accepts and validates the new top-level
field; runtime semantics land in the dispatch commit. Unknown values
are rejected at load time so a typo in a system YAML surfaces fast.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: `dispatch_prompt` assembly helper

**Files:**
- Create: `pkg/mcp/strategies/dispatch_prompt.go`
- Create: `pkg/mcp/strategies/dispatch_prompt_test.go`

Assembles the single-shot prompt the sub-agent receives. Inputs:
- The dispatch-mode playbook (its nodes' prose is the agent's instructions).
- The parent investigation session's findings map (so the sub-agent has context).
- The most recent summarize output (the operator-facing conclusion).
- The operator's refinement comment, if any (passed as a free-form string).

Output: a single string prompt.

- [ ] **Step 1: Create failing tests**

Create `pkg/mcp/strategies/dispatch_prompt_test.go`:

```go
package strategies

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildDispatchPrompt_IncludesPlaybookNodesInOrder(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "pb",
		Symptom:    "s",
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "first step prose"},
			"b": {ID: "b", Description: "second step prose"},
		},
	}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Findings: map[string]any{"x": 1},
		Summary:  "the summary",
	})
	// Both node descriptions present.
	assert.True(t, strings.Contains(prompt, "first step prose"))
	assert.True(t, strings.Contains(prompt, "second step prose"))
	// Findings present.
	assert.True(t, strings.Contains(prompt, "\"x\""))
	// Summary present.
	assert.True(t, strings.Contains(prompt, "the summary"))
}

func TestBuildDispatchPrompt_AppendsRefinementWhenPresent(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:           pb,
		Findings:           nil,
		Summary:            "s",
		OperatorRefinement: "split the wiki into two entries",
	})
	assert.True(t, strings.Contains(prompt, "split the wiki into two entries"))
	assert.True(t, strings.Contains(strings.ToLower(prompt), "refinement"))
}

func TestBuildDispatchPrompt_NoRefinementOmitsSection(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Findings: nil,
		Summary:  "s",
	})
	assert.False(t, strings.Contains(strings.ToLower(prompt), "refinement"))
}
```

- [ ] **Step 2: Run; expect compile failure**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestBuildDispatchPrompt
```

Expected: FAIL — `DispatchInputs`, `BuildDispatchPrompt` undefined.

- [ ] **Step 3: Create `dispatch_prompt.go`**

```go
package strategies

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// DispatchInputs carries every input BuildDispatchPrompt needs. The caller
// (walk_playbook in dispatch mode) wires this from the parent session state.
type DispatchInputs struct {
	Playbook           *Playbook
	Findings           map[string]any
	Summary            string
	OperatorRefinement string
}

// BuildDispatchPrompt assembles the single-turn prompt the sub-agent receives.
// Sections (in order):
//   1. Role: one paragraph naming what playbook the sub-agent is executing.
//   2. Playbook instructions: each node's description in entrypoint-first
//      traversal order, separated by a horizontal-rule line.
//   3. Findings: pretty-printed JSON of the parent session's findings map.
//   4. Summary: the most recent summarize() output verbatim.
//   5. Operator refinement (optional): when non-empty, an explicit "honour
//      this refinement over the playbook's defaults" instruction.
func BuildDispatchPrompt(in DispatchInputs) string {
	var b strings.Builder
	if in.Playbook != nil {
		fmt.Fprintf(&b, "# Sub-agent dispatch: %s\n\n", in.Playbook.ID)
		fmt.Fprintf(&b, "You are running the %q playbook as a single sub-agent task. Read the playbook nodes below, then execute them as one coherent flow.\n\n", in.Playbook.ID)
		b.WriteString("## Playbook nodes\n\n")
		for _, id := range orderedNodeIDs(in.Playbook) {
			node := in.Playbook.Nodes[id]
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", id, strings.TrimSpace(node.Description))
		}
	}
	if len(in.Findings) > 0 {
		b.WriteString("## Findings from the parent investigation\n\n```json\n")
		findingsJSON, _ := json.MarshalIndent(in.Findings, "", "  ")
		b.Write(findingsJSON)
		b.WriteString("\n```\n\n")
	}
	if strings.TrimSpace(in.Summary) != "" {
		b.WriteString("## Investigation summary\n\n")
		b.WriteString(strings.TrimSpace(in.Summary))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(in.OperatorRefinement) != "" {
		b.WriteString("## Operator refinement\n\nHonour this refinement over the playbook's defaults:\n\n> ")
		b.WriteString(strings.TrimSpace(in.OperatorRefinement))
		b.WriteString("\n\n")
	}
	return b.String()
}

// orderedNodeIDs returns the playbook's node ids in entrypoint-first order
// followed by the remaining ids alphabetically. Deterministic for caching
// and for tests; the sub-agent reads the nodes as one prose document, so
// order matters only for human readability of the prompt.
func orderedNodeIDs(pb *Playbook) []string {
	if pb == nil {
		return nil
	}
	out := make([]string, 0, len(pb.Nodes))
	if pb.Entrypoint != "" {
		if _, ok := pb.Nodes[pb.Entrypoint]; ok {
			out = append(out, pb.Entrypoint)
		}
	}
	rest := make([]string, 0, len(pb.Nodes))
	for id := range pb.Nodes {
		if id == pb.Entrypoint {
			continue
		}
		rest = append(rest, id)
	}
	sort.Strings(rest)
	return append(out, rest...)
}
```

- [ ] **Step 4: Run tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestBuildDispatchPrompt
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/mcp/strategies/dispatch_prompt.go pkg/mcp/strategies/dispatch_prompt_test.go
git commit -m "$(cat <<'EOF'
feat(strategies): add dispatch prompt assembly helper

Pure function: takes a playbook + parent findings + summary +
optional operator refinement, returns one prompt string. Used by
the dispatch path landing in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: `walk_playbook` dispatches to sub-agent; mark proposal YAMLs

**Files:**
- Create: `pkg/mcp/strategies/dispatch.go`
- Create: `pkg/mcp/strategies/dispatch_test.go`
- Modify: `pkg/mcp/strategies/server.go`
- Modify: `system/wiki_proposal.yaml`
- Modify: `system/playbook_proposal.yaml`

When `walk_playbook` resolves a playbook with `Dispatch == DispatchSubagent`, the strategies MCP spawns a single `pkg/mcp/subagent.Run` instead of returning a walker stepView. The sub-agent runs on the profile's `Models.Subagent` default with an allowlist scoped to the proposal-relevant tools.

- [ ] **Step 1: Plumb `subagent.Runner` + `Models` + parent-state accessor into the strategies `Server`**

Edit `pkg/mcp/strategies/server.go`. Add to the `Options` struct (the one passed to `New`):

```go
	// Models picks the LLM models for sub-agent dispatches. Subagent is
	// required when any loaded playbook is dispatch: subagent; if empty
	// the dispatch handler falls back to inheriting the parent.
	Models DispatchModels

	// SubAgentRunner runs the actual sub-agent. Defaults to subagent.Run
	// when nil; tests pass a stub. Decoupled so the strategies package
	// tests don't spawn a real claude.
	SubAgentRunner func(ctx context.Context, opts subagent.Options) (subagent.Result, error)

	// ParentSessionState returns the findings + most-recent summary of the
	// parent investigation session. Provided by the launcher; nil falls
	// back to empty inputs.
	ParentSessionState func(parentSessionID string) (findings map[string]any, summary string, ok bool)
```

Add the `DispatchModels` type (a project-local copy that mirrors profile.Models so the package doesn't import profile):

```go
type DispatchModels struct {
	Subagent string
}
```

Imports: add `"github.com/sourcehawk/triagent/pkg/mcp/subagent"` to `server.go` if not already present.

In the `Server` struct, store the values:

```go
	models             DispatchModels
	subAgentRunner     func(ctx context.Context, opts subagent.Options) (subagent.Result, error)
	parentSessionState func(parentSessionID string) (map[string]any, string, bool)
```

Wire them in `New(opts Options)`:

```go
	s.models = opts.Models
	if opts.SubAgentRunner != nil {
		s.subAgentRunner = opts.SubAgentRunner
	} else {
		s.subAgentRunner = subagent.Run
	}
	s.parentSessionState = opts.ParentSessionState
```

- [ ] **Step 2: Create failing tests for the dispatch path**

Create `pkg/mcp/strategies/dispatch_test.go`:

```go
package strategies

import (
	"context"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkPlaybook_DispatchSubagentRunsRunnerWithModelAndPrompt(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "wiki_proposal_test",
		Symptom:    "test",
		Dispatch:   DispatchSubagent,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "draft the wiki entry", TerminalAdvice: "done"},
		},
	}

	var capturedOpts subagent.Options
	runner := func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		capturedOpts = opts
		return subagent.Result{Summary: "dispatched ok"}, nil
	}
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = runner
	srv.models = DispatchModels{Subagent: "claude-haiku-4-5-20251001"}
	srv.parentSessionState = func(string) (map[string]any, string, bool) {
		return map[string]any{"finding_a": "x"}, "the investigation summary", true
	}

	ctx := context.Background()
	res, out, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID:      pb.ID,
		ParentSessionID: "parent-1",
	})
	require.NoError(t, err)
	require.Nil(t, res, "no error result on success")
	assert.Equal(t, "claude-haiku-4-5-20251001", capturedOpts.Model)
	assert.Contains(t, capturedOpts.Prompt, "draft the wiki entry")
	assert.Contains(t, capturedOpts.Prompt, "the investigation summary")
	assert.Contains(t, capturedOpts.Prompt, "finding_a")
	assert.True(t, strings.HasPrefix(out.Dispatched.Summary, "dispatched"))
}

func TestWalkPlaybook_DefaultDispatchUsesWalker(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "regular_pb",
		Symptom:    "test",
		Dispatch:   DispatchDefault,
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "step", TerminalAdvice: "done"},
		},
	}
	runnerCalled := false
	srv := newEmptyServer(t)
	srv.playbooks[pb.ID] = pb
	srv.subAgentRunner = func(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
		runnerCalled = true
		return subagent.Result{}, nil
	}

	ctx := context.Background()
	_, out, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{PlaybookID: pb.ID})
	require.NoError(t, err)
	assert.False(t, runnerCalled, "default-dispatch playbooks must not call the sub-agent runner")
	assert.NotEmpty(t, out.SessionID, "default dispatch returns a walker session id")
	assert.Equal(t, "a", out.Step.NodeID)
}
```

The tests reference a new `Dispatched` field on `walkPlaybookOut` populated only on dispatch. Step 3 introduces it.

- [ ] **Step 3: Run tests; expect compile failure**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestWalkPlaybook_Dispatch
```

Expected: FAIL — `walkPlaybookOut.Dispatched` missing; dispatch branch missing.

- [ ] **Step 4: Implement the dispatch branch in `walkPlaybook` + create `dispatch.go`**

Create `pkg/mcp/strategies/dispatch.go`:

```go
package strategies

import (
	"context"
	"fmt"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
)

// dispatchAllowedToolsFor returns the per-playbook sub-agent tool allowlist.
// Scoped narrowly — the dispatch sub-agent should reach only the tools its
// proposal flow legitimately needs.
func dispatchAllowedToolsFor(playbookID string) string {
	switch playbookID {
	case "wiki_proposal":
		return "mcp__triagent-wiki__propose_wiki_draft,mcp__triagent-wiki__wiki_get,mcp__triagent-wiki__wiki_list_entities,mcp__triagent-wiki__wiki_search"
	case "playbook_proposal":
		return "mcp__triagent-strategies__playbook_proposal_draft,mcp__triagent-strategies__list_playbooks,mcp__triagent-strategies__get_playbook_raw,mcp__triagent-strategies__playbook_correlate"
	default:
		// Unknown dispatch-mode playbooks get no MCP tools beyond what
		// claude's built-ins provide. Operator can extend the table when
		// they add a new dispatch playbook.
		return ""
	}
}

// runDispatch executes a dispatch-mode playbook as one sub-agent run and
// returns a Result the caller can stuff into walkPlaybookOut.Dispatched.
func (s *Server) runDispatch(ctx context.Context, pb *Playbook, parentSessionID, operatorRefinement string) (subagent.Result, error) {
	var (
		findings map[string]any
		summary  string
	)
	if s.parentSessionState != nil {
		if f, sm, ok := s.parentSessionState(parentSessionID); ok {
			findings = f
			summary = sm
		}
	}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:           pb,
		Findings:           findings,
		Summary:            summary,
		OperatorRefinement: operatorRefinement,
	})
	if s.subAgentRunner == nil {
		return subagent.Result{}, fmt.Errorf("dispatch %q: subagent runner not configured", pb.ID)
	}
	return s.subAgentRunner(ctx, subagent.Options{
		AllowedTools: dispatchAllowedToolsFor(pb.ID),
		Prompt:       prompt,
		Model:        s.models.Subagent,
		// No timeout override — relies on the runner's default 5-min cap.
	})
}
```

Edit `pkg/mcp/strategies/server.go`. Find `walkPlaybookOut` and add:

```go
	Dispatched *DispatchedResult `json:"dispatched,omitempty"`
```

Add the type:

```go
// DispatchedResult is what walk_playbook returns for dispatch: subagent
// playbooks instead of a stepView. The Summary is the sub-agent's final
// message — the agent reads it as "this is what the dispatched flow
// concluded".
type DispatchedResult struct {
	Summary string `json:"summary"`
}
```

If `walkPlaybookIn` does not already carry an `OperatorRefinement` input, add it:

```go
	OperatorRefinement string `json:"operator_refinement,omitempty" jsonschema:"Optional. When the operator pushed back on a proposal (e.g. 'split into two entries'), pass their refinement so the dispatched sub-agent honours it. Ignored for default-dispatch playbooks."`
```

Find the `walkPlaybook` handler. Just before the existing code that opens a walker session, branch on `Dispatch`:

```go
	pb, ok := s.playbooks[in.PlaybookID]
	if !ok {
		return errorResult(fmt.Sprintf("unknown playbook_id %q; call list_playbooks for valid ids", in.PlaybookID)), walkPlaybookOut{}, nil
	}
	if pb.Dispatch == DispatchSubagent {
		res, err := s.runDispatch(ctx, pb, in.ParentSessionID, in.OperatorRefinement)
		if err != nil {
			return errorResult(fmt.Sprintf("dispatch %q: %v", pb.ID, err)), walkPlaybookOut{}, nil
		}
		return nil, walkPlaybookOut{Dispatched: &DispatchedResult{Summary: res.Summary}}, nil
	}
```

(Keep the existing default-walk code unchanged after this branch.)

- [ ] **Step 5: Run dispatch tests; expect PASS**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... -run TestWalkPlaybook_Dispatch
```

Expected: PASS.

- [ ] **Step 6: Mark `wiki_proposal` and `playbook_proposal` as `dispatch: subagent`**

Edit `system/wiki_proposal.yaml`. Near the top (after `schema_version: 1`), add:

```yaml
dispatch: subagent
```

Edit `system/playbook_proposal.yaml`. Same addition:

```yaml
dispatch: subagent
```

- [ ] **Step 7: Wire the runner + parent-session accessor in the launcher**

Find where `strategies.New(...)` is called (likely `cmd/triagent-mcp/serve.go`'s `case "strategies"` arm, or `internal/server` if the strategies MCP is in-proc). For the in-proc / launcher-owned path, pass:

```go
strategies.New(strategies.Options{
    // ...existing fields...
    Models: strategies.DispatchModels{Subagent: profile.Models.Subagent},
    SubAgentRunner: nil, // nil → defaults to subagent.Run
    ParentSessionState: launcher.SessionFindingsAccessor,
})
```

For the standalone `triagent-mcp --kind=strategies` subprocess, the profile is read from env (or passed-through config). Identify how the existing per-kind subprocess receives the profile and add `Models.Subagent` to that channel. If the subprocess can't see the launcher's per-session state, `ParentSessionState` stays nil — the dispatch path then runs with empty findings, which is suboptimal but not broken; this is a follow-up integration the spec already flags as out-of-scope here (see Future Work).

Concretely for this commit: confirm the in-proc launcher path is wired and the standalone path falls back gracefully. The launcher-internal call site is the load-bearing one for cost wins on real investigations.

- [ ] **Step 8: Run the whole strategies package + dependent server tests**

```bash
go test -race -count=1 ./pkg/mcp/strategies/... ./internal/server/...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/mcp/strategies/dispatch.go pkg/mcp/strategies/dispatch_test.go pkg/mcp/strategies/server.go system/wiki_proposal.yaml system/playbook_proposal.yaml
git commit -m "$(cat <<'EOF'
feat(strategies): walk_playbook dispatches subagent-mode playbooks

When a loaded playbook is marked dispatch: subagent, walk_playbook
spawns one pkg/mcp/subagent run on the profile's Models.Subagent
default (Haiku) instead of returning a walker stepView. The sub-agent
gets a prompt built from the playbook's prose + parent investigation
findings + summary + operator refinement. wiki_proposal and
playbook_proposal are marked dispatch: subagent — the ~20-turn
capture tail collapses to a single Haiku sub-agent run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

After writing the plan, I checked it against the spec:

**Spec coverage:**
- §Design 1 (`list_namespaces` trim) → Task 1.
- §Design 1 (`namespace_derivation` profile) → Tasks 2 + 3.
- §Design 2 (capture-tail trim) → Tasks 10 + 11.
- §Design 2 (Haiku sub-agent dispatch) → Tasks 12 + 13 + 14.
- §Design 3 (`list_playbooks` trim + filter) → Task 4.
- §Design 4 (walker auto-advance) → Task 5.
- §Cross-cutting (profile-configurable models) → Tasks 6 + 7 + 8 + 9.

**Placeholder scan:** no TBD/TODO/"appropriate"/"similar to" patterns. Each step carries the actual code or command.

**Type consistency:** confirmed across tasks —
- `NamespaceDerivation`, `NamespaceRule`, `RenderNamespaceHints` (Task 2) → consumed in `renderSystemPromptAddendum` (Task 3).
- `Models`, `applyDefaults` (Task 6) → consumed in `claude.SessionOpts.Model` (Task 7), `subagent.Options.Model` (Task 8), `internal/sessions/session.go` + `internal/server/manager.go` (Task 9).
- `DispatchMode`, `DispatchDefault`, `DispatchSubagent` (Task 12) → consumed in dispatch branch (Task 14).
- `DispatchInputs`, `BuildDispatchPrompt` (Task 13) → consumed in `runDispatch` (Task 14).
- `stepCompleteOut.AutoAdvancedThrough` (Task 5) → no downstream consumer.

**Gaps:** none surfaced. Task 14 step 7 leaves the standalone `triagent-mcp --kind=strategies` subprocess path with `ParentSessionState=nil` as a known limitation (in-proc path is the cost-win path); the spec's Future Work section already flags richer signal-watch integration as out-of-scope here.
