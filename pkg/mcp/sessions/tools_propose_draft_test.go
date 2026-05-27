package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProposeSessionDraft_MissingArgs(t *testing.T) {
	t.Parallel()
	s, err := New(Options{ProposalsPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.proposeDraftInternal(context.Background(), proposeDraftInput{}, "")
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestProposeSessionDraft_MissingMetadataFile(t *testing.T) {
	t.Parallel()
	s, err := New(Options{ProposalsPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	eventsPath := filepath.Join(tmp, "events.jsonl")
	_ = os.WriteFile(eventsPath, []byte("{}\n"), 0o644)
	_, err = s.proposeDraftInternal(context.Background(), proposeDraftInput{
		MetadataPath: filepath.Join(tmp, "nonexistent.json"),
		EventsPath:   eventsPath,
		ProposalID:   "p1",
	}, "")
	if err == nil {
		t.Fatal("expected error for missing metadata file")
	}
}

func TestProposeSessionDraft_WritesProposal(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()
	s, err := New(Options{ProposalsPath: proposals})
	if err != nil {
		t.Fatal(err)
	}

	s.runSubAgent = func(ctx context.Context, prompt, _ string) (string, error) {
		// Simulate the sub-agent writing a draft. The prompt embeds the
		// outPath as the first %q arg; we derive it from proposals+proposalID
		// rather than parsing the prompt, since we control proposalID="p1".
		draftPath := filepath.Join(proposals, "p1.md")
		return "ok", os.WriteFile(draftPath, []byte("---\nschema_version: 1\n---\n## Summary\ntest\n"), 0o644)
	}

	tmp := t.TempDir()
	metaPath := filepath.Join(tmp, "metadata.json")
	eventsPath := filepath.Join(tmp, "events.jsonl")
	_ = os.WriteFile(metaPath, []byte("{}"), 0o644)
	_ = os.WriteFile(eventsPath, []byte("{}\n"), 0o644)

	out, err := s.proposeDraftInternal(context.Background(), proposeDraftInput{
		MetadataPath: metaPath,
		EventsPath:   eventsPath,
		ProposalID:   "p1",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(proposals, "p1.md")
	if out.ProposalPath != want {
		t.Fatalf("unexpected path: got %q, want %q", out.ProposalPath, want)
	}
	if out.ProposalID != "p1" {
		t.Fatalf("unexpected proposal id: %q", out.ProposalID)
	}
}

func TestProposeSessionDraft_SubAgentDoesNotWrite(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()
	s, err := New(Options{ProposalsPath: proposals})
	if err != nil {
		t.Fatal(err)
	}

	// Sub-agent returns success but doesn't write the file.
	s.runSubAgent = func(_ context.Context, _, _ string) (string, error) {
		return "ok", nil
	}

	tmp := t.TempDir()
	metaPath := filepath.Join(tmp, "metadata.json")
	eventsPath := filepath.Join(tmp, "events.jsonl")
	_ = os.WriteFile(metaPath, []byte("{}"), 0o644)
	_ = os.WriteFile(eventsPath, []byte("{}\n"), 0o644)

	_, err = s.proposeDraftInternal(context.Background(), proposeDraftInput{
		MetadataPath: metaPath,
		EventsPath:   eventsPath,
		ProposalID:   "p2",
	}, "")
	if err == nil {
		t.Fatal("expected error when sub-agent did not write file")
	}
}

func TestNew_RequiresProposalsPath(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	if err == nil {
		t.Fatal("expected error for missing ProposalsPath")
	}
}

func TestDeleteProposal_NoopWhenMissing(t *testing.T) {
	t.Parallel()
	err := DeleteProposal(t.TempDir(), "nonexistent-prop")
	if err != nil {
		t.Fatalf("DeleteProposal should be no-op when file missing, got: %v", err)
	}
}

func TestDeleteProposal_RemovesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "prop-abc.md")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if err := DeleteProposal(dir, "prop-abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file should be deleted")
	}
}
