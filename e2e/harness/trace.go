//go:build e2e

package harness

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Trace is the parsed claude-stub trace for one role. It exposes what the
// launcher fed the agent: the full argv, the resolved system-prompt body,
// the parsed allowed-tools, the MCP config JSON (raw), and every event read
// on stdin. Wave-2 assertions read these.
type Trace struct {
	Argv         []string
	SystemPrompt string
	AllowedTools []string
	MCPConfig    json.RawMessage
	StdinEvents  []string
}

// traceLine is the on-disk shape the stub writes (one JSON object per line).
type traceLine struct {
	Event        string          `json:"event"`
	Argv         []string        `json:"argv,omitempty"`
	SystemPrompt string          `json:"systemPrompt,omitempty"`
	AllowedTools []string        `json:"allowedTools,omitempty"`
	MCPConfig    string          `json:"mcpConfig,omitempty"`
	Stdin        string          `json:"stdin,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

// StubTrace reads and parses the claude-stub trace for the given role. The
// stub writes one file per process (claude-stub.trace.<role>.<pid>.jsonl);
// the most recent one for the role wins, which is the right turn for
// single-invocation flows. It fails the test if no trace exists for the
// role.
func (h *Harness) StubTrace(t *testing.T, role string) Trace {
	t.Helper()
	path, ok := latestTraceFile(h.StateDir, "claude-stub.trace."+role+".")
	if !ok {
		t.Fatalf("harness: no claude-stub trace for role %q under %s", role, h.StateDir)
	}
	lines, err := readTraceLines(path)
	if err != nil {
		t.Fatalf("harness: read stub trace %s: %v", path, err)
	}
	var tr Trace
	for _, l := range lines {
		switch l.Event {
		case "args":
			tr.Argv = l.Argv
			tr.SystemPrompt = l.SystemPrompt
			tr.AllowedTools = l.AllowedTools
			if l.MCPConfig != "" {
				if data, err := os.ReadFile(l.MCPConfig); err == nil {
					tr.MCPConfig = data
				}
			}
		case "stdin":
			tr.StdinEvents = append(tr.StdinEvents, l.Stdin)
		}
	}
	return tr
}

// GhTrace is the parsed gh-stub trace: every gh invocation, in order.
type GhTrace struct {
	Invocations [][]string
}

// GhTrace reads and parses the gh-stub trace (gh-stub.trace.jsonl). An
// absent trace yields an empty GhTrace — a test that scripted no gh calls
// legitimately produces no invocations.
func (h *Harness) GhTrace(t *testing.T) GhTrace {
	t.Helper()
	path := filepath.Join(h.StateDir, "traces", "gh-stub.trace.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GhTrace{}
		}
		t.Fatalf("harness: open gh trace %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var gt GhTrace
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var entry struct {
			Argv []string `json:"argv"`
		}
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatalf("harness: parse gh trace line %q: %v", raw, err)
		}
		gt.Invocations = append(gt.Invocations, entry.Argv)
	}
	return gt
}

// latestTraceFile returns the lexically-last trace file under
// <stateDir>/traces whose name starts with prefix. Trace filenames embed
// the pid, so for a single-invocation flow there's one match; for multi-turn
// flows the last-modified file is the latest turn.
func latestTraceFile(stateDir, prefix string) (string, bool) {
	dir := filepath.Join(stateDir, "traces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var matches []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			matches = append(matches, filepath.Join(dir, e.Name()))
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, ei := os.Stat(matches[i])
		fj, ej := os.Stat(matches[j])
		if ei != nil || ej != nil {
			return matches[i] < matches[j]
		}
		return fi.ModTime().Before(fj.ModTime())
	})
	return matches[len(matches)-1], true
}

func readTraceLines(path string) ([]traceLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []traceLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var l traceLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, sc.Err()
}
