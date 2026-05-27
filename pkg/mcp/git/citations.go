package git

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
)

// gitValidator existence-checks GitHub citations against the cloned repo.
// The validator runs `git cat-file -e <ref>:<path>` and `git rev-parse
// <sha>^{commit}` against the cache clone — no network calls. PR
// existence isn't checked here (it requires gh + network); the sub-agent
// prompt mandates self-verification via `gh pr view` instead. This
// validator only enforces that github_pr citations point at the scoped
// repo.
type gitValidator struct {
	repo    string // "<owner>/<name>" — must match every citation's Repo
	repoDir string // cloned repo path
	ctx     context.Context
}

func (v *gitValidator) Validate(cits []citations.Citation) []string {
	var errs []string
	for i, c := range cits {
		idx := i + 1
		switch c.Kind {
		case citations.KindGithubFile:
			if c.Repo != v.repo {
				errs = append(errs, fmt.Sprintf("[%d] github_file: repo %q does not match scoped repo %q", idx, c.Repo, v.repo))
				continue
			}
			if !v.fileExistsAtRef(c.Ref, c.Path) {
				errs = append(errs, fmt.Sprintf("[%d] github_file: %s does not exist at ref %s in %s", idx, c.Path, c.Ref, v.repo))
			}
		case citations.KindGithubCommit:
			if c.Repo != v.repo {
				errs = append(errs, fmt.Sprintf("[%d] github_commit: repo %q does not match scoped repo %q", idx, c.Repo, v.repo))
				continue
			}
			if !v.commitResolves(c.SHA) {
				errs = append(errs, fmt.Sprintf("[%d] github_commit: sha %s does not resolve in %s", idx, c.SHA, v.repo))
			}
		case citations.KindGithubPR:
			if c.Repo != v.repo {
				errs = append(errs, fmt.Sprintf("[%d] github_pr: repo %q does not match scoped repo %q", idx, c.Repo, v.repo))
			}
			// PR existence is the sub-agent's responsibility (gh pr view
			// in the prompt). Validator stops at repo-match.
		default:
			errs = append(errs, fmt.Sprintf("[%d] this MCP only supports github_* citations (got %q)", idx, c.Kind))
		}
	}
	return errs
}

func (v *gitValidator) fileExistsAtRef(ref, path string) bool {
	cmd := exec.CommandContext(v.ctx, "git", "cat-file", "-e", ref+":"+path)
	cmd.Dir = v.repoDir
	return cmd.Run() == nil
}

func (v *gitValidator) commitResolves(sha string) bool {
	cmd := exec.CommandContext(v.ctx, "git", "rev-parse", sha+"^{commit}")
	cmd.Dir = v.repoDir
	return cmd.Run() == nil
}
