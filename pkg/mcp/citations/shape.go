package citations

import (
	"fmt"
	"regexp"
)

// fullSHA matches a full 40-char hex git sha. github_commit citations
// must use the full sha (not an abbreviation) so they're stable references.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ShapeCheck verifies that each citation has its kind's required fields
// and no fields belonging to other kinds. Returns one error per problem;
// empty slice means all entries are well-shaped. Existence checks (does
// this thread/file/sha actually exist in ground truth) are the per-MCP
// Validator's job — ShapeCheck only enforces the schema.
func ShapeCheck(citations []Citation) []string {
	var errs []string
	for i, c := range citations {
		idx := i + 1
		switch c.Kind {
		case KindSlackThread:
			errs = append(errs, slackThreadShape(idx, c)...)
		case KindGithubFile:
			errs = append(errs, githubFileShape(idx, c)...)
		case KindGithubCommit:
			errs = append(errs, githubCommitShape(idx, c)...)
		case KindGithubPR:
			errs = append(errs, githubPRShape(idx, c)...)
		default:
			errs = append(errs, fmt.Sprintf("[%d] unknown kind %q", idx, c.Kind))
		}
	}
	return errs
}

func slackThreadShape(i int, c Citation) []string {
	var errs []string
	if c.ChannelID == "" {
		errs = append(errs, fmt.Sprintf("[%d] slack_thread: channel_id is required", i))
	}
	if c.ThreadTS == "" {
		errs = append(errs, fmt.Sprintf("[%d] slack_thread: thread_ts is required", i))
	}
	for _, f := range disallowedSlack(c) {
		errs = append(errs, fmt.Sprintf("[%d] slack_thread: %s must not be set", i, f))
	}
	return errs
}

func githubFileShape(i int, c Citation) []string {
	var errs []string
	if c.Repo == "" {
		errs = append(errs, fmt.Sprintf("[%d] github_file: repo is required", i))
	}
	if c.Path == "" {
		errs = append(errs, fmt.Sprintf("[%d] github_file: path is required", i))
	}
	if c.Ref == "" {
		errs = append(errs, fmt.Sprintf("[%d] github_file: ref is required", i))
	}
	if c.LineStart < 0 || c.LineEnd < 0 {
		errs = append(errs, fmt.Sprintf("[%d] github_file: line numbers must be >= 0", i))
	}
	if c.LineEnd != 0 && c.LineEnd < c.LineStart {
		errs = append(errs, fmt.Sprintf("[%d] github_file: line_end (%d) must be >= line_start (%d)", i, c.LineEnd, c.LineStart))
	}
	for _, f := range disallowedGithubFile(c) {
		errs = append(errs, fmt.Sprintf("[%d] github_file: %s must not be set", i, f))
	}
	return errs
}

func githubCommitShape(i int, c Citation) []string {
	var errs []string
	if c.Repo == "" {
		errs = append(errs, fmt.Sprintf("[%d] github_commit: repo is required", i))
	}
	if !fullSHA.MatchString(c.SHA) {
		errs = append(errs, fmt.Sprintf("[%d] github_commit: sha must be a full 40-character hex string (got %q)", i, c.SHA))
	}
	for _, f := range disallowedGithubCommit(c) {
		errs = append(errs, fmt.Sprintf("[%d] github_commit: %s must not be set", i, f))
	}
	return errs
}

func githubPRShape(i int, c Citation) []string {
	var errs []string
	if c.Repo == "" {
		errs = append(errs, fmt.Sprintf("[%d] github_pr: repo is required", i))
	}
	if c.PRNum <= 0 {
		errs = append(errs, fmt.Sprintf("[%d] github_pr: pr_num must be > 0 (got %d)", i, c.PRNum))
	}
	for _, f := range disallowedGithubPR(c) {
		errs = append(errs, fmt.Sprintf("[%d] github_pr: %s must not be set", i, f))
	}
	return errs
}

// disallowed* return names of fields that are set but don't belong to the
// kind. Centralised so adding a new kind only requires updating one
// switch arm here plus a constant in types.go.

func disallowedSlack(c Citation) []string {
	var f []string
	if c.Repo != "" {
		f = append(f, "repo")
	}
	if c.Path != "" {
		f = append(f, "path")
	}
	if c.Ref != "" {
		f = append(f, "ref")
	}
	if c.SHA != "" {
		f = append(f, "sha")
	}
	if c.PRNum != 0 {
		f = append(f, "pr_num")
	}
	if c.LineStart != 0 || c.LineEnd != 0 {
		f = append(f, "line_start/line_end")
	}
	return f
}

func disallowedGithubFile(c Citation) []string {
	var f []string
	if c.ChannelID != "" {
		f = append(f, "channel_id")
	}
	if c.ThreadTS != "" {
		f = append(f, "thread_ts")
	}
	if c.MessageTS != "" {
		f = append(f, "message_ts")
	}
	if c.SHA != "" {
		f = append(f, "sha")
	}
	if c.PRNum != 0 {
		f = append(f, "pr_num")
	}
	return f
}

func disallowedGithubCommit(c Citation) []string {
	var f []string
	if c.ChannelID != "" {
		f = append(f, "channel_id")
	}
	if c.ThreadTS != "" {
		f = append(f, "thread_ts")
	}
	if c.MessageTS != "" {
		f = append(f, "message_ts")
	}
	if c.Path != "" {
		f = append(f, "path")
	}
	if c.Ref != "" {
		f = append(f, "ref")
	}
	if c.PRNum != 0 {
		f = append(f, "pr_num")
	}
	if c.LineStart != 0 || c.LineEnd != 0 {
		f = append(f, "line_start/line_end")
	}
	return f
}

func disallowedGithubPR(c Citation) []string {
	var f []string
	if c.ChannelID != "" {
		f = append(f, "channel_id")
	}
	if c.ThreadTS != "" {
		f = append(f, "thread_ts")
	}
	if c.MessageTS != "" {
		f = append(f, "message_ts")
	}
	if c.Path != "" {
		f = append(f, "path")
	}
	if c.Ref != "" {
		f = append(f, "ref")
	}
	if c.SHA != "" {
		f = append(f, "sha")
	}
	if c.LineStart != 0 || c.LineEnd != 0 {
		f = append(f, "line_start/line_end")
	}
	return f
}
