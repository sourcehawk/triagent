package wiki

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// wikilinkPattern matches lowercase-with-hyphens entity references in body
// prose. Mirrors investigate/internal/server/wiki_pr.go::wikilinkPattern;
// the duplication is deliberate so the investigate module doesn't depend
// on the mcp module.
var wikilinkPattern = regexp.MustCompile(`\[\[([a-z0-9][a-z0-9-]*)\]\]`)

type proposeWikiDraftIn struct {
	Slug                    string `json:"slug" jsonschema:"the wiki entry slug — lowercase-with-hyphens (^[a-z0-9][a-z0-9-]*$). Prefix with inc-/inv-/alert- depending on the strongest source. The orchestrator owns this value; the sub-agent must echo it verbatim into frontmatter.id."`
	Summary                 string `json:"summary" jsonschema:"the agent's most recent summary of the investigation"`
	AdditionalContext       string `json:"additional_context,omitempty" jsonschema:"anything else to highlight: tool results, prior decline-refinement notes, etc."`
	InvestigationURL        string `json:"investigation_url,omitempty" jsonschema:"launcher URL pointing at the investigation that produced this entry; written verbatim to frontmatter.links.investigation"`
	InvestigationEventsPath string `json:"investigation_events_path,omitempty" jsonschema:"absolute path to the investigation's events.jsonl — when set, the sub-agent is instructed to read it for primary evidence"`
	IncidentIOURL           string `json:"incidentio_url,omitempty" jsonschema:"incident.io URL for the related ticket; written verbatim to frontmatter.links.incident_io"`
	SlackChannelURL         string `json:"slack_channel_url,omitempty" jsonschema:"Slack channel URL; written verbatim to frontmatter.links.slack_channel"`
	SlackMessageURL         string `json:"slack_message_url,omitempty" jsonschema:"Slack message permalink (alert/thread); written verbatim to frontmatter.links.slack_message"`
	Status                  string `json:"status" jsonschema:"one of resolved|open|wontfix — the parent agent supplies this from the investigation conversation; do NOT infer in the sub-agent"`
	Severity                string `json:"severity,omitempty" jsonschema:"one of sev1|sev2|sev3 — optional, omit if unknown"`
}

// proposeWikiDraftOut carries the diff payload the WikiProposalCard renders.
// kind="wiki_proposal_draft" lets investigate's events.ts route it to the
// right card without colliding with the playbook proposal kind.
type proposeWikiDraftOut struct {
	Kind        string          `json:"kind"`
	ProposalID  string          `json:"proposal_id"`
	Slug        string          `json:"slug"`
	BaseMD      string          `json:"base_md,omitempty"`
	NewMD       string          `json:"new_md"`
	IsNew       bool            `json:"is_new"`
	Message     string          `json:"message"`
	TimedOut    bool            `json:"timed_out,omitempty"`
	NewEntities []NewEntityStub `json:"new_entities,omitempty"`
}

// NewEntityStub carries the description the sub-agent wrote for a newly
// introduced entity. The orchestrator passes these to the approve flow
// so the vault writes get non-empty Description sections. RawMD is the
// stub file's full markdown so the proposal UI can render it alongside
// new_md in the raw view.
type NewEntityStub struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RawMD       string `json:"raw_md,omitempty"`
}

func (s *Server) proposeWikiDraft(ctx context.Context, _ *mcp.CallToolRequest, in proposeWikiDraftIn) (*mcp.CallToolResult, proposeWikiDraftOut, error) {
	// 1. Validate inputs.
	if err := ValidateSlug(in.Slug); err != nil {
		return errorResult(err.Error()), proposeWikiDraftOut{}, nil
	}
	if strings.TrimSpace(in.Summary) == "" {
		return errorResult("summary is required"), proposeWikiDraftOut{}, nil
	}
	validStatuses := map[string]bool{"resolved": true, "open": true, "wontfix": true}
	if !validStatuses[in.Status] {
		return errorResult(`status is required and must be one of resolved|open|wontfix`), proposeWikiDraftOut{}, nil
	}
	validSeverities := map[string]bool{"": true, "sev1": true, "sev2": true, "sev3": true}
	if !validSeverities[in.Severity] {
		return errorResult(`severity must be one of sev1|sev2|sev3 (or omit)`), proposeWikiDraftOut{}, nil
	}
	for _, link := range []struct{ name, val string }{
		{"investigation_url", in.InvestigationURL},
		{"incidentio_url", in.IncidentIOURL},
		{"slack_channel_url", in.SlackChannelURL},
		{"slack_message_url", in.SlackMessageURL},
	} {
		if link.val == "" {
			continue
		}
		if _, err := url.ParseRequestURI(link.val); err != nil {
			return errorResult(link.name + " is not a valid URL: " + err.Error()), proposeWikiDraftOut{}, nil
		}
	}

	// 2. Look up base content if this is an update.
	var baseMD string
	isNew := true
	if existing, err := ReadEntry(s.vaultPath, in.Slug); err == nil {
		// Reconstruct the on-disk markdown from the parsed structure
		// for diffing in the UI. This is best-effort — comments and
		// exact formatting may differ from the on-disk file.
		raw, _ := ReadNote(s.vaultPath, existing.Path)
		baseMD = string(raw)
		isNew = false
	}
	// 3. Build the existing entity list for the sub-agent prompt.
	entities, err := ListEntities(s.vaultPath, "")
	if err != nil {
		return errorResult(fmt.Sprintf("read entities: %v", err)), proposeWikiDraftOut{}, nil
	}

	// 4. Generate proposal id and target file path.
	proposalID := NewProposalID()
	if err := os.MkdirAll(s.proposalsPath, 0o700); err != nil {
		return errorResult(fmt.Sprintf("create proposals dir: %v", err)), proposeWikiDraftOut{}, nil
	}
	draftPath := filepath.Join(s.proposalsPath, proposalFilename(proposalID, in.Slug))

	// 5. Build the prompt and run the sub-agent.
	prompt := proposeWikiSubAgentPrompt(proposeWikiPromptArgs{
		Slug:                    in.Slug,
		Date:                    time.Now().UTC().Format("2006-01-02"),
		Status:                  in.Status,
		Severity:                in.Severity,
		Summary:                 in.Summary,
		AdditionalContext:       in.AdditionalContext,
		InvestigationEventsPath: in.InvestigationEventsPath,
		Entities:                entities,
		BaseMD:                  baseMD,
		DraftPath:               draftPath,
		ProposalID:              proposalID,
	})
	parentID := telemetry.CurrentToolID(ctx)
	if _, err := s.runSubAgent(ctx, prompt, parentID); err != nil {
		// Sub-agent crashed or timed out. Best-effort cleanup of any
		// partial draft (the sub-agent may not have written anything).
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult(fmt.Sprintf("sub-agent failed: %v", err)), proposeWikiDraftOut{}, nil
	}

	// 6. Read back what the sub-agent wrote.
	rawDraft, _, err := ReadProposal(s.proposalsPath, proposalID)
	if err != nil {
		return errorResult("sub-agent did not produce a draft file"), proposeWikiDraftOut{}, nil
	}

	// 7. Authoritatively post-process the source-links, session id, status, and severity.
	postProcessed, perr := applyAuthoritativeFields(rawDraft, Links{
		Investigation: in.InvestigationURL,
		IncidentIO:    in.IncidentIOURL,
		SlackChannel:  in.SlackChannelURL,
		SlackMessage:  in.SlackMessageURL,
	}, in.Status, in.Severity)
	if perr != nil {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult(fmt.Sprintf("post-process draft: %v", perr)), proposeWikiDraftOut{}, nil
	}

	// 8. Validate schema + headers.
	fm, body, splitErr := SplitFrontmatter(postProcessed)
	if splitErr != nil {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult("draft is missing valid YAML frontmatter: " + splitErr.Error()), proposeWikiDraftOut{}, nil
	}
	var parsed Frontmatter
	if err := yaml.Unmarshal(fm, &parsed); err != nil {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult("frontmatter parse failed: " + err.Error()), proposeWikiDraftOut{}, nil
	}
	if parsed.ID != in.Slug {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult(fmt.Sprintf("draft id %q != tool input %q", parsed.ID, in.Slug)), proposeWikiDraftOut{}, nil
	}
	if errs := ValidateFrontmatter(parsed); len(errs) > 0 {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult("frontmatter validation failed: " + strings.Join(errs, "; ")), proposeWikiDraftOut{}, nil
	}
	if errs := ValidateBodyHeaders(string(body)); len(errs) > 0 {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult("body validation failed: " + strings.Join(errs, "; ")), proposeWikiDraftOut{}, nil
	}

	// 9. Re-write the post-processed bytes back to disk so a later
	// promote-proposal reads the canonical form.
	if _, err := WriteProposal(s.proposalsPath, proposalID, in.Slug, postProcessed); err != nil {
		return errorResult(err.Error()), proposeWikiDraftOut{}, nil
	}

	// 10. Scan for entity stub sibling files written by the sub-agent and
	// require every NEW [[wikilink]] entity (one not in the existing entity
	// list) to have a stub file containing a non-empty `description:` field.
	// Sub-agent compliance with the side-channel stub-writing instruction
	// is unreliable; without this check, a missed stub silently produces a
	// vault entity with an empty Description after approve.
	newEntities, stubParseErrs := scanEntityStubs(s.proposalsPath, proposalID)
	if errMsg := validateNewEntityCoverage(string(body), entities, newEntities, stubParseErrs, s.proposalsPath, proposalID); errMsg != "" {
		_ = DeleteProposal(s.proposalsPath, proposalID)
		return errorResult(errMsg), proposeWikiDraftOut{}, nil
	}

	msg := fmt.Sprintf("Wiki draft persisted as %s. Surface this to the user for approval.", proposalID)
	return nil, proposeWikiDraftOut{
		Kind:        "wiki_proposal_draft",
		ProposalID:  proposalID,
		Slug:        in.Slug,
		BaseMD:      baseMD,
		NewMD:       string(postProcessed),
		IsNew:       isNew,
		Message:     msg,
		NewEntities: newEntities,
	}, nil
}

// scanEntityStubs looks for files matching <proposalID>__entity__<type>__<name>.md
// in proposalsDir and returns their parsed stubs alongside any per-file parse
// errors. Errors are surfaced (rather than silently dropped) so the caller
// can fail the proposal loudly when a stub is malformed; without this the
// sub-agent's mistake silently turns into an empty Description in the vault.
func scanEntityStubs(proposalsDir, proposalID string) ([]NewEntityStub, []string) {
	entries, err := os.ReadDir(proposalsDir)
	if err != nil {
		return nil, nil
	}
	prefix := proposalID + "__entity__"
	var stubs []NewEntityStub
	var parseErrs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// Parse <type>__<name> from the filename.
		inner := strings.TrimSuffix(strings.TrimPrefix(e.Name(), prefix), ".md")
		sep := strings.Index(inner, "__")
		if sep < 0 {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: filename missing '__' between type and name", e.Name()))
			continue
		}
		typ := inner[:sep]
		name := inner[sep+2:]
		if typ == "" || name == "" {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: empty type or name segment in filename", e.Name()))
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proposalsDir, e.Name()))
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: read failed: %v", e.Name(), err))
			continue
		}
		desc, err := extractEntityStubDescription(raw)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		stubs = append(stubs, NewEntityStub{Type: typ, Name: name, Description: desc, RawMD: string(raw)})
	}
	return stubs, parseErrs
}

// extractEntityStubDescription parses the description field out of a
// simple frontmatter-only entity stub file written by the sub-agent.
// Returns an error when the file is missing the leading '---' delimiter
// or the YAML frontmatter fails to parse — silent fallback to "" used to
// mask sub-agent malformed output, see validateNewEntityCoverage.
func extractEntityStubDescription(raw []byte) (string, error) {
	const sep = "---\n"
	s := string(raw)
	if !strings.HasPrefix(s, sep) {
		return "", fmt.Errorf("missing leading '---' delimiter")
	}
	rest := s[len(sep):]
	// Find closing --- (if present).
	end := strings.Index(rest, "\n---")
	fm := rest
	if end >= 0 {
		fm = rest[:end]
	}
	var parsed struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		return "", fmt.Errorf("frontmatter yaml parse: %w", err)
	}
	return strings.TrimSpace(parsed.Description), nil
}

// validateNewEntityCoverage returns a non-empty error message when the
// sub-agent's output fails the entity-stub contract: every [[wikilink]] in
// body that does NOT name an existing vault entity must have a sibling
// stub file with a non-empty `description:` field. Returns "" when all
// requirements are met.
//
// The error message is shaped so the parent agent can re-call propose_wiki_draft
// and the sub-agent has enough context to fix what's missing.
func validateNewEntityCoverage(body string, existing []EntityRef, stubs []NewEntityStub, parseErrs []string, proposalsDir, proposalID string) string {
	existingNames := make(map[string]bool, len(existing))
	for _, e := range existing {
		existingNames[e.Name] = true
	}
	stubByName := make(map[string]NewEntityStub, len(stubs))
	for _, st := range stubs {
		stubByName[st.Name] = st
	}

	matches := wikilinkPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	var missing, emptyDesc []string
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if existingNames[name] {
			continue
		}
		st, ok := stubByName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if strings.TrimSpace(st.Description) == "" {
			emptyDesc = append(emptyDesc, name)
		}
	}

	if len(missing) == 0 && len(emptyDesc) == 0 && len(parseErrs) == 0 {
		return ""
	}

	var parts []string
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("no stub file written for new entities: %s", strings.Join(missing, ", ")))
	}
	if len(emptyDesc) > 0 {
		parts = append(parts, fmt.Sprintf("stub file present but `description:` is empty for: %s", strings.Join(emptyDesc, ", ")))
	}
	if len(parseErrs) > 0 {
		parts = append(parts, fmt.Sprintf("malformed stub files: %s", strings.Join(parseErrs, "; ")))
	}

	stubPathHint := filepath.Join(proposalsDir, proposalID+"__entity__<type>__<name>.md")
	return fmt.Sprintf(
		"entity stubs incomplete: %s. For every new [[wikilink]] entity (one not already in the vault), the sub-agent must write a frontmatter-only file at %s with `type:`, `name:`, and a non-empty `description:` field. Re-run propose_wiki_draft so the sub-agent can address this.",
		strings.Join(parts, "; "),
		stubPathHint,
	)
}

// applyAuthoritativeFields rewrites frontmatter so links.* carry the
// values the orchestrator received as inputs, and status/severity carry
// the parent-agent inputs. Any value the sub-agent put in these fields
// is overwritten — they are not sub-agent-trusted.
func applyAuthoritativeFields(raw []byte, links Links, status, severity string) ([]byte, error) {
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var fmStruct Frontmatter
	if err := yaml.Unmarshal(fm, &fmStruct); err != nil {
		return nil, err
	}
	fmStruct.SchemaVersion = CurrentSchemaVersion
	if links.Investigation != "" {
		fmStruct.Links.Investigation = links.Investigation
	}
	if links.IncidentIO != "" {
		fmStruct.Links.IncidentIO = links.IncidentIO
	}
	if links.SlackChannel != "" {
		fmStruct.Links.SlackChannel = links.SlackChannel
	}
	if links.SlackMessage != "" {
		fmStruct.Links.SlackMessage = links.SlackMessage
	}
	if status != "" {
		fmStruct.Status = status
	}
	if severity != "" {
		fmStruct.Severity = severity
	}
	out, err := yaml.Marshal(&fmStruct)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(out)
	if !strings.HasSuffix(string(out), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.Write(body)
	return []byte(b.String()), nil
}
