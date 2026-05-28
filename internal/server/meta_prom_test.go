package server

import (
	"strings"
	"testing"
)

func TestToolCatalog_IncludesPromTools(t *testing.T) {
	t.Parallel()
	cat := toolCatalog()
	found := false
	for _, s := range cat {
		if strings.HasPrefix(s.Name, "prom_") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("toolCatalog() missing prom_* tools")
	}
}
