package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPromForwarder must be safely callable in every server
// configuration — including the no-profile case. The previous gate
// (`if opts.Profile != nil`) left override-only prom configs with
// `triagent-prom` attached but no forwarder behind the resolver,
// surfacing as 503 "prom not configured" on every MCP call. Pin the
// no-profile-dependency contract so a future refactor can't quietly
// reintroduce the gate.
func TestNewPromForwarder_NoProfileDependency(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)

	got := newPromForwarder(mgr)
	require.NotNil(t, got)
	assert.NotNil(t, got, "promForwarder must be wired regardless of whether a profile is configured")
}
