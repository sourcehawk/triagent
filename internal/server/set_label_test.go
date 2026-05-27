package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManagerWithInvestigationForLabel builds a Manager + a registered
// Investigation for label tests. Uses t.TempDir for isolation. The
// suffix ForLabel keeps it from colliding with any other test helper.
func newTestManagerWithInvestigationForLabel(t *testing.T) (*Manager, *Investigation) {
	t.Helper()
	m := NewManager(context.Background(), t.TempDir())
	inv, err := m.Register(&Investigation{
		Namespace: "default",
	})
	require.NoError(t, err, "Register")
	return m, inv
}

func TestManager_SetLabel_HappyPath(t *testing.T) {
	m, inv := newTestManagerWithInvestigationForLabel(t)
	require.NoError(t, m.SetLabel(inv.ID, "  rebuild zeebe broker  "), "SetLabel")
	assert.Equal(t, "rebuild zeebe broker", inv.Snapshot().Label, "Label after SetLabel")
}

func TestManager_SetLabel_Validation(t *testing.T) {
	m, inv := newTestManagerWithInvestigationForLabel(t)
	err := m.SetLabel(inv.ID, "   ")
	require.Error(t, err, "expected empty-label error, got nil")
	assert.Contains(t, err.Error(), "empty", "empty-label error message")
	tooLong := strings.Repeat("a", 81)
	err2 := m.SetLabel(inv.ID, tooLong)
	require.Error(t, err2, "expected length error, got nil")
	assert.Contains(t, err2.Error(), "≤80", "length error message")
}

func TestManager_SetLabel_UnknownID(t *testing.T) {
	m, _ := newTestManagerWithInvestigationForLabel(t)
	require.Error(t, m.SetLabel("does-not-exist", "x"), "expected not-found error, got nil")
}

func TestManager_SetLabel_PublishesLabelEnvelope(t *testing.T) {
	m, inv := newTestManagerWithInvestigationForLabel(t)
	_, events, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)
	require.NoError(t, m.SetLabel(inv.ID, "x"), "SetLabel")
	select {
	case env := <-events:
		assert.Equal(t, envKindLabel, env.Kind, "kind")
		assert.Equal(t, "x", env.Text, "text")
		assert.Equal(t, inv.ID, env.InvestigationID, "InvestigationID")
	case <-time.After(time.Second):
		t.Fatal("expected envKindLabel event on stream, got none")
	}
}
