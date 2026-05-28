package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cardCatalog(name string, card int) *catalog {
	cat := emptyCatalog()
	cat.names = []string{name}
	cat.cardEst = map[string]int{name: card}
	cat.metadata = map[string]MetricMetadata{name: {Type: "gauge"}}
	return cat
}

func TestScope_AllowsScopedHighCard(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `container_cpu_usage{namespace="payments"}`)
	require.NoError(t, err)
}

func TestScope_RejectsUnscopedHighCard(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, "container_cpu_usage")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
	assert.Contains(t, err.Error(), "container_cpu_usage")
}

func TestScope_RejectsOnlyNameMatcher(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `container_cpu_usage{__name__="container_cpu_usage"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}

func TestScope_AllowsLowCardUnscoped(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("up", 12)
	err := checkScope(context.Background(), nil, cat, "up")
	require.NoError(t, err)
}

func TestScope_AllowsLabelMatcherWithRegex(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("http_requests_total", 500)
	err := checkScope(context.Background(), nil, cat, `http_requests_total{job=~".*api.*"}`)
	require.NoError(t, err)
}

// A query that already carries a non-__name__ matcher is scoped no
// matter what the cardinality is. The probe is therefore unnecessary
// — and on a high-fanout Thanos like the camunda dev cluster it can
// take seconds, blowing the request budget before the actual query
// runs. Skip it entirely when scope is already satisfied.
func TestScope_AlreadyScopedSkipsProbe(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("series probe must not run when query is already scoped")
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"high_card_metric"}
	// cardEst unset → without the short-circuit, the probe would fire.
	err := checkScope(context.Background(), c, cat, `high_card_metric{namespace="x"}`)
	require.NoError(t, err)
}

func TestScope_ProbesUnknownCardinality(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := []map[string]string{}
			for i := 0; i < 12; i++ {
				rows = append(rows, map[string]string{"__name__": "small_metric", "instance": itoa(i)})
			}
			_ = jsonWrite(w, map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"small_metric"}
	// cardEst is unset → checkScope must probe it.
	err := checkScope(context.Background(), c, cat, "small_metric")
	require.NoError(t, err, "low-card metric should pass unscoped")
}

// jsonWrite is a tiny helper to keep this file independent of the
// testify-only writeJSON in promclient_test.go (same package, exported).
func jsonWrite(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func TestScope_EmptyCatalog(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	err := checkScope(context.Background(), nil, cat, "anything_at_all")
	require.NoError(t, err, "empty catalog matches no metric name → vacuously scoped")
}

func TestScope_MetricNotInCatalog(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("known_metric", 500)
	err := checkScope(context.Background(), nil, cat, "unknown_metric")
	require.NoError(t, err, "name not in catalog can't be matched, so scope check is a no-op")
}

func TestScope_LongestNameWinsOnPrefixOverlap(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.names = []string{"http_requests", "http_requests_total"}
	cat.cardEst = map[string]int{
		"http_requests":       500,
		"http_requests_total": 500,
	}
	// The longer name has a label matcher; the shorter prefix does not
	// appear standalone. checkScope must NOT report the shorter name as
	// unscoped — longest-first sort + standalone-identifier boundary
	// check together guarantee this.
	err := checkScope(context.Background(), nil, cat, `http_requests_total{job="api"}`)
	require.NoError(t, err)
}

func TestScope_TwoRefsOneUnscoped(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.names = []string{"high_card_a", "high_card_b"}
	cat.cardEst = map[string]int{
		"high_card_a": 500,
		"high_card_b": 500,
	}
	// First metric is scoped, second is not — checkScope must surface
	// the unscoped one.
	err := checkScope(context.Background(), nil, cat, `high_card_a{job="x"} / high_card_b`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "high_card_b")
}

func TestScope_RejectsRegexNameMatcherWithoutScope(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `{__name__=~"container_.*"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
	assert.Contains(t, err.Error(), "__name__")
}

func TestScope_RejectsLiteralNameMatcherWithoutScope(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `{__name__="container_cpu_usage"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}

func TestScope_AllowsNameMatcherWithScope(t *testing.T) {
	t.Parallel()
	// hasUnscopedNameMatcher must not fire when a __name__ matcher is
	// paired with another label. Use a metric name that isn't in the
	// catalog so the catalog-substring scan also passes cleanly.
	cat := emptyCatalog()
	err := checkScope(context.Background(), nil, cat, `{__name__="container_cpu_usage",namespace="x"}`)
	require.NoError(t, err)
}

func TestScope_RejectsWildcardNameMatcher(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `{__name__=~".*"}`)
	require.Error(t, err)
}

// Regression: a comma inside a quoted __name__ regex used to fool the
// matcher splitter into treating `kube_.*"` as a second non-__name__
// matcher, letting `{__name__=~"container_.*,kube_.*"}` bypass the
// scope guard entirely.
func TestScope_RejectsNameRegexWithCommaInValue(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `{__name__=~"container_.*,kube_.*"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
	assert.Contains(t, err.Error(), "__name__")
}

// Regression: a literal comma inside a label value must not be counted
// as a matcher separator. With a real second matcher present, the
// selector is scoped and should pass.
func TestScope_AllowsCommaInsideLabelValue(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	err := checkScope(context.Background(), nil, cat,
		`{__name__="container_cpu_usage",pod=~"a,b"}`)
	require.NoError(t, err)
}

// Regression: PromQL identifiers may contain `:` (recording-rule
// naming convention). A scoped query against a recording rule whose
// name contains the substring of a high-cardinality raw metric used
// to be falsely rejected as an unscoped reference to that raw metric.
func TestScope_AllowsRecordingRuleNameContainingHighCardSubstring(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.names = []string{"http_requests", "job:http_requests:rate5m"}
	cat.cardEst = map[string]int{
		"http_requests":            500,
		"job:http_requests:rate5m": 5,
	}
	// The recording rule is low-card; a scoped query against it must pass.
	// Without `:` in isIdentChar, the substring scan would land on the
	// `http_requests` inside the recording-rule name and reject because
	// no scope follows.
	err := checkScope(context.Background(), nil, cat, `job:http_requests:rate5m{job="x"}`)
	require.NoError(t, err)
}

// Regression: the unscoped-name-matcher guard must also see through
// quoted braces. A `}` inside a quoted regex value should not be
// mistaken for the end of the matcher block.
func TestScope_RejectsNameRegexWithBraceInValue(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `{__name__=~"foo}bar"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}
