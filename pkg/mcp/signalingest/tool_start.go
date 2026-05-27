package signalingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type startInvestigationIn struct {
	Briefing      string   `json:"briefing"`
	CitedItemIDs  []string `json:"citedItemIds"`
	Clusters      []string `json:"clusters,omitempty"`
	SlackChannel  string   `json:"slackChannel,omitempty"`
	IncidentioURL string   `json:"incidentioUrl,omitempty"`
	Repos         []string `json:"repos,omitempty"`
	AutoMode      bool     `json:"autoMode"`
}

type startInvestigationOut struct {
	SignalID string `json:"signalId"`
	Queued   bool   `json:"queued"`
	Position int    `json:"position"`
}

func (s *Server) startInvestigation(ctx context.Context, in startInvestigationIn) (startInvestigationOut, error) {
	body, _ := json.Marshal(in)
	url := s.opts.LauncherURL + "/api/watches/" + s.opts.TraceID + "/ingest/start-investigation"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return startInvestigationOut{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.LauncherToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return startInvestigationOut{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return startInvestigationOut{}, fmt.Errorf("start-investigation: status %d", resp.StatusCode)
	}
	var out startInvestigationOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return startInvestigationOut{}, err
	}
	return out, nil
}

func (s *Server) registerStart() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "start_investigation",
		Description: "Emit a signal that becomes an investigation. Pass a tight briefing, the cited item IDs that the signal groups, and the autoMode flag. clusters is OPTIONAL — pass only when the signal NAMES a cluster concretely. The launcher enqueues and runs the investigation; this returns once enqueued (M5: synchronous create).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in startInvestigationIn) (*mcp.CallToolResult, startInvestigationOut, error) {
		out, err := s.startInvestigation(ctx, in)
		if err != nil {
			return nil, startInvestigationOut{}, err
		}
		return nil, out, nil
	})
}
