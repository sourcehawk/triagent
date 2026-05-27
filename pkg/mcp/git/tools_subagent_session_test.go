package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

// TestAnalyzeChange_CitationsRetry_ResumesSession verifies the
// analyze_change citation-correction retry resumes the prior turn's
// Claude session rather than spawning fresh. Without this, every
// citation retry pays the cost of re-orienting in the repo, which is
// substantial for an analyze_change run that has typically navigated
// to a specific commit and read several files before emitting.
func TestAnalyzeChange_CitationsRetry_ResumesSession(t *testing.T) {
	t.Parallel()
	cacheDir, _ := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "remote", "add", "origin", bareDir)
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "push", "origin", "main")
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "fetch", "--prune", "--tags")

	var observedResumeIDs []string
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")},
		runSubAgent: func(_ context.Context, _, _, _, resumeID string) (subagent.Result, error) {
			observedResumeIDs = append(observedResumeIDs, resumeID)
			// No citations block on either call → citations runner
			// triggers the retry; both calls return the same session id.
			return subagent.Result{
				Summary:   "I have no citations.",
				SessionID: "sess-analyze-1",
			}, nil
		},
	}

	_, _, err := s.analyzeChange(context.Background(), nil, analyzeChangeIn{
		Ref:      "v0.2.0",
		Question: "what changed?",
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(observedResumeIDs), 2,
		"citation retry must invoke the sub-agent at least twice")
	require.Empty(t, observedResumeIDs[0], "first call must not carry a resume id")
	require.Equal(t, "sess-analyze-1", observedResumeIDs[1],
		"citation retry must resume the session captured from the first call")
}

// TestCorrelateWithFindings_CitationsRetry_ResumesSession — same
// pattern as analyze_change, applied to correlate_with_findings.
func TestCorrelateWithFindings_CitationsRetry_ResumesSession(t *testing.T) {
	t.Parallel()
	cacheDir, _ := initFixtureRepo(t, "o", "n")
	var observedResumeIDs []string
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")},
		runSubAgent: func(_ context.Context, _, _, _, resumeID string) (subagent.Result, error) {
			observedResumeIDs = append(observedResumeIDs, resumeID)
			return subagent.Result{
				Summary:   "no relevant correlations.",
				SessionID: "sess-correlate-1",
			}, nil
		},
	}

	_, _, err := s.correlateWithFindings(context.Background(), nil, correlateIn{
		Findings: "broker oom",
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(observedResumeIDs), 2,
		"correlate's citation retry must fire at least twice")
	require.Empty(t, observedResumeIDs[0])
	require.Equal(t, "sess-correlate-1", observedResumeIDs[1],
		"correlate's citation retry must resume the captured session id")
}

