// Package citations is the shared parse/validate/retry plumbing for MCP
// sub-agent tools that emit prose grounded in structured citations.
//
// Each per-MCP package supplies a Validator that knows how to existence-
// check its kinds against ground truth (Slack cache snapshot, cloned git
// repo, etc.). The runner (Run) handles parsing the <<<CITATIONS>>> tail
// block, the [N]-marker cross-check, and the one-shot corrective retry.
package citations

// Kind discriminates a Citation. Each value names one artifact type the
// future render layer knows how to display. New kinds are added by
// extending the constant set and (optionally) widening the Citation
// struct with kind-specific optional fields.
type Kind string

const (
	KindSlackThread  Kind = "slack_thread"
	KindGithubFile   Kind = "github_file"
	KindGithubCommit Kind = "github_commit"
	KindGithubPR     Kind = "github_pr"
)

// Citation is the polymorphic envelope. All fields except Kind are
// optional; per-kind requirements are enforced by ShapeCheck and the
// per-MCP Validator.
type Citation struct {
	Kind Kind `json:"kind"`

	// slack_thread
	ChannelID string `json:"channel_id,omitempty"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	MessageTS string `json:"message_ts,omitempty"`

	// github_*: required for all three GitHub kinds
	Repo string `json:"repo,omitempty"` // "<owner>/<name>"

	// github_file
	Path      string `json:"path,omitempty"`
	Ref       string `json:"ref,omitempty"`        // sha or symbolic ref
	LineStart int    `json:"line_start,omitempty"` // 1-based, optional
	LineEnd   int    `json:"line_end,omitempty"`   // optional

	// github_commit
	SHA string `json:"sha,omitempty"`

	// github_pr
	PRNum int `json:"pr_num,omitempty"`
}

// Marker delimiters for the trailing JSON block the sub-agent emits.
const (
	OpenMarker  = "<<<CITATIONS"
	CloseMarker = "CITATIONS>>>"
)
