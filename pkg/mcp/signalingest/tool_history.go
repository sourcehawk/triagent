package signalingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type querySignalHistoryIn struct {
	SinceHours int `json:"sinceHours" jsonschema:"how far back to look; clamped to [1,168]"`
}

type historyEntry struct {
	SignalID            string   `json:"signalId"`
	CreatedAt           string   `json:"createdAt,omitempty"`
	Outcome             string   `json:"outcome"`
	Clusters            []string `json:"clusters,omitempty"`
	Briefing            string   `json:"briefing,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	InvestigationID     string   `json:"investigationId,omitempty"`
	InvestigationStatus string   `json:"investigationStatus,omitempty"`
}

type querySignalHistoryOut struct {
	Entries []historyEntry `json:"entries"`
}

func (s *Server) queryHistory(ctx context.Context, in querySignalHistoryIn) (querySignalHistoryOut, error) {
	body, _ := json.Marshal(in)
	url := s.opts.LauncherURL + "/api/watches/" + s.opts.TraceID + "/ingest/history"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return querySignalHistoryOut{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.LauncherToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return querySignalHistoryOut{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return querySignalHistoryOut{}, fmt.Errorf("history: status %d", resp.StatusCode)
	}
	var out querySignalHistoryOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return querySignalHistoryOut{}, err
	}
	return out, nil
}

func (s *Server) registerHistory() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "query_signal_history",
		Description: "Read the last sinceHours of THIS watch's signals (queued + investigation_started + unclear + dismissed + failed + disabled). Always call this first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in querySignalHistoryIn) (*mcp.CallToolResult, querySignalHistoryOut, error) {
		out, err := s.queryHistory(ctx, in)
		if err != nil {
			return nil, querySignalHistoryOut{}, err
		}
		return nil, out, nil
	})
}
