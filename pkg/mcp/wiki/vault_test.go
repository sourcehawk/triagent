package wiki

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEntities_EmptyVault(t *testing.T) {
	t.Parallel()
	vault := t.TempDir()
	got, err := ListEntities(vault, "")
	require.NoError(t, err, "ListEntities")
	assert.Empty(t, got)
}

func TestListEntities_FindsEntitiesByType(t *testing.T) {
	t.Parallel()
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "entities/services/zeebe-broker.md"), "stub")
	mustWrite(t, filepath.Join(vault, "entities/services/elasticsearch.md"), "stub")
	mustWrite(t, filepath.Join(vault, "entities/errors/oom-kill.md"), "stub")

	all, err := ListEntities(vault, "")
	require.NoError(t, err, "ListEntities all")
	assert.Len(t, all, 3, "expected 3 entities")

	services, err := ListEntities(vault, "service")
	require.NoError(t, err, "ListEntities services")
	require.Len(t, services, 2, "expected 2 services")
	assert.Equal(t, "service", services[0].Type)
}

func TestReadEntry_RoundTrip(t *testing.T) {
	t.Parallel()
	vault := t.TempDir()
	body := `---
id: inc-12345-broker-ooms
date: 2026-04-12
title: Test
status: resolved
services: [zeebe-broker]
errors: [oom-kill]
symptoms: [stuck-reconciliation]
sources:
  investigation_session: sess-abc
---

## Summary
A summary.

## Root cause
A cause.

## Fix
A fix.
`
	mustWrite(t, filepath.Join(vault, "entries/inc-12345-broker-ooms.md"), body)

	note, err := ReadEntry(vault, "inc-12345-broker-ooms")
	require.NoError(t, err, "ReadEntry")
	assert.Equal(t, "Test", note.Frontmatter.Title)
	require.Len(t, note.Frontmatter.Services, 1)
	assert.Equal(t, "zeebe-broker", note.Frontmatter.Services[0])
	assert.Contains(t, note.Body, "## Summary", "body missing summary header")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
