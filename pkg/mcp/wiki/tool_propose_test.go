package wiki

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSubAgent is a test seam — instead of calling subagent.Run, the tool
// dispatches through this function pointer when set. Tests inject a stub
// that simulates the sub-agent writing a draft to disk.
type stubResult struct {
	writeBody string
	summary   string
	// entityStubs simulates the sibling stub files the sub-agent writes
	// for new [[wikilink]] entities. Keyed by entity name. When non-empty,
	// the test seam writes a frontmatter-only file at
	// <proposalsPath>/<proposalID>__entity__<Type>__<name>.md with the
	// supplied Description.
	entityStubs map[string]stubEntity
	// rawEntityFiles lets a test write malformed or otherwise hand-shaped
	// stub files alongside the main draft. Keyed by filename suffix
	// (everything after "<proposalID>__"); the suffix should typically be
	// "entity__<type>__<name>.md".
	rawEntityFiles map[string]string
}

// stubEntity is the per-entity payload the test seam translates into a
// well-formed sibling stub file.
type stubEntity struct {
	Type        string
	Description string
}

func TestProposeWikiDraft_RejectsBadIncidentID(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, nil)
	_, out, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:    "Not A Valid Slug",
		Summary: "x",
		Status:  "open",
	})
	require.NoError(t, err)
	assert.Empty(t, out.ProposalID, "expected empty proposal id on validation failure")
}

func TestProposeWikiDraft_RejectsMissingStatus(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, nil)
	_, out, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:    "inc-123-foo",
		Summary: "x",
		// Status intentionally omitted — must be rejected.
	})
	require.NoError(t, err)
	assert.Empty(t, out.ProposalID, "expected empty proposal id on missing status")
}

func TestProposeWikiDraft_HappyPath_NewIncident(t *testing.T) {
	t.Parallel()
	stub := &stubResult{
		writeBody: `---
id: inc-12345-broker-ooms
date: 2026-04-12
title: Broker OOMs
status: resolved
services: [zeebe-broker]
errors: [oom-kill]
symptoms: [stuck-reconciliation]
links: {}
---

## Summary
Broker hit OOM during reconciliation.

## Root cause
[[zeebe-broker]] heap exhausted by [[oom-kill]] under [[stuck-reconciliation]] load.

## Fix
Increased heap; investigated leak.
`,
		entityStubs: map[string]stubEntity{
			"zeebe-broker":         {Type: "service", Description: "Zeebe broker — workflow engine state store."},
			"oom-kill":             {Type: "error", Description: "Container killed by the kernel for exceeding limits.memory."},
			"stuck-reconciliation": {Type: "symptom", Description: "Partition reconciliation that doesn't progress within its expected window."},
		},
		summary: "wrote draft",
	}
	srv := newTestServer(t, stub)
	_, out, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:             "inc-12345-broker-ooms",
		Summary:          "the investigation found broker OOMs",
		InvestigationURL: "http://localhost:8080/investigations/?id=sess-abc",
		SlackChannelURL:  "https://example.slack.com/archives/CXXX/p123",
		IncidentIOURL:    "https://app.incident.io/foo/123",
		Status:           "resolved",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.ProposalID, "bad proposal id")
	assert.True(t, strings.HasPrefix(out.ProposalID, "prop-"), "bad proposal id: %q", out.ProposalID)
	assert.Equal(t, "inc-12345-broker-ooms", out.Slug)
	assert.True(t, out.IsNew, "expected IsNew=true for new incident")
	// Source links must be authoritatively injected as links.* fields.
	assert.Contains(t, out.NewMD, "slack_channel: https://example.slack.com/archives/CXXX/p123", "slack_channel link not injected")
	assert.Contains(t, out.NewMD, "incident_io: https://app.incident.io/foo/123", "incident_io link not injected")
	assert.Contains(t, out.NewMD, "investigation: http://localhost:8080/investigations/?id=sess-abc", "investigation link not injected")
	// PLACEHOLDER never gets written anymore — the sub-agent stub writes
	// `links: {}` and the orchestrator stamps the real values post-write.
	assert.NotContains(t, out.NewMD, "PLACEHOLDER", "PLACEHOLDER should never appear in output")
	// Entity stubs must surface so the approve flow can populate ## Description.
	assert.Len(t, out.NewEntities, 3, "expected 3 entity stubs")
}

// TestProposeWikiDraft_RejectsMissingEntityStub guarantees that a sub-agent
// which references a new [[wikilink]] without writing the corresponding stub
// file fails the call, instead of silently producing a vault entity with an
// empty Description on approve.
func TestProposeWikiDraft_RejectsMissingEntityStub(t *testing.T) {
	t.Parallel()
	stub := &stubResult{
		writeBody: `---
id: inc-12345-broker-ooms
date: 2026-04-12
title: Broker OOMs
status: resolved
services: [zeebe-broker]
errors: []
symptoms: []
links: {}
---

## Summary
Broker hit OOM.

## Root cause
[[zeebe-broker]] ran out of heap.

## Fix
Increased heap.
`,
		// No entityStubs — sub-agent forgot to write one for [[zeebe-broker]].
		summary: "wrote draft",
	}
	srv := newTestServer(t, stub)
	res, out, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:    "inc-12345-broker-ooms",
		Summary: "broker OOMs",
		Status:  "resolved",
	})
	require.NoError(t, err)
	assert.Empty(t, out.ProposalID, "expected proposal cleanup on validation failure")
	require.NotNil(t, res, "expected error result")
	require.True(t, res.IsError, "expected error result")
	msg := errorMessage(res)
	assert.Contains(t, msg, "zeebe-broker", "error message should name the missing entity")
	assert.Contains(t, msg, "no stub file written", "error message should explain the missing-stub failure")
}

// TestProposeWikiDraft_RejectsEmptyDescription rejects a stub whose
// `description:` field exists but is blank — the bug we set out to fix.
func TestProposeWikiDraft_RejectsEmptyDescription(t *testing.T) {
	t.Parallel()
	stub := &stubResult{
		writeBody: `---
id: inc-12345-broker-ooms
date: 2026-04-12
title: Broker OOMs
status: resolved
services: [zeebe-broker]
errors: []
symptoms: []
links: {}
---

## Summary
Broker hit OOM.

## Root cause
[[zeebe-broker]] ran out of heap.

## Fix
Increased heap.
`,
		entityStubs: map[string]stubEntity{
			"zeebe-broker": {Type: "service", Description: "   "}, // whitespace-only — must fail
		},
		summary: "wrote draft",
	}
	srv := newTestServer(t, stub)
	res, out, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:    "inc-12345-broker-ooms",
		Summary: "broker OOMs",
		Status:  "resolved",
	})
	require.NoError(t, err)
	assert.Empty(t, out.ProposalID, "expected proposal cleanup on validation failure")
	require.NotNil(t, res, "expected error result")
	require.True(t, res.IsError, "expected error result")
	msg := errorMessage(res)
	assert.Contains(t, msg, "zeebe-broker", "error message should name the entity with empty description")
	assert.Contains(t, msg, "empty", "error message should call out the empty description")
}

// TestProposeWikiDraft_RejectsMalformedStub rejects a stub file whose
// frontmatter is unparseable — also a previously silent failure.
func TestProposeWikiDraft_RejectsMalformedStub(t *testing.T) {
	t.Parallel()
	stub := &stubResult{
		writeBody: `---
id: inc-12345-broker-ooms
date: 2026-04-12
title: Broker OOMs
status: resolved
services: [zeebe-broker]
errors: []
symptoms: []
links: {}
---

## Summary
Broker hit OOM.

## Root cause
[[zeebe-broker]] ran out of heap.

## Fix
Increased heap.
`,
		rawEntityFiles: map[string]string{
			// No leading '---', so the parser can't identify it as frontmatter.
			"entity__service__zeebe-broker.md": "type: service\nname: zeebe-broker\ndescription: hi\n",
		},
		summary: "wrote draft",
	}
	srv := newTestServer(t, stub)
	res, _, err := srv.proposeWikiDraft(context.Background(), nil, proposeWikiDraftIn{
		Slug:    "inc-12345-broker-ooms",
		Summary: "broker OOMs",
		Status:  "resolved",
	})
	require.NoError(t, err)
	require.NotNil(t, res, "expected error result")
	require.True(t, res.IsError, "expected error result")
	msg := errorMessage(res)
	assert.True(t, strings.Contains(msg, "malformed") || strings.Contains(msg, "no stub file"),
		"error message should call out the malformed file: %s", msg)
}

// newTestServer builds a Server with an injected sub-agent stub. The stub,
// when non-nil, writes its WriteBody to the requested draft path before
// returning, plus any sibling entity stub files the test specified.
func newTestServer(t *testing.T, stub *stubResult) *Server {
	t.Helper()
	vault := t.TempDir()
	props := t.TempDir()
	s, err := New(Options{VaultPath: vault, ProposalsPath: props})
	require.NoError(t, err, "New")
	if stub != nil {
		s.runSubAgent = func(ctx context.Context, prompt, parentID string) (string, error) {
			// The prompt embeds the draft path; extract and write the
			// stubbed body there to simulate the real sub-agent's Write
			// tool call.
			marker := "Write the full markdown file (frontmatter + body) to:\n\n  "
			i := strings.Index(prompt, marker)
			require.GreaterOrEqual(t, i, 0, "prompt missing draft-path marker")
			rest := prompt[i+len(marker):]
			draftPath := strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
			if err := os.WriteFile(draftPath, []byte(stub.writeBody), 0o600); err != nil {
				return "", err
			}
			proposalsDir := filepath.Dir(draftPath)
			proposalID := strings.SplitN(filepath.Base(draftPath), "__", 2)[0]
			for name, info := range stub.entityStubs {
				stubPath := filepath.Join(proposalsDir, fmt.Sprintf("%s__entity__%s__%s.md", proposalID, info.Type, name))
				content := fmt.Sprintf("---\ntype: %s\nname: %s\ndescription: %s\n---\n", info.Type, name, info.Description)
				if err := os.WriteFile(stubPath, []byte(content), 0o600); err != nil {
					return "", err
				}
			}
			for suffix, content := range stub.rawEntityFiles {
				stubPath := filepath.Join(proposalsDir, proposalID+"__"+suffix)
				if err := os.WriteFile(stubPath, []byte(content), 0o600); err != nil {
					return "", err
				}
			}
			return stub.summary, nil
		}
	}
	return s
}

// errorMessage extracts the human-readable text out of an mcp error result so
// tests can assert on it without poking at the Content slice directly.
func errorMessage(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
