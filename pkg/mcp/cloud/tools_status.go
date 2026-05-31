package cloud

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descSessionStatus = "Report the pinned read-only cloud identity this session acts as and whether it is currently valid. You cannot choose or change it. Read-only."

// SessionStatusInput is the input schema for session_status. It takes no
// parameters: the identity is pinned by the deployment.
type SessionStatusInput struct{}

// SessionStatusOutput is the response schema for session_status. It is the
// shared IdentityStatus the connections panel and preflight gate also render.
type SessionStatusOutput = IdentityStatus

// sessionStatus runs the shared identity probe and returns its result. It never
// errors on an invalid identity — a stale credential surfaces as Valid:false
// with a Hint, the same visible-degrade contract the launcher renders.
func (s *Server) sessionStatus(ctx context.Context, _ *mcp.CallToolRequest, _ SessionStatusInput) (*mcp.CallToolResult, SessionStatusOutput, error) {
	st, err := Probe(ctx, s.provider, s.expectedIdentity, s.subprocessEnv())
	if err != nil {
		return errorResult(err.Error()), SessionStatusOutput{}, nil
	}
	st.ActiveTarget = s.activeTarget
	return nil, st, nil
}
