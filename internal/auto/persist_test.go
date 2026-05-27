package auto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestState_DefaultPhaseIsEmpty(t *testing.T) {
	var s State
	require.Equal(t, Phase(""), s.Phase)
	require.False(t, s.Enabled)
}

func TestState_IsActive(t *testing.T) {
	cases := []struct {
		phase Phase
		want  bool
	}{
		{PhaseStarted, true}, {PhaseResumed, true},
		{PhasePaused, false}, {PhaseFinished, false}, {PhaseAborted, false},
	}
	for _, c := range cases {
		s := State{Enabled: true, Phase: c.phase}
		require.Equal(t, c.want, s.IsActive(), "phase=%s", c.phase)
	}
}
