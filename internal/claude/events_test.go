package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func first(events []Event) Event {
	if len(events) == 0 {
		return Event{}
	}
	return events[0]
}

func TestParseLine_System(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"system","subtype":"init","session_id":"sess-1"}`))
	require.Len(t, evs, 1)
	ev := evs[0]
	assert.Equal(t, EventSystem, ev.Kind)
	assert.Equal(t, "sess-1", ev.SessionID)
	assert.Equal(t, "init", ev.Subtype)
}

func TestParseLine_AssistantTextOnly(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"content":[
		{"type":"text","text":"hello "},
		{"type":"text","text":"world"}
	]}}`)
	evs := parseLine(line)
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, EventAssistant, ev.Kind)
	assert.Equal(t, "hello world", ev.Text)
}

// TestParseLine_AssistantWithEmbeddedToolUse exercises the actual claude-code
// stream-json shape: an assistant message whose content array carries a
// tool_use block alongside text. We expect to flatten it into one
// EventAssistant for the text plus one EventToolUse per block.
func TestParseLine_AssistantWithEmbeddedToolUse(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"content":[
		{"type":"text","text":"checking pods"},
		{"type":"tool_use","id":"toolu_01a","name":"mcp__triagent-k8s__list_resources","input":{"kind":"Pod"}},
		{"type":"tool_use","id":"toolu_01b","name":"mcp__triagent-k8s__get_logs","input":{"pod":"zeebe-0"}}
	]}}`)
	evs := parseLine(line)
	require.Len(t, evs, 3, "expected 3 events (1 text + 2 tool_use)")
	assert.Equal(t, EventAssistant, evs[0].Kind)
	assert.Equal(t, "checking pods", evs[0].Text)
	assert.Equal(t, EventToolUse, evs[1].Kind)
	assert.Equal(t, "toolu_01a", evs[1].ToolID)
	assert.Equal(t, "mcp__triagent-k8s__list_resources", evs[1].ToolName)
	assert.Equal(t, EventToolUse, evs[2].Kind)
	assert.Equal(t, "toolu_01b", evs[2].ToolID)
	assert.Equal(t, "Pod", evs[1].ToolInput["kind"])
}

// TestParseLine_AssistantOnlyToolUseNoText exercises the no-text path —
// claude emits these when its turn is purely "I'll call this tool".
func TestParseLine_AssistantOnlyToolUseNoText(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"content":[
		{"type":"tool_use","id":"toolu_01a","name":"mcp__triagent-k8s__list_resource_kinds","input":{}}
	]}}`)
	evs := parseLine(line)
	require.Len(t, evs, 1)
	assert.Equal(t, EventToolUse, evs[0].Kind)
}

// TestParseLine_UserToolResults exercises the synthesised `user` message
// claude emits when reporting a tool result back into the conversation.
func TestParseLine_UserToolResults(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"user","session_id":"s","message":{"content":[
		{"type":"tool_result","tool_use_id":"toolu_01a","content":[{"type":"text","text":"3 pods listed"}]}
	]}}`)
	evs := parseLine(line)
	require.Len(t, evs, 1)
	assert.Equal(t, EventToolResult, evs[0].Kind)
	assert.Equal(t, "toolu_01a", evs[0].ToolID)
	assert.Equal(t, "3 pods listed", evs[0].ToolResult)
}

// TestParseLine_UserToolResultStringContent covers the alternate wire
// shape where `content` is a bare string instead of an array of blocks.
func TestParseLine_UserToolResultStringContent(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"user","session_id":"s","message":{"content":[
		{"type":"tool_result","tool_use_id":"toolu_01a","content":"plain string body"}
	]}}`)
	evs := parseLine(line)
	require.Len(t, evs, 1)
	assert.Equal(t, EventToolResult, evs[0].Kind)
	assert.Equal(t, "plain string body", evs[0].ToolResult)
}

func TestParseLine_TopLevelToolUseLegacy(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"tool_use","id":"t1","name":"mcp__example__list_resources","input":{"kind":"Pod"}}`))
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, EventToolUse, ev.Kind)
	assert.Equal(t, "t1", ev.ToolID)
	assert.Equal(t, "mcp__example__list_resources", ev.ToolName)
	assert.Equal(t, "Pod", ev.ToolInput["kind"])
}

func TestParseLine_TopLevelToolResultLegacy(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"{\"kinds\":[...]}"}]}`)
	evs := parseLine(line)
	require.Len(t, evs, 1)
	assert.Equal(t, "t1", first(evs).ToolID)
	assert.NotEmpty(t, first(evs).ToolResult)
}

func TestParseLine_Result(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"result","subtype":"success","session_id":"s","result":"all done"}`))
	require.Len(t, evs, 1)
	require.Equal(t, EventResult, evs[0].Kind)
	assert.Equal(t, "all done", evs[0].FinalText)
	assert.Equal(t, "success", evs[0].Subtype)
}

// TestParseLine_AssistantUsage covers the per-message usage block claude
// emits on every assistant turn. The usage rides on a dedicated EventUsage
// alongside the EventAssistant so the launcher's aggregator can sum across
// every API call — including the pure-tool_use messages that previously
// lost their usage when EventToolUse got dropped downstream.
func TestParseLine_AssistantUsage(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"id":"msg_abc","content":[
		{"type":"text","text":"hi"}
	],"usage":{"input_tokens":12,"output_tokens":34,"cache_creation_input_tokens":56,"cache_read_input_tokens":78}}}`)
	evs := parseLine(line)
	require.Len(t, evs, 2)
	assert.Equal(t, EventAssistant, evs[0].Kind)
	assert.Nil(t, evs[0].Usage, "assistant event itself no longer carries Usage; the dedicated EventUsage does")
	require.Equal(t, EventUsage, evs[1].Kind)
	require.NotNil(t, evs[1].Usage)
	assert.Equal(t, "msg_abc", evs[1].MessageID, "EventUsage carries message.id so the stream loop can dedupe split-message lines")
	assert.Equal(t, 12, evs[1].Usage.InputTokens)
	assert.Equal(t, 34, evs[1].Usage.OutputTokens)
	assert.Equal(t, 56, evs[1].Usage.CacheCreationInputTokens)
	assert.Equal(t, 78, evs[1].Usage.CacheReadInputTokens)
}

// TestParseLine_AssistantUsageSharedAcrossSplitEvents asserts that when
// an assistant line carries both text and tool_use blocks (and so gets
// flattened into multiple Events), the usage block rides on the single
// trailing EventUsage rather than being replicated onto each split event.
// Aggregators sum from EventUsage; the split events don't double-count.
func TestParseLine_AssistantUsageSharedAcrossSplitEvents(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"content":[
		{"type":"text","text":"checking"},
		{"type":"tool_use","id":"toolu_1","name":"x","input":{}}
	],"usage":{"input_tokens":10,"output_tokens":20}}}`)
	evs := parseLine(line)
	require.Len(t, evs, 3)
	assert.Equal(t, EventAssistant, evs[0].Kind)
	assert.Equal(t, EventToolUse, evs[1].Kind)
	require.Equal(t, EventUsage, evs[2].Kind)
	require.NotNil(t, evs[2].Usage)
	assert.Equal(t, 10, evs[2].Usage.InputTokens)
	assert.Nil(t, evs[0].Usage, "usage must not be replicated onto the text split")
	assert.Nil(t, evs[1].Usage, "usage must not be replicated onto the tool_use split")
}

// TestParseLine_PureToolUseAssistantStillCarriesUsage covers the case the
// previous "stamp on out[0]" approach quietly dropped: an assistant message
// with no text content, only tool_use blocks. The usage previously landed
// on EventToolUse which the server-side mapper drops — so the launcher
// never saw the tokens. With the dedicated EventUsage carrier, every API
// call's usage survives.
func TestParseLine_PureToolUseAssistantStillCarriesUsage(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","session_id":"s","message":{"content":[
		{"type":"tool_use","id":"toolu_1","name":"x","input":{}}
	],"usage":{"input_tokens":7,"output_tokens":11,"cache_read_input_tokens":99}}}`)
	evs := parseLine(line)
	require.Len(t, evs, 2)
	assert.Equal(t, EventToolUse, evs[0].Kind)
	assert.Nil(t, evs[0].Usage)
	require.Equal(t, EventUsage, evs[1].Kind)
	require.NotNil(t, evs[1].Usage)
	assert.Equal(t, 7, evs[1].Usage.InputTokens)
	assert.Equal(t, 11, evs[1].Usage.OutputTokens)
	assert.Equal(t, 99, evs[1].Usage.CacheReadInputTokens)
}

// TestParseLine_ResultUsageAndCost covers the result line claude emits
// at the end of every CLI invocation: usage + total_cost_usd. Summing
// these across result events gives session totals.
func TestParseLine_ResultUsageAndCost(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"result","subtype":"success","session_id":"s","result":"done","total_cost_usd":0.1234,"usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400}}`))
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, EventResult, ev.Kind)
	require.NotNil(t, ev.Usage)
	assert.Equal(t, 100, ev.Usage.InputTokens)
	assert.Equal(t, 200, ev.Usage.OutputTokens)
	assert.Equal(t, 300, ev.Usage.CacheCreationInputTokens)
	assert.Equal(t, 400, ev.Usage.CacheReadInputTokens)
	assert.InDelta(t, 0.1234, ev.CostUSD, 1e-9)
}

// TestParseLine_ResultNoUsage covers result lines from older / unusual
// claude builds that omit the usage and cost fields entirely. Parser
// must degrade to a nil Usage and zero cost without erroring.
func TestParseLine_ResultNoUsage(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"result","subtype":"success","session_id":"s","result":"done"}`))
	require.Len(t, evs, 1)
	assert.Nil(t, evs[0].Usage)
	assert.Equal(t, 0.0, evs[0].CostUSD)
}

func TestParseLine_MalformedYieldsErrorEvent(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`not json`))
	require.Len(t, evs, 1)
	require.Equal(t, EventError, evs[0].Kind)
	assert.NotNil(t, evs[0].Err)
}

func TestParseLine_UnknownTypeIsPreserved(t *testing.T) {
	t.Parallel()
	evs := parseLine([]byte(`{"type":"future_thing"}`))
	assert.Len(t, evs, 1)
	if len(evs) == 1 {
		assert.Equal(t, EventUnknown, evs[0].Kind)
	}
}

// TestUsageDeduper_DropsRepeatMessageID covers the core symptom: Claude
// Code splits one assistant API message into multiple stream-json lines
// (thinking / tool_use / text) and each line repeats the same `usage`
// block. If the launcher sums each EventUsage we'd count one API call N
// times — observed ~2× over-reporting on real sessions. The deduper
// keeps the first EventUsage per message id and drops the rest; non-usage
// events pass through untouched and usage events without a message id
// (legacy / older claude builds) pass through too so we don't silently
// lose accounting.
func TestUsageDeduper_DropsRepeatMessageID(t *testing.T) {
	t.Parallel()
	d := newUsageDeduper()

	// First line of msg_A: thinking block + the duplicated usage.
	first := d.filter([]Event{
		{Kind: EventAssistant, Text: ""},
		{Kind: EventUsage, MessageID: "msg_A", Usage: &Usage{CacheReadInputTokens: 100}},
	})
	require.Len(t, first, 2, "first line passes both events")
	assert.Equal(t, EventUsage, first[1].Kind)

	// Second line of msg_A: tool_use block + the SAME usage (claude wire
	// repeats the block on every split). Tool_use must still propagate;
	// only the duplicate usage is dropped.
	second := d.filter([]Event{
		{Kind: EventToolUse, ToolID: "t1"},
		{Kind: EventUsage, MessageID: "msg_A", Usage: &Usage{CacheReadInputTokens: 100}},
	})
	require.Len(t, second, 1, "duplicate usage for msg_A dropped, tool_use survives")
	assert.Equal(t, EventToolUse, second[0].Kind)

	// New message id: usage is a real new API call, must pass.
	third := d.filter([]Event{
		{Kind: EventUsage, MessageID: "msg_B", Usage: &Usage{CacheReadInputTokens: 200}},
	})
	require.Len(t, third, 1)
	assert.Equal(t, "msg_B", third[0].MessageID)

	// Usage without a message id (old claude builds): pass through — we
	// can't dedupe, but dropping would silently under-report.
	fourth := d.filter([]Event{
		{Kind: EventUsage, Usage: &Usage{CacheReadInputTokens: 9}},
	})
	require.Len(t, fourth, 1)
}
