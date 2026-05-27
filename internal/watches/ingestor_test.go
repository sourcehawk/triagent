package watches

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestBuildSystemPromptWithWiki(t *testing.T) {
	in := PromptInputs{
		WatchName: "n", WatchKind: "github_issues", SourceDescription: "example-org/example",
		KubeContexts: []string{"prod"}, Repos: []string{"example-org/example — host"}, SlackChannels: []string{"#alerts"},
		CustomInstructions: "Treat external contribs as unclear.", WikiAvailable: true,
	}
	got, err := BuildSystemPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"signal-ingestion agent", "Watch", "n", "Wiki tools", "wiki_correlate",
		"Watch-specific instructions", "Treat external contribs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildSystemPromptWithoutWiki(t *testing.T) {
	got, _ := BuildSystemPrompt(PromptInputs{WatchName: "n", WatchKind: "github_issues"})
	if strings.Contains(got, "Wiki tools") {
		t.Error("Wiki section should be omitted when WikiAvailable=false")
	}
}

func TestBuildUserPromptIncludesItems(t *testing.T) {
	items := []Item{
		{ID: "a", SourceKind: "github_issues", Snapshot: Snapshot{Title: "OOMs in prod", Body: "engine died", URL: "https://example.test/1", Author: "x"}},
		{ID: "b", SourceKind: "github_issues", Snapshot: Snapshot{Title: "Docs typo", Body: "missing word", URL: "https://example.test/2", Author: "y"}},
	}
	got := BuildUserPrompt(items)
	for _, want := range []string{"Item a:", "Item b:", "OOMs in prod", "Docs typo", "Begin."} {
		if !strings.Contains(got, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
}

func TestBuildMCPConfigIncludesSignalIngest(t *testing.T) {
	dir := t.TempDir()
	cfgPath, err := WriteMCPConfig(MCPConfigOpts{
		Dir:           dir,
		MCPBinaryPath: "/usr/bin/triagent-mcp",
		LauncherURL:   "http://127.0.0.1:5000",
		LauncherToken: "tok",
		WatchID:       "w1",
		WikiPath:      "",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfgPath)
	if !bytes.Contains(b, []byte(`"triagent-signal-ingest"`)) {
		t.Fatalf("missing triagent-signal-ingest: %s", b)
	}
	if bytes.Contains(b, []byte(`"triagent-wiki"`)) {
		t.Fatal("wiki should be omitted when WikiPath empty")
	}
}

func TestBuildMCPConfigIncludesWikiWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	cfgPath, _ := WriteMCPConfig(MCPConfigOpts{
		Dir:           dir,
		MCPBinaryPath: "x",
		LauncherURL:   "u",
		LauncherToken: "t",
		WatchID:       "w1",
		WikiPath:      "/vault",
	})
	b, _ := os.ReadFile(cfgPath)
	if !bytes.Contains(b, []byte(`"triagent-wiki"`)) {
		t.Fatalf("expected triagent-wiki block when WikiPath set: %s", b)
	}
}
