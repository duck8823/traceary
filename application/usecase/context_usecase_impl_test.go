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

	t.Run("skips stale active session unless explicitly allowed", func(t *testing.T) {
		t.Parallel()

		staleSession := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-stale"),
			domtypes.Workspace("duck8823/traceary"),
			time.Now().Add(-48*time.Hour),
			domtypes.None[time.Time](),
			"stale",
			1,
			0,
			[]string{"codex"},
			"",
			"old active context",
			domtypes.SessionID(""),
		)
		sut := usecase.NewContextUsecase(
			&sessionQueryServiceStub{listSummariesResult: []apptypes.SessionSummary{staleSession}},
			&eventQueryServiceStub{},
			nil,
		)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().
				StaleAfter(24*time.Hour).
				Build(),
		)
		if err != nil {
			t.Fatalf("Handoff(stale default) error = %v", err)
		}
		if _, ok := got.Value(); ok {
			t.Fatalf("Handoff(stale default) returned a pack, want empty")
		}

		allowed, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().
				StaleAfter(24*time.Hour).
				AllowStale(true).
				Build(),
		)
		if err != nil {
			t.Fatalf("Handoff(allow stale) error = %v", err)
		}
		pack, ok := allowed.Value()
		if !ok {
			t.Fatalf("Handoff(allow stale) returned empty, want stale pack")
		}
		if diff := cmp.Diff(domtypes.SessionID("session-stale"), pack.SessionID()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
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

func TestContextUsecase_Handoff_WorkspaceFallback(t *testing.T) {
	t.Parallel()

	parentWorkspace := domtypes.Workspace("/Users/duck/repos/project")
	childWorkspace := domtypes.Workspace("/Users/duck/repos/project/sub")
	siblingWorkspace := domtypes.Workspace("/Users/duck/repos/project/other")

	newParentSession := func() apptypes.SessionSummary {
		return apptypes.SessionSummaryOf(
			domtypes.SessionID("session-parent"),
			parentWorkspace,
			time.Now().Add(-time.Hour),
			domtypes.None[time.Time](),
			"active",
			5,
			3,
			[]string{"claude"},
			"",
			"",
			domtypes.SessionID(""),
		)
	}

	newChildEvent := func(t *testing.T, workspace domtypes.Workspace) *model.Event {
		t.Helper()
		event, err := model.NewEvent(
			domtypes.EventID("event-child"),
			domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			domtypes.SessionID("session-parent"),
			workspace,
			"go test ./...",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		return event
	}

	t.Run("parent session matches when child workspace has event evidence", func(t *testing.T) {
		t.Parallel()

		parent := newParentSession()
		evidence := newChildEvent(t, childWorkspace)

		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{
				parentWorkspace: {parent},
			},
		}
		eventQuery := &eventQueryServiceStub{
			listRecentResultByEvidence: map[eventEvidenceKey][]*model.Event{
				{sessionID: domtypes.SessionID("session-parent"), workspace: childWorkspace}: {evidence},
			},
			listRecentResultByKind: map[domtypes.EventKind][]*model.Event{
				domtypes.EventKindCommandExecuted: {evidence},
			},
		}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(childWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		pack, ok := got.Value()
		if !ok {
			t.Fatalf("Handoff() returned empty pack, want parent-fallback match")
		}
		if diff := cmp.Diff(parentWorkspace, pack.Workspace()); diff != "" {
			t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(childWorkspace, pack.RequestedWorkspace()); diff != "" {
			t.Errorf("RequestedWorkspace() mismatch (-want +got):\n%s", diff)
		}
		if !pack.WorkspaceFallbackUsed() {
			t.Errorf("WorkspaceFallbackUsed() = false, want true after parent fallback")
		}
	})

	t.Run("windows drive parent session matches when child workspace has event evidence", func(t *testing.T) {
		t.Parallel()

		windowsParentWorkspace := domtypes.Workspace("C:/Users/duck/repos/project")
		windowsChildWorkspace := domtypes.Workspace("C:/Users/duck/repos/project/sub")
		windowsSessionID := domtypes.SessionID("session-windows")
		parent := apptypes.SessionSummaryOf(
			windowsSessionID,
			windowsParentWorkspace,
			time.Now().Add(-time.Hour),
			domtypes.None[time.Time](),
			"active",
			5,
			3,
			[]string{"claude"},
			"",
			"",
			domtypes.SessionID(""),
		)
		evidence, err := model.NewEvent(
			domtypes.EventID("event-windows-child"),
			domtypes.EventKindCommandExecuted,
			domtypes.Client("cli"),
			domtypes.Agent("claude"),
			windowsSessionID,
			windowsChildWorkspace,
			"go test ./...",
		)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}

		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{
				windowsParentWorkspace: {parent},
			},
		}
		eventQuery := &eventQueryServiceStub{
			listRecentResultByEvidence: map[eventEvidenceKey][]*model.Event{
				{sessionID: windowsSessionID, workspace: windowsChildWorkspace}: {evidence},
			},
		}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(windowsChildWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		pack, ok := got.Value()
		if !ok {
			t.Fatalf("Handoff() returned empty pack, want Windows parent-fallback match")
		}
		if diff := cmp.Diff(windowsParentWorkspace, pack.Workspace()); diff != "" {
			t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
		}
		if !pack.WorkspaceFallbackUsed() {
			t.Errorf("WorkspaceFallbackUsed() = false, want true after Windows parent fallback")
		}
	})

	t.Run("exact workspace match wins without fallback", func(t *testing.T) {
		t.Parallel()

		exactSession := apptypes.SessionSummaryOf(
			domtypes.SessionID("session-exact"),
			childWorkspace,
			time.Now(),
			domtypes.None[time.Time](),
			"active",
			1,
			0,
			[]string{"claude"},
			"",
			"",
			domtypes.SessionID(""),
		)
		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{
				childWorkspace:  {exactSession},
				parentWorkspace: {newParentSession()},
			},
		}
		eventQuery := &eventQueryServiceStub{}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(childWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		pack, ok := got.Value()
		if !ok {
			t.Fatalf("Handoff() returned empty pack, want exact match")
		}
		if diff := cmp.Diff(domtypes.SessionID("session-exact"), pack.SessionID()); diff != "" {
			t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if pack.WorkspaceFallbackUsed() {
			t.Errorf("WorkspaceFallbackUsed() = true, want false on exact match")
		}
	})

	t.Run("sibling workspace with events does not satisfy fallback", func(t *testing.T) {
		t.Parallel()

		parent := newParentSession()
		siblingEvent := newChildEvent(t, siblingWorkspace)

		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{
				parentWorkspace: {parent},
			},
		}
		// Events exist under the sibling workspace only — the fallback
		// helper queries ListRecent with the *requested* workspace
		// (childWorkspace), so this evidence must not be visible there.
		eventQuery := &eventQueryServiceStub{
			listRecentResultByEvidence: map[eventEvidenceKey][]*model.Event{
				{sessionID: domtypes.SessionID("session-parent"), workspace: siblingWorkspace}: {siblingEvent},
			},
		}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(childWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); ok {
			t.Errorf("Handoff() returned a pack, want empty when only sibling has evidence")
		}
	})

	t.Run("no matching session under any ancestor returns empty", func(t *testing.T) {
		t.Parallel()

		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{},
		}
		eventQuery := &eventQueryServiceStub{}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(childWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); ok {
			t.Errorf("Handoff() returned a pack, want empty when no session matches")
		}
	})

	t.Run("git remote URL workspace skips fallback walk", func(t *testing.T) {
		t.Parallel()

		// Sessions exist neither at the requested URL workspace nor at
		// any URL "ancestor". The helper must not walk url path segments
		// for non-filesystem workspaces, so the result stays empty.
		remoteWorkspace := domtypes.Workspace("github.com/duck/traceary/sub")
		sessionQuery := &sessionQueryServiceStub{
			listSummariesResultByWorkspace: map[domtypes.Workspace][]apptypes.SessionSummary{
				domtypes.Workspace("github.com/duck/traceary"): {newParentSession()},
			},
		}
		eventQuery := &eventQueryServiceStub{}
		sut := usecase.NewContextUsecase(sessionQuery, eventQuery, nil)

		got, err := sut.Handoff(
			context.Background(),
			apptypes.NewContextPackCriteriaBuilder().Workspace(remoteWorkspace).Build(),
		)
		if err != nil {
			t.Fatalf("Handoff() error = %v", err)
		}
		if _, ok := got.Value(); ok {
			t.Errorf("Handoff() returned a pack, want empty for URL workspace fallback skip")
		}
		// Only the exact workspace should have been queried; no walk.
		if got := len(sessionQuery.listSummariesWorkspaceCalls); got != 1 {
			t.Errorf("ListSummaries calls = %d, want 1 (no ancestor walk for URL workspace)", got)
		}
	})
}
