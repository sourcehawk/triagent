package sources

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/internal/watches"
)

type fakeSlack struct {
	messages []SlackMessage
}

func (f *fakeSlack) History(_ context.Context, _ string, _ string) ([]SlackMessage, error) {
	return f.messages, nil
}

func TestSlackPollNewMessages(t *testing.T) {
	client := &fakeSlack{messages: []SlackMessage{
		{TS: "1715420822.000100", User: "U1", Text: "alert: keda failed scale-up", Permalink: "https://slack/..."},
		{TS: "1715420900.000100", User: "U2", Text: "noise", Permalink: "https://slack/..."},
	}}
	src := &Slack{client: client}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceSlackChannel, ChannelID: "C0"}}
	items, wm, err := src.Poll(context.Background(), w, watches.WatermarkState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].SourceRef.TS != "1715420822.000100" {
		t.Fatalf("expected 2 items oldest-first, got %+v", items)
	}
	if wm.SlackOldestTS != "1715420900.000100" {
		t.Fatalf("watermark = %q, want latest ts", wm.SlackOldestTS)
	}
}

func TestSlackThreadReplyFilter(t *testing.T) {
	client := &fakeSlack{messages: []SlackMessage{
		{TS: "1", Subtype: "", Text: "top-level"},
		{TS: "2", Subtype: "thread_broadcast", Text: "reply"},
	}}
	src := &Slack{client: client}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceSlackChannel, ChannelID: "C0", IncludeThreadReplies: false}}
	items, _, _ := src.Poll(context.Background(), w, watches.WatermarkState{})
	if len(items) != 1 || items[0].SourceRef.TS != "1" {
		t.Fatalf("expected only top-level, got %+v", items)
	}
}

// Regression: the previous filter dropped every message with a non-empty
// subtype, which silently ate bot alerts (PagerDuty/alertmanager/Sentry),
// file shares, and /me messages — i.e. most of what an incident-response
// monitoring channel actually carries. Make sure those flow through.
func TestSlackAdmitsBotAndFileShareSubtypes(t *testing.T) {
	client := &fakeSlack{messages: []SlackMessage{
		{TS: "1", Subtype: "bot_message", Text: "PagerDuty: incident triggered"},
		{TS: "2", Subtype: "file_share", Text: "screenshot.png"},
		{TS: "3", Subtype: "me_message", Text: "/me deploys"},
		{TS: "4", Subtype: "", Text: "regular"},
		{TS: "5", Subtype: "channel_join", Text: "user joined"},
		{TS: "6", Subtype: "message_changed", Text: "edit"},
	}}
	src := &Slack{client: client}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceSlackChannel, ChannelID: "C0"}}
	items, _, err := src.Poll(context.Background(), w, watches.WatermarkState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("want 4 admitted (bot, file, me, regular), got %d: %+v", len(items), items)
	}
	gotTS := make(map[string]bool, len(items))
	for _, it := range items {
		gotTS[it.SourceRef.TS] = true
	}
	for _, want := range []string{"1", "2", "3", "4"} {
		if !gotTS[want] {
			t.Errorf("ts=%s should have been admitted", want)
		}
	}
	for _, drop := range []string{"5", "6"} {
		if gotTS[drop] {
			t.Errorf("ts=%s should have been dropped (housekeeping)", drop)
		}
	}
}
