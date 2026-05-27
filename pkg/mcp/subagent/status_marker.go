package subagent

import (
	"regexp"
	"time"
)

// statusMarkerPattern matches <<<STATUS message="..." >>>. The
// message body is non-greedy and rejects unescaped quotes.
var statusMarkerPattern = regexp.MustCompile(`<<<STATUS\s+message="([^"]*)"\s*>>>`)

// statusMarkerEmitter is the function signature the telemetry
// SendStatus satisfies. Tests inject a recording stub.
type statusMarkerEmitter func(parentToolID, message string, ts time.Time)

// statusMarkerParser scans assistant text chunks for status markers
// and forwards each match via emit. Streaming-friendly per-chunk; a
// marker split across two feed() calls won't match (rare in practice
// — the sub-agent emits markers on their own line). Worst case: one
// dropped status update — not a correctness issue.
type statusMarkerParser struct {
	parentToolID string
	emit         statusMarkerEmitter
}

func newStatusMarkerParser(parentToolID string, emit statusMarkerEmitter) *statusMarkerParser {
	return &statusMarkerParser{parentToolID: parentToolID, emit: emit}
}

func (p *statusMarkerParser) feed(chunk string) {
	matches := statusMarkerPattern.FindAllStringSubmatch(chunk, -1)
	now := time.Now()
	for _, m := range matches {
		if len(m) >= 2 && m[1] != "" {
			p.emit(p.parentToolID, m[1], now)
		}
	}
}

// feedAndStrip is feed + removal: emits each matched status as a
// telemetry update, then returns the chunk with all well-formed markers
// removed so callers can post the cleaned text to the assistant
// stream / aggregate it into finalText without double-rendering. Use
// this from relaySubEvents and anywhere else that surfaces the raw
// assistant chunk to the operator.
//
// Malformed markers (missing/empty/unterminated message) intentionally
// pass through unchanged — the operator should still see what the
// sub-agent actually wrote so we can spot prompt-discipline issues.
func (p *statusMarkerParser) feedAndStrip(chunk string) string {
	p.feed(chunk)
	return statusMarkerPattern.ReplaceAllString(chunk, "")
}
