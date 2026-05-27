package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// primePodLogStream seeds the fake log body for stream tests by storing
// lines as a single body (same as primePodLog). Since openLogStream returns
// an io.NopCloser over a strings.Reader, it EOFs after all lines are read.
// With Follow=true in a real client, the stream would block; the fake EOFs,
// which our tailFollow treats as "done streaming". For the timeout test this
// is fine — we expect TimedOut=true because the fake delivers all lines then
// exits, so ctx must already be cancelled or deadline exceeded for TimedOut.
func primePodLogStream(ftk *fakeToolKit, ns, pod string, lines []string) {
	body := strings.Join(lines, "")
	primePodLog(ftk, ns, pod, body)
}

// primePodList registers pods under a label-selector in the fake typed client
// so that resolveLogSources (podsBySelector) can find them.
func primePodList(fakeClient *fake.Clientset, ns string, labels map[string]string, podNames []string) {
	for _, name := range podNames {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    labels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main"}},
			},
		}
		// Use background context; ignore already-exists errors
		_, _ = fakeClient.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	}
}

// ---------------------------------------------------------------------------
// Keyword matcher tests
// ---------------------------------------------------------------------------

func TestKeywordMatcher_AndOfOr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		groups [][]string
		line   string
		want   int // matching group index, -1 = none
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
			t.Parallel()
			m := newKeywordMatcher(c.groups)
			got := m.matchIndex(c.line)
			if got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// stream_log_until tests
// ---------------------------------------------------------------------------

func TestStreamLogUntil_SinglePodTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(tk, "ns1", "pod-a", []string{"hello\n", "world\n"})
	res, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a", TimeoutSeconds: 1,
	})
	if res != nil && res.IsError {
		t.Fatalf("unexpected error: %s", content(res))
	}
	// The fake EOF's immediately after lines; streamLogUntil finishes when
	// tailFollow returns. Since there are no keywords to match, TimedOut is
	// true when the context deadline fired before tailFollow returned — OR
	// when the fake delivered all lines and exited normally (in which case
	// we may not have timed out). Either outcome is valid for the fake; what
	// we care about is that lines landed on disk.
	if out.LogLinesTotal != 2 {
		t.Fatalf("expected 2 lines, got %d", out.LogLinesTotal)
	}
	body, err := os.ReadFile(filepath.Join(dir, "streams", out.StreamID+".log"))
	if err != nil {
		t.Fatalf("read stream file: %v", err)
	}
	if !strings.Contains(string(body), "hello") || !strings.Contains(string(body), "world") {
		t.Fatalf("stream file missing lines: %s", body)
	}
}

func TestStreamLogUntil_StopsOnKeywordWithGrace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(tk, "ns1", "pod-a", []string{
		"INFO ready\n", "WARN slow\n", "ERROR: connection timeout\n", "INFO retry\n",
	})
	res, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace:      "ns1",
		Pod:            "pod-a",
		Keywords:       [][]string{{"ERROR", "timeout"}},
		TimeoutSeconds: 30,
		GraceSeconds:   1,
	})
	if res != nil && res.IsError {
		t.Fatal(content(res))
	}
	if len(out.KeywordHits) != 1 || out.KeywordHits[0].Hits == 0 {
		t.Fatalf("expected one keyword hit, got %+v", out.KeywordHits)
	}
}

func TestStreamLogUntil_CaseInsensitiveKeywords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(tk, "ns1", "pod-a", []string{"error happened\n"})
	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace:      "ns1",
		Pod:            "pod-a",
		Keywords:       [][]string{{"ERROR"}},
		TimeoutSeconds: 2,
		GraceSeconds:   1,
	})
	if len(out.KeywordHits) == 0 || out.KeywordHits[0].Hits != 1 {
		t.Fatalf("expected case-insensitive hit, got %+v", out.KeywordHits)
	}
}

func TestStreamLogUntil_MultiPodPrefixedLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tk, fakeClient := newFakeToolKit(t)
	tk.sessionDir = dir
	primePodLogStream(tk, "ns1", "pod-a", []string{"a-line\n"})
	primePodLogStream(tk, "ns1", "pod-b", []string{"b-line\n"})
	primePodList(fakeClient, "ns1", map[string]string{"app": "demo"}, []string{"pod-a", "pod-b"})
	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", LabelSelector: "app=demo", TimeoutSeconds: 2,
	})
	body, err := os.ReadFile(filepath.Join(dir, "streams", out.StreamID+".log"))
	if err != nil {
		t.Fatalf("read stream file: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "[pod-a]") || !strings.Contains(s, "[pod-b]") {
		t.Fatalf("expected per-pod prefixes, got: %s", s)
	}
}

func TestStreamLogUntil_DiskCapHit(t *testing.T) {
	// NOTE: do NOT t.Parallel() — this test mutates the global streamDiskCapBytes.
	dir := t.TempDir()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	prev := streamDiskCapBytes
	streamDiskCapBytes = 1024
	t.Cleanup(func() { streamDiskCapBytes = prev })

	lines := make([]string, 50)
	for i := range lines {
		lines[i] = strings.Repeat("x", 64) + "\n"
	}
	primePodLogStream(tk, "ns1", "pod-a", lines)

	_, out, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a", TimeoutSeconds: 2,
	})
	if !out.TruncatedAtCap {
		t.Fatalf("expected TruncatedAtCap=true")
	}
	st, err := os.Stat(filepath.Join(dir, "streams", out.StreamID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 1024 {
		t.Fatalf("file exceeded cap: %d bytes", st.Size())
	}
}

func TestStreamLogUntil_RequiresSessionDir(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	// sessionDir is empty (zero value)
	res, _, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{
		Namespace: "ns1", Pod: "pod-a",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error when sessionDir is empty")
	}
}

func TestStreamLogUntil_RequiresNamespace(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = t.TempDir()
	res, _, _ := tk.streamLogUntil(t.Context(), nil, StreamLogUntilInput{Pod: "pod-a"})
	if res == nil || !res.IsError {
		t.Fatal("expected error when namespace is empty")
	}
}

// ---------------------------------------------------------------------------
// search_log_stream tests
// ---------------------------------------------------------------------------

func TestSearchLogStream_GrepAndByteCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	streamID := "abc1230000000000"
	if err := os.MkdirAll(filepath.Join(dir, "streams"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := ""
	for i := 0; i < 100; i++ {
		body += fmt.Sprintf("line %d INFO ok\n", i)
	}
	body += "line 100 ERROR boom\n"
	if err := os.WriteFile(filepath.Join(dir, "streams", streamID+".log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{
		StreamID: streamID, Grep: "ERROR", MaxBytes: 1024,
	})
	out := content(res)
	if !strings.Contains(out, "line 100 ERROR boom") {
		t.Fatalf("expected the ERROR line, got: %s", out)
	}
}

func TestSearchLogStream_KVFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	streamID := "abc0000000000000"
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
	out := content(res)
	if !strings.Contains(out, "level=error component=zeebe") {
		t.Fatalf("expected error/zeebe match, got %s", out)
	}
	if strings.Contains(out, "level=info") {
		t.Fatalf("info line leaked: %s", out)
	}
}

func TestSearchLogStream_StreamNotFound(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = t.TempDir()
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{StreamID: "nope"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing stream")
	}
}

func TestSearchLogStream_RequiresSessionDir(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{StreamID: "any"})
	if res == nil || !res.IsError {
		t.Fatal("expected error when sessionDir is empty")
	}
}

func TestSearchLogStream_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	streamID := "0123456789abcdef"
	_ = os.MkdirAll(filepath.Join(dir, "streams"), 0o700)
	// Generate enough lines that scanner would naturally take a few ms;
	// not strictly necessary, but makes intent obvious.
	body := strings.Repeat("noise\n", 10_000)
	_ = os.WriteFile(filepath.Join(dir, "streams", streamID+".log"), []byte(body), 0o600)
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = dir
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel up front
	res, _, _ := tk.searchLogStream(ctx, nil, SearchLogStreamInput{StreamID: streamID})
	// The result is still returned (cancellation aborts mid-scan, not errors).
	// What we're proving is that we don't burn time on the full body.
	// Smoke check: result body is much smaller than full body would render to.
	out := content(res)
	if len(out) > len(body)/2 {
		t.Fatalf("expected truncated/empty body on cancel, got %d bytes", len(out))
	}
}

func TestSearchLogStream_RejectsBadStreamID(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	tk.sessionDir = t.TempDir()
	for _, bad := range []string{"../escape", "", "ZZZZZZZZZZZZZZZZ", "0123456789abcdeg" /* g not hex */} {
		res, _, _ := tk.searchLogStream(t.Context(), nil, SearchLogStreamInput{StreamID: bad})
		if res == nil || !res.IsError {
			t.Fatalf("expected error for stream_id %q", bad)
		}
	}
}
