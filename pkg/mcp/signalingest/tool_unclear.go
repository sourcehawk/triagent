package signalingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reportUnclearIn struct {
	CitedItemIDs []string `json:"citedItemIds"`
	Reason       string   `json:"reason"`
}

type ackOut struct {
	SignalID string `json:"signalId"`
}

func (s *Server) reportUnclear(ctx context.Context, in reportUnclearIn) (ackOut, error) {
	return s.postIngest(ctx, "report-unclear", in)
}

func (s *Server) postIngest(ctx context.Context, segment string, in any) (ackOut, error) {
	body, _ := json.Marshal(in)
	url := s.opts.LauncherURL + "/api/watches/" + s.opts.TraceID + "/ingest/" + segment
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ackOut{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.LauncherToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return ackOut{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return ackOut{}, fmt.Errorf("%s: status %d", segment, resp.StatusCode)
	}
	var out ackOut
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, nil
}

func (s *Server) registerUnclear() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "report_unclear",
		Description: "Mark items that you cannot classify confidently. The operator decides. Provide a one-sentence reason that names what's unclear.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in reportUnclearIn) (*mcp.CallToolResult, ackOut, error) {
		out, err := s.reportUnclear(ctx, in)
		if err != nil {
			return nil, ackOut{}, err
		}
		return nil, out, nil
	})
}
