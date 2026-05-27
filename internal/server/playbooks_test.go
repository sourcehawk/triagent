package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// playbookBodiesMatch is the single oracle for "is this user playbook
// drifted from upstream?" — used by the list endpoint, the editor's
// push-PR gate (mirrored in TS), and the auto-commit-message classifier.
// Drift between any of these reports is a UX bug: the operator sees the
// list claim "unsynced local changes" while the editor refuses to push
// because there's no diff.
//
// The transient fields stripped on both sides are infrastructure stamps
// or directory-derived metadata, not operator intent:
//   - active: stamped by writeUserPlaybook / push-PR (the "is the
//     loader picking this file?" toggle).
//   - version: launcher-side cache stamp; never an operator concern.
//   - type: redundant with the directory the file lives in (loader uses
//     the directory; the editor injects this field into draft.type from
//     the API response, so dumpPlaybookYAML emits it on push, but the
//     user-side save path drops it. Without stripping here, every push
//     shows the list as drifted forever.)
func TestPlaybookBodiesMatch_StripsTransientFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		a, b      string
		wantMatch bool
	}{
		{
			name: "type field present on one side only — list/editor disagreement scenario",
			a: `id: backup
symptom: x
entrypoint: n1
nodes:
  n1:
    description: d
`,
			b: `active: true
id: backup
symptom: x
entrypoint: n1
nodes:
  n1:
    description: d
type: investigation
`,
			wantMatch: true,
		},
		{
			name: "active stripped on both sides",
			a: `id: x
symptom: x
entrypoint: n
nodes: {n: {description: d}}
active: true
`,
			b: `id: x
symptom: x
entrypoint: n
nodes: {n: {description: d}}
active: false
`,
			wantMatch: true,
		},
		{
			name: "real semantic diff (extra node) — must report differing",
			a: `id: x
symptom: x
entrypoint: n
nodes:
  n: {description: d}
`,
			b: `id: x
symptom: x
entrypoint: n
nodes:
  n: {description: d}
  m: {description: d}
`,
			wantMatch: false,
		},
		{
			name: "real semantic diff (description text) — must report differing",
			a: `id: x
symptom: x
entrypoint: n
nodes:
  n: {description: original}
`,
			b: `id: x
symptom: x
entrypoint: n
nodes:
  n: {description: changed}
`,
			wantMatch: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := playbookBodiesMatch(tc.a, tc.b)
			assert.Equal(t, tc.wantMatch, got, "playbookBodiesMatch result wrong")
		})
	}
}

// stripVersionFromPlaybookYAML must drop redundant infrastructure fields
// before the body lands in upstream — otherwise every push leaves a
// directory/file mismatch the list endpoint will surface as drift.
// Currently strips `version` (launcher cache stamp); must also strip
// `type` (directory-as-type, redundant in body).
func TestStripPushBodyDropsRedundantFields(t *testing.T) {
	t.Parallel()
	body := `id: backup
symptom: x
entrypoint: n
nodes:
  n: {description: d}
type: investigation
version: launcher-stamp-123
`
	out, err := stripVersionFromPlaybookYAML(body)
	require.NoError(t, err)
	assert.NotContains(t, out, "version:", "version stamp must not leak into upstream")
	assert.NotContains(t, out, "type:", "redundant type field must not leak into upstream — directory carries it")
	// Sanity: real fields survive.
	assert.Contains(t, out, "id: backup", "id must survive the strip")
	assert.Contains(t, out, "symptom:", "symptom must survive the strip")
	assert.True(t, strings.Contains(out, "n:"), "node must survive the strip")
}
