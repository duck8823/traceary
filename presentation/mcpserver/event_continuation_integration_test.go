package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestEventContinuationKeysetPreservesSnapshotAcrossToolsAndProjections(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"list_events", "search", "get_context"} {
		tool := tool
		for _, projection := range []string{"metadata", "bounded", "full"} {
			projection := projection
			t.Run(tool+"/"+projection, func(t *testing.T) {
				t.Parallel()

				server, datasource := newEventContinuationIntegrationServer(t)
				ctx := context.Background()
				sharedTimestamp := time.Date(2026, 7, 25, 12, 0, 0, 123456789, time.UTC)
				want := []string{"event-f", "event-e", "event-d", "event-c", "event-b", "event-a"}
				for _, eventID := range []string{"event-a", "event-b", "event-c", "event-d", "event-e", "event-f"} {
					saveEventContinuationFixture(t, datasource, eventID, "needle "+eventID, sharedTimestamp)
				}

				call := newEventContinuationToolCall(t, server, tool, projection)
				first, err := call(ctx, "", false)
				if err != nil {
					t.Fatalf("first page error = %v", err)
				}
				if first.Continuation == "" {
					t.Fatalf("first page continuation is empty: %+v", first)
				}
				if len(first.Events) != 2 {
					t.Fatalf("first page event count = %d, want 2", len(first.Events))
				}

				if _, err := call(ctx, first.Continuation, true); err == nil {
					t.Fatal("continuation with changed criteria or offset was accepted")
				}

				// A write after the first response is newer than the cursor's
				// fixed upper bound and must not shift any later page.
				saveEventContinuationFixture(
					t,
					datasource,
					"event-concurrent",
					"needle concurrent",
					time.Now().UTC().Add(time.Hour),
				)

				got := eventOutputIDs(first.Events)
				continuation := first.Continuation
				for continuation != "" {
					page, pageErr := call(ctx, continuation, false)
					if pageErr != nil {
						t.Fatalf("continuation page error = %v", pageErr)
					}
					got = append(got, eventOutputIDs(page.Events)...)
					continuation = page.Continuation
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("snapshot event IDs mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestAggregateBudgetContinuationRecoversFirstUnreturnedEvent(t *testing.T) {
	t.Parallel()

	server, datasource := newEventContinuationIntegrationServer(t)
	createdAt := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	body := strings.Repeat("x", 40_000)
	for _, eventID := range []string{"event-a", "event-b", "event-c"} {
		saveEventContinuationFixture(t, datasource, eventID, body, createdAt)
	}

	_, first, err := server.listEvents()(
		context.Background(),
		nil,
		listEventsInput{Workspace: "repo", Projection: "full", Limit: 3},
	)
	if err != nil {
		t.Fatalf("first listEvents() error = %v", err)
	}
	if got := eventOutputIDs(first.Events); cmp.Diff([]string{"event-c", "event-b"}, got) != "" {
		t.Fatalf("first page IDs = %v, want [event-c event-b]", got)
	}
	if first.Continuation == "" {
		t.Fatal("first page continuation is empty")
	}
	for _, event := range first.Events {
		if event.Body == nil {
			t.Fatalf("first page returned body-less event: %+v", event)
		}
	}

	_, second, err := server.listEvents()(
		context.Background(),
		nil,
		listEventsInput{
			Workspace: "repo", Projection: "full", Limit: 3,
			Continuation: first.Continuation,
		},
	)
	if err != nil {
		t.Fatalf("second listEvents() error = %v", err)
	}
	if got := eventOutputIDs(second.Events); cmp.Diff([]string{"event-a"}, got) != "" {
		t.Fatalf("second page IDs = %v, want [event-a]", got)
	}
	if second.Events[0].Body == nil {
		t.Fatalf("recovered event body is absent: %+v", second.Events[0])
	}
}

type eventContinuationToolCall func(context.Context, string, bool) (eventsOutput, error)

func newEventContinuationToolCall(
	t *testing.T,
	server *Server,
	tool, projection string,
) eventContinuationToolCall {
	t.Helper()
	switch tool {
	case "list_events":
		return func(ctx context.Context, continuation string, mismatch bool) (eventsOutput, error) {
			workspace := "repo"
			offset := 0
			if continuation == "" {
				workspace = " repo "
			}
			if mismatch {
				offset = 1
			}
			_, output, err := server.listEvents()(ctx, nil, listEventsInput{
				Workspace: workspace, Projection: projection, Limit: 2,
				Offset: offset, Continuation: continuation,
			})
			return output, err
		}
	case "search":
		return func(ctx context.Context, continuation string, mismatch bool) (eventsOutput, error) {
			query := "needle"
			workspace := "repo"
			if continuation == "" {
				query = " needle "
				workspace = " repo "
			}
			if mismatch {
				workspace = "other-repo"
			}
			_, output, err := server.search()(ctx, nil, searchInput{
				Query: query, Workspace: workspace, Projection: projection, Limit: 2,
				Continuation: continuation,
			})
			return output.eventsOutput, err
		}
	case "get_context":
		return func(ctx context.Context, continuation string, mismatch bool) (eventsOutput, error) {
			workspace := "repo"
			sessionID := "session-1"
			if continuation == "" {
				workspace = " repo "
				sessionID = " session-1 "
			}
			if mismatch {
				sessionID = "session-other"
			}
			_, output, err := server.getContext()(ctx, nil, getContextInput{
				Workspace: workspace, SessionID: sessionID, Projection: projection, Limit: 2,
				Continuation: continuation,
			})
			return output, err
		}
	default:
		t.Fatalf("unsupported test tool %q", tool)
		return nil
	}
}

func newEventContinuationIntegrationServer(t *testing.T) (*Server, *sqlite.EventDatasource) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() could not resolve the test source path")
	}
	migrationDir := filepath.Join(filepath.Dir(file), "..", "..", "schema", "sqlite", "migrations")
	if info, err := os.Stat(migrationDir); err != nil || !info.IsDir() {
		t.Fatalf("SQLite migration directory %q is unavailable: %v", migrationDir, err)
	}
	db := sqlite.NewDatabase(
		filepath.Join(t.TempDir(), "traceary.db"),
		os.DirFS(migrationDir),
	)
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	datasource := sqlite.NewEventDatasource(db)
	return &Server{
		event:         usecase.NewEventUsecase(datasource, datasource),
		eventMetadata: usecase.NewEventMetadataUsecase(datasource),
		eventBounded:  usecase.NewEventBoundedUsecase(datasource),
	}, datasource
}

func saveEventContinuationFixture(
	t *testing.T,
	datasource *sqlite.EventDatasource,
	eventID, body string,
	createdAt time.Time,
) {
	t.Helper()
	event := model.EventOf(
		types.EventID(eventID),
		types.EventKindNote,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-1"),
		types.Workspace("repo"),
		body,
		createdAt,
	)
	if err := datasource.Save(context.Background(), event); err != nil {
		t.Fatalf("Save(%s) error = %v", eventID, err)
	}
}

func eventOutputIDs(events []eventOutput) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	return ids
}
