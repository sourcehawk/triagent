package citations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShapeCheck_SlackThread_OK(t *testing.T) {
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "500.000100"},
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "480.000100", MessageTS: "485.000700"},
	}
	assert.Empty(t, ShapeCheck(cits))
}

func TestShapeCheck_SlackThread_MissingChannelID(t *testing.T) {
	cits := []Citation{{Kind: KindSlackThread, ThreadTS: "500.000100"}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "channel_id")
}

func TestShapeCheck_GithubFile_OK(t *testing.T) {
	cits := []Citation{
		{Kind: KindGithubFile, Repo: "example-org/example-repo", Path: "x.go", Ref: "abc"},
		{Kind: KindGithubFile, Repo: "example-org/example-repo", Path: "x.go", Ref: "abc", LineStart: 10, LineEnd: 20},
	}
	assert.Empty(t, ShapeCheck(cits))
}

func TestShapeCheck_GithubFile_MissingPath(t *testing.T) {
	cits := []Citation{{Kind: KindGithubFile, Repo: "example-org/example-repo", Ref: "abc"}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "path")
}

func TestShapeCheck_GithubFile_BadLineRange(t *testing.T) {
	cits := []Citation{{Kind: KindGithubFile, Repo: "x/y", Path: "f", Ref: "r", LineStart: 50, LineEnd: 10}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "line_end")
}

func TestShapeCheck_GithubCommit_RequiresFullSHA(t *testing.T) {
	cits := []Citation{{Kind: KindGithubCommit, Repo: "x/y", SHA: "abc123"}} // 6 chars, not 40
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "sha")
}

func TestShapeCheck_GithubCommit_OK(t *testing.T) {
	cits := []Citation{{Kind: KindGithubCommit, Repo: "x/y", SHA: "0123456789abcdef0123456789abcdef01234567"}}
	assert.Empty(t, ShapeCheck(cits))
}

func TestShapeCheck_GithubPR_OK(t *testing.T) {
	cits := []Citation{{Kind: KindGithubPR, Repo: "x/y", PRNum: 42}}
	assert.Empty(t, ShapeCheck(cits))
}

func TestShapeCheck_GithubPR_NonPositiveNum(t *testing.T) {
	cits := []Citation{{Kind: KindGithubPR, Repo: "x/y", PRNum: 0}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "pr_num")
}

func TestShapeCheck_FieldsFromOtherKind(t *testing.T) {
	// A github_file citation with PRNum set is malformed.
	cits := []Citation{{Kind: KindGithubFile, Repo: "x/y", Path: "f", Ref: "r", PRNum: 42}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "pr_num")
}

func TestShapeCheck_UnknownKind(t *testing.T) {
	cits := []Citation{{Kind: Kind("not_a_kind"), ChannelID: "x"}}
	errs := ShapeCheck(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "kind")
}
