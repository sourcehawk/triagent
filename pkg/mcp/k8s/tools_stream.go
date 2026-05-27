package k8s

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
)

// ---------------------------------------------------------------------------
// Keyword matcher
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Stream-file writer
// ---------------------------------------------------------------------------

const (
	defaultStreamTimeoutSeconds = 60
	maxStreamTimeoutSeconds     = 300
	defaultStreamGraceSeconds   = 5
	maxStreamGraceSeconds       = 30
)

// streamDiskCapBytes is the per-stream file size ceiling. var (not const)
// so tests can shrink it; production callers never mutate it.
var streamDiskCapBytes int64 = 16 * 1024 * 1024

var errDiskCapHit = errors.New("stream disk cap hit")

// streamWriter is the bounded, line-prefixed appender for one stream file.
// All callers serialise on its mutex so concurrent pod readers never
// interleave a partial line.
type streamWriter struct {
	mu      sync.Mutex
	f       *os.File
	written int64
	capped  bool
}

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
		// stays at or below the ceiling.
		w.capped = true
		return errDiskCapHit
	}
	n, err := w.f.WriteString(formatted)
	w.written += int64(n)
	return err
}

// ---------------------------------------------------------------------------
// stream_log_until types and implementation
// ---------------------------------------------------------------------------

// StreamLogUntilInput selects log sources and controls the streaming window.
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

// KeywordGroupHit reports hits for one AND-group.
type KeywordGroupHit struct {
	Group        []string `json:"group"`
	Hits         int      `json:"hits"`
	FirstLineNum int      `json:"first_line,omitempty"`
}

// StreamLogUntilOutput is the structured return from stream_log_until.
type StreamLogUntilOutput struct {
	StreamID       string            `json:"stream_id"`
	LogLinesTotal  int               `json:"log_lines_total"`
	KeywordHits    []KeywordGroupHit `json:"keyword_hits,omitempty"`
	TimedOut       bool              `json:"timed_out"`
	TruncatedAtCap bool              `json:"truncated_at_cap"`
}

// streamState tracks shared mutable state across concurrent pod goroutines.
type streamState struct {
	mu         sync.Mutex
	matcher    *keywordMatcher
	hitCounts  []int
	hitLines   []int
	linesCount int
	firstHit   bool
	grace      time.Duration
	cancel     context.CancelFunc
	graceOnce  sync.Once
}

func (s *streamState) recordLine(lineNum int, line string) {
	s.mu.Lock()
	s.linesCount++
	idx := s.matcher.matchIndex(line)
	if idx >= 0 {
		s.hitCounts[idx]++
		if s.hitLines[idx] == 0 {
			s.hitLines[idx] = lineNum
		}
		if !s.firstHit {
			s.firstHit = true
			s.graceOnce.Do(func() {
				time.AfterFunc(s.grace, s.cancel)
			})
		}
	}
	s.mu.Unlock()
}

func (s *streamState) linesTotal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linesCount
}

func (s *streamState) hitFired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstHit
}

func (s *streamState) snapshot(groups [][]string) []KeywordGroupHit {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := make([]KeywordGroupHit, 0, len(groups))
	for i, g := range groups {
		if i >= len(s.hitCounts) {
			break
		}
		hits = append(hits, KeywordGroupHit{
			Group:        g,
			Hits:         s.hitCounts[i],
			FirstLineNum: s.hitLines[i],
		})
	}
	return hits
}

func clampSeconds(v, defaultVal, maxVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func newStreamID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (t *ToolKit) openStreamFile() (string, *streamWriter, error) {
	streamsDir := filepath.Join(t.sessionDir, "streams")
	if err := os.MkdirAll(streamsDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create streams dir: %w", err)
	}
	id, err := newStreamID()
	if err != nil {
		return "", nil, fmt.Errorf("generate stream id: %w", err)
	}
	path := filepath.Join(streamsDir, id+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("open stream file: %w", err)
	}
	return id, &streamWriter{f: f}, nil
}

// tailFollow opens the pod's log stream and writes lines to the streamWriter
// until the context is done (timeout or keyword-triggered cancel) or EOF.
// Uses the openLogStream seam for tests.
func tailFollow(ctx context.Context, t *ToolKit, snap *clientSnapshot, ns, pod, container string, w *streamWriter, prefix string, state *streamState) {
	kopts := &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
	}

	var rc io.ReadCloser
	if t.openLogStream != nil {
		var err error
		rc, err = t.openLogStream(ctx, snap, ns, pod, kopts)
		if err != nil {
			_ = w.writeLine(prefix, "!! open log stream: "+err.Error())
			return
		}
	} else {
		var err error
		req := snap.typed.CoreV1().Pods(ns).GetLogs(pod, kopts)
		rc, err = req.Stream(ctx)
		if err != nil {
			_ = w.writeLine(prefix, "!! open log stream: "+err.Error())
			return
		}
	}
	if rc == nil {
		return
	}
	defer func() { _ = rc.Close() }()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNum := 0
	for {
		// Check context before each scan attempt.
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !scanner.Scan() {
			// EOF or scan error — emit a diagnostic if it wasn't a clean EOF.
			if err := scanner.Err(); err != nil {
				_ = w.writeLine(prefix, "!! log stream error: "+err.Error())
			}
			return
		}
		line := scanner.Text()
		lineNum++
		state.recordLine(lineNum, line)
		if err := w.writeLine(prefix, line); err != nil {
			if errors.Is(err, errDiskCapHit) {
				return
			}
			// Other write error: stop streaming this pod.
			return
		}
	}
}

// streamLogUntil tails log streams into a session-scoped file, stopping on
// keyword hit + grace period or timeout. Returns a stream_id the caller can
// pass to search_log_stream.
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
		Namespace:     in.Namespace,
		Pod:           in.Pod,
		LabelSelector: in.LabelSelector,
		Workload:      in.Workload,
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
	defer func() { _ = w.f.Close() }()

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	state := &streamState{
		matcher:   matcher,
		hitCounts: make([]int, len(matcher.groups)),
		hitLines:  make([]int, len(matcher.groups)),
		grace:     time.Duration(grace) * time.Second,
		cancel:    cancel,
	}

	var wg sync.WaitGroup
	for _, p := range pods {
		wg.Add(1)
		prefix := ""
		if len(pods) > 1 {
			prefix = p
		}
		go func(pod string) {
			defer wg.Done()
			tailFollow(timeoutCtx, t, snap, in.Namespace, pod, in.Container, w, prefix, state)
		}(p)
	}
	wg.Wait()

	timedOut := errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && !state.hitFired()

	return nil, StreamLogUntilOutput{
		StreamID:       streamID,
		LogLinesTotal:  state.linesTotal(),
		KeywordHits:    state.snapshot(matcher.groups),
		TimedOut:       timedOut,
		TruncatedAtCap: w.capped,
	}, nil
}

// ---------------------------------------------------------------------------
// search_log_stream types and implementation
// ---------------------------------------------------------------------------

// KVFilter matches lines that contain `key=value` or `key: value`.
type KVFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SearchLogStreamInput selects a stream file and filters its contents.
type SearchLogStreamInput struct {
	StreamID  string     `json:"stream_id" jsonschema:"the id returned by stream_log_until"`
	Grep      string     `json:"grep,omitempty" jsonschema:"case-sensitive substring (mirrors get_logs.grep)"`
	KVFilters []KVFilter `json:"kvFilters,omitempty" jsonschema:"AND-joined substring filters of the form key=value or key: value found in each line"`
	TailLines int        `json:"tailLines,omitempty"`
	MaxBytes  int        `json:"maxBytes,omitempty" jsonschema:"per-response byte cap (default 32768, hard cap 262144). Truncated body ends at a line boundary; same marker as get_logs."`
}

// streamIDRe validates that a stream_id matches what newStreamID produces: 16
// lowercase hex chars. Rejecting anything else prevents path-traversal via
// crafted stream_id values.
var streamIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// searchLogStream reads a previously-captured stream file, filters it, and
// returns a byte-capped response. Uses the same truncateToBytes helper as
// get_logs so the truncation marker is consistent.
func (t *ToolKit) searchLogStream(ctx context.Context, _ *mcp.CallToolRequest, in SearchLogStreamInput) (*mcp.CallToolResult, any, error) {
	if t.sessionDir == "" {
		return errorResult("search_log_stream requires a session dir"), nil, nil
	}
	if !streamIDRe.MatchString(in.StreamID) {
		return errorResult("invalid stream_id format"), nil, nil
	}
	path := filepath.Join(t.sessionDir, "streams", in.StreamID+".log")
	f, err := os.Open(path)
	if err != nil {
		return errorResult(fmt.Sprintf("stream %q not found", in.StreamID)), nil, nil
	}
	defer func() { _ = f.Close() }()

	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultLogMaxBytes
	}
	if maxBytes > maxLogMaxBytes {
		maxBytes = maxLogMaxBytes
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var keepRing []string
	tail := in.TailLines
	if tail > maxLogTailLines {
		tail = maxLogTailLines
	}
scan:
	for scanner.Scan() {
		// Check context cancellation between lines.
		select {
		case <-ctx.Done():
			break scan
		default:
		}
		line := scanner.Text()
		if in.Grep != "" && !strings.Contains(line, in.Grep) {
			continue
		}
		if !matchesKV(line, in.KVFilters) {
			continue
		}
		if tail > 0 {
			keepRing = append(keepRing, line)
			if len(keepRing) > tail {
				keepRing = keepRing[len(keepRing)-tail:]
			}
			continue
		}
		keepRing = append(keepRing, line)
	}
	body := strings.Join(keepRing, "\n")
	if len(keepRing) > 0 {
		body += "\n"
	}
	cut, marker := truncateToBytes(body, maxBytes)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: marker + cut}}}, nil, nil
}

// matchesKV returns true when the line satisfies all KV filters. A filter
// matches if the line contains `key=value` or `key: value` (both key and
// value are checked as substrings, so prefix-matching works naturally).
func matchesKV(line string, kvs []KVFilter) bool {
	for _, kv := range kvs {
		i := strings.Index(line, kv.Key+"=")
		if i < 0 {
			i = strings.Index(line, kv.Key+": ")
		}
		if i < 0 {
			return false
		}
		if kv.Value != "" && !strings.Contains(line[i:], kv.Value) {
			return false
		}
	}
	return true
}
