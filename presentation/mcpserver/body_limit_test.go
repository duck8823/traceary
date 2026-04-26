package mcpserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/mcpserver"
)

func TestResolveBodyLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		bodyLimit int
		fullBody  bool
		want      int
	}{
		{name: "default kicks in when caller passes nothing", bodyLimit: 0, fullBody: false, want: mcpserver.DefaultListEventBodyLimit},
		{name: "explicit positive body_limit wins", bodyLimit: 200, fullBody: false, want: 200},
		{name: "full_body=true disables truncation", bodyLimit: 0, fullBody: true, want: 0},
		{name: "full_body wins over body_limit", bodyLimit: 200, fullBody: true, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mcpserver.ResolveBodyLimit(tc.bodyLimit, tc.fullBody); got != tc.want {
				t.Fatalf("ResolveBodyLimit(%d, %v) = %d, want %d", tc.bodyLimit, tc.fullBody, got, tc.want)
			}
		})
	}
}

func TestConvertEventsWithBodyLimit_TruncatesLongBody(t *testing.T) {
	t.Parallel()

	longBody := strings.Repeat("x", 600)
	event := model.EventOf(
		types.EventID("evt-long"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-1"),
		types.Workspace("github.com/duck8823/traceary"),
		longBody,
		time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
	)

	out := mcpserver.ConvertEventsWithBodyLimit([]*model.Event{event}, 100)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	got := out[0]
	if !got.BodyTruncated {
		t.Fatalf("BodyTruncated = false, want true for body of 600 runes vs limit 100")
	}
	if got.BodyLength != 600 {
		t.Fatalf("BodyLength = %d, want 600", got.BodyLength)
	}
	if want := 101; len([]rune(got.Body)) != want { // 100 runes + ellipsis
		t.Fatalf("len(Body) in runes = %d, want %d", len([]rune(got.Body)), want)
	}
	if !strings.HasSuffix(got.Body, "…") {
		t.Fatalf("truncated body must end with ellipsis, got %q", got.Body)
	}
}

func TestConvertEventsWithBodyLimit_FullBodyDisables(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("y", 600)
	event := model.EventOf(
		types.EventID("evt-full"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-1"),
		types.Workspace("github.com/duck8823/traceary"),
		body,
		time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
	)

	out := mcpserver.ConvertEventsWithBodyLimit([]*model.Event{event}, 0)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	got := out[0]
	if got.BodyTruncated {
		t.Fatalf("BodyTruncated = true with bodyLimit=0; want full body")
	}
	if got.Body != body {
		t.Fatalf("Body diff: got %d runes, want %d (full passthrough)", len([]rune(got.Body)), len([]rune(body)))
	}
}

func TestConvertEventsWithBodyLimit_ShortBodyUntouched(t *testing.T) {
	t.Parallel()

	body := "ok"
	event := model.EventOf(
		types.EventID("evt-short"),
		types.EventKindNote,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-1"),
		types.Workspace("github.com/duck8823/traceary"),
		body,
		time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
	)

	out := mcpserver.ConvertEventsWithBodyLimit([]*model.Event{event}, 500)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	got := out[0]
	if got.BodyTruncated || got.BodyLength != 0 {
		t.Fatalf("short body must not flip truncation flags: %+v", got)
	}
	if got.Body != body {
		t.Fatalf("Body = %q, want %q", got.Body, body)
	}
}
