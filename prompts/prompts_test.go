package prompts

import (
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/stretchr/testify/assert"
)

// testProf returns a minimal inline profile that exercises the prompt-building
// paths without depending on any embedded profile file. The investigation
// inputs exercise prompt_key rendering with a typical cluster_id entry
// that check prompt_key rendering intact.
func testProf() *profile.Profile {
	return &profile.Profile{
		Name: "test",
		Auth: profile.Auth{Kind: "teleport"},
		Playbooks: profile.Playbooks{
			Entrypoint: "investigation",
			Closing:    "capture_offer",
		},
		Prompts: map[string]string{
			"system.md":       "SRE assistant — on-call engineer for the cluster.",
			"architecture.md": "## Cluster architecture\n\nDescribes the platform.",
			"strategies.md":   "## Investigation strategies\n\nGuides the agent.",
			"editor.md":       "Editor assistant.",
			"wiki_editor.md":  "Wiki editor assistant.",
		},
		InvestigationInputs: []profile.InvestigationInput{
			{
				ID:       "cluster_id",
				Label:    "Cluster",
				Type:     "cluster_id",
				Optional: true,
				PromptKeys: []profile.PromptKey{
					{Key: "cluster-id", Value: "{{.value}}", If: `{{ne .value ""}}`},
					{Key: "cluster-resource-namespace", Value: "{{.value}}-workload", If: `{{ne .value ""}}`},
				},
			},
			{
				ID:       "incident_url",
				Label:    "Incident URL",
				Type:     "url",
				Optional: true,
				PromptKeys: []profile.PromptKey{
					{Key: "incident-url", Value: "{{.value}}", If: `{{ne .value ""}}`},
				},
			},
			{
				ID:       "slack_channel",
				Label:    "Slack channel",
				Type:     "slack_channel",
				Optional: true,
				PromptKeys: []profile.PromptKey{
					{Key: "slack-channel-id", Value: "{{.id}}", If: `{{ne .id ""}}`},
					{Key: "slack-channel-name", Value: "{{.name}}", If: `{{ne .name ""}}`},
					{Key: "slack-channel-url", Value: "{{.url}}", If: `{{and (eq .id "") (ne .url "")}}`},
				},
			},
			{
				ID:       "notes",
				Label:    "Notes",
				Type:     "textarea",
				Optional: true,
				PromptKeys: []profile.PromptKey{
					{Key: "operator-notes", Value: "{{.value}}", If: `{{ne .value ""}}`},
				},
			},
		},
	}
}

func TestBuild_IncludesAllSections(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context: "ctx-A",
		InputValues: map[string]map[string]any{
			"cluster_id": {"value": "abc"},
			"notes":      {"value": "broker restarts"},
		},
	}, testProf())

	for _, want := range []string{
		"SRE assistant",
		"## Cluster architecture",
		"## Investigation strategies",
		"## Environment",
		"kubernetes-context: ctx-A",
		"cluster-id: abc",
		"cluster-resource-namespace: abc-workload",
		"suggested-entrypoint-playbook: investigation",
		"suggested-closing-playbook: capture_offer",
		"## User-supplied context",
		"broker restarts",
		"mcp__triagent-strategies__*",
		"mcp__triagent-k8s__*",
	} {
		assert.Contains(t, out, want)
	}
}

func TestBuild_EmptyUserNotesPromptsClarification(t *testing.T) {
	t.Parallel()
	out := Build(Env{Context: "c"}, testProf())
	assert.Contains(t, out, "(none provided", "expected empty-notes fallback")
}

func TestBuild_ExtraMCPsBulletAbsentWhenProfileEmpty(t *testing.T) {
	t.Parallel()
	// When the profile has no extra_mcps, no extra MCP bullets appear.
	prof := &profile.Profile{
		Playbooks: profile.Playbooks{Entrypoint: "investigation", Closing: "capture_offer"},
		Prompts:   map[string]string{"system.md": "", "architecture.md": "", "strategies.md": ""},
	}
	out := Build(Env{Context: "c"}, prof)
	assert.NotContains(t, out, "Docs search tools", "docs section included even though extra_mcps is empty")
	assert.NotContains(t, out, "mcp__example-docs__", "example-docs bullet should be absent when not in extra_mcps")
}

func TestBuild_ParameterBlockEmitsClusterIDFromInputValues(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context: "c",
		InputValues: map[string]map[string]any{
			"cluster_id": {"value": "abc"},
		},
	}, testProf())
	assert.Contains(t, out, "cluster-id: abc")
	assert.Contains(t, out, "cluster-resource-namespace: abc-workload")
	assert.Contains(t, out, "kubernetes-context: c")
}

func TestBuild_ParameterBlockOmitsClusterKeysWhenNoClusterID(t *testing.T) {
	t.Parallel()
	// cluster_id input has if: '{{ne .value ""}}' — keys are omitted when empty.
	out := Build(Env{Context: "c"}, testProf())
	assert.NotContains(t, out, "cluster-id:")
	assert.NotContains(t, out, "cluster-resource-namespace:")
	assert.Contains(t, out, "kubernetes-context: c")
}

func TestBuild_ParameterBlockEmitsUnsetContextForEmptyEnv(t *testing.T) {
	t.Parallel()
	out := Build(Env{}, testProf())
	assert.Contains(t, out, "kubernetes-context: <unset>")
}

func TestBuildIncludesExtraMCPs(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		Playbooks: profile.Playbooks{Entrypoint: "investigation", Closing: "capture_offer"},
		Prompts:   map[string]string{"system.md": "", "architecture.md": "", "strategies.md": ""},
		ExtraMCPs: []profile.ExtraMCP{
			{Alias: "alpha", Description: "alpha docs"},
			{Alias: "beta", Description: "beta runbooks"},
		},
	}
	got := Build(Env{}, prof)
	if !strings.Contains(got, "mcp__alpha__*") || !strings.Contains(got, "alpha docs") {
		t.Errorf("alpha entry missing: %s", got)
	}
	if !strings.Contains(got, "mcp__beta__*") || !strings.Contains(got, "beta runbooks") {
		t.Errorf("beta entry missing: %s", got)
	}
}

func TestBuild_LinkedReposSectionListsAliases(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context: "c",
		LinkedRepos: []repos.LinkedRepo{
			{Owner: "example-org", Name: "example-service", Description: "Example service"},
			{Owner: "example-org", Name: "example-ui", Alias: "ui"},
		},
	}, testProf())
	assert.Contains(t, out, "## Linked repositories", "missing linked repositories section")
	assert.Contains(t, out, "`example-service` — example-org/example-service — Example service", "expected example-service entry with description")
	assert.Contains(t, out, "`ui` — example-org/example-ui", "expected example-ui entry with custom alias")
}

func TestBuild_LinkedReposSectionOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	out := Build(Env{Context: "c"}, testProf())
	// Match the section header specifically — the strategies prompt
	// also mentions "Linked repositories" inside the citation-style
	// principle (it tells the agent which section of the Environment
	// to read for repo metadata), which is fine to keep in even when
	// the section itself is omitted.
	assert.NotContains(t, out, "## Linked repositories\n", "linked repositories section should be omitted when LinkedRepos is empty")
}

func TestBuild_SlackChannelURLForm(t *testing.T) {
	out := Build(Env{
		InputValues: map[string]map[string]any{
			"slack_channel": {"id": "C0123ABC", "name": "incident-foo", "url": "https://example.slack.com/archives/C0123ABC"},
		},
	}, testProf())
	assert.Contains(t, out, "slack_link: https://example.slack.com/archives/C0123ABC")
	assert.Contains(t, out, "slack_channel_name: #incident-foo")
}

func TestBuild_SlackChannelIDOnlyFallback(t *testing.T) {
	out := Build(Env{
		InputValues: map[string]map[string]any{
			"slack_channel": {"id": "C0123ABC", "name": "incident-foo", "url": ""},
		},
	}, testProf())
	assert.NotContains(t, out, "slack_link:", "no URL → no slack_link")
	assert.Contains(t, out, "slack_channel_id: C0123ABC")
	assert.Contains(t, out, "slack_channel_name: #incident-foo")
	assert.Contains(t, out, "no Slack URL available", "agent must be told slack_link is unavailable")
}

func TestBuild_NoSlackContextOmitsSection(t *testing.T) {
	out := Build(Env{}, testProf()) // nothing slack-related
	assert.NotContains(t, out, "slack_link:")
	assert.NotContains(t, out, "slack_channel_id:")
}

func TestBuild_EmitsIncidentioMCPLine(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context:                "kind",
		IncidentioMCPAvailable: true,
	}, testProf())
	assert.Contains(t, out, "- incident.io tools: mcp__triagent-incidentio__*",
		"expected incidentio MCP line in Environment block")
}

func TestBuild_EmitsSlackMCPLine(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context:           "kind",
		SlackMCPAvailable: true,
	}, testProf())
	assert.Contains(t, out, "- Slack tools: mcp__triagent-slack__*",
		"expected slack MCP line in Environment block")
}

func TestBuild_OmitsLinesWhenNotAvailable(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context: "kind",
	}, testProf())
	assert.NotContains(t, out, "- incident.io tools: mcp__triagent-incidentio__*",
		"Environment block should not emit the incident.io tools line when IncidentioMCPAvailable=false")
	assert.NotContains(t, out, "- Slack tools: mcp__triagent-slack__*",
		"Environment block should not emit the Slack tools line when SlackMCPAvailable=false")
}

func TestBuildEditor_WikiSubject_ListsStrategiesMCP(t *testing.T) {
	t.Parallel()
	out := BuildEditor(WikiSubject{
		Kind: "entry",
		ID:   "inc-1234-test",
	}, BaseEnv{}, testProf())
	assert.Contains(t, out, "- Investigation playbooks: mcp__triagent-strategies__*",
		"wiki editor environment block must advertise the strategies MCP so the agent knows playbooks are reachable")
	// The parenthetical names tools the agent will actually call —
	// every name in the list must exist in the strategies MCP server.
	// Listing a non-existent tool teaches the agent to call something
	// that returns "tool not found" at runtime.
	for _, tool := range []string{"list_playbooks", "walk_playbook", "get_state", "step_complete"} {
		assert.Contains(t, out, tool,
			"strategies MCP parenthetical must name real tools — "+tool+" missing")
	}
}

// Backfill case: new wiki subject + at least one source MCP wired.
// Closing block drives the agent into wiki_backfill_ingestion.
func TestBuildEditor_WikiSubject_BackfillBlockDrivesPlaybook(t *testing.T) {
	t.Parallel()
	out := BuildEditor(WikiSubject{
		Kind:             "entry",
		ID:               "inc-1234-test",
		ExistingMarkdown: "",
	}, BaseEnv{
		Sources: Sources{
			IncidentioURL: "https://app.incident.io/acme/incidents/427",
			IncidentioRef: "427",
		},
	}, testProf())
	assert.Contains(t, out, "## Backfill resolved incident",
		"backfill sessions must use the backfill closing-block heading")
	assert.Contains(t, out, "wiki_backfill_ingestion",
		"backfill closing block must name the meta-playbook")
	assert.Contains(t, out, "mcp__triagent-strategies__walk_playbook",
		"backfill closing block must direct the agent into walk_playbook")
	assert.NotContains(t, out, "Wait for their first request",
		"backfill sessions must not contain the wait-for-operator instruction")
}

// Sourceless-new case: new wiki subject, no sources wired. Falls back
// to the existing "wait for the operator" closing block — operator
// drives by chatting.
func TestBuildEditor_WikiSubject_SourcelessNewWaits(t *testing.T) {
	t.Parallel()
	out := BuildEditor(WikiSubject{
		Kind:             "entry",
		ID:               "inc-1234-test",
		ExistingMarkdown: "",
	}, BaseEnv{}, testProf())
	assert.Contains(t, out, "## New wiki entry",
		"sourceless-new sessions keep the existing 'New wiki entry' heading")
	assert.Contains(t, out, "Wait for their first request",
		"sourceless-new sessions wait for the operator's first message")
	assert.NotContains(t, out, "## Backfill resolved incident",
		"sourceless-new sessions must not emit the backfill closing block")
}

// Edit case: ExistingMarkdown present. Backfill condition does not
// fire even when sources are wired — the operator opened the editor
// drawer on an existing entry to drive turn-by-turn.
func TestBuildEditor_WikiSubject_EditModeUnchanged(t *testing.T) {
	t.Parallel()
	out := BuildEditor(WikiSubject{
		Kind:             "entry",
		ID:               "inc-1234-test",
		ExistingMarkdown: "## Summary\n\nExisting body.\n",
	}, BaseEnv{
		Sources: Sources{
			IncidentioURL: "https://app.incident.io/acme/incidents/427",
			IncidentioRef: "427",
		},
	}, testProf())
	assert.Contains(t, out, "## Existing wiki entry",
		"edit-mode sessions keep the existing-entry heading")
	assert.Contains(t, out, "Wait for their first request",
		"edit-mode sessions wait for the operator's first message")
	assert.NotContains(t, out, "## Backfill resolved incident",
		"edit-mode sessions must not emit the backfill closing block")
}

func TestBuild_LinkedRepos_AdvertisesArchitectureSummaryFirstStop(t *testing.T) {
	t.Parallel()
	out := Build(Env{
		Context: "ctx",
		LinkedRepos: []repos.LinkedRepo{
			{Owner: "example-org", Name: "example-service", Description: "Example service — engine and leader-election."},
		},
	}, testProf())
	// The new sentence must mention the cheap tool by name AND tell the
	// agent it's the first-stop. Anything weaker doesn't change the
	// agent's behaviour.
	assert.Contains(t, out, "get_repo_architecture_summary",
		"linked-repos prose must name the cheap-tool first-stop")
	assert.Contains(t, out, "first",
		"linked-repos prose must convey first-stop semantics")
}

func TestBuildIncludesAutoTriggerHintWhenSet(t *testing.T) {
	prof := testProf()
	plain := Build(Env{}, prof)
	if strings.Contains(plain, "Auto-triggered investigation") {
		t.Fatal("plain prompt should not include the auto-trigger hint")
	}

	hinted := Build(Env{OriginatingSignalSet: true}, prof)
	if !strings.Contains(hinted, "Auto-triggered investigation") {
		t.Fatal("hinted prompt should include the auto-trigger hint")
	}
	if !strings.Contains(hinted, "choose `wiki`") {
		t.Fatal("hinted prompt should nudge toward wiki capture decision")
	}
}

// Every session the launcher spawns writes prose a human reads later
// (summaries, wiki entries, playbook YAML). The writing-simply skill
// rides in the system prompt rather than relying on skill discovery,
// which the investigation and editor sessions cannot use (their cwd is
// the operator's launch directory).
func TestBuild_AppendsWritingStyleSection(t *testing.T) {
	t.Parallel()
	out := Build(Env{}, testProf())
	assert.Contains(t, out, "## Writing style")
	assert.Contains(t, out, "### Self-check")
	assert.Less(t, strings.Index(out, "## Writing style"), strings.Index(out, "## Environment"),
		"writing style is guidance, so it belongs before the Environment block")
}

func TestBuildEditor_AppendsWritingStyleSection(t *testing.T) {
	t.Parallel()
	for _, subject := range []Subject{
		PlaybookSubject{ID: "pb", Version: "v1"},
		WikiSubject{Kind: "entry", ID: "inc-x"},
	} {
		out := BuildEditor(subject, BaseEnv{}, testProf())
		assert.Contains(t, out, "## Writing style", "%T", subject)
		assert.Contains(t, out, "### Self-check", "%T", subject)
	}
}
