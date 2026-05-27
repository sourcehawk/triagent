package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TouchedContext is one entry in the list of kubernetes contexts an
// investigation has bound to over its lifetime.
type TouchedContext struct {
	Name string    // the context name passed to switch_context
	At   time.Time // event timestamp (zero if events.jsonl lacked one)
}

// ContextsTouched scans <sessionDir>/events.jsonl and returns every
// kubernetes context the agent has successfully switched into, in order.
// Returns (nil, nil) when events.jsonl doesn't exist — that's a fresh
// session with no events.
func ContextsTouched(sessionDir string) ([]TouchedContext, error) {
	path := filepath.Join(sessionDir, "events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []TouchedContext
	scanner := bufio.NewScanner(f)
	// Allow long lines — tool results can be sizeable.
	buf := make([]byte, 0, 256*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		var ev struct {
			Phase     string `json:"phase"`
			ToolName  string `json:"toolName"`
			Result    string `json:"result"`
			ErrorText string `json:"errorText"`
			At        string `json:"at,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Phase != "end" {
			continue
		}
		if !strings.HasSuffix(ev.ToolName, "__switch_context") {
			continue
		}
		if ev.ErrorText != "" {
			continue
		}
		var payload struct {
			ActiveContext string `json:"activeContext"`
		}
		if err := json.Unmarshal([]byte(ev.Result), &payload); err != nil {
			continue
		}
		if payload.ActiveContext == "" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, ev.At)
		out = append(out, TouchedContext{Name: payload.ActiveContext, At: ts})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
