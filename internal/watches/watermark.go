package watches

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SeenSetMax bounds the dedupe ring used to guard against updatedAt ties
// and clock skew. K=200 is what the spec calls for.
const SeenSetMax = 200

type WatermarkState struct {
	LastPolledAt     time.Time `json:"lastPolledAt"`
	GitHubUpdatedAt  time.Time `json:"githubUpdatedAt,omitempty"`
	SeenIssueNumbers []int     `json:"seenIssueNumbers,omitempty"`
	SlackOldestTS    string    `json:"slackOldestTs,omitempty"`
	SeenTS           []string  `json:"seenTs,omitempty"`
}

// InitialWatermark returns the watermark a freshly-created watch should
// start from. Without this seed, sources interpret the zero watermark
// as "fetch everything" — slack's conversations.history with no oldest
// pulls ~100 messages of channel history, github with no since pulls
// every open issue. The first poll would then surface those as items
// the watch never wanted (the operator just created the watch to
// monitor what arrives *next*). Seeding to "now" caps both sources at
// "messages/issues newer than this".
func InitialWatermark(kind SourceKind, now time.Time) WatermarkState {
	wm := WatermarkState{LastPolledAt: now}
	switch kind {
	case SourceGitHubIssues:
		wm.GitHubUpdatedAt = now
	case SourceSlackChannel:
		// Slack's `oldest` parameter is a unix timestamp string with
		// microsecond precision (e.g. "1715479200.000000"). Anything
		// at or before this is excluded by the API.
		wm.SlackOldestTS = fmt.Sprintf("%d.000000", now.Unix())
	}
	return wm
}

// LoadWatermark + SaveWatermark share the per-path mutex (lockFor in
// log.go) so two pollOnce invocations on the same watch — e.g. the
// background loop racing the synchronous /poll-now — don't both write
// to the same ".tmp" sidecar and lose one another's rename.
func LoadWatermark(path string) (WatermarkState, error) {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return WatermarkState{}, nil
	}
	if err != nil {
		return WatermarkState{}, err
	}
	var s WatermarkState
	if err := json.Unmarshal(b, &s); err != nil {
		return WatermarkState{}, err
	}
	return s, nil
}

func SaveWatermark(path string, s WatermarkState) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RememberSeenIssue appends an issue number, bounded to SeenSetMax.
func RememberSeenIssue(st WatermarkState, n int) WatermarkState {
	st.SeenIssueNumbers = append(st.SeenIssueNumbers, n)
	if len(st.SeenIssueNumbers) > SeenSetMax {
		st.SeenIssueNumbers = st.SeenIssueNumbers[len(st.SeenIssueNumbers)-SeenSetMax:]
	}
	return st
}

// RememberSeenTS does the same for Slack ts strings.
func RememberSeenTS(st WatermarkState, ts string) WatermarkState {
	st.SeenTS = append(st.SeenTS, ts)
	if len(st.SeenTS) > SeenSetMax {
		st.SeenTS = st.SeenTS[len(st.SeenTS)-SeenSetMax:]
	}
	return st
}

// HasSeenIssue / HasSeenTS are linear scans — fine at K=200.
func HasSeenIssue(st WatermarkState, n int) bool {
	for _, x := range st.SeenIssueNumbers {
		if x == n {
			return true
		}
	}
	return false
}

func HasSeenTS(st WatermarkState, ts string) bool {
	for _, x := range st.SeenTS {
		if x == ts {
			return true
		}
	}
	return false
}
