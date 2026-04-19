package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
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

// TestRunTail_FollowSessionAdvancesCursorOnUnfilteredBatch pins the regression
// for an earlier version of runTail that only advanced the tail cursor after
// filterEventsBySessionPrefix. In a busy multi-session store, a stream of
// non-matching events kept the cursor at the original start time and each
// poll re-scanned the same growing window. The runtime now advances the
// cursor on the unfiltered poll batch so the next window always starts after
// the last observed event.
func TestRunTail_FollowSessionAdvancesCursorOnUnfilteredBatch(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	otherSessionTime := time.Date(2026, 4, 18, 10, 0, 30, 0, time.UTC)
	matchedSessionTime := time.Date(2026, 4, 18, 10, 1, 0, 0, time.UTC)
	ticker := newFakeTailTicker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	otherEvent := mustTailEvent(
		t,
		"event-other",
		"cli",
		"codex",
		"abcdef1234567890",
		"ws",
		"noise from another session",
		otherSessionTime,
	)
	matchedEvent := mustTailEvent(
		t,
		"event-matched",
		"cli",
		"codex",
		"sess0001followme",
		"ws",
		"matching event",
		matchedSessionTime,
	)

	eventStub := &tailEventUsecaseStub{
		listWindowResponses: [][]*model.Event{
			{otherEvent},
			{matchedEvent},
		},
		onListWindow: func(callIndex int, criteria apptypes.EventListCriteria) {
			switch callIndex {
			case 0:
				if got := criteria.From(); !got.Equal(startTime) {
					t.Fatalf("first poll criteria.From() = %v, want %v", got, startTime)
				}
			case 1:
				if got := criteria.From(); !got.Equal(otherSessionTime) {
					t.Fatalf("second poll criteria.From() = %v, want %v (cursor must advance over unfiltered batch)", got, otherSessionTime)
				}
				cancel()
			}
		},
	}

	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
			dbPath:           "/tmp/test-traceary.db",
			limit:            0,
			repo:             "ws",
			wide:             true,
			utc:              true,
			followSession:    "sess0001",
			followSessionSet: true,
			nowFunc:          func() time.Time { return startTime },
			tickerFactory:    func(time.Duration) tailTicker { return ticker },
		})
	}()

	ticker.ch <- time.Now()
	ticker.ch <- time.Now()

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "matching event") {
		t.Fatalf("expected matched event in output, got %q", got)
	}
	if strings.Contains(stdout.String(), "noise from another session") {
		t.Fatalf("non-matching session event should have been filtered, got %q", stdout.String())
	}
}
