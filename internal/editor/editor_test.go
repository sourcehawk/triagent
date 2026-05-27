package editor

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveIncidentioRefFromSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		slug string
		want string
	}{
		{"basic", "inc-5466-invalid-tasklist-urls", "5466"},
		{"trims whitespace", "  inc-7-foo-bar  ", "7"},
		{"long slug", "inc-12345-zeebe-broker-oom-event-loop", "12345"},
		{"support prefix not derived", "inc-support-99-foo", ""},
		{"entity id", "service/zeebe-broker", ""},
		{"missing slug", "inc-5466", ""},
		{"trailing dash", "inc-5466-", ""},
		{"empty", "", ""},
		{"no leading inc", "5466-foo", ""},
		{"uppercase prefix rejected", "INC-5466-foo", ""},
		{"inv prefix not derived", "inv-broker-rebalance", ""},
		{"alert prefix not derived", "alert-cpu-spike", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, DeriveIncidentioRefFromSlug(tc.slug))
		})
	}
}

func newTestSession(t *testing.T, mgr *Manager, subject Subject) *Session {
	t.Helper()
	sess, err := mgr.Register(&Session{Subject: subject})
	require.NoError(t, err)
	return sess
}

func TestManagerRebind_MovesByKeyIndex(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)

	sess := newTestSession(t, mgr, prompts.PlaybookSubject{ID: "__new", Version: "HEAD"})
	originalID := sess.ID

	saved := prompts.PlaybookSubject{ID: "saved_id", Version: "HEAD"}
	out, err := mgr.Rebind(sess.ID, saved)
	require.NoError(t, err)

	assert.Equal(t, originalID, out.ID, "rebind preserves session id")
	assert.Equal(t, saved.Key(), out.Subject.Key(), "session subject is updated")
	assert.Same(t, out, mgr.ExistingByKey(saved.Key()), "byKey index points to new key")
	assert.Nil(t, mgr.ExistingByKey("playbook:__new@HEAD"), "old key is freed")
}

func TestManagerRebind_NotFound(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)

	_, err := mgr.Rebind("does-not-exist", prompts.PlaybookSubject{ID: "x", Version: "HEAD"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.Contains(t, err.Error(), "not found")
}

func TestManagerRebind_KeyCollision(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)

	a := newTestSession(t, mgr, prompts.PlaybookSubject{ID: "__new", Version: "HEAD"})
	occupant := newTestSession(t, mgr, prompts.PlaybookSubject{ID: "saved_id", Version: "HEAD"})

	_, err := mgr.Rebind(a.ID, prompts.PlaybookSubject{ID: "saved_id", Version: "HEAD"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionExists)
	assert.Contains(t, err.Error(), "already")

	// Original mapping intact on collision; both sessions still reachable.
	assert.Same(t, a, mgr.ExistingByKey("playbook:__new@HEAD"))
	assert.Same(t, occupant, mgr.ExistingByKey("playbook:saved_id@HEAD"))
}

func TestManagerRebind_Noop(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)

	subj := prompts.PlaybookSubject{ID: "same", Version: "HEAD"}
	sess := newTestSession(t, mgr, subj)

	out, err := mgr.Rebind(sess.ID, subj)
	require.NoError(t, err)
	assert.Same(t, sess, out)
	assert.Same(t, sess, mgr.ExistingByKey(subj.Key()))
}
