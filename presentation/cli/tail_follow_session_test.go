package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/types"

	"github.com/duck8823/traceary/domain/model"
)

func TestValidateFollowSessionPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		wantErr  bool
		wantRune string
	}{
		{name: "empty is a no-op", input: "", wantRune: ""},
		{name: "exactly 8 runes is accepted", input: "abc12345", wantRune: "abc12345"},
		{name: "7 runes rejected", input: "abc1234", wantErr: true},
		{name: "trims whitespace then checks length", input: "   abc12345   ", wantRune: "abc12345"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateFollowSessionPrefix(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "follow-session") {
					t.Fatalf("error should mention --follow-session, got %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error = %v", err)
			}
			if got != tc.wantRune {
				t.Fatalf("got %q, want %q", got, tc.wantRune)
			}
		})
	}
}

func TestFilterEventsBySessionPrefix(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	newEvent := func(id, sessionID string) *model.Event {
		return model.EventOf(
			types.EventID(id),
			"note",
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID(sessionID),
			types.Workspace("ws"),
			"body",
			now,
		)
	}

	events := []*model.Event{
		newEvent("e1", "abc12345678"),
		newEvent("e2", "def12345999"),
		newEvent("e3", "abc12345foo"),
	}

	all := filterEventsBySessionPrefix(events, "")
	if len(all) != 3 {
		t.Fatalf("empty prefix should be a no-op, got %d", len(all))
	}

	matched := filterEventsBySessionPrefix(events, "abc12345")
	if len(matched) != 2 {
		t.Fatalf("expected 2 matches for abc12345, got %d", len(matched))
	}

	noMatch := filterEventsBySessionPrefix(events, "zzzzzzzzz")
	if len(noMatch) != 0 {
		t.Fatalf("expected no matches, got %d", len(noMatch))
	}
}
