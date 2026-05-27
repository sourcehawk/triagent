package signalingest

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type dismissItemsIn struct {
	ItemIDs            []string `json:"itemIds"`
	Reason             string   `json:"reason"`
	DismissedRelatedTo []string `json:"dismissedRelatedTo,omitempty"`
	DismissedWikiSlugs []string `json:"dismissedWikiSlugs,omitempty"`
}

func (s *Server) dismissItems(ctx context.Context, in dismissItemsIn) (ackOut, error) {
	return s.postIngest(ctx, "dismiss-items", in)
}

func (s *Server) registerDismiss() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "dismiss_items",
		Description: "Drop items as noise or as already-handled. Reason is required. Pass dismissedRelatedTo when the items correlate to an existing recent signal; pass dismissedWikiSlugs when a wiki entry explains why these are non-actionable.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in dismissItemsIn) (*mcp.CallToolResult, ackOut, error) {
		out, err := s.dismissItems(ctx, in)
		if err != nil {
			return nil, ackOut{}, err
		}
		return nil, out, nil
	})
}
