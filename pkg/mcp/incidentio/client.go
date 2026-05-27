package incidentio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bare net/http wrapper around the incident.io v2 API. The
// surface is small (~5 endpoints), the response shapes are stable, and
// the community Go SDKs are partial — so we hand-roll the calls.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient builds a Client with a 15s timeout.
func NewClient(base, token string) *Client {
	return &Client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Incident is the projected incident shape we surface. The full API
// response is huge; we keep what a wiki author actually needs.
type Incident struct {
	ID            string                 `json:"id"`
	Reference     string                 `json:"reference"`
	Name          string                 `json:"name"`
	Summary       string                 `json:"summary,omitempty"`
	Severity      string                 `json:"severity,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Mode          string                 `json:"mode,omitempty"`
	CreatedAt     string                 `json:"created_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	ReportedAt    string                 `json:"reported_at,omitempty"`
	ClosedAt      string                 `json:"closed_at,omitempty"`
	PermalinkURL  string                 `json:"permalink_url,omitempty"`
	IncidentRoles []IncidentRole         `json:"incident_role_assignments,omitempty"`
	CustomFields  []IncidentCustomFieldV `json:"custom_field_entries,omitempty"`
}

// IncidentRole names a role and the user (display name) currently
// assigned. We deliberately drop email and avatar URLs.
type IncidentRole struct {
	Role     string `json:"role"`
	UserName string `json:"user_name,omitempty"`
}

// CustomField is the catalog entry returned by /v2/custom_fields.
type CustomField struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	FieldType   string `json:"field_type,omitempty"`
}

// IncidentCustomFieldV is a value entry on an incident — the field's
// name (joined from the catalog) plus a flattened value string.
type IncidentCustomFieldV struct {
	FieldID   string `json:"field_id"`
	FieldName string `json:"field_name,omitempty"`
	Value     string `json:"value,omitempty"`
}

// TimelineEntry is the merged shape for incident_updates / actions /
// follow_ups, sorted by Time. Kind names which source it came from so
// the agent can quote it correctly ("incident update at …").
type TimelineEntry struct {
	Kind   string `json:"kind"` // "update" | "action" | "follow_up"
	Time   string `json:"time,omitempty"`
	Author string `json:"author,omitempty"`
	Text   string `json:"text,omitempty"`
	Status string `json:"status,omitempty"`
}

// Postmortem is a normalised post-mortem document (markdown body).
type Postmortem struct {
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	URL      string `json:"url,omitempty"`
}

// GetIncident reads /v2/incidents/{ref}. Reference may be UUID or numeric.
func (c *Client) GetIncident(ctx context.Context, ref string) (*Incident, error) {
	resp, err := c.get(ctx, "/v2/incidents/"+url.PathEscape(ref), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	// incident.io wraps single-resource reads as {"incident": {...}}.
	var body struct {
		Incident rawIncident `json:"incident"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return projectIncident(body.Incident), nil
}

// ListCustomFields fetches /v2/custom_fields once at startup.
func (c *Client) ListCustomFields(ctx context.Context) ([]CustomField, error) {
	resp, err := c.get(ctx, "/v2/custom_fields", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		CustomFields []CustomField `json:"custom_fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.CustomFields, nil
}

// GetTimeline merges incident_updates + actions + follow_ups into one
// chronologically-sorted list.
func (c *Client) GetTimeline(ctx context.Context, incidentID string) ([]TimelineEntry, error) {
	updates, err := c.listIncidentUpdates(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("updates: %w", err)
	}
	actions, err := c.listActions(ctx, incidentID)
	if err != nil {
		// Degrade: actions failing shouldn't kill the whole timeline.
		actions = nil
	}
	followUps, err := c.listFollowUps(ctx, incidentID)
	if err != nil {
		followUps = nil
	}
	out := make([]TimelineEntry, 0, len(updates)+len(actions)+len(followUps))
	out = append(out, updates...)
	out = append(out, actions...)
	out = append(out, followUps...)
	// Sort ascending by time string. RFC3339 sorts lexicographically
	// the same as chronologically, so a string compare suffices.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Time > out[j].Time; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

func (c *Client) listIncidentUpdates(ctx context.Context, incidentID string) ([]TimelineEntry, error) {
	q := url.Values{}
	q.Set("incident_id", incidentID)
	resp, err := c.get(ctx, "/v2/incident_updates", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		IncidentUpdates []struct {
			ID         string `json:"id"`
			CreatedAt  string `json:"created_at"`
			Message    string `json:"message"`
			NewStatus  struct {
				Name string `json:"name"`
			} `json:"new_status"`
			NewSeverity struct {
				Name string `json:"name"`
			} `json:"new_severity"`
			Updater struct {
				User struct {
					Name string `json:"name"`
				} `json:"user"`
			} `json:"updater"`
		} `json:"incident_updates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]TimelineEntry, 0, len(body.IncidentUpdates))
	for _, u := range body.IncidentUpdates {
		text := u.Message
		if u.NewStatus.Name != "" {
			text = strings.TrimSpace(text + " · status → " + u.NewStatus.Name)
		}
		if u.NewSeverity.Name != "" {
			text = strings.TrimSpace(text + " · severity → " + u.NewSeverity.Name)
		}
		out = append(out, TimelineEntry{
			Kind:   "update",
			Time:   u.CreatedAt,
			Author: u.Updater.User.Name,
			Text:   text,
		})
	}
	return out, nil
}

func (c *Client) listActions(ctx context.Context, incidentID string) ([]TimelineEntry, error) {
	q := url.Values{}
	q.Set("incident_id", incidentID)
	resp, err := c.get(ctx, "/v2/actions", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		Actions []struct {
			ID          string `json:"id"`
			CreatedAt   string `json:"created_at"`
			Description string `json:"description"`
			Status      string `json:"status"`
			Assignee    struct {
				Name string `json:"name"`
			} `json:"assignee"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]TimelineEntry, 0, len(body.Actions))
	for _, a := range body.Actions {
		out = append(out, TimelineEntry{
			Kind:   "action",
			Time:   a.CreatedAt,
			Author: a.Assignee.Name,
			Text:   a.Description,
			Status: a.Status,
		})
	}
	return out, nil
}

func (c *Client) listFollowUps(ctx context.Context, incidentID string) ([]TimelineEntry, error) {
	q := url.Values{}
	q.Set("incident_id", incidentID)
	resp, err := c.get(ctx, "/v2/follow_ups", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		FollowUps []struct {
			ID          string `json:"id"`
			CreatedAt   string `json:"created_at"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Status      string `json:"status"`
		} `json:"follow_ups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]TimelineEntry, 0, len(body.FollowUps))
	for _, f := range body.FollowUps {
		text := strings.TrimSpace(f.Title + ": " + f.Description)
		out = append(out, TimelineEntry{
			Kind:   "follow_up",
			Time:   f.CreatedAt,
			Text:   text,
			Status: f.Status,
		})
	}
	return out, nil
}

// GetPostmortem fetches /v1/postmortem_documents (the V1 endpoint — the
// only one that exposes the document). Empty Postmortem on no-result.
func (c *Client) GetPostmortem(ctx context.Context, incidentID string) (*Postmortem, error) {
	q := url.Values{}
	q.Set("incident_id", incidentID)
	resp, err := c.get(ctx, "/v1/postmortem_documents", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return &Postmortem{}, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		PostmortemDocuments []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
			// content is rich-text blocks; some installs expose
			// `markdown_content` as a convenience. Try both.
			MarkdownContent string `json:"markdown_content,omitempty"`
			Content         []json.RawMessage `json:"content,omitempty"`
		} `json:"postmortem_documents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.PostmortemDocuments) == 0 {
		return &Postmortem{}, nil
	}
	doc := body.PostmortemDocuments[0]
	md := doc.MarkdownContent
	if md == "" && len(doc.Content) > 0 {
		md = richTextBlocksToMarkdown(doc.Content)
	}
	return &Postmortem{
		Title:    doc.Title,
		Markdown: md,
		URL:      doc.URL,
	}, nil
}

// SearchByCustomField returns incidents that share `value` on the named
// custom field. Used to derive related incidents client-side, since
// there's no first-class "related" endpoint.
func (c *Client) SearchByCustomField(ctx context.Context, fieldID, value string, limit int) ([]Incident, error) {
	q := url.Values{}
	q.Set("custom_field_values["+fieldID+"]", value)
	if limit > 0 {
		q.Set("page_size", fmt.Sprintf("%d", limit))
	}
	resp, err := c.get(ctx, "/v2/incidents", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		Incidents []rawIncident `json:"incidents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Incident, 0, len(body.Incidents))
	for _, ri := range body.Incidents {
		out = append(out, *projectIncident(ri))
	}
	return out, nil
}

// rawIncident is the upstream wire shape — projected to Incident before
// returning so we don't leak transient field names downstream.
type rawIncident struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Severity  struct {
		Name string `json:"name"`
	} `json:"severity"`
	IncidentStatus struct {
		Name string `json:"name"`
	} `json:"incident_status"`
	Mode             string `json:"mode"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ReportedAt       string `json:"reported_at"`
	ClosedAt         string `json:"closed_at"`
	PermalinkURL     string `json:"permalink_url"`
	IncidentRoleAssignments []struct {
		Role struct {
			Name string `json:"name"`
		} `json:"role"`
		Assignee struct {
			Name string `json:"name"`
		} `json:"assignee"`
	} `json:"incident_role_assignments"`
	CustomFieldEntries []struct {
		CustomField struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"custom_field"`
		Values []struct {
			ValueText        string `json:"value_text,omitempty"`
			ValueLink        string `json:"value_link,omitempty"`
			ValueNumeric     string `json:"value_numeric,omitempty"`
			ValueOption      struct {
				Value string `json:"value"`
			} `json:"value_option,omitempty"`
		} `json:"values"`
	} `json:"custom_field_entries"`
}

func projectIncident(r rawIncident) *Incident {
	out := &Incident{
		ID:           r.ID,
		Reference:    r.Reference,
		Name:         r.Name,
		Summary:      r.Summary,
		Severity:     r.Severity.Name,
		Status:       r.IncidentStatus.Name,
		Mode:         r.Mode,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		ReportedAt:   r.ReportedAt,
		ClosedAt:     r.ClosedAt,
		PermalinkURL: r.PermalinkURL,
	}
	for _, a := range r.IncidentRoleAssignments {
		out.IncidentRoles = append(out.IncidentRoles, IncidentRole{
			Role:     a.Role.Name,
			UserName: a.Assignee.Name,
		})
	}
	for _, e := range r.CustomFieldEntries {
		var parts []string
		for _, v := range e.Values {
			switch {
			case v.ValueText != "":
				parts = append(parts, v.ValueText)
			case v.ValueLink != "":
				parts = append(parts, v.ValueLink)
			case v.ValueNumeric != "":
				parts = append(parts, v.ValueNumeric)
			case v.ValueOption.Value != "":
				parts = append(parts, v.ValueOption.Value)
			}
		}
		out.CustomFields = append(out.CustomFields, IncidentCustomFieldV{
			FieldID:   e.CustomField.ID,
			FieldName: e.CustomField.Name,
			Value:     strings.Join(parts, ", "),
		})
	}
	return out
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u = u + "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// richTextBlocksToMarkdown is a best-effort flattener for incident.io's
// rich-text post-mortem blocks. The block schema isn't fully documented
// publicly; we extract whatever text fields we recognise and join them
// with double-newlines. Markdown installs surface a `markdown_content`
// alongside, so this fallback only fires on rich-only deployments.
func richTextBlocksToMarkdown(blocks []json.RawMessage) string {
	var sb strings.Builder
	for _, raw := range blocks {
		// Try a few common shapes: {"type":"…","text":"…"}, {"content":[…]} etc.
		var node struct {
			Type    string            `json:"type"`
			Text    string            `json:"text"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		switch {
		case node.Text != "":
			if node.Type == "heading" {
				sb.WriteString("## ")
			}
			sb.WriteString(node.Text)
			sb.WriteString("\n\n")
		case len(node.Content) > 0:
			sb.WriteString(richTextBlocksToMarkdown(node.Content))
		}
	}
	return strings.TrimSpace(sb.String())
}
