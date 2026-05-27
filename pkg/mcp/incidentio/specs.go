package incidentio

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the incident.io server's tool catalog.
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{Server: "triagent-incidentio", Name: "incidentio_get_incident", Description: "Read the full incident.io record (severity, status, timestamps, role assignments, custom field values). Pass `incident_id` (numeric reference or UUID).", Inputs: toolspec.FromStruct(getIncidentIn{})},
		{Server: "triagent-incidentio", Name: "incidentio_get_timeline", Description: "Merged chronological timeline (incident_updates + actions + follow_ups). Pass `incident_id`.", Inputs: toolspec.FromStruct(getTimelineIn{})},
		{Server: "triagent-incidentio", Name: "incidentio_get_postmortem", Description: "Post-incident review document, normalised to markdown. Returns empty when none exists yet. Pass `incident_id`.", Inputs: toolspec.FromStruct(getPostmortemIn{})},
		{Server: "triagent-incidentio", Name: "incidentio_search_related", Description: "Find related prior incidents that share a custom-field value (e.g. affected service) with the source incident. Pass `incident_id` as the source.", Inputs: toolspec.FromStruct(searchRelatedIn{})},
	}
}
