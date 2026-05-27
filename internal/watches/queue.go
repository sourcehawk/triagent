package watches

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// QueuedEntry is one pending start_investigation that has not yet been
// popped by the spawner. Attempts counts how many Create calls have
// already failed for this entry; NotBefore (RFC3339, empty=now) is the
// retry-backoff gate so the spawner skips this entry until the wall
// clock crosses it. Both fields persist via queue.json so retry state
// survives launcher restarts.
type QueuedEntry struct {
	SignalID   string `json:"signalId"`
	EnqueuedAt string `json:"enqueuedAt"`
	Attempts   int    `json:"attempts,omitempty"`
	NotBefore  string `json:"notBefore,omitempty"`
}

// QueueState is the on-disk shape of `queue.json` next to signals.jsonl
// in a watch's data directory. Running holds investigation IDs of
// in-flight spawns; Queued is the FIFO of pending signals.
type QueueState struct {
	Running []string      `json:"running"`
	Queued  []QueuedEntry `json:"queued"`
}

func queuePath(dir string) string { return filepath.Join(dir, "queue.json") }

// LoadQueue reads queue.json. Missing file is not an error — returns an
// empty QueueState so first-run watches don't have to seed the file.
func LoadQueue(dir string) (QueueState, error) {
	b, err := os.ReadFile(queuePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return QueueState{}, nil
	}
	if err != nil {
		return QueueState{}, err
	}
	var q QueueState
	if err := json.Unmarshal(b, &q); err != nil {
		return QueueState{}, err
	}
	return q, nil
}

// SaveQueue writes queue.json atomically via rename. Caller holds whatever
// lock makes the snapshot consistent; this function only marshals.
func SaveQueue(dir string, q QueueState) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(q, "", "  ")
	tmp := queuePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, queuePath(dir))
}
