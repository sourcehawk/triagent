package k8s

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeToolKit is the test handle returned by newFakeToolKit. It embeds the
// ToolKit and holds the per-pod log body map so primePodLog can reach it.
type fakeToolKit struct {
	*ToolKit
	mu      sync.Mutex
	logData map[string]string // key: "namespace/pod"
}

func (f *fakeToolKit) getLog(ns, pod string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logData[ns+"/"+pod]
}

// newFakeToolKit creates a ToolKit backed by a fake clientset, with log bodies
// controlled by primePodLog. The returned *fakeToolKit embeds *ToolKit, so all
// getLogs / listEvents etc. methods are directly callable on it.
func newFakeToolKit(t *testing.T) (*fakeToolKit, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	ftk := &fakeToolKit{}
	ftk.ToolKit = &ToolKit{}
	ftk.snapshot.Store(&clientSnapshot{typed: cs})
	ftk.openLogStream = func(_ context.Context, _ *clientSnapshot, ns, pod string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(ftk.getLog(ns, pod))), nil
	}
	return ftk, cs
}

// primePodLog seeds the fake log body returned for a specific pod.
// The first argument is the *fakeToolKit returned by newFakeToolKit (not the
// *fake.Clientset — we need the store, which lives on the fakeToolKit).
func primePodLog(ftk *fakeToolKit, ns, pod, body string) {
	ftk.mu.Lock()
	defer ftk.mu.Unlock()
	if ftk.logData == nil {
		ftk.logData = make(map[string]string)
	}
	ftk.logData[ns+"/"+pod] = body
}


func TestFilterLogs_EmptyGrepKeepsAll(t *testing.T) {
	t.Parallel()
	body, err := filterLogs(strings.NewReader("a\nb\nc\n"), "")
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc\n", body)
}

func TestFilterLogs_FiltersBySubstring(t *testing.T) {
	t.Parallel()
	body, err := filterLogs(strings.NewReader("info x\nERROR boom\ninfo y\nERROR kaput\n"), "ERROR")
	require.NoError(t, err)
	assert.Equal(t, "ERROR boom\nERROR kaput\n", body)
}

func TestListEvents_FiltersAndSorts(t *testing.T) {
	t.Parallel()

	old := time.Now().Add(-1 * time.Hour)
	mid := time.Now().Add(-30 * time.Minute)
	now := time.Now()

	events := []runtime.Object{
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "app"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "zeebe-0"},
			Type:           "Warning",
			Reason:         "BackOff",
			Message:        "start failed",
			LastTimestamp:  metav1.NewTime(old),
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e2", Namespace: "app"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "zeebe-1"},
			Type:           "Normal",
			Reason:         "Started",
			Message:        "container started",
			LastTimestamp:  metav1.NewTime(mid),
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e3", Namespace: "app"},
			InvolvedObject: corev1.ObjectReference{Kind: "Deployment", Name: "gateway"},
			Type:           "Warning",
			Reason:         "Scaled",
			Message:        "",
			LastTimestamp:  metav1.NewTime(now),
		},
	}

	cs := fake.NewSimpleClientset(events...)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})

	// Filter by kind=Pod, type=Warning should match only e1.
	_, out, err := kit.listEvents(context.Background(), nil, ListEventsInput{
		Namespace:          "app",
		InvolvedObjectKind: "Pod",
		Type:               "Warning",
	})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	require.Equal(t, "BackOff", out.Items[0].Reason)

	// No filters: all three, newest first.
	_, out, err = kit.listEvents(context.Background(), nil, ListEventsInput{Namespace: "app"})
	require.NoError(t, err)
	require.Len(t, out.Items, 3)
	assert.Equal(t, "Scaled", out.Items[0].Reason, "not sorted newest-first")
	assert.Equal(t, "BackOff", out.Items[2].Reason, "not sorted newest-first")
}

func TestListEvents_InvolvedObjectNameFilter(t *testing.T) {
	t.Parallel()

	events := []runtime.Object{
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "app"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "zeebe-0"},
			Reason:         "Started",
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e2", Namespace: "app"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "zeebe-1"},
			Reason:         "Started",
		},
	}
	cs := fake.NewSimpleClientset(events...)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})

	_, out, err := kit.listEvents(context.Background(), nil, ListEventsInput{
		Namespace:          "app",
		InvolvedObjectName: "zeebe-1",
	})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, "zeebe-1", out.Items[0].InvolvedName)
}

func TestGetLogs_RequiresNamespace(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: fake.NewSimpleClientset()})
	res, _, err := kit.getLogs(context.Background(), nil, GetLogsInput{Pod: "zeebe-0"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, content(res), "requires `namespace`")
}

func TestListEvents_RequiresNamespace(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: fake.NewSimpleClientset()})
	r, _, err := kit.listEvents(context.Background(), nil, ListEventsInput{})
	require.NoError(t, err)
	require.True(t, r.IsError)
	assert.Contains(t, content(r), "requires `namespace`")
}

func TestGetLogs_RequiresASelector(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: fake.NewSimpleClientset()})
	res, _, err := kit.getLogs(context.Background(), nil, GetLogsInput{Namespace: "app"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, content(res), "one of pod")
}

func TestGetLogs_RejectsMultipleSelectors(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: fake.NewSimpleClientset()})
	res, _, err := kit.getLogs(context.Background(), nil, GetLogsInput{Namespace: "app", Pod: "p", LabelSelector: "a=b"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, content(res), "mutually exclusive")
}

func TestPrefixLines(t *testing.T) {
	t.Parallel()
	got := prefixLines("a\nb\n", "pod-x")
	assert.Equal(t, "[pod-x] a\n[pod-x] b\n", got)
	assert.Equal(t, "", prefixLines("", "pod-x"), "empty body should stay empty")
}

func TestWorkloadSelector_Deployment(t *testing.T) {
	t.Parallel()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "example-service", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "example-service"}},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	snap := &clientSnapshot{typed: cs}
	kit := &ToolKit{}
	sel, err := kit.workloadSelector(context.Background(), snap, "app", "Deployment", "example-service")
	require.NoError(t, err)
	assert.Equal(t, "app=example-service", sel)
}

func TestWorkloadSelector_UnsupportedKind(t *testing.T) {
	t.Parallel()
	snap := &clientSnapshot{typed: fake.NewSimpleClientset()}
	kit := &ToolKit{}
	_, err := kit.workloadSelector(context.Background(), snap, "app", "Job", "x")
	require.ErrorContains(t, err, "not supported")
}

func TestPodsForWorkload_InvalidRef(t *testing.T) {
	t.Parallel()
	snap := &clientSnapshot{typed: fake.NewSimpleClientset()}
	kit := &ToolKit{}
	_, err := kit.podsForWorkload(context.Background(), snap, "app", "no-slash")
	require.ErrorContains(t, err, "Kind/name")
}

func TestGetLogs_MultiPodByLabelSelectorPrefixesEachLine(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "zeebe-0", Namespace: "app", Labels: map[string]string{"app": "zeebe"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "zeebe"}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "zeebe-1", Namespace: "app", Labels: map[string]string{"app": "zeebe"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "zeebe"}}},
		},
	)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})

	res, _, err := kit.getLogs(context.Background(), nil, GetLogsInput{Namespace: "app", LabelSelector: "app=zeebe"})
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error: %q", content(res))
	out := content(res)
	// The fake client returns a canned "fake logs" body; what we assert is
	// that it was prefixed with each pod name — proving multi-pod fanout
	// + prefixing both ran.
	assert.Contains(t, out, "[zeebe-0]", "multi-pod prefixing missing in output")
	assert.Contains(t, out, "[zeebe-1]", "multi-pod prefixing missing in output")
	assert.Contains(t, out, "pods=2", "header should report pod count")
}

func TestGetLogs_DefaultByteCap(t *testing.T) {
	t.Parallel()
	// Each line is 4096 bytes; 200 default tailLines × 4 KB = 800 KB raw.
	// With the 32 KB default byte cap we expect roughly 8 lines + marker.
	line := strings.Repeat("X", 4095) + "\n"
	body := strings.Repeat(line, 200)
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	out := content(res)
	if len(out) > 64*1024 { // generous upper bound: 32 KB body + small headers
		t.Fatalf("output exceeded byte cap: %d bytes", len(out))
	}
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation marker, got:\n%s", out[:min(500, len(out))])
	}
	trimmed := strings.TrimRight(out, "\n")
	if !strings.HasSuffix(trimmed, "X") {
		t.Fatalf("body did not end on a complete line")
	}
}

func TestGetLogs_RespectsExplicitMaxBytes(t *testing.T) {
	t.Parallel()
	// Body is 80 KB (1 KB lines × 80). We pass MaxBytes=64 KB.
	// The default 32 KB cap would also truncate, so we assert that the output
	// body is > 32 KB — proving the explicit 64 KB value was honoured, not
	// the default.
	line := strings.Repeat("Y", 1023) + "\n"
	body := strings.Repeat(line, 80)
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{
		Namespace: "ns1", Pod: "pod-a", MaxBytes: 64 * 1024,
	})
	out := content(res)
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation at the 64 KB cap")
	}
	if len(out) <= 32*1024 {
		t.Fatalf("output %d bytes is not > 32 KB — explicit 64 KB cap was not applied", len(out))
	}
}

func TestGetLogs_NoTruncationUnderCap(t *testing.T) {
	t.Parallel()
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", "small log\nbody\n")
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	out := content(res)
	if strings.Contains(out, "# truncated:") {
		t.Fatalf("did not expect truncation marker, got:\n%s", out)
	}
}

func TestGetLogs_MaxBytesHardCap(t *testing.T) {
	t.Parallel()
	// Body is 400 KB raw (4 KB lines × 100). Caller passes a 1 GiB MaxBytes.
	// The hard 256 KB cap must clamp the output — not the 400 KB raw body,
	// and not the caller's absurdly large value.
	line := strings.Repeat("H", 4095) + "\n" // 4 KB per line
	body := strings.Repeat(line, 100)         // 400 KB total
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{
		Namespace: "ns1", Pod: "pod-a", MaxBytes: 1 << 30,
	})
	out := content(res)
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation marker (hard cap should have triggered)")
	}
	// Output must be substantially less than the 400 KB raw body.
	// 256 KB hard cap + small header overhead = well under 300 KB.
	if len(out) > 300*1024 {
		t.Fatalf("output %d bytes exceeds 300 KB — hard cap not applied", len(out))
	}
}

func TestGetLogs_TruncatedEndsAtLineBoundary(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("Z", 1023) + "\n"
	body := strings.Repeat(line, 100)
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	trimmed := strings.TrimRight(content(res), "\n")
	if !strings.HasSuffix(trimmed, "Z") {
		t.Fatalf("body did not end at a line boundary")
	}
}

func TestGetLogs_TruncatesSingleHugeLine(t *testing.T) {
	t.Parallel()
	// One huge line with no newline within the default 32 KB cap.
	// This exercises the cutAt < 0 branch in truncateToBytes, which keeps
	// the first maxBytes of the oversized line rather than emitting an empty body.
	body := strings.Repeat("Q", 64*1024)
	tk, _ := newFakeToolKit(t)
	primePodLog(tk, "ns1", "pod-a", body)
	res, _, _ := tk.getLogs(t.Context(), nil, GetLogsInput{Namespace: "ns1", Pod: "pod-a"})
	out := content(res)
	if !strings.Contains(out, "# truncated:") {
		t.Fatalf("expected truncation marker for single huge line")
	}
	if len(out) > 64*1024 {
		t.Fatalf("output exceeded reasonable bound: %d bytes", len(out))
	}
}
