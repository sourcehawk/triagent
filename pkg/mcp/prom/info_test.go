package prom

import (
	"strings"
	"testing"
)

func TestRenderInfo_PopulatedCatalog(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.endpoint = "http://prom.local:9090"
	cat.names = []string{
		"container_cpu_usage", "container_memory_usage",
		"apiserver_requests_total", "node_load1",
	}
	cat.prefixIdx = buildPrefixIndex(cat.names)
	body := renderInfo(cat)
	if !strings.Contains(body, "4 metrics indexed at http://prom.local:9090") {
		t.Errorf("missing header line, body:\n%s", body)
	}
	if !strings.Contains(body, "container_*") {
		t.Errorf("missing container_ prefix line:\n%s", body)
	}
	if !strings.Contains(body, "prom_list_metrics") {
		t.Errorf("missing tool guidance line:\n%s", body)
	}
}

func TestRenderInfo_EmptyCatalog(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.endpoint = "http://prom.local:9090"
	body := renderInfo(cat)
	if !strings.Contains(body, "0 metrics indexed at http://prom.local:9090") {
		t.Errorf("empty header missing:\n%s", body)
	}
	if !strings.Contains(body, "catalog empty") {
		t.Errorf("empty-catalog hint missing:\n%s", body)
	}
}
