package incidentio

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── incidentio_get_incident ───────────────────────────────────────────────

type getIncidentIn struct {
	IncidentID string `json:"incident_id" jsonschema:"incident.io reference (numeric like '5466' or UUID); required. The system prompt usually carries the operator's session-scoped incident — pass it through here."`
}

type getIncidentOut struct {
	Incident Incident `json:"incident"`
}

func (s *Server) handleGetIncident(ctx context.Context, _ *mcp.CallToolRequest, in getIncidentIn) (*mcp.CallToolResult, getIncidentOut, error) {
	ref := strings.TrimSpace(in.IncidentID)
	if ref == "" {
		return errorResult("incident_id is required"), getIncidentOut{}, nil
	}
	inc, err := s.client.GetIncident(ctx, ref)
	if err != nil {
		return errorResult(err.Error()), getIncidentOut{}, nil
	}
	return nil, getIncidentOut{Incident: *inc}, nil
}

// ── incidentio_get_timeline ───────────────────────────────────────────────

type getTimelineIn struct {
	IncidentID string `json:"incident_id" jsonschema:"incident.io reference (numeric or UUID); required."`
}

type getTimelineOut struct {
	Entries []TimelineEntry `json:"entries"`
}

func (s *Server) handleGetTimeline(ctx context.Context, _ *mcp.CallToolRequest, in getTimelineIn) (*mcp.CallToolResult, getTimelineOut, error) {
	ref := strings.TrimSpace(in.IncidentID)
	if ref == "" {
		return errorResult("incident_id is required"), getTimelineOut{}, nil
	}
	// First fetch the incident to resolve the canonical ID — the
	// timeline endpoints filter by UUID, not numeric reference.
	inc, err := s.client.GetIncident(ctx, ref)
	if err != nil {
		return errorResult(err.Error()), getTimelineOut{}, nil
	}
	entries, err := s.client.GetTimeline(ctx, inc.ID)
	if err != nil {
		return errorResult(err.Error()), getTimelineOut{}, nil
	}
	return nil, getTimelineOut{Entries: entries}, nil
}

// ── incidentio_get_postmortem ─────────────────────────────────────────────

type getPostmortemIn struct {
	IncidentID string `json:"incident_id" jsonschema:"incident.io reference (numeric or UUID); required."`
}

type getPostmortemOut struct {
	Postmortem Postmortem `json:"postmortem"`
	Empty      bool       `json:"empty,omitempty"`
}

func (s *Server) handleGetPostmortem(ctx context.Context, _ *mcp.CallToolRequest, in getPostmortemIn) (*mcp.CallToolResult, getPostmortemOut, error) {
	ref := strings.TrimSpace(in.IncidentID)
	if ref == "" {
		return errorResult("incident_id is required"), getPostmortemOut{}, nil
	}
	inc, err := s.client.GetIncident(ctx, ref)
	if err != nil {
		return errorResult(err.Error()), getPostmortemOut{}, nil
	}
	pm, err := s.client.GetPostmortem(ctx, inc.ID)
	if err != nil {
		return errorResult(err.Error()), getPostmortemOut{}, nil
	}
	empty := pm == nil || pm.Markdown == ""
	if pm == nil {
		pm = &Postmortem{}
	}
	return nil, getPostmortemOut{Postmortem: *pm, Empty: empty}, nil
}

// ── incidentio_search_related ─────────────────────────────────────────────

type searchRelatedIn struct {
	IncidentID string `json:"incident_id" jsonschema:"incident.io reference (numeric or UUID) to use as the join source; required."`
	By         string `json:"by" jsonschema:"custom-field name (case-insensitive substring match against field labels) to join on; e.g. 'service' or 'affected component'"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max related incidents to return; default 10"`
}

type searchRelatedOut struct {
	Related []Incident `json:"related"`
	Field   string     `json:"field,omitempty"`
	Value   string     `json:"value,omitempty"`
}

func (s *Server) handleSearchRelated(ctx context.Context, _ *mcp.CallToolRequest, in searchRelatedIn) (*mcp.CallToolResult, searchRelatedOut, error) {
	ref := strings.TrimSpace(in.IncidentID)
	if ref == "" {
		return errorResult("incident_id is required"), searchRelatedOut{}, nil
	}
	if strings.TrimSpace(in.By) == "" {
		return errorResult("by is required (custom-field label, case-insensitive substring)"), searchRelatedOut{}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}

	// Resolve the named incident's value for the requested field.
	inc, err := s.client.GetIncident(ctx, ref)
	if err != nil {
		return errorResult(err.Error()), searchRelatedOut{}, nil
	}
	fieldID, fieldName, value := matchCustomField(inc.CustomFields, in.By)
	if fieldID == "" {
		return errorResult("custom field matching " + in.By + " not found on this incident; tip: incidentio_get_incident lists the fields the API exposes for this org"), searchRelatedOut{}, nil
	}
	if value == "" {
		return nil, searchRelatedOut{Field: fieldName}, nil
	}

	related, err := s.client.SearchByCustomField(ctx, fieldID, value, limit+1)
	if err != nil {
		return errorResult(err.Error()), searchRelatedOut{}, nil
	}
	// Drop the source incident itself if it shows up in the result.
	out := make([]Incident, 0, len(related))
	for _, r := range related {
		if r.ID == inc.ID {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return nil, searchRelatedOut{Related: out, Field: fieldName, Value: value}, nil
}

func matchCustomField(fields []IncidentCustomFieldV, byLabel string) (id, name, value string) {
	want := strings.ToLower(byLabel)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f.FieldName), want) {
			return f.FieldID, f.FieldName, f.Value
		}
	}
	return "", "", ""
}
