package prom

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// catalog holds the per-binding metric index. Built once per endpoint
// bind; replaced wholesale on rebind. Read-many / write-once after
// construction — no locks on the hot path.
//
// cardEst is filled lazily by the cardinality probe; the inner mutex
// guards that single field. labelsCache is similarly lazy (see Task 8).
type catalog struct {
	endpoint  string
	names     []string                  // sorted
	metadata  map[string]MetricMetadata // help/type/unit; may be missing entries
	prefixIdx map[string]int            // "<prefix>_" → count

	mu          sync.Mutex
	cardEst     map[string]int   // lazy; presence (ok from map lookup) means "probed"; value of -1 is the high-card sentinel
	labelsCache map[string]labelProfile
}

func emptyCatalog() *catalog {
	return &catalog{
		metadata:    map[string]MetricMetadata{},
		prefixIdx:   map[string]int{},
		cardEst:     map[string]int{},
		labelsCache: map[string]labelProfile{},
	}
}

// buildCatalog issues the two startup HTTP calls and assembles the index.
func buildCatalog(ctx context.Context, c *promClient) (*catalog, error) {
	names, err := c.labelNames(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	md, err := c.metadata(ctx)
	if err != nil {
		return nil, err
	}
	cat := emptyCatalog()
	cat.endpoint = c.endpoint
	cat.names = names
	cat.metadata = md
	cat.prefixIdx = buildPrefixIndex(names)
	return cat, nil
}

// buildPrefixIndex groups metric names by their first underscore-bounded
// prefix segment (e.g. "zeebe_partition_health" → "zeebe_"). Names with
// no underscore (or a leading underscore at position 0) are skipped — they
// have no informative prefix to group by.
func buildPrefixIndex(names []string) map[string]int {
	out := map[string]int{}
	for _, n := range names {
		i := strings.IndexByte(n, '_')
		if i <= 0 {
			continue
		}
		out[n[:i+1]]++ // include the trailing underscore
	}
	return out
}

// labelProfile is what describe_metric returns about a metric's labels.
// Filled lazily by the series probe (Task 7).
type labelProfile struct {
	labels           []labelInfo
	relatedMetrics   []string
	totalCardinality int
}

type labelInfo struct {
	Key          string   `json:"key"`
	Cardinality  int      `json:"cardinality"`
	SampleValues []string `json:"sample_values"`
	TypicalScope bool     `json:"typical_scope,omitempty"`
}
