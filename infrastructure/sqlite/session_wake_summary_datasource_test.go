package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSessionWakeSummaryDatasource_ListEligible(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := sqlite.NewSessionDatasource(database)
	events := sqlite.NewEventDatasource(database)
	refinements := sqlite.NewSessionRefinementDatasource(database)
	sut := sqlite.NewSessionWakeSummaryDatasource(database)

	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	ws := types.Workspace("github.com/duck8823/traceary")
	otherWS := types.Workspace("github.com/other/repo")

	// Eligible top-level sessions in workspace (newest first by started_at).
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-new", ws, base.Add(3*time.Hour), "newest summary", false, "")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-mid", ws, base.Add(2*time.Hour), "middle summary", false, "")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-old", ws, base.Add(1*time.Hour), "oldest summary", false, "")

	// Exclusions: mechanical-only (degraded, no agent reasoning), other workspace,
	// subagent, waking session itself. Mixed rows stay eligible (#1877).
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-degraded", ws, base.Add(4*time.Hour), "degraded summary", true, "")
	seedWakeSessionWithReasoning(ctx, t, sessions, events, refinements, "sess-mixed", ws, base.Add(210*time.Minute), "mixed composed summary", true, true, "")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-other-ws", otherWS, base.Add(5*time.Hour), "other workspace", false, "")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-parent", ws, base, "parent of subagent", false, "")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-child", ws, base.Add(30*time.Minute), "subagent summary", false, "sess-parent")
	seedWakeSession(ctx, t, sessions, events, refinements, "sess-waking", ws, base.Add(6*time.Hour), "current session summary", false, "")

	tests := []struct {
		name      string
		workspace types.Workspace
		exclude   types.SessionID
		limit     int
		wantIDs   []string
	}{
		{
			name:      "returns rows with agent reasoning in the same workspace excluding waking session newest first",
			workspace: ws,
			exclude:   "sess-waking",
			limit:     64,
			wantIDs:   []string{"sess-mixed", "sess-new", "sess-mid", "sess-old", "sess-parent"},
		},
		{
			name:      "limit truncates rows before budget arithmetic",
			workspace: ws,
			exclude:   "sess-waking",
			limit:     2,
			wantIDs:   []string{"sess-mixed", "sess-new"},
		},
		{
			name:      "unknown workspace yields empty",
			workspace: "missing",
			exclude:   "sess-waking",
			limit:     64,
			wantIDs:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sut.ListEligible(ctx, tt.workspace, tt.exclude, tt.limit)
			if err != nil {
				t.Fatalf("ListEligible() error = %v", err)
			}
			gotIDs := make([]string, 0, len(got))
			for _, row := range got {
				gotIDs = append(gotIDs, row.SessionID.String())
				if row.Summary == "" {
					t.Fatalf("empty summary for %s", row.SessionID)
				}
			}
			if tt.wantIDs == nil {
				tt.wantIDs = []string{}
			}
			if diff := cmp.Diff(tt.wantIDs, gotIDs); diff != "" {
				t.Fatalf("session ids mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSessionWakeSummaryDatasource_MissingTableReturnsEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	// Migration 46 creates session_refinements; stop before it.
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 46))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sut := sqlite.NewSessionWakeSummaryDatasource(database)

	got, err := sut.ListEligible(ctx, "ws", "sess", 64)
	if err != nil {
		t.Fatalf("ListEligible() error = %v, want nil (missing table is empty, not failure)", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListEligible() = %#v, want empty when session_refinements is absent", got)
	}
}

func seedWakeSession(
	ctx context.Context,
	t *testing.T,
	sessions *sqlite.SessionDatasource,
	events *sqlite.EventDatasource,
	refinements *sqlite.SessionRefinementDatasource,
	sessionID string,
	workspace types.Workspace,
	startedAt time.Time,
	summary string,
	degraded bool,
	parentSessionID string,
) {
	t.Helper()
	seedWakeSessionWithReasoning(ctx, t, sessions, events, refinements, sessionID, workspace, startedAt, summary, degraded, !degraded, parentSessionID)
}

func seedWakeSessionWithReasoning(
	ctx context.Context,
	t *testing.T,
	sessions *sqlite.SessionDatasource,
	events *sqlite.EventDatasource,
	refinements *sqlite.SessionRefinementDatasource,
	sessionID string,
	workspace types.Workspace,
	startedAt time.Time,
	summary string,
	degraded bool,
	hasAgentReasoning bool,
	parentSessionID string,
) {
	t.Helper()

	var session *model.Session
	if parentSessionID != "" {
		parent, err := sessions.FindByID(ctx, types.SessionID(parentSessionID))
		if err != nil {
			t.Fatalf("FindByID(%s) error = %v", parentSessionID, err)
		}
		parentVal, ok := parent.Value()
		if !ok || parentVal == nil {
			t.Fatalf("parent session %s not found", parentSessionID)
		}
		session = model.NewChildSession(
			parentVal,
			types.SessionID(sessionID),
			startedAt,
			"codex",
			workspace,
			types.EventID(sessionID+"-spawn"),
			"explore",
			1,
		)
	} else {
		session = model.NewSession(types.SessionID(sessionID), startedAt, "cli", "codex", workspace)
	}

	boundary, err := model.NewEventWithClock(
		types.EventID(sessionID+"-start"),
		types.EventKindSessionStarted,
		"cli",
		"codex",
		types.SessionID(sessionID),
		workspace,
		"session started for "+sessionID,
		fixedEventClock{at: startedAt},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(%s) error = %v", sessionID, err)
	}
	if err := sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatalf("SaveBoundary(%s) error = %v", sessionID, err)
	}

	note, err := model.NewEventWithClock(
		types.EventID(sessionID+"-note"),
		types.EventKindNote,
		"cli",
		"codex",
		types.SessionID(sessionID),
		workspace,
		"event body must never appear in wake injection for "+sessionID,
		fixedEventClock{at: startedAt.Add(time.Second)},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(note %s) error = %v", sessionID, err)
	}
	if err := events.Save(ctx, note); err != nil {
		t.Fatalf("Save(note %s) error = %v", sessionID, err)
	}

	refinement, err := model.NewSessionRefinement(
		types.SessionID(sessionID),
		1,
		types.EventID(sessionID+"-start"),
		types.EventID(sessionID+"-note"),
		summary,
		"",
		"agent",
		startedAt.Add(2*time.Second),
		degraded,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement(%s) error = %v", sessionID, err)
	}
	refinement = refinement.WithHasAgentReasoning(hasAgentReasoning)
	ok, err := refinements.SaveIfAdvances(ctx, refinement, 0)
	if err != nil {
		t.Fatalf("SaveIfAdvances(%s) error = %v", sessionID, err)
	}
	if !ok {
		t.Fatalf("SaveIfAdvances(%s) = false, want true", sessionID)
	}
}

// Ensure the query DTO type stays imported when only used via inference.
var _ = queryservice.SessionWakeSummary{}
