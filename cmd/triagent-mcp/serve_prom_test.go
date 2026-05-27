package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunServe_PromKindRequiresURL(t *testing.T) {
	t.Parallel()
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
