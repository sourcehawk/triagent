# Tool-result token budget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut per-session token usage ~50–70% by capping tool-result blast radius in three places: a byte cap on `get_logs`, lean projection on `get_resource`, and a consolidated `step_complete` tool that collapses the strategies walker's per-step round trips. Add a streaming-search pair so the agent's escalation path doesn't widen the budget.

**Architecture:** Three independent surfaces under one spec. Each task ends with a single commit. Build stays green between commits — additive changes land first (Task 5 adds `step_complete` alongside existing tools), then the migration commit (Task 6) deletes the old surface in lockstep with all callers. Tool surface changes always pair the handler change with the matching `mcp/internal/<server>/specs.go` description update so `c1-mcp dump-meta` (and the frontend MCP Catalog View it feeds) stays accurate.

**Tech Stack:** Go (k8s MCP, strategies MCP, launcher), go-sdk MCP framework, `k8s.io/client-go` (typed + dynamic), YAML system playbooks, TypeScript / Next.js for frontend prose only (no render changes).

**Spec:** `docs/superpowers/specs/2026-05-14-tool-result-token-budget-design.md`

---

## File Map

### Created
- `mcp/internal/k8s/tools_stream.go` — `stream_log_until` + `search_log_stream` handlers, keyword matcher, stream writer
- `mcp/internal/k8s/tools_stream_test.go` — tests for the streaming pair
- `investigate/system/strategic_log_extraction.yaml` — companion playbook
- `mcp/internal/strategies/server_step_complete_test.go` — table-driven tests for `step_complete`

### Modified
- `mcp/internal/k8s/tools_logs.go` — add `MaxBytes` input + byte-cap truncation
- `mcp/internal/k8s/tools_logs_test.go` — tests for byte cap
- `mcp/internal/k8s/tools_resources.go` — add `View` input + lean projection helpers
- `mcp/internal/k8s/tools_resources_test.go` — tests for lean / raw
- `mcp/internal/k8s/server.go` — register streaming tools, plumb `SessionDir` into `Options`
- `mcp/internal/k8s/specs.go` — descriptions + new entries
- `mcp/cmd/serve.go` — pass `SessionDir` to the k8s server when `--kind=k8s`
- `mcp/internal/strategies/server.go` — Task 5 adds `step_complete` + shared helper; Task 6 deletes `record_finding`/`advance`
- `mcp/internal/strategies/specs.go` — Task 5 adds; Task 6 deletes
- `mcp/internal/strategies/playbook_test.go` — Task 6 adds scan-for-old-tool-names test
- `investigate/internal/server/manager.go` — Task 2 adds `streams/` boot sweep inside `Restore()`
- `investigate/system/*.yaml` — Task 6 search-and-replace `record_finding`/`advance` → `step_complete`
- `investigate/system/investigation.yaml` — Task 3 adds delegate_to branches to `strategic_log_extraction`
- `investigate/frontend/lib/mcps.ts` — Task 6 catalog blurb text
- `investigate/frontend/public/docs/playbooks.md` + `overview.md` + `investigations.md` — Task 6 prose
- `investigate/internal/profile/profiles/camunda/prompts/strategies.md` — Task 6 prose

---

## Task 1: `get_logs` byte cap (default-on)

**Files:**
- Modify: `mcp/internal/k8s/tools_logs.go`
- Modify: `mcp/internal/k8s/tools_logs_test.go`
- Modify: `mcp/internal/k8s/specs.go`

Goal: a single tool call cannot land more than 32 KB of log body per pod, regardless of `tailLines` setting. Existing line-cap behavior stays; byte cap is the second wall.

- [ ] **Step 1: Write failing test for default byte cap**

Add to `tools_logs_test.go`:

```go
func TestGetLogs_DefaultByteCap(t *testing.T) {
	// Each line is 4096 bytes; 200 default tailLines × 4 KB = 800 KB raw.
	// With the 32 KB default byte cap we expect roughly 8 lines + marker.
	line := strings.Repeat("X", 4095) + "\n"
	body := strings.Repeat(line, 200)
	tk, fakeClient := newFakeToolKit(t)
	primePodLog(fakeClient, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	out := textOf(res)
	if len(out) > 64*1024 { // generous upper bound: 32 KB body + small headers
		t.Fatalf("output exceeded byte cap: %d bytes", len(out))
	}
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation marker, got:\n%s", out[:min(500, len(out))])
	}
	// Body must end at a line boundary (last char before any trailing newlines is one).
	trimmed := strings.TrimRight(out, "\n")
	if !strings.HasSuffix(trimmed, "X") {
		t.Fatalf("body did not end on a complete line")
	}
}
```

If `newFakeToolKit`/`primePodLog`/`textOf` helpers don't already exist in the file, lift the pattern from any nearby existing test (the k8s tests use `fake.Clientset`); duplicate the helper inline rather than designing a new shared one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./mcp/internal/k8s/ -run TestGetLogs_DefaultByteCap -count=1`
Expected: FAIL — output exceeds cap or no truncation marker.

- [ ] **Step 3: Implement byte cap**

In `tools_logs.go`, add to the constants block:

```go
const (
	defaultLogMaxBytes = 32 * 1024
	maxLogMaxBytes     = 256 * 1024
)
```

Add to `GetLogsInput`:

```go
MaxBytes int `json:"maxBytes,omitempty" jsonschema:"Soft byte cap per pod (default 32768, hard cap 262144). Truncates at the last line boundary; emits a # truncated: marker when the body is cut. Tighten grep/sinceSeconds, or escalate to stream_log_until for keyword search."`
```

In `getLogs`, after `tail` is resolved and before the parallel fetch, derive the byte cap:

```go
maxBytes := in.MaxBytes
if maxBytes <= 0 {
	maxBytes = defaultLogMaxBytes
}
if maxBytes > maxLogMaxBytes {
	maxBytes = maxLogMaxBytes
}
```

Pass it through `podLogFetchOpts` (add a `MaxBytes int` field on that struct). In `fetchOnePodLogs`, after the body is collected and grep is applied, call a new `truncateToBytes(body, maxBytes)` helper that:

1. If `len(body) <= maxBytes`, returns `(body, "", false)`.
2. Otherwise: walks back from `body[maxBytes-1]` to the previous `'\n'`; takes `body[:cutAt+1]`.
3. Returns `(cut, "# truncated: showed N of M lines, K bytes more available — tighten grep/sinceSeconds, or use stream_log_until for keyword search.\n", true)` where N/M/K are computed from the original-vs-cut byte/line counts.

Prepend the marker to the returned body when truncated. Helper signature:

```go
// truncateToBytes cuts body at the nearest preceding line boundary so the
// result is <= maxBytes. Returns the cut body, a one-line marker to prepend
// (empty when no truncation happened), and a bool flag for tests.
func truncateToBytes(body string, maxBytes int) (string, string, bool)
```

Tests are unit-testable in isolation; preserve the existing line-count behavior for non-truncated bodies (the helper is a no-op).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./mcp/internal/k8s/ -run TestGetLogs_DefaultByteCap -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Add edge-case tests**

In the same file:

```go
func TestGetLogs_RespectsExplicitMaxBytes(t *testing.T) {
	line := strings.Repeat("Y", 1023) + "\n" // 1 KB lines
	body := strings.Repeat(line, 80)         // 80 KB raw
	tk, fakeClient := newFakeToolKit(t)
	primePodLog(fakeClient, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{
		Namespace: "ns1", Pod: "pod-a", MaxBytes: 64 * 1024,
	})
	out := textOf(res)
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation at the 64 KB cap")
	}
}

func TestGetLogs_NoTruncationUnderCap(t *testing.T) {
	tk, fakeClient := newFakeToolKit(t)
	primePodLog(fakeClient, "ns1", "pod-a", "small log\nbody\n")
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	out := textOf(res)
	if strings.Contains(out, "# truncated:") {
		t.Fatalf("did not expect truncation marker, got:\n%s", out)
	}
}

func TestGetLogs_MaxBytesHardCap(t *testing.T) {
	tk, _ := newFakeToolKit(t)
	// Schema layer doesn't bound; the handler must.
	in := GetLogsInput{Namespace: "ns1", Pod: "pod-a", MaxBytes: 1 << 30}
	// Run only to confirm no panic / no slice-bounds error.
	_, _, _ = tk.getLogs(t.Context(), nil, in)
}

func TestGetLogs_TruncatedEndsAtLineBoundary(t *testing.T) {
	// 1 KB lines so the cap is hit mid-stream; verify last char before
	// any trailing whitespace is part of a complete line.
	line := strings.Repeat("Z", 1023) + "\n"
	body := strings.Repeat(line, 100)
	tk, fakeClient := newFakeToolKit(t)
	primePodLog(fakeClient, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	trimmed := strings.TrimRight(textOf(res), "\n")
	if !strings.HasSuffix(trimmed, "Z") {
		t.Fatalf("body did not end at a line boundary")
	}
}
```

Run: `go test ./mcp/internal/k8s/ -run TestGetLogs_ -count=1 -race` — all pass.

- [ ] **Step 6: Update specs.go description**

In `mcp/internal/k8s/specs.go`, find the `get_logs` entry and replace the `Description` field:

```go
Description: "Fetch recent log lines for a pod / workload / labelSelector. Body is byte-capped per pod (default 32 KB, hard cap 256 KB) and truncated at line boundaries; a # truncated: marker tells the agent to tighten grep/sinceSeconds or escalate to stream_log_until.",
```

- [ ] **Step 7: Run the broader k8s + wire tests**

Run: `go test ./mcp/internal/k8s/ -race -count=1`
Expected: PASS — no regressions on existing tests.

Run: `go test ./mcp/... -run Wire -count=1`
Expected: PASS — `tools_wire_test.go` confirms catalog ↔ handler symmetry.

- [ ] **Step 8: Commit**

```bash
git add mcp/internal/k8s/tools_logs.go mcp/internal/k8s/tools_logs_test.go mcp/internal/k8s/specs.go
git commit -m "feat(mcp/k8s): byte cap on get_logs

Adds maxBytes (default 32 KB, cap 256 KB) so a single get_logs call
can't dump 800 KB of structured JSON logs into the cache — every
subsequent tool iteration would re-bill it. Truncates at the nearest
line boundary and emits a one-line # truncated: marker so the agent
can tighten grep/sinceSeconds or escalate to stream_log_until."
```

---

## Task 2: `stream_log_until` + `search_log_stream` (with launcher boot sweep)

**Files:**
- Create: `mcp/internal/k8s/tools_stream.go`
- Create: `mcp/internal/k8s/tools_stream_test.go`
- Modify: `mcp/internal/k8s/server.go` — `Options.SessionDir`, register handlers, plumb the dir into the ToolKit
- Modify: `mcp/internal/k8s/specs.go` — new entries
- Modify: `mcp/cmd/serve.go` — pass `--session-dir` to the k8s server too
- Modify: `investigate/internal/server/manager.go` — boot sweep inside `Restore()`

This is the largest task. Keep it as one commit per the spec: the streaming pair is one logical unit and partial commits are confusing.

- [ ] **Step 1: Add `SessionDir` to k8s `Options`, thread it through ToolKit**

In `mcp/internal/k8s/server.go`:

```go
type Options struct {
	KubeconfigPath          string
	AllowlistPath           string
	CrossplaneGroupPatterns []string
	SessionDir              string // optional; required for streaming tools
}
```

In `ToolKit`, add `sessionDir string` and set it in `New`:

```go
toolkit := &ToolKit{
	kubeconfigPath: opts.KubeconfigPath,
	allowlist:      allow,
	crossplanePats: patterns,
	sessionDir:     opts.SessionDir,
}
```

In `mcp/cmd/serve.go`, find the `case "k8s"` branch (look near `out.sessionDir = ...`) and pass `SessionDir: f.sessionDir`. If the case doesn't already accept it, this is a one-line add.

- [ ] **Step 2: Write the keyword-matcher unit tests**

In `tools_stream_test.go`:

```go
func TestKeywordMatcher_AndOfOr(t *testing.T) {
	cases := []struct {
		name    string
		groups  [][]string
		line    string
		want    int // matching group index, -1 = none
	}{
		{"single-and-match", [][]string{{"error", "timeout"}}, "ERROR: connection timeout", 0},
		{"single-and-miss", [][]string{{"error", "timeout"}}, "INFO ok", -1},
		{"second-group-match", [][]string{{"nope"}, {"panic"}}, "fatal: panic in main", 1},
		{"case-insensitive", [][]string{{"FATAL"}}, "fatal", 0},
		{"partial-and-miss", [][]string{{"error", "auth"}}, "ERROR processing", -1},
		{"empty-groups", [][]string{{}}, "anything", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newKeywordMatcher(c.groups)
			got := m.matchIndex(c.line)
			if got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run — verify failure**

Run: `go test ./mcp/internal/k8s/ -run TestKeywordMatcher -count=1`
Expected: FAIL — `newKeywordMatcher` undefined.

- [ ] **Step 4: Implement the keyword matcher**

Start `tools_stream.go`:

```go
package k8s

import "strings"

// keywordMatcher applies a disjunction-of-conjunctions matcher to log lines.
// All comparisons are case-insensitive substring; an empty group never matches.
type keywordMatcher struct {
	groups [][]string // each inner slice is AND-joined; outer is OR-joined
}

func newKeywordMatcher(groups [][]string) *keywordMatcher {
	out := &keywordMatcher{groups: make([][]string, 0, len(groups))}
	for _, g := range groups {
		lower := make([]string, 0, len(g))
		for _, term := range g {
			t := strings.ToLower(strings.TrimSpace(term))
			if t != "" {
				lower = append(lower, t)
			}
		}
		if len(lower) > 0 {
			out.groups = append(out.groups, lower)
		}
	}
	return out
}

// matchIndex returns the index of the first group whose terms all appear in
// line (case-insensitive substring), or -1 if no group matches.
func (m *keywordMatcher) matchIndex(line string) int {
	lower := strings.ToLower(line)
	for i, group := range m.groups {
		all := true
		for _, term := range group {
			if !strings.Contains(lower, term) {
				all = false
				break
			}
		}
		if all {
			return i
		}
	}
	return -1
}

func (m *keywordMatcher) empty() bool { return len(m.groups) == 0 }
```

Run: `go test ./mcp/internal/k8s/ -run TestKeywordMatcher -count=1`
Expected: PASS.

- [ ] **Step 5: Define stream input/output types and the stream-file writer**

Append to `tools_stream.go`:

```go
const (
	defaultStreamTimeoutSeconds = 60
	maxStreamTimeoutSeconds     = 300
	defaultStreamGraceSeconds   = 5
	maxStreamGraceSeconds       = 30
)

// streamDiskCapBytes is the per-stream file size ceiling. var (not const)
// so tests can shrink it; production callers never mutate it.
var streamDiskCapBytes int64 = 16 * 1024 * 1024

type StreamLogUntilInput struct {
	Namespace      string     `json:"namespace" jsonschema:"required workload namespace"`
	Pod            string     `json:"pod,omitempty" jsonschema:"exactly one of pod / labelSelector / workload"`
	LabelSelector  string     `json:"labelSelector,omitempty"`
	Workload       string     `json:"workload,omitempty"`
	Container      string     `json:"container,omitempty"`
	Keywords       [][]string `json:"keywords,omitempty" jsonschema:"disjunction of conjunctions; case-insensitive. e.g. [[\"ERROR\",\"timeout\"],[\"panic\"]] stops on (ERROR AND timeout on one line) OR panic."`
	TimeoutSeconds int        `json:"timeoutSeconds,omitempty" jsonschema:"default 60, cap 300"`
	GraceSeconds   int        `json:"graceSeconds,omitempty" jsonschema:"linger after first keyword hit so multi-line outputs are captured; default 5, cap 30"`
}

type KeywordGroupHit struct {
	Group        []string `json:"group"`
	Hits         int      `json:"hits"`
	FirstLineNum int      `json:"first_line,omitempty"`
}

type StreamLogUntilOutput struct {
	StreamID       string             `json:"stream_id"`
	LogLinesTotal  int                `json:"log_lines_total"`
	KeywordHits    []KeywordGroupHit  `json:"keyword_hits,omitempty"`
	TimedOut       bool               `json:"timed_out"`
	TruncatedAtCap bool               `json:"truncated_at_cap"`
}
```

Plus the file writer:

```go
// streamWriter is the bounded, line-prefixed appender for one stream file.
// All callers serialise on its mutex so concurrent pod readers never
// interleave a partial line. Returns ErrDiskCapHit once the file reaches
// streamDiskCapBytes.
type streamWriter struct {
	mu       sync.Mutex
	f        *os.File
	written  int64
	capped   bool
}

var errDiskCapHit = errors.New("stream disk cap hit")

func (w *streamWriter) writeLine(prefix, line string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capped {
		return errDiskCapHit
	}
	formatted := line
	if prefix != "" {
		formatted = "[" + prefix + "] " + line
	}
	if !strings.HasSuffix(formatted, "\n") {
		formatted += "\n"
	}
	if w.written+int64(len(formatted)) > streamDiskCapBytes {
		// First overflow: mark capped and refuse the write so the file
		// stays at or below the ceiling. Subsequent calls short-circuit
		// on the w.capped check at the top of this method.
		w.capped = true
		return errDiskCapHit
	}
	n, err := w.f.WriteString(formatted)
	w.written += int64(n)
	return err
}
```

(Imports: `errors`, `os`, `sync`, plus existing.)

- [ ] **Step 6: Write the `stream_log_until` single-pod test**

```go
func TestStreamLogUntil_SinglePodTimeout(t *testing.T) {
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(fakeClient, "ns1", "pod-a", []string{"hello\n", "world\n"})
	res, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a", TimeoutSeconds: 1,
	})
	if res != nil && res.IsError {
		t.Fatalf("unexpected error: %s", textOf(res))
	}
	if !out.TimedOut {
		t.Fatalf("expected TimedOut=true")
	}
	if out.LogLinesTotal != 2 {
		t.Fatalf("expected 2 lines, got %d", out.LogLinesTotal)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "streams", out.StreamID+".log"))
	if !strings.Contains(string(body), "hello") || !strings.Contains(string(body), "world") {
		t.Fatalf("stream file missing lines: %s", body)
	}
}
```

`primePodLogStream` adapts the existing primePodLog helper to deliver lines incrementally; the fake client supports both shapes via `Watch`-style returners.

Run: `go test ./mcp/internal/k8s/ -run TestStreamLogUntil_SinglePodTimeout -count=1`
Expected: FAIL — handler undefined.

- [ ] **Step 7: Implement `streamLogUntil` (single-pod path first)**

In `tools_stream.go`:

```go
func (t *ToolKit) streamLogUntil(ctx context.Context, _ *mcp.CallToolRequest, in StreamLogUntilInput) (*mcp.CallToolResult, StreamLogUntilOutput, error) {
	if t.sessionDir == "" {
		return errorResult("stream_log_until requires a session dir; relaunch the investigation"), StreamLogUntilOutput{}, nil
	}
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, StreamLogUntilOutput{}, nil
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return errorResult("stream_log_until requires `namespace`"), StreamLogUntilOutput{}, nil
	}
	pods, err := t.resolveLogSources(ctx, snap, GetLogsInput{
		Namespace: in.Namespace, Pod: in.Pod, LabelSelector: in.LabelSelector, Workload: in.Workload,
	})
	if err != nil {
		return errorResult(err.Error()), StreamLogUntilOutput{}, nil
	}
	if len(pods) == 0 {
		return errorResult("no pods matched"), StreamLogUntilOutput{}, nil
	}

	timeout := clampSeconds(in.TimeoutSeconds, defaultStreamTimeoutSeconds, maxStreamTimeoutSeconds)
	grace := clampSeconds(in.GraceSeconds, defaultStreamGraceSeconds, maxStreamGraceSeconds)
	matcher := newKeywordMatcher(in.Keywords)

	streamID, w, err := t.openStreamFile()
	if err != nil {
		return errorResult(fmt.Sprintf("open stream file: %v", err)), StreamLogUntilOutput{}, nil
	}
	defer w.f.Close()

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	state := &streamState{
		matcher:   matcher,
		hitCounts: make([]int, len(matcher.groups)),
		hitLines:  make([]int, len(matcher.groups)),
		grace:     time.Duration(grace) * time.Second,
		cancel:    cancel,
	}

	// Single-pod first: just iterate stream reader. Multi-pod added in next step.
	tailFollow(timeoutCtx, snap, in.Namespace, pods[0], in.Container, w, "", state)

	out := StreamLogUntilOutput{
		StreamID:       streamID,
		LogLinesTotal:  state.linesTotal(),
		KeywordHits:    state.snapshot(matcher.groups),
		TimedOut:       errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && !state.hitFired(),
		TruncatedAtCap: w.capped,
	}
	return nil, out, nil
}
```

Plus support funcs in the same file:

```go
func clampSeconds(v, def, max int) int {
	if v <= 0 { return def }
	if v > max { return max }
	return v
}

func (t *ToolKit) openStreamFile() (string, *streamWriter, error) {
	streamID := newStreamID()
	dir := filepath.Join(t.sessionDir, "streams")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, streamID+".log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", nil, err
	}
	return streamID, &streamWriter{f: f}, nil
}

func newStreamID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type streamState struct {
	mu        sync.Mutex
	matcher   *keywordMatcher
	hitCounts []int
	hitLines  []int
	lineNum   int
	grace     time.Duration
	graceFire sync.Once
	cancel    context.CancelFunc
	hitAny    bool
}

func (s *streamState) recordLine(line string) {
	s.mu.Lock()
	s.lineNum++
	idx := -1
	if s.matcher != nil && !s.matcher.empty() {
		idx = s.matcher.matchIndex(line)
	}
	if idx >= 0 {
		s.hitCounts[idx]++
		if s.hitLines[idx] == 0 {
			s.hitLines[idx] = s.lineNum
		}
		s.hitAny = true
	}
	fired := s.hitAny
	cancel := s.cancel
	grace := s.grace
	s.mu.Unlock()
	if fired {
		s.graceFire.Do(func() {
			time.AfterFunc(grace, cancel)
		})
	}
}

func (s *streamState) linesTotal() int { s.mu.Lock(); defer s.mu.Unlock(); return s.lineNum }
func (s *streamState) hitFired() bool   { s.mu.Lock(); defer s.mu.Unlock(); return s.hitAny }
func (s *streamState) snapshot(groups [][]string) []KeywordGroupHit {
	s.mu.Lock(); defer s.mu.Unlock()
	out := make([]KeywordGroupHit, len(groups))
	for i, g := range groups {
		out[i] = KeywordGroupHit{Group: g, Hits: s.hitCounts[i], FirstLineNum: s.hitLines[i]}
	}
	return out
}

func tailFollow(ctx context.Context, snap *clientSnapshot, namespace, pod, container string, w *streamWriter, prefix string, state *streamState) {
	req := snap.typed.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
	})
	rc, err := req.Stream(ctx)
	if err != nil {
		return
	}
	defer rc.Close()
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select { case <-ctx.Done(): return; default: }
		line := scanner.Text()
		if err := w.writeLine(prefix, line); err != nil {
			return
		}
		state.recordLine(line)
	}
}
```

Imports to add: `bufio`, `context`, `crypto/rand`, `encoding/hex`, `errors`, `fmt`, `os`, `path/filepath`, `strings`, `sync`, `time`, `corev1 "k8s.io/api/core/v1"`.

Run: `go test ./mcp/internal/k8s/ -run TestStreamLogUntil_SinglePodTimeout -count=1 -race`
Expected: PASS.

- [ ] **Step 8: Add multi-pod + keyword + grace tests**

```go
func TestStreamLogUntil_StopsOnKeywordWithGrace(t *testing.T) {
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(fakeClient, "ns1", "pod-a", []string{
		"INFO ready\n", "WARN slow\n", "ERROR: connection timeout\n", "INFO retry\n",
	})
	res, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a",
		Keywords:       [][]string{{"ERROR", "timeout"}},
		TimeoutSeconds: 30,
		GraceSeconds:   1,
	})
	if res != nil && res.IsError { t.Fatal(textOf(res)) }
	if out.TimedOut { t.Fatal("expected early stop, got TimedOut=true") }
	if len(out.KeywordHits) != 1 || out.KeywordHits[0].Hits == 0 {
		t.Fatalf("expected one keyword hit, got %+v", out.KeywordHits)
	}
}

func TestStreamLogUntil_CaseInsensitiveKeywords(t *testing.T) {
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(fakeClient, "ns1", "pod-a", []string{"error happened\n"})
	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a",
		Keywords:       [][]string{{"ERROR"}},
		TimeoutSeconds: 2,
		GraceSeconds:   1,
	})
	if out.KeywordHits[0].Hits != 1 {
		t.Fatalf("expected case-insensitive hit, got %+v", out.KeywordHits)
	}
}

func TestStreamLogUntil_MultiPodPrefixedLines(t *testing.T) {
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(fakeClient, "ns1", "pod-a", []string{"a-line\n"})
	primePodLogStream(fakeClient, "ns1", "pod-b", []string{"b-line\n"})
	// Use labelSelector or workload to match both; simplest is to seed the
	// fake so a label selector resolves to both pods.
	primePodList(fakeClient, "ns1", map[string]string{"app": "demo"}, []string{"pod-a", "pod-b"})
	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", LabelSelector: "app=demo", TimeoutSeconds: 2,
	})
	body, _ := os.ReadFile(filepath.Join(dir, "streams", out.StreamID+".log"))
	s := string(body)
	if !strings.Contains(s, "[pod-a]") || !strings.Contains(s, "[pod-b]") {
		t.Fatalf("expected per-pod prefixes, got: %s", s)
	}
}

func TestStreamLogUntil_DiskCapHit(t *testing.T) {
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	// Make streamDiskCapBytes a var (see step 5) so this test can shrink it.
	prev := streamDiskCapBytes
	streamDiskCapBytes = 1024
	t.Cleanup(func() { streamDiskCapBytes = prev })

	// Deliver enough lines to blow the 1 KB cap.
	lines := make([]string, 50)
	for i := range lines { lines[i] = strings.Repeat("x", 64) + "\n" }
	primePodLogStream(fakeClient, "ns1", "pod-a", lines)

	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a", TimeoutSeconds: 2,
	})
	if !out.TruncatedAtCap { t.Fatalf("expected TruncatedAtCap=true") }
	st, err := os.Stat(filepath.Join(dir, "streams", out.StreamID+".log"))
	if err != nil { t.Fatal(err) }
	if st.Size() > 1024 { t.Fatalf("file exceeded cap: %d bytes", st.Size()) }
}
```

(For `TestStreamLogUntil_DiskCapHit`, the cleanest approach is to make `streamDiskCapBytes` a `var` and override it in the test; do that.)

- [ ] **Step 9: Implement multi-pod loop in `streamLogUntil`**

Replace the "Single-pod first" line in step 7's implementation with:

```go
var wg sync.WaitGroup
for _, p := range pods {
	wg.Add(1)
	prefix := ""
	if len(pods) > 1 { prefix = p }
	go func(pod string) {
		defer wg.Done()
		tailFollow(timeoutCtx, snap, in.Namespace, pod, in.Container, w, prefix, state)
	}(p)
}
wg.Wait()
```

Run: `go test ./mcp/internal/k8s/ -run TestStreamLogUntil -count=1 -race`
Expected: ALL PASS.

- [ ] **Step 10: Write `search_log_stream` tests**

```go
func TestSearchLogStream_GrepAndByteCap(t *testing.T) {
	dir := t.TempDir()
	streamID := "abc123"
	if err := os.MkdirAll(filepath.Join(dir, "streams"), 0o700); err != nil { t.Fatal(err) }
	body := ""
	for i := 0; i < 100; i++ {
		body += fmt.Sprintf("line %d INFO ok\n", i)
	}
	body += "line 100 ERROR boom\n"
	if err := os.WriteFile(filepath.Join(dir, "streams", streamID+".log"), []byte(body), 0o600); err != nil { t.Fatal(err) }

	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{
		StreamID: streamID, Grep: "ERROR", MaxBytes: 1024,
	})
	out := textOf(res)
	if !strings.Contains(out, "line 100 ERROR boom") {
		t.Fatalf("expected the ERROR line, got: %s", out)
	}
}

func TestSearchLogStream_KVFilter(t *testing.T) {
	dir := t.TempDir()
	streamID := "abc"
	_ = os.MkdirAll(filepath.Join(dir, "streams"), 0o700)
	body := "level=info component=zeebe msg=ok\nlevel=error component=zeebe msg=oom\nlevel=info component=elasticsearch msg=ok\n"
	_ = os.WriteFile(filepath.Join(dir, "streams", streamID+".log"), []byte(body), 0o600)
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{
		StreamID: streamID,
		KVFilters: []KVFilter{
			{Key: "level", Value: "error"}, {Key: "component", Value: "zeebe"},
		},
	})
	out := textOf(res)
	if !strings.Contains(out, "level=error component=zeebe") { t.Fatalf("expected error/zeebe match, got %s", out) }
	if strings.Contains(out, "level=info") { t.Fatalf("info line leaked: %s", out) }
}

func TestSearchLogStream_StreamNotFound(t *testing.T) {
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = t.TempDir()
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{StreamID: "nope"})
	if res == nil || !res.IsError { t.Fatal("expected error for missing stream") }
}
```

- [ ] **Step 11: Implement `search_log_stream`**

Append to `tools_stream.go`:

```go
type SearchLogStreamInput struct {
	StreamID  string     `json:"stream_id" jsonschema:"the id returned by stream_log_until"`
	Grep      string     `json:"grep,omitempty" jsonschema:"case-sensitive substring (mirrors get_logs.grep)"`
	KVFilters []KVFilter `json:"kvFilters,omitempty" jsonschema:"AND-joined substring filters of the form key=value or key: value found in each line"`
	TailLines int        `json:"tailLines,omitempty"`
	MaxBytes  int        `json:"maxBytes,omitempty" jsonschema:"per-response byte cap (default 32768, hard cap 262144). Truncated body ends at a line boundary; same marker as get_logs."`
}

type KVFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (t *ToolKit) searchLogStream(ctx context.Context, _ *mcp.CallToolRequest, in SearchLogStreamInput) (*mcp.CallToolResult, any, error) {
	if t.sessionDir == "" {
		return errorResult("search_log_stream requires a session dir"), nil, nil
	}
	path := filepath.Join(t.sessionDir, "streams", in.StreamID+".log")
	f, err := os.Open(path)
	if err != nil {
		return errorResult(fmt.Sprintf("stream %q not found", in.StreamID)), nil, nil
	}
	defer f.Close()

	maxBytes := in.MaxBytes
	if maxBytes <= 0 { maxBytes = defaultLogMaxBytes }
	if maxBytes > maxLogMaxBytes { maxBytes = maxLogMaxBytes }

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var keepRing []string
	tail := in.TailLines
	if tail > maxLogTailLines { tail = maxLogTailLines }
	for scanner.Scan() {
		line := scanner.Text()
		if in.Grep != "" && !strings.Contains(line, in.Grep) { continue }
		if !matchesKV(line, in.KVFilters) { continue }
		if tail > 0 {
			keepRing = append(keepRing, line)
			if len(keepRing) > tail { keepRing = keepRing[len(keepRing)-tail:] }
			continue
		}
		keepRing = append(keepRing, line)
	}
	body := strings.Join(keepRing, "\n") + "\n"
	cut, marker, _ := truncateToBytes(body, maxBytes)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: marker + cut}}}, nil, nil
}

func matchesKV(line string, kvs []KVFilter) bool {
	for _, kv := range kvs {
		// Loose match: look for "key=" or "key: " then check the value substring.
		i := strings.Index(line, kv.Key+"=")
		if i < 0 { i = strings.Index(line, kv.Key+": ") }
		if i < 0 { return false }
		if kv.Value != "" && !strings.Contains(line[i:], kv.Value) { return false }
	}
	return true
}
```

Run: `go test ./mcp/internal/k8s/ -run TestSearchLogStream -count=1 -race`
Expected: PASS.

- [ ] **Step 12: Register the new tools + update specs.go**

In `mcp/internal/k8s/server.go`, inside `register` (or wherever existing tools like `get_logs` are registered), add:

```go
mcp.AddTool(s.impl, &mcp.Tool{
	Name:        "stream_log_until",
	Description: "Tail logs into a per-investigation file; stop on a keyword group hit (AND-of-OR, case-insensitive) or timeout. Returns a stream_id for search_log_stream; the bulk volume never enters the conversation context.",
}, telemetry.Wrap("stream_log_until", t.streamLogUntil))

mcp.AddTool(s.impl, &mcp.Tool{
	Name:        "search_log_stream",
	Description: "Filter a previously-streamed log file by grep and/or key=value substrings; response is byte-capped (default 32 KB) and truncated at line boundaries.",
}, telemetry.Wrap("search_log_stream", t.searchLogStream))
```

Add corresponding entries in `mcp/internal/k8s/specs.go`:

```go
{
	Server:      "c1-k8s",
	Name:        "stream_log_until",
	Description: "Stream logs to a per-investigation file until a keyword group hits (AND-of-OR, case-insensitive) or timeout. Bulk volume stays out of the conversation; search_log_stream filters the file.",
	Inputs:      toolspec.FromStruct(StreamLogUntilInput{}),
},
{
	Server:      "c1-k8s",
	Name:        "search_log_stream",
	Description: "Filter a captured stream by grep + key=value substrings; byte-capped per response (32 KB default).",
	Inputs:      toolspec.FromStruct(SearchLogStreamInput{}),
},
```

- [ ] **Step 13: Wire the boot sweep in the launcher**

In `investigate/internal/server/manager.go`'s `Restore()`, inside the per-investigation loop, after `inv` is loaded:

```go
if inv.SessionDir != "" {
	streamsDir := filepath.Join(inv.SessionDir, "streams")
	if err := os.RemoveAll(streamsDir); err != nil {
		// Non-fatal: log + continue; next stream_log_until will recreate.
		// (Match the file's existing logger convention.)
	}
}
```

Add the import for `path/filepath` and `os` if not already present.

- [ ] **Step 14: Wire `SessionDir` into the k8s MCP launch**

In `mcp/cmd/serve.go`'s case for `--kind=k8s` (search for `case "k8s":`), construct `Options` with `SessionDir: f.sessionDir`. The CLI flag already exists for the strategies case (`--session-dir`); confirm `f.sessionDir` is populated for k8s too. If it's only set when `--kind=strategies` was the original gate, lift it out of that branch so it lands for k8s as well.

- [ ] **Step 15: Run full k8s + wire test suites**

Run: `go test ./mcp/internal/k8s/ -race -count=1`
Expected: PASS.

Run: `go test ./mcp/... -run Wire -count=1`
Expected: PASS — new tools appear in catalog.

Run: `go test ./investigate/... -race -count=1`
Expected: PASS.

- [ ] **Step 16: Commit**

```bash
git add mcp/internal/k8s/tools_stream.go mcp/internal/k8s/tools_stream_test.go mcp/internal/k8s/server.go mcp/internal/k8s/specs.go mcp/cmd/serve.go investigate/internal/server/manager.go
git commit -m "feat(mcp/k8s): stream_log_until + search_log_stream

Adds a streaming-search pair so the agent's log-investigation
escalation path doesn't widen the context budget. stream_log_until
tails into a per-investigation file under <sessionDir>/streams/,
stops on a case-insensitive AND-of-OR keyword group or timeout, and
returns a stream_id. search_log_stream filters that file with grep
+ kv substrings and returns a byte-capped response. The bulk log
volume never enters the conversation. Launcher Restore() sweeps the
streams dir on boot."
```

---

## Task 3: `strategic_log_extraction` system playbook

**Files:**
- Create: `investigate/system/strategic_log_extraction.yaml`
- Modify: `investigate/system/investigation.yaml` — delegate_to the new playbook
- Modify: `investigate/system/embed_test.go` if it asserts a count (verify by running it first)

- [ ] **Step 1: Author the new playbook YAML**

`investigate/system/strategic_log_extraction.yaml`:

```yaml
id: strategic_log_extraction
type: general
symptom: "200 lines from get_logs wasn't enough; need a structured search through a larger window"
description: |
  Companion playbook for the c1-k8s streaming search pair. Delegated from
  investigation playbooks when the agent needs to find a specific pattern
  in a log volume larger than get_logs' byte cap. Captures the volume in
  a per-investigation stream file (no bulk in context) and filters with
  search_log_stream.
entrypoint: probe_structure

nodes:
  - id: probe_structure
    description: |
      Pull a tiny sample (tailLines: 5) of the target pod or workload to
      inspect the log shape. Decide log_format and pick candidate keyword(s)
      / kv-keys to drive the stream.
    suggested_calls:
      - tool: c1-k8s/get_logs
        purpose: "sample shape; tailLines: 5"
    expected_findings: [log_format, candidate_keyword, candidate_kv_keys]
    next:
      - goto: stream_capture
        condition: "always"

  - id: stream_capture
    description: |
      Call stream_log_until on the same selector with the chosen keyword
      group when one was identified; otherwise timeout-only. The keywords
      shape is [[a, b], [c]] meaning (a AND b on one line) OR c — terms
      are case-insensitive substrings. Returns a stream_id.
    suggested_calls:
      - tool: c1-k8s/stream_log_until
        purpose: "capture volume until keyword group or timeout"
    expected_findings: [stream_id, keyword_hits_summary]
    next:
      - goto: filtered_search
        condition: "always"

  - id: filtered_search
    description: |
      Use search_log_stream with progressively tighter grep / kvFilters
      until the relevant lines fit in the byte cap. Iterate by tightening
      filters rather than raising maxBytes.
    suggested_calls:
      - tool: c1-k8s/search_log_stream
        purpose: "filter the captured stream"
    expected_findings: [matched_lines_summary]
    next:
      - goto: return_to_parent
        condition: "always"

  - id: return_to_parent
    terminal_advice: "Return to the parent investigation with the extracted findings in mind. Reference the stream_id and the key filters that landed the evidence."
```

- [ ] **Step 2: Wire `delegate_to` from `investigation.yaml`**

In `investigate/system/investigation.yaml`, find log-investigation branches that today end with `c1-k8s/get_logs` and append a node option that delegates to `strategic_log_extraction` for the "need more than 200 lines" path. Concretely, find the node where the agent has just called `get_logs` and add a next-option:

```yaml
- goto: extract_more_logs
  condition: "the get_logs output was truncated and the relevant signal wasn't found in the captured tail"
```

Then add the `extract_more_logs` node:

```yaml
- id: extract_more_logs
  description: "Use the strategic_log_extraction playbook to capture a larger log window and filter it."
  delegate_to: strategic_log_extraction
```

(The exact host node depends on the current shape of `investigation.yaml`; look for the existing log-related step and add the branch there. If multiple log-related steps exist, add the delegate_to from the one most likely to need volume.)

- [ ] **Step 3: Run the strategies playbook test**

Run: `go test ./mcp/internal/strategies/ -race -count=1`
Expected: PASS — the YAML loads, delegate targets resolve.

Run: `go test ./investigate/internal/ -race -count=1`
Expected: PASS — the embed_test still finds the new file via the `*.yaml` glob.

- [ ] **Step 4: Commit**

```bash
git add investigate/system/strategic_log_extraction.yaml investigate/system/investigation.yaml
git commit -m "feat(investigate): strategic_log_extraction system playbook

Companion to stream_log_until + search_log_stream. Delegated from the
investigation playbook's log-investigation branches when get_logs'
byte cap isn't enough. Teaches the agent the structure-probe →
stream → filter loop so bulk log volume stays out of context."
```

---

## Task 4: `get_resource` lean default

**Files:**
- Modify: `mcp/internal/k8s/tools_resources.go`
- Modify: `mcp/internal/k8s/tools_resources_test.go`
- Modify: `mcp/internal/k8s/specs.go`

- [ ] **Step 1: Write failing tests**

In `tools_resources_test.go`:

```go
func TestGetResource_LeanByDefault(t *testing.T) {
	tk, fakeClient := newFakeToolKit(t)
	primePod(fakeClient, "ns1", "pod-a", podWithBloat()) // helper that sets managedFields + noisy annotations
	res, _, _ := tk.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a"})
	out := textOf(res)
	if strings.Contains(out, "managedFields") {
		t.Fatalf("lean default leaked managedFields:\n%s", out)
	}
	if strings.Contains(out, "kubectl.kubernetes.io/last-applied-configuration") {
		t.Fatalf("lean default leaked kubectl noise")
	}
	if !strings.HasPrefix(out, "# lean view") {
		t.Fatalf("expected lean preamble, got first line: %q", firstLine(out))
	}
}

func TestGetResource_RawIncludesEverything(t *testing.T) {
	tk, fakeClient := newFakeToolKit(t)
	primePod(fakeClient, "ns1", "pod-a", podWithBloat())
	res, _, _ := tk.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a", View: "raw"})
	out := textOf(res)
	if !strings.Contains(out, "managedFields") {
		t.Fatalf("raw view dropped managedFields")
	}
	if strings.HasPrefix(out, "# lean view") {
		t.Fatalf("raw view should not show lean preamble")
	}
}

func TestGetResource_LeanStripsRedundantAnnotations(t *testing.T) {
	tk, fakeClient := newFakeToolKit(t)
	primePodWithAnnotations(fakeClient, "ns1", "pod-a", map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": `{"big":"blob"}`,
		"meta.helm.sh/release-name":                        "camunda",
		"team":                                             "platform", // keep
	})
	res, _, _ := tk.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a"})
	out := textOf(res)
	if strings.Contains(out, "last-applied-configuration") {
		t.Fatalf("expected kubectl annotation to be stripped")
	}
	if !strings.Contains(out, "team: platform") {
		t.Fatalf("expected custom annotation to survive")
	}
}

func TestGetResource_ConfigMapRedactionStillApplies(t *testing.T) {
	tk, fakeClient := newFakeToolKit(t)
	primeConfigMap(fakeClient, "ns1", "cm", map[string]string{"password": "supersecret"})
	res, _, _ := tk.getResource(t.Context(), nil, GetResourceInput{Kind: "ConfigMap", Namespace: "ns1", Name: "cm"})
	out := textOf(res)
	if strings.Contains(out, "supersecret") {
		t.Fatalf("lean view leaked a redacted value")
	}
}
```

Run: `go test ./mcp/internal/k8s/ -run TestGetResource_ -count=1`
Expected: FAIL (compile error or assertions, depending on what's missing).

- [ ] **Step 2: Implement the `View` input and lean projection**

In `tools_resources.go`:

```go
type GetResourceInput struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Output    string `json:"output,omitempty" jsonschema:"yaml (default), json, or describe"`
	View      string `json:"view,omitempty" jsonschema:"lean (default) strips managedFields, kubectl/helm noise annotations, and projects status by kind; raw returns the unprojected object. Pass raw when the projection hides a field you need (e.g. a CRD with unusual status shape)."`
}

var leanStrippedAnnotationKeys = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision-history",
}

var leanStrippedAnnotationPrefixes = []string{
	"meta.helm.sh/",
}

var leanStrippedMetaFields = []string{
	"managedFields", "resourceVersion", "uid", "generation", "selfLink",
}
```

Add a helper `applyLeanInPlace(obj *unstructured.Unstructured, rk ResolvedKind)`:

```go
func applyLeanInPlace(obj *unstructured.Unstructured, rk ResolvedKind) {
	for _, f := range leanStrippedMetaFields {
		unstructured.RemoveNestedField(obj.Object, "metadata", f)
	}
	if ann, ok, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations"); ok {
		filtered := make(map[string]string, len(ann))
		for k, v := range ann {
			if containsString(leanStrippedAnnotationKeys, k) { continue }
			if hasAnyPrefix(k, leanStrippedAnnotationPrefixes) { continue }
			filtered[k] = v
		}
		if len(filtered) == 0 {
			unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedStringMap(obj.Object, filtered, "metadata", "annotations")
		}
	}
	if status, ok, _ := unstructured.NestedMap(obj.Object, "status"); ok && len(status) > 0 {
		_ = unstructured.SetNestedMap(obj.Object, projectStatus(rk.Kind.Kind, status), "status")
	}
}

func containsString(xs []string, x string) bool {
	for _, s := range xs { if s == x { return true } }
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes { if strings.HasPrefix(s, p) { return true } }
	return false
}
```

In the `getResource` handler, after redaction is applied and before rendering, branch on `View`:

```go
view := strings.ToLower(strings.TrimSpace(in.View))
if view == "" { view = "lean" }
var preamble string
switch view {
case "lean":
	applyLeanInPlace(obj, rk)
	preamble = "# lean view — stripped: managedFields, helm/kubectl annotations, status fields not in projection. Pass view: \"raw\" for the unprojected object.\n"
case "raw":
	// no-op
default:
	return errorResult(fmt.Sprintf("unknown view %q; use lean or raw", in.View)), nil, nil
}
```

Prepend `preamble` to the rendered output before returning.

Imports: `unstructured`'s `RemoveNestedField` / `NestedStringMap` / `SetNestedStringMap` / `SetNestedMap` are in the existing import.

Run: `go test ./mcp/internal/k8s/ -run TestGetResource_ -count=1 -race`
Expected: ALL PASS.

- [ ] **Step 3: Update `mcp/internal/k8s/specs.go`**

Replace the `get_resource` description:

```go
Description: "Fetch one allow-listed resource as YAML / JSON / describe. Defaults to lean view (strips managedFields, kubectl/helm noise annotations, projects status by kind); pass view: \"raw\" for the unprojected object.",
```

- [ ] **Step 4: Run full k8s + wire tests**

Run: `go test ./mcp/internal/k8s/ -race -count=1`
Run: `go test ./mcp/... -run Wire -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add mcp/internal/k8s/tools_resources.go mcp/internal/k8s/tools_resources_test.go mcp/internal/k8s/specs.go
git commit -m "feat(mcp/k8s): lean default for get_resource, opt-in view: raw

Defaults strip managedFields, kubectl/helm noise annotations, and
project status by kind (mirroring what list_resources already does).
A one-line preamble tells the agent what was stripped so it can flip
to view: \"raw\" for the rare CRD case where the projection hides
something. Redaction (ConfigMap heuristic) still runs in both views."
```

---

## Task 5: `step_complete` additive

**Files:**
- Modify: `mcp/internal/strategies/server.go` — extract shared helper, add `step_complete` handler
- Create: `mcp/internal/strategies/server_step_complete_test.go`
- Modify: `mcp/internal/strategies/specs.go`

This commit is additive: `record_finding` and `advance` stay, both becoming thin wrappers around the new shared helper. `step_complete` is the third entry point on top.

- [ ] **Step 1: Write failing tests for `step_complete`**

Test helpers referenced below (`newTestServer`, `mustWalk`, `mustGetState`, `newTestServerWithDelegate`, `newTestServerWithHandoff`, `findOption`, `textOf`) follow the pattern already used by the existing strategies test files — if they don't already exist with these exact names, copy the equivalent setup pattern from `server_test.go` (or wherever `advance` is tested) and rename inline. The fixture playbooks live under `mcp/internal/strategies/testdata`; reuse whichever ones already exercise delegate_to and handoff if they exist, otherwise add minimal fixtures alongside the test file.

Create `server_step_complete_test.go`:

```go
package strategies

import (
	"testing"
)

func TestStepComplete_RecordsThenAdvances(t *testing.T) {
	s := newTestServer(t) // existing helper; copy pattern from server_test.go if needed
	walkOut := mustWalk(t, s, "investigation", "cluster-1", "ns-1")
	res, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings: []FindingEntry{
			{Key: "pod_state", Value: "CrashLoopBackOff", SourceTool: "c1-k8s/get_resource"},
			{Key: "restart_count", Value: 17, SourceTool: "c1-k8s/get_resource"},
		},
		Goto: walkOut.Step.NextOptions[0].Goto,
	})
	if res != nil && res.IsError { t.Fatal(textOf(res)) }
	// Step view returned for the new node.
	if out.Step.Description == "" { t.Fatal("expected non-empty step description") }
	// Findings landed.
	state := mustGetState(t, s, walkOut.SessionID)
	if state.Session.Findings["pod_state"] != "CrashLoopBackOff" {
		t.Fatalf("findings missing: %+v", state.Session.Findings)
	}
}

func TestStepComplete_InvalidGotoRejectsAtomically(t *testing.T) {
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "investigation", "cluster-1", "ns-1")
	res, _, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings:  []FindingEntry{{Key: "pod_state", Value: "Running"}},
		Goto:      "nonexistent-node",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid goto")
	}
	state := mustGetState(t, s, walkOut.SessionID)
	if _, exists := state.Session.Findings["pod_state"]; exists {
		t.Fatal("findings should not be recorded on invalid goto")
	}
}

func TestStepComplete_EmptyFindings(t *testing.T) {
	s := newTestServer(t)
	walkOut := mustWalk(t, s, "investigation", "cluster-1", "ns-1")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Goto:      walkOut.Step.NextOptions[0].Goto,
	})
	if out.Step.Description == "" { t.Fatal("expected step advance with no findings") }
}

func TestStepComplete_DelegatePush(t *testing.T) {
	// Reuse whichever fixture playbook in mcp/internal/strategies/testdata
	// has a delegate_to node (the existing advance tests already do — copy
	// their newTestServer setup). The walker should jump straight to the
	// delegate's entrypoint when step_complete points at the delegate_to node.
	s := newTestServerWithDelegate(t)
	walkOut := mustWalk(t, s, "parent_playbook", "cluster-1", "ns-1")
	delegateOpt := findOption(t, walkOut.Step.NextOptions, "to_delegate")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Findings:  []FindingEntry{{Key: "checked", Value: true}},
		Goto:      delegateOpt.Goto,
	})
	// Returned step should be the DELEGATE's entrypoint, not the original
	// delegate_to node itself (the walker transparently descends).
	if out.Step.NodeID == delegateOpt.Goto {
		t.Fatalf("expected descent into delegate, still at delegate_to node %q", out.Step.NodeID)
	}
}

func TestStepComplete_HandoffTerminal(t *testing.T) {
	// Use a fixture playbook whose terminal node has handoff: <other>.
	// Walk to that terminal via step_complete and verify the response
	// carries the terminal_advice. If the session was popped from a
	// delegate frame, DelegateReturned should be set with the popped
	// playbook id + its terminal_advice.
	s := newTestServerWithHandoff(t)
	walkOut := mustWalk(t, s, "parent_with_handoff", "cluster-1", "ns-1")
	terminalOpt := findOption(t, walkOut.Step.NextOptions, "to_terminal")
	_, out, _ := s.stepComplete(t.Context(), nil, stepCompleteIn{
		SessionID: walkOut.SessionID,
		Goto:      terminalOpt.Goto,
	})
	if out.Step.TerminalAdvice == "" {
		t.Fatalf("expected terminal_advice on the landed terminal step")
	}
}
```

Run: `go test ./mcp/internal/strategies/ -run TestStepComplete_ -count=1`
Expected: FAIL — `stepCompleteIn`/`stepComplete` undefined.

- [ ] **Step 2: Extract shared `recordAndAdvance` helper**

In `mcp/internal/strategies/server.go`, identify the existing body of `advance` and `recordFinding`. Create the shared helper near the existing handler functions:

```go
// recordAndAdvance applies a batch of findings and transitions to goto in
// one atomic update. Returns the rendered step view (plus DelegateReturned
// if popping). Used by step_complete directly and by the legacy
// record_finding (with empty goto) + advance (with empty findings) wrappers
// while they coexist.
func (s *Server) recordAndAdvance(sessionID string, findings []FindingEntry, gotoID string) (*mcp.CallToolResult, stepView, *DelegateReturn, error) {
	sess, err := s.store.get(sessionID)
	if err != nil {
		return errorResult(err.Error()), stepView{}, nil, nil
	}
	pb, ok := s.playbooks[sess.PlaybookID]
	if !ok {
		return errorResult(fmt.Sprintf("playbook %q referenced by session no longer exists", sess.PlaybookID)), stepView{}, nil, nil
	}
	// Validate goto FIRST so an invalid call doesn't record findings.
	if gotoID != "" {
		if _, err := findNode(pb, gotoID); err != nil {
			return errorResult(err.Error()), stepView{}, nil, nil
		}
	}
	// Record findings under the session lock; the existing helper.
	if len(findings) > 0 {
		sess, err = s.store.update(sessionID, func(sn *Session) {
			for _, f := range findings {
				sn.Findings[f.Key] = f.Value
				sn.RecordedCalls = append(sn.RecordedCalls, RecordedCall{
					Tool: f.SourceTool, FindingKey: f.Key, At: time.Now().UTC(),
				})
			}
		})
		if err != nil { return errorResult(err.Error()), stepView{}, nil, nil }
	}
	// If no goto, return current step view (matches the old record_finding
	// behavior — used only by the legacy wrapper during this commit).
	if gotoID == "" {
		node, err := findNode(pb, sess.CurrentNode)
		if err != nil { return errorResult(err.Error()), stepView{}, nil, nil }
		return nil, renderStep(node, sess), nil, nil
	}
	// Lift the existing advance body into a separate helper if it's not
	// already one; call it here for the transition.
	return s.applyAdvance(sess, pb, gotoID)
}
```

(Extract `applyAdvance` from the existing `advance` function body: everything after the `findNode` validation up to the end. It returns `(*mcp.CallToolResult, stepView, *DelegateReturn, error)`. Then the existing `advance` handler becomes:)

```go
func (s *Server) advance(ctx context.Context, _ *mcp.CallToolRequest, in advanceIn) (*mcp.CallToolResult, advanceOut, error) {
	res, step, deleg, err := s.recordAndAdvance(in.SessionID, nil, in.Goto)
	return res, advanceOut{Step: step, DelegateReturned: deleg}, err
}
```

And `recordFinding` becomes:

```go
func (s *Server) recordFinding(ctx context.Context, _ *mcp.CallToolRequest, in recordFindingIn) (*mcp.CallToolResult, recordFindingOut, error) {
	if in.Key == "" { return errorResult("key is required"), recordFindingOut{}, nil }
	res, step, _, err := s.recordAndAdvance(in.SessionID, []FindingEntry{{
		Key: in.Key, Value: in.Value, SourceTool: in.SourceTool,
	}}, "")
	return res, recordFindingOut{Step: step}, err
}
```

- [ ] **Step 3: Implement `step_complete` handler**

In `server.go`:

```go
type stepCompleteIn struct {
	SessionID string         `json:"session_id"`
	Findings  []FindingEntry `json:"findings,omitempty" jsonschema:"findings to record before transitioning; pass [] if none"`
	Goto      string         `json:"goto" jsonschema:"the node id to advance to; one of get_state's next_options[].goto"`
}

type FindingEntry struct {
	Key        string `json:"key"`
	Value      any    `json:"value"`
	SourceTool string `json:"source_tool,omitempty"`
}

type stepCompleteOut struct {
	Step             stepView        `json:"step"`
	DelegateReturned *DelegateReturn `json:"delegate_returned,omitempty"`
}

func (s *Server) stepComplete(ctx context.Context, _ *mcp.CallToolRequest, in stepCompleteIn) (*mcp.CallToolResult, stepCompleteOut, error) {
	if strings.TrimSpace(in.Goto) == "" {
		return errorResult("step_complete requires goto; use get_state to list next_options"), stepCompleteOut{}, nil
	}
	res, step, deleg, err := s.recordAndAdvance(in.SessionID, in.Findings, in.Goto)
	return res, stepCompleteOut{Step: step, DelegateReturned: deleg}, err
}
```

Register it in the `register()` function alongside `advance`:

```go
mcp.AddTool(s.impl, &mcp.Tool{
	Name:        "step_complete",
	Description: "Record findings and advance to the next node in one call. Atomic: an invalid goto rejects the whole call (no findings recorded). Pass findings: [] for a pure transition. The agent should default to this over record_finding + advance.",
}, telemetry.Wrap("step_complete", s.stepComplete))
```

- [ ] **Step 4: Update `mcp/internal/strategies/specs.go`**

Add the `step_complete` entry near the existing `advance` entry:

```go
{
	Server:      "c1-strategies",
	Name:        "step_complete",
	Description: "Record findings and advance atomically; replaces the record_finding + advance pattern. Pass goto + optional findings.",
	Inputs:      toolspec.FromStruct(stepCompleteIn{}),
},
```

- [ ] **Step 5: Run tests**

Run: `go test ./mcp/internal/strategies/ -race -count=1`
Expected: PASS — new tests + all existing `record_finding`/`advance` tests still green (they exercise the same shared helper now).

Run: `go test ./mcp/... -run Wire -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add mcp/internal/strategies/server.go mcp/internal/strategies/server_step_complete_test.go mcp/internal/strategies/specs.go
git commit -m "feat(mcp/strategies): step_complete tool

Collapses the walker's per-step round trips: record_finding + advance
become one atomic step_complete call. record_finding and advance stay
as thin wrappers around the shared helper while the system playbooks
are migrated; commit B deletes them. step_complete validates goto
before recording findings so an invalid call doesn't half-update the
session."
```

---

## Task 6: Migrate playbooks + delete old tools + prose updates

**Files:**
- Modify: `mcp/internal/strategies/server.go` — delete `record_finding` + `advance` handlers, schemas, registrations
- Modify: `mcp/internal/strategies/specs.go` — delete the two entries
- Modify: `mcp/internal/strategies/playbook_test.go` — add scan test
- Modify: `investigate/system/*.yaml` — search-and-replace tool names in `suggested_calls`
- Modify: `investigate/frontend/lib/mcps.ts` — catalog blurb
- Modify: `investigate/frontend/public/docs/{playbooks,overview,investigations}.md` — prose
- Modify: `investigate/internal/profile/profiles/camunda/prompts/strategies.md` — agent-facing prompt

- [ ] **Step 1: Audit system playbook references**

```bash
grep -rn "record_finding\|c1-strategies/advance\|tool: c1-strategies/advance" investigate/system/
```

For each match, the substitution rule is:

- `tool: c1-strategies/record_finding` → `tool: c1-strategies/step_complete`
- `tool: c1-strategies/advance` → `tool: c1-strategies/step_complete`
- Prose mentions of "record_finding then advance" / "call record_finding for each finding" → "call step_complete with findings + goto"
- `expected_findings:` lists stay; they describe what to capture, not which tool to call.

Make these edits manually rather than `sed` — context matters (sometimes a prose line will need rewording, not just renaming).

- [ ] **Step 2: Write the playbook scan test**

Add to `mcp/internal/strategies/playbook_test.go`:

```go
func TestSystemPlaybooks_NoDeletedToolReferences(t *testing.T) {
	deleted := []string{
		"c1-strategies/record_finding",
		"c1-strategies/advance",
	}
	dir := "../../../investigate/system" // resolve relative to this package
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil { t.Fatal(err) }
	for _, p := range matches {
		body, err := os.ReadFile(p)
		if err != nil { t.Fatal(err) }
		for _, needle := range deleted {
			if bytes.Contains(body, []byte(needle)) {
				t.Errorf("%s: references deleted tool %q", p, needle)
			}
		}
	}
}
```

(Adjust the relative path if the strategies package layout is different; use a `go list -f` lookup at test time if a hardcoded path is brittle.)

Run: `go test ./mcp/internal/strategies/ -run TestSystemPlaybooks_NoDeletedToolReferences -count=1`
Expected: PASS if Step 1's substitutions covered everything; FAIL pointing at the missed file otherwise.

- [ ] **Step 3: Delete the old handlers and specs**

In `mcp/internal/strategies/server.go`:
- Delete the two `mcp.AddTool(...)` calls for `record_finding` and `advance` in `register()`.
- Delete the `recordFinding` function, the `recordFindingIn` and `recordFindingOut` types.
- Delete the `advance` function, the `advanceIn` and `advanceOut` types.
- Keep `applyAdvance` and `recordAndAdvance` — those still serve `step_complete`.

In `mcp/internal/strategies/specs.go`:
- Delete the two ToolSpec entries for `record_finding` and `advance`.

- [ ] **Step 4: Update frontend catalog blurb**

In `investigate/frontend/lib/mcps.ts:31`, change:

```ts
"Investigation walker — list_playbooks, walk_playbook, advance, record_finding, summarize."
```

To:

```ts
"Investigation walker — list_playbooks, walk_playbook, step_complete, summarize."
```

- [ ] **Step 5: Update operator-facing docs**

Replace `record_finding` / `advance` references in:
- `investigate/frontend/public/docs/playbooks.md` (sequence diagram + body text — replace the two-step Mermaid edges with one `step_complete` edge; update the "auditable" bullet)
- `investigate/frontend/public/docs/overview.md`
- `investigate/frontend/public/docs/investigations.md`

The substitutions follow the same pattern: a call to step_complete now does what the two old calls did. Where prose explains the loop ("make calls → record findings → advance"), rewrite as ("make calls → step_complete with findings + goto").

- [ ] **Step 6: Update the camunda profile prompt**

In `investigate/internal/profile/profiles/camunda/prompts/strategies.md`:
- Replace `mcp__c1-strategies__record_finding` with `mcp__c1-strategies__step_complete`
- Replace prose like "call record_finding then advance" with the new pattern
- Add one sentence noting that `step_complete` accepts a `findings` array so the agent collects evidence first, then transitions

- [ ] **Step 7: Run all tests**

Run: `go test ./mcp/... -race -count=1`
Expected: PASS.

Run: `go test ./mcp/... -run Wire -count=1`
Expected: PASS — catalog ↔ handler symmetry holds after deletions.

Run: `go test ./investigate/... -race -count=1`
Expected: PASS.

Run: `cd investigate/frontend && npm run typecheck && npm test -- --run`
Expected: PASS — no frontend regressions (only static text changed).

Run: `cd investigate/frontend && npm run build`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add mcp/internal/strategies/server.go mcp/internal/strategies/specs.go mcp/internal/strategies/playbook_test.go investigate/system/*.yaml investigate/frontend/lib/mcps.ts investigate/frontend/public/docs/playbooks.md investigate/frontend/public/docs/overview.md investigate/frontend/public/docs/investigations.md investigate/internal/profile/profiles/camunda/prompts/strategies.md
git commit -m "refactor(investigate/system): migrate playbooks to step_complete + delete old tools

System playbooks now call c1-strategies/step_complete instead of the
record_finding + advance pair. The old handlers, schemas, and
spec entries are gone; the catalog blurb, frontend docs, and the
camunda profile prompt all reference step_complete. New playbook test
scans every loaded YAML for the deleted tool names so a future
addition can't regress the migration. User playbooks under <userDir>
are not migrated — operators handle those (or upstream maintainers
update the upstream playbooks repo)."
```

---

## Self-Review Notes

- **Spec coverage:** Task 1 = commit 1a; Task 2 = commit 1b (streaming pair + boot sweep); Task 3 = commit 1c; Task 4 = commit 2; Task 5 = commit 3A; Task 6 = commit 3B. All six spec commits accounted for.
- **Archived-session compatibility:** Tasks 1, 2, and 4 are forward-only (handler-time changes; old events.jsonl entries replay unchanged). Tasks 5/6 don't affect render — the ActivityPanel reads tool_use envelopes generically.
- **`specs.go` updates:** Each tool-surface task includes the matching `mcp/internal/<server>/specs.go` change.
- **Build-green between commits:** Task 5 is additive; Task 6 deletes the old tools in the same commit as the migration so there's never a broken intermediate state.

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-14-tool-result-token-budget.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
