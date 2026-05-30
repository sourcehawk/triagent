package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunServe_CloudKindRequiresProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud"})
	if err == nil {
		t.Fatal("expected error when --provider is missing")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error should mention --provider, got: %v", err)
	}
}

func TestRunServe_CloudKindRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud", cloudProvider: "azure"})
	if err == nil {
		t.Fatal("expected error for an unknown provider")
	}
	if !strings.Contains(err.Error(), "azure") {
		t.Fatalf("error should name the rejected provider, got: %v", err)
	}
}

func TestRunServe_UnknownKindErrorListsCloud(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "cloud") {
		t.Fatalf("kind list should include cloud, got: %v", err)
	}
}

func TestServeCmd_KnowsCloudKind(t *testing.T) {
	t.Parallel()
	cmd := serveCmd()
	if !strings.Contains(cmd.Long, "cloud") {
		t.Fatalf("serve --help should list cloud, got: %q", cmd.Long)
	}
}
