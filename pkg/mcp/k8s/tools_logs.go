package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultLogTailLines = 200
	maxLogTailLines     = 2000
	maxLinesAfterGrep   = 2000
	// When matching multiple pods, cap at this many to keep the response
	// size bounded. Callers can raise explicitly via MaxPods; this is the
	// hard ceiling.
	defaultMaxPods = 5
	hardMaxPods    = 20
	// Parallel fetch pool size; picked to keep the API server happy while
	// still being clearly "concurrent".
	logFetchConcurrency = 4
	// Byte cap on the log body per pod. defaultLogMaxBytes is applied when
	// GetLogsInput.MaxBytes is zero. maxLogMaxBytes is the hard ceiling for
	// explicit caller overrides.
	defaultLogMaxBytes = 32 * 1024
	maxLogMaxBytes     = 256 * 1024
)

// GetLogsInput selects a log source in one of three ways (exactly one must
// be set): a single pod, a label selector, or a workload reference.
type GetLogsInput struct {
	Namespace     string `json:"namespace" jsonschema:"Required. Pass the workload namespace from the Environment block's cluster-resource-namespace, or call list_namespaces to discover one."`
	Pod           string `json:"pod,omitempty" jsonschema:"Explicit pod name (e.g. 'zeebe-0'). Use for precise single-pod investigation."`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"Kubernetes label selector — KEY=VALUE pairs comma-separated, e.g. 'app=zeebe' or 'app.kubernetes.io/component=opentelemetry-collector,app.kubernetes.io/instance=external-monitoring'. The keys MUST come from a label that actually exists on the pod: read the pod's metadata.labels first (list_resources kind=Pod, get_resource kind=Pod, or get_resource on the parent Deployment/StatefulSet to see spec.selector). Don't invent labels — selectors that match nothing return 'no pods matched' and burn a round-trip. Common workload labels: Pairs are AND-joined."`
	Workload      string `json:"workload,omitempty" jsonschema:"Workload reference in Kind/name form: 'Deployment/my-service', 'StatefulSet/my-db'. Resolves to the workload's selector — preferred over labelSelector when you already know the workload, since this avoids guessing which labels the operator uses. Supported kinds: Deployment, StatefulSet, DaemonSet, ReplicaSet."`
	Container     string `json:"container,omitempty" jsonschema:"Container name; defaults to the pod's first container."`
	TailLines     int    `json:"tailLines,omitempty" jsonschema:"Lines from the end of each pod's log (default 200, cap 2000). Applied per pod."`
	SinceSeconds  int    `json:"sinceSeconds,omitempty" jsonschema:"Only return logs newer than this many seconds."`
	Previous      bool   `json:"previous,omitempty" jsonschema:"Return logs from the previous terminated instance of the container."`
	Grep          string `json:"grep,omitempty" jsonschema:"Case-sensitive substring filter applied server-side before returning lines."`
	MaxPods       int    `json:"maxPods,omitempty" jsonschema:"Cap on matching pods when using labelSelector/workload (default 5, cap 20)."`
	MaxBytes      int    `json:"maxBytes,omitempty" jsonschema:"Soft byte cap per pod (default 32768, hard cap 262144). Truncates at the last line boundary; emits a # truncated: marker when the body is cut. Tighten grep/sinceSeconds, or escalate to stream_log_until for keyword search."`
}

// getLogs is the unified logs tool. Dispatches based on which selector
// field is set; multi-pod results are prefixed with `[pod]`.
// Returns (result, any, error) rather than a typed Out: with any+nil the
// SDK skips StructuredContent entirely, leaving the raw log body in
// Content as the sole payload — a typed empty-struct output would set
// StructuredContent="{}" and some Claude clients prefer that over Content.
func (t *ToolKit) getLogs(ctx context.Context, _ *mcp.CallToolRequest, in GetLogsInput) (*mcp.CallToolResult, any, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, nil, nil
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return errorResult("get_logs requires `namespace`. Pass cluster-resource-namespace from the Environment block, or call list_namespaces to find candidates."), nil, nil
	}
	pods, err := t.resolveLogSources(ctx, snap, in)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if len(pods) == 0 {
		return errorResult("no pods matched the selector"), nil, nil
	}

	maxPods := in.MaxPods
	if maxPods <= 0 {
		maxPods = defaultMaxPods
	}
	if maxPods > hardMaxPods {
		maxPods = hardMaxPods
	}
	truncatedPodList := len(pods) > maxPods
	if truncatedPodList {
		sort.Strings(pods)
		pods = pods[:maxPods]
	}

	tail := in.TailLines
	if tail <= 0 {
		tail = defaultLogTailLines
	}
	if tail > maxLogTailLines {
		tail = maxLogTailLines
	}

	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultLogMaxBytes
	}
	if maxBytes > maxLogMaxBytes {
		maxBytes = maxLogMaxBytes
	}

	prefix := len(pods) > 1
	bodies := t.fetchLogsInParallel(ctx, snap, pods, podLogFetchOpts{
		Namespace:    in.Namespace,
		Container:    in.Container,
		TailLines:    tail,
		SinceSeconds: in.SinceSeconds,
		Previous:     in.Previous,
		Grep:         in.Grep,
		Prefix:       prefix,
		MaxBytes:     maxBytes,
	})

	var b strings.Builder
	if prefix {
		fmt.Fprintf(&b, "# pods=%d tail=%d (per pod)\n", len(pods), tail)
		if truncatedPodList {
			fmt.Fprintf(&b, "# note: more than %d pods matched — only the first %d are shown. Tighten the selector or raise maxPods.\n", maxPods, maxPods)
		}
	} else {
		fmt.Fprintf(&b, "# pod=%s container=%s tail=%d\n", pods[0], in.Container, tail)
	}
	for _, pod := range pods {
		b.WriteString(bodies[pod])
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, nil, nil
}

// resolveLogSources turns whichever selector is set into a concrete list of
// pod names in the bound namespace. Exactly one of Pod / LabelSelector /
// Workload must be set.
func (t *ToolKit) resolveLogSources(ctx context.Context, snap *clientSnapshot, in GetLogsInput) ([]string, error) {
	selectors := 0
	if in.Pod != "" {
		selectors++
	}
	if in.LabelSelector != "" {
		selectors++
	}
	if in.Workload != "" {
		selectors++
	}
	if selectors == 0 {
		return nil, fmt.Errorf("one of pod, labelSelector, or workload is required")
	}
	if selectors > 1 {
		return nil, fmt.Errorf("pod, labelSelector, and workload are mutually exclusive")
	}

	switch {
	case in.Pod != "":
		return []string{in.Pod}, nil
	case in.LabelSelector != "":
		return t.podsBySelector(ctx, snap, in.Namespace, in.LabelSelector)
	default:
		return t.podsForWorkload(ctx, snap, in.Namespace, in.Workload)
	}
}

func (t *ToolKit) podsBySelector(ctx context.Context, snap *clientSnapshot, namespace, selector string) ([]string, error) {
	list, err := snap.typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list pods in %q with selector %q: %w", namespace, selector, err)
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	return names, nil
}

// podsForWorkload parses `Kind/name` and looks up the workload's selector,
// then lists pods matching it.
func (t *ToolKit) podsForWorkload(ctx context.Context, snap *clientSnapshot, namespace, ref string) ([]string, error) {
	kind, name, ok := strings.Cut(ref, "/")
	if !ok || kind == "" || name == "" {
		return nil, fmt.Errorf("workload reference must be Kind/name, got %q", ref)
	}

	selector, err := t.workloadSelector(ctx, snap, namespace, kind, name)
	if err != nil {
		return nil, err
	}
	if selector == "" {
		return nil, fmt.Errorf("workload %s/%s in namespace %q has no pod selector", kind, name, namespace)
	}
	return t.podsBySelector(ctx, snap, namespace, selector)
}

// workloadSelector reads the requested workload and turns its LabelSelector
// into the string form client-go's pod List expects.
func (t *ToolKit) workloadSelector(ctx context.Context, snap *clientSnapshot, namespace, kind, name string) (string, error) {
	ns := namespace
	var ls *metav1.LabelSelector
	switch strings.ToLower(kind) {
	case "deployment":
		d, err := snap.typed.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", wrapGetErr(kind, name, err)
		}
		ls = d.Spec.Selector
	case "statefulset":
		s, err := snap.typed.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", wrapGetErr(kind, name, err)
		}
		ls = s.Spec.Selector
	case "daemonset":
		d, err := snap.typed.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", wrapGetErr(kind, name, err)
		}
		ls = d.Spec.Selector
	case "replicaset":
		r, err := snap.typed.AppsV1().ReplicaSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", wrapGetErr(kind, name, err)
		}
		ls = r.Spec.Selector
	default:
		return "", fmt.Errorf("workload kind %q not supported (try Deployment, StatefulSet, DaemonSet, ReplicaSet)", kind)
	}
	sel, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return "", fmt.Errorf("parse workload selector: %w", err)
	}
	return sel.String(), nil
}

func wrapGetErr(kind, name string, err error) error {
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%s %q not found", kind, name)
	}
	return fmt.Errorf("get %s %q: %w", kind, name, err)
}

type podLogFetchOpts struct {
	Namespace    string
	Container    string
	TailLines    int
	SinceSeconds int
	Previous     bool
	Grep         string
	Prefix       bool
	MaxBytes     int
}

// fetchLogsInParallel runs up to logFetchConcurrency fetches concurrently
// and returns per-pod body strings keyed by pod name. A pod whose fetch
// fails gets a short error-formatted body rather than being dropped, so
// the user still sees *which* pod was unreadable.
func (t *ToolKit) fetchLogsInParallel(ctx context.Context, snap *clientSnapshot, pods []string, opts podLogFetchOpts) map[string]string {
	out := make(map[string]string, len(pods))
	var mu sync.Mutex

	var wg sync.WaitGroup
	gate := make(chan struct{}, logFetchConcurrency)
	for _, pod := range pods {
		pod := pod
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-gate }()
			body := t.fetchOnePodLogs(ctx, snap, pod, opts)
			mu.Lock()
			out[pod] = body
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// fetchOnePodLogs is the per-pod stream + filter + byte cap. Errors are
// formatted inline so the caller can show them alongside successful pods.
func (t *ToolKit) fetchOnePodLogs(ctx context.Context, snap *clientSnapshot, pod string, opts podLogFetchOpts) string {
	kopts := &corev1.PodLogOptions{
		Container: opts.Container,
		TailLines: int64Ptr(int64(opts.TailLines)),
		Previous:  opts.Previous,
	}
	if opts.SinceSeconds > 0 {
		s := int64(opts.SinceSeconds)
		kopts.SinceSeconds = &s
	}

	var stream io.ReadCloser
	var err error
	if t.openLogStream != nil {
		stream, err = t.openLogStream(ctx, snap, opts.Namespace, pod, kopts)
	} else {
		req := snap.typed.CoreV1().Pods(opts.Namespace).GetLogs(pod, kopts)
		stream, err = req.Stream(ctx)
	}
	if err != nil {
		return fmt.Sprintf("[%s] !! get logs: %v\n", pod, err)
	}
	defer func() { _ = stream.Close() }()

	body, err := filterLogs(stream, opts.Grep)
	if err != nil {
		body += fmt.Sprintf("[%s] !! read stream: %v\n", pod, err)
	}

	cut, marker := truncateToBytes(body, opts.MaxBytes)
	body = marker + cut

	if opts.Prefix {
		return prefixLines(body, pod)
	}
	return body
}

// truncateToBytes cuts body at the nearest preceding line boundary so the
// result is <= maxBytes. Returns the cut body and a one-line marker to prepend
// (empty when no truncation happened).
func truncateToBytes(body string, maxBytes int) (string, string) {
	if len(body) <= maxBytes {
		return body, ""
	}
	// Walk back from maxBytes-1 to find the preceding newline.
	cutAt := strings.LastIndex(body[:maxBytes], "\n")
	if cutAt < 0 {
		// No newline within maxBytes — keep the first maxBytes of the oversized line.
		// Better than emitting an empty body; agent will see the line is partial.
		cutAt = maxBytes
	} else {
		cutAt++ // include the newline itself
	}
	cut := body[:cutAt]
	// Count lines in cut and original for the marker.
	nCut := strings.Count(cut, "\n")
	nFull := strings.Count(body, "\n")
	bytesMore := len(body) - len(cut)
	marker := fmt.Sprintf("# truncated: showed %d of %d lines, %d bytes more available — tighten grep/sinceSeconds, or use stream_log_until for keyword search.\n",
		nCut, nFull, bytesMore)
	return cut, marker
}

// prefixLines turns `foo\nbar\n` into `[pod] foo\n[pod] bar\n` so
// aggregated multi-pod output stays legible.
func prefixLines(body, pod string) string {
	if body == "" {
		return body
	}
	var b strings.Builder
	b.Grow(len(body) + len(pod)*8)
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		b.WriteByte('[')
		b.WriteString(pod)
		b.WriteString("] ")
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	return b.String()
}

// filterLogs reads a log stream line by line and returns the first
// maxLinesAfterGrep lines (post-filter). An empty filter keeps everything.
func filterLogs(r io.Reader, filter string) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var b strings.Builder
	lines := 0
	for scanner.Scan() {
		line := scanner.Text()
		if filter != "" && !strings.Contains(line, filter) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		lines++
		if lines >= maxLinesAfterGrep {
			fmt.Fprintf(&b, "... [truncated after %d lines] ...\n", maxLinesAfterGrep)
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

// --- list_events ------------------------------------------------------------

type ListEventsInput struct {
	Namespace          string `json:"namespace" jsonschema:"Required. Pass the workload namespace from the Environment block's cluster-resource-namespace."`
	InvolvedObjectKind string `json:"involvedObjectKind,omitempty" jsonschema:"Filter events whose involvedObject.kind matches (e.g. Pod)."`
	InvolvedObjectName string `json:"involvedObjectName,omitempty" jsonschema:"Filter events whose involvedObject.name matches."`
	Type               string `json:"type,omitempty" jsonschema:"Event type filter: Normal or Warning."`
}

type EventSummary struct {
	LastTimestamp  string `json:"lastTimestamp,omitempty"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int32  `json:"count,omitempty"`
	InvolvedKind   string `json:"involvedKind"`
	InvolvedName   string `json:"involvedName"`
	ReportingField string `json:"reportingField,omitempty"`
}

type ListEventsOutput struct {
	Items []EventSummary `json:"items"`
}

func (t *ToolKit) listEvents(ctx context.Context, _ *mcp.CallToolRequest, in ListEventsInput) (*mcp.CallToolResult, ListEventsOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, ListEventsOutput{}, nil
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return errorResult("list_events requires `namespace`. Pass cluster-resource-namespace from the Environment block."), ListEventsOutput{}, nil
	}
	list, err := snap.typed.CoreV1().Events(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errorResult(fmt.Sprintf("list events: %v", err)), ListEventsOutput{}, nil
	}

	items := make([]corev1.Event, 0, len(list.Items))
	for _, e := range list.Items {
		if in.InvolvedObjectKind != "" && !strings.EqualFold(e.InvolvedObject.Kind, in.InvolvedObjectKind) {
			continue
		}
		if in.InvolvedObjectName != "" && e.InvolvedObject.Name != in.InvolvedObjectName {
			continue
		}
		if in.Type != "" && !strings.EqualFold(e.Type, in.Type) {
			continue
		}
		items = append(items, e)
	}

	sortEventsNewestFirst(items)

	const maxEvents = 200
	if len(items) > maxEvents {
		items = items[:maxEvents]
	}

	out := ListEventsOutput{Items: make([]EventSummary, 0, len(items))}
	for _, e := range items {
		ts := ""
		if !e.LastTimestamp.IsZero() {
			ts = e.LastTimestamp.UTC().Format("2006-01-02T15:04:05Z")
		} else if !e.EventTime.IsZero() {
			ts = e.EventTime.UTC().Format("2006-01-02T15:04:05Z")
		}
		out.Items = append(out.Items, EventSummary{
			LastTimestamp:  ts,
			Type:           e.Type,
			Reason:         e.Reason,
			Message:        e.Message,
			Count:          e.Count,
			InvolvedKind:   e.InvolvedObject.Kind,
			InvolvedName:   e.InvolvedObject.Name,
			ReportingField: e.ReportingController,
		})
	}
	return nil, out, nil
}

func sortEventsNewestFirst(items []corev1.Event) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && eventBefore(items[j-1], items[j]); j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}

func eventBefore(a, b corev1.Event) bool {
	return eventTs(a).Time.Before(eventTs(b).Time)
}

func eventTs(e corev1.Event) metav1.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp
	}
	if !e.EventTime.IsZero() {
		return metav1.Time{Time: e.EventTime.Time}
	}
	return metav1.Time{}
}

func int64Ptr(v int64) *int64 { return &v }
