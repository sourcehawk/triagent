package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleEvents = `
{"phase":"start","traceId":"t","toolId":"a","toolName":"mcp__triagent-k8s__switch_context","input":{"name":"prod-eu-1"}}
{"phase":"end","traceId":"t","toolId":"a","toolName":"mcp__triagent-k8s__switch_context","result":"{\"activeContext\":\"prod-eu-1\"}"}
{"phase":"start","traceId":"t","toolId":"b","toolName":"mcp__triagent-k8s__list_resources","input":{}}
{"phase":"end","traceId":"t","toolId":"b","toolName":"mcp__triagent-k8s__list_resources","result":"ok"}
{"phase":"end","traceId":"t","toolId":"c","toolName":"mcp__triagent-k8s__switch_context","errorText":"build rest config: bad"}
{"phase":"end","traceId":"t","toolId":"d","toolName":"mcp__triagent-k8s__switch_context","result":"{\"activeContext\":\"dev-eu-1\"}"}
`

func TestContextsTouched_FiltersToSuccessfulSwitches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(sampleEvents), 0o600))

	got, err := ContextsTouched(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "prod-eu-1", got[0].Name)
	assert.Equal(t, "dev-eu-1", got[1].Name)
}

func TestContextsTouched_MissingFile_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := ContextsTouched(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)
}
