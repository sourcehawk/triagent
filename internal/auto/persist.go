// Package auto orchestrates the operator-agent claude session that drives
// the operator-side chat role during an auto-mode investigation. The State
// in this file is the persisted snapshot the launcher writes to
// metadata.json; the live AutoOperator (operator.go) owns the runtime
// goroutines.
package auto

// Phase is the lifecycle stage of an auto-mode investigation.
type Phase string

const (
	PhaseStarted  Phase = "started"
	PhasePaused   Phase = "paused"
	PhaseResumed  Phase = "resumed"
	PhaseFinished Phase = "finished"
	PhaseAborted  Phase = "aborted"
)

// State is the persisted Auto block. Read by the rehydrate path; written
// on every phase transition.
type State struct {
	Enabled           bool   `json:"enabled"`
	Phase             Phase  `json:"phase,omitempty"`
	OperatorSessionID string `json:"operatorSessionID,omitempty"`
	PausedAtSeq       int    `json:"pausedAtSeq,omitempty"`
	LastSentSeq       int    `json:"lastSentSeq,omitempty"`
	WakeCount         int    `json:"wakeCount,omitempty"`
	ConsecFailures    int    `json:"consecFailures,omitempty"`
}

// IsActive reports whether the boundary watcher should be running.
func (s State) IsActive() bool {
	return s.Phase == PhaseStarted || s.Phase == PhaseResumed
}
