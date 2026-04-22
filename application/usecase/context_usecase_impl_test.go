package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestContextUsecase_Handoff(t *testing.T) {
	t.Parallel()

	t.Run("returns empty Optional when no session matches", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewContextUsecase(&sessionQueryServiceStub{}, &eventQueryServiceStub{}, nil)
		got, err := sut.Handoff(context.Background(), apptypes.NewContextPackCriteriaBuilder().Build())
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); ok {
			t.Fatalf("Handoff() result is present, want empty")
		}
	})

	t.Run("builds context pack from session, compact summary, and accepted memories", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now().Add(-time.Hour),
			domtypes.None[time.Time](),
			"active",
			42,
			30,
			[]string{"claude", "codex"},
			"docs",
			"Wrapped up documentation task.",
			domtypes.SessionID(""),
		)
		commandEvent, err := model.NewEvent(
			domtypes.EventID("event-1"),
			domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			"go test ./...",
		)
		if err != nil {
			t.Fatalf("NewEvent() command error = %v", err)
		}
		compactEvent, err := model.NewEvent(
			domtypes.EventID("event-2"),
			domtypes.EventKindCompactSummary,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			"<summary>\n8. Current Work:\n   Finalize handoff semantics\n9. Pending Tasks:\n   Add MCP support\n</summary>",
		)
		if err != nil {
			t.Fatalf("NewEvent() compact error = %v", err)
		}
		memorySummary, err := apptypes.MemorySummaryOf(
			domtypes.MemoryID("memory-1"),
			domtypes.MemoryTypeDecision,
			domtypes.WorkspaceScopeOf(domtypes.Workspace("duck8823/traceary")),
			"Use ContextUsecase for structured handoff output",
			domtypes.MemoryStatusAccepted,
			domtypes.ConfidenceVerified,
			domtypes.MemorySourceManual,
			domtypes.None[domtypes.MemoryID](),
			domtypes.None[time.Time](),
			time.Now(),
			domtypes.None[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf() error = %v", err)
		}

		sessionQuery := &sessionQueryServiceStub{
			listSummariesResult: []apptypes.SessionSummary{session},
		}
		eventQuery := &eventQueryServiceStub{
			listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
				domtypes.EventKindCommandExecuted: {commandEvent},
				domtypes.EventKindCompactSummary:  {compactEvent},
			},
		}
		memoryQuery := &memoryQueryStub{listResult: []apptypes.MemorySummary{memorySummary}}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, memoryQuery)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().
				SessionID(domtypes.SessionID("session-1")).
				Workspace(domtypes.Workspace("duck8823/traceary")).
				RecentCommandsLimit(5).
				MemoryLimit(5).
				Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); !ok {
			t.Fatalf("Handoff() result is empty, want present")
		}

		pack, _ := got.Value()
		if diff := cmp.Diff(domtypes.SessionID("session-1"), pack.SessionID()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("Wrapped up documentation task.", pack.WorkingState().SessionSummary()); diff != "" {
			t.Fatalf("SessionSummary() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(
			"Finalize handoff semantics | Add MCP support",
			pack.WorkingState().CompactSummary(),
		); diff != "" {
			t.Fatalf("CompactSummary() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"go test ./..."}, pack.RecentCommands()); diff != "" {
			t.Fatalf("RecentCommands() mismatch (-want +got):\n%s", diff)
		}
		if len(pack.Memories()) != 1 {
			t.Fatalf("Memories() length = %d, want 1", len(pack.Memories()))
		}
		if diff := cmp.Diff(memorySummary.MemoryID(), pack.Memories()[0].MemoryID()); diff != "" {
			t.Fatalf("MemoryID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(memorySummary.Fact(), pack.Memories()[0].Fact()); diff != "" {
			t.Fatalf("Memory fact mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(5, eventQuery.listRecentLimitByKind[domtypes.EventKindCommandExecuted]); diff != "" {
			t.Fatalf("command limit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(10, eventQuery.listRecentLimitByKind[domtypes.EventKindCompactSummary]); diff != "" {
			t.Fatalf("compact limit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(domtypes.Workspace(""), eventQuery.listRecentWorkspaceByKind[domtypes.EventKindCommandExecuted]); diff != "" {
			t.Fatalf("command workspace filter mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(domtypes.Workspace("duck8823/traceary"), eventQuery.listRecentWorkspaceByKind[domtypes.EventKindCompactSummary]); diff != "" {
			t.Fatalf("compact workspace filter mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("skips memory lookup when no scopes are available", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-1"),
			domtypes.Workspace(""),
			time.Now(),
			domtypes.None[time.Time](),
			"active",
			0,
			0,
			nil,
			"",
			"",
			domtypes.SessionID(""),
		)
		sessionQuery := &sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}}
		eventQuery := &eventQueryServiceStub{}
		memoryQuery := &memoryQueryStub{}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, memoryQuery)

		got, err := sut.Handoff(context.Background(), apptypes.NewContextPackCriteriaBuilder().MemoryLimit(5).Build())
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); !ok {
			t.Fatalf("Handoff() result is empty, want present")
		}
		pack, _ := got.Value()
		if len(pack.Memories()) != 0 {
			t.Fatalf("Memories() length = %d, want 0", len(pack.Memories()))
		}
	})

	t.Run("propagates recent command query errors", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now(),
			domtypes.None[time.Time](),
			"active",
			0,
			0,
			nil,
			"",
			"",
			domtypes.SessionID(""),
		)
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
			&eventQueryServiceStub{
				listRecentErrByKind: map[domtypes.EventKind]error{
					domtypes.EventKindCommandExecuted: errors.New("commands failed"),
				},
			},
			nil,
		)

		if _, err := sut.Handoff(context.Background(), apptypes.NewContextPackCriteriaBuilder().Build()); err == nil {
			t.Fatal("Handoff() error = nil, want error")
		}
	})

	t.Run("degrades when compact summary lookup fails", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now(),
			domtypes.None[time.Time](),
			"active",
			3,
			1,
			[]string{"claude"},
			"docs",
			"Session summary",
			domtypes.SessionID(""),
		)
		commandEvent, err := model.NewEvent(
			domtypes.EventID("event-1"),
			domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			"go test ./...",
		)
		if err != nil {
			t.Fatalf("NewEvent() command error = %v", err)
		}
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
			&eventQueryServiceStub{
				listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
					domtypes.EventKindCommandExecuted: {commandEvent},
				},
				listRecentErrByKind: map[domtypes.EventKind]error{
					domtypes.EventKindCompactSummary: errors.New("compact lookup failed"),
				},
			},
			nil,
		)

		got, err := sut.Handoff(context.Background(), apptypes.NewContextPackCriteriaBuilder().Build())
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); !ok {
			t.Fatalf("Handoff() result is empty, want present")
		}

		pack, _ := got.Value()
		if diff := cmp.Diff("", pack.WorkingState().CompactSummary()); diff != "" {
			t.Fatalf("CompactSummary() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"go test ./..."}, pack.RecentCommands()); diff != "" {
			t.Fatalf("RecentCommands() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("propagates MemoryAsOf from ContextPackCriteria to the memory query builder", func(t *testing.T) {
		t.Parallel()

		asOf := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-asof"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now().Add(-time.Hour),
			domtypes.None[time.Time](),
			"active",
			1,
			0,
			[]string{"claude"},
			"",
			"",
			domtypes.SessionID(""),
		)

		memoryQuery := &memoryQueryStub{}
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
			&eventQueryServiceStub{},
			memoryQuery,
		)

		criteria := apptypes.NewContextPackCriteriaBuilder().
			SessionID(domtypes.SessionID("session-asof")).
			Workspace(domtypes.Workspace("duck8823/traceary")).
			MemoryLimit(5).
			MemoryAsOf(domtypes.Some(asOf)).
			Build()

		if _, err := sut.Handoff(context.Background(), criteria); err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}

		gotAsOf, ok := memoryQuery.listCriteria.AsOf().Value()
		if !ok {
			t.Fatalf("memory list AsOf = None, want Some(%v)", asOf)
		}
		if !gotAsOf.Equal(asOf) {
			t.Errorf("memory list AsOf = %v, want %v", gotAsOf, asOf)
		}
	})

	t.Run("omits MemoryAsOf when ContextPackCriteria does not set it", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-noasof"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now().Add(-time.Hour),
			domtypes.None[time.Time](),
			"active",
			1,
			0,
			[]string{"claude"},
			"",
			"",
			domtypes.SessionID(""),
		)

		memoryQuery := &memoryQueryStub{}
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
			&eventQueryServiceStub{},
			memoryQuery,
		)

		criteria := apptypes.NewContextPackCriteriaBuilder().
			SessionID(domtypes.SessionID("session-noasof")).
			Workspace(domtypes.Workspace("duck8823/traceary")).
			MemoryLimit(5).
			Build()

		if _, err := sut.Handoff(context.Background(), criteria); err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}

		if _, ok := memoryQuery.listCriteria.AsOf().Value(); ok {
			t.Errorf("memory list AsOf = Some(...), want None when criteria omits as-of")
		}
	})

	t.Run("skips pre-compact snapshots in favor of post-compact summary", func(t *testing.T) {
		t.Parallel()

		session := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now(),
			domtypes.None[time.Time](),
			"active",
			5, 2, nil, "", "", domtypes.SessionID(""),
		)
		// ListRecent returns events in descending time order. A
		// cancelled compact cycle can leave a pre-compact snapshot as
		// the newest compact_summary row; the builder must walk past
		// it to return the post-compact digest.
		preCompact, err := model.NewEvent(
			domtypes.EventID("event-pre"),
			domtypes.EventKindCompactSummary,
			domtypes.Client("hook"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			domtypes.EventBodyMarkerCompactPreSnapshot+" cancelled snapshot",
		)
		if err != nil {
			t.Fatalf("NewEvent() pre-compact error = %v", err)
		}
		postCompact, err := model.NewEvent(
			domtypes.EventID("event-post"),
			domtypes.EventKindCompactSummary,
			domtypes.Client("hook"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-1"),
			domtypes.Workspace("duck8823/traceary"),
			"<summary>\n8. Current Work:\n   Wired SubagentStop + PreCompact\n</summary>",
		)
		if err != nil {
			t.Fatalf("NewEvent() post-compact error = %v", err)
		}
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{session}},
			&eventQueryServiceStub{
				listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
					domtypes.EventKindCompactSummary: {preCompact, postCompact},
				},
			},
			nil,
		)
		got, err := sut.Handoff(context.Background(), apptypes.NewContextPackCriteriaBuilder().Build())
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		pack, ok := got.Value()
		if !ok {
			t.Fatalf("Handoff() returned empty context pack")
		}
		compact := pack.WorkingState().CompactSummary()
		if strings.Contains(compact, "cancelled snapshot") {
			t.Errorf("context pack picked up pre-compact snapshot; compact summary = %q", compact)
		}
		if !strings.Contains(compact, "Wired SubagentStop") {
			t.Errorf("context pack missing post-compact summary; compact = %q", compact)
		}
	})
}
