package sqlite_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestEventDatasource_SumBodyBytesAfter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := types.SessionID("sess-pressure")
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	// #1185 hazard: whole-second then fractional-second timestamps must
	// order by ts_norm, not plain TEXT. Body sizes are known so the sum
	// can pin which events contributed.
	//
	// order: start (before seeds) < whole < frac < later
	// bodies: whole=100, frac=200, later=400
	seeds := []struct {
		id   string
		at   time.Time
		body string
	}{
		{id: "evt-whole", at: base, body: strings.Repeat("a", 100)},
		// 0.5s later; formatTimestamp trims trailing zeros so this persists
		// with a fractional suffix while whole does not.
		{id: "evt-frac", at: base.Add(500 * time.Millisecond), body: strings.Repeat("b", 200)},
		{id: "evt-later", at: base.Add(2 * time.Second), body: strings.Repeat("c", 400)},
	}

	session := model.NewSession(sessionID, base, "cli", "codex", "ws")
	boundary, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "codex", sessionID, "ws", "session started",
		fixedEventClock{at: base.Add(-time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		event, err := model.NewEventWithClock(
			types.EventID(seed.id), types.EventKindNote, "cli", "codex", sessionID, "ws", seed.body,
			fixedEventClock{at: seed.at},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.events.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	// start body = "session started" (15 bytes)
	startBytes := int64(len("session started"))
	wholeBytes := int64(100)
	fracBytes := int64(200)
	laterBytes := int64(400)

	tests := []struct {
		name     string
		coversTo types.Optional[types.EventID]
		want     int64
	}{
		{
			name:     "whole session sums all bodies",
			coversTo: types.None[types.EventID](),
			want:     startBytes + wholeBytes + fracBytes + laterBytes,
		},
		{
			name:     "after whole second excludes whole and start",
			coversTo: types.Some(types.EventID("evt-whole")),
			// frac and later only — if TEXT order were used, frac would look
			// *before* whole and be excluded incorrectly.
			want: fracBytes + laterBytes,
		},
		{
			name:     "after fractional second includes only later",
			coversTo: types.Some(types.EventID("evt-frac")),
			want:     laterBytes,
		},
		{
			name:     "after latest event is zero",
			coversTo: types.Some(types.EventID("evt-later")),
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := fx.events.SumBodyBytesAfter(ctx, sessionID, tt.coversTo)
			if err != nil {
				t.Fatalf("SumBodyBytesAfter() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("SumBodyBytesAfter() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEventDatasource_SumBodyBytesAfter_EmptySession(t *testing.T) {
	t.Parallel()

	fx := newSessionRefinementFixture(t)
	got, err := fx.events.SumBodyBytesAfter(
		context.Background(),
		types.SessionID("missing"),
		types.None[types.EventID](),
	)
	if err != nil {
		t.Fatalf("SumBodyBytesAfter() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("SumBodyBytesAfter() = %d, want 0", got)
	}
}

// Compile-time check that EventDatasource still satisfies the pressure port.
var _ model.SessionConsolidationPressureRepository = (*sqlite.EventDatasource)(nil)
