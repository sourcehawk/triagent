package server

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_CloseHook_FiresOnRemove(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	var mu sync.Mutex
	var called []string
	mgr.SetCloseHook(func(id string) {
		mu.Lock()
		defer mu.Unlock()
		called = append(called, id)
	})
	require.True(t, mgr.Remove(inv.ID, false))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{inv.ID}, called)
}

func TestManager_CloseHook_FiresOnShutdown(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	var mu sync.Mutex
	var called []string
	mgr.SetCloseHook(func(id string) {
		mu.Lock()
		defer mu.Unlock()
		called = append(called, id)
	})
	mgr.Shutdown()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{inv.ID}, called)
}

func TestManager_CloseHook_NilIsNoop(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	// Default: no hook set. Must not panic.
	require.True(t, mgr.Remove(inv.ID, false))
}
