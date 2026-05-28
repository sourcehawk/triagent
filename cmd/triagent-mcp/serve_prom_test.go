package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunServe_PromKindRequiresURL(t *testing.T) {
	// runProm reads TRIAGENT_MCP_PROM_RESOLVER_URL and TRIAGENT_MCP_TELEMETRY_TOKEN
	// directly from the environment. Without isolation a developer or CI
	// job that exports either would build the prom server and enter Run
	// against the real launcher loopback, hanging the test until timeout.
	// Clear both before exercising the missing-URL path. t.Setenv also
	// makes the test incompatible with t.Parallel, so drop that here.
	t.Setenv("TRIAGENT_MCP_PROM_RESOLVER_URL", "")
	t.Setenv("TRIAGENT_MCP_TELEMETRY_TOKEN", "")
	err := runServe(context.Background(), serveFlags{kind: "prom"})
	if err == nil {
		t.Fatal("expected error for missing --prom-url")
	}
	if !strings.Contains(err.Error(), "prom-url") {
		t.Fatalf("error should mention --prom-url, got: %v", err)
	}
}

func TestRunServe_UnknownKindErrorListsProm(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "prom") {
		t.Fatalf("kind list should include prom, got: %v", err)
	}
}
