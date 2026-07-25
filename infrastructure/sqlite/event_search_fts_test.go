package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestEventSearchFTS_PreservesLiteralVisibleTextSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	fixtures := []*model.Event{
		newSearchEventFixture(
			t,
			"event-literal",
			types.EventKindNote,
			workspace.String(),
			`literal boolean OR; say "quoted"; 100%_; C:\Traceary; ÄBC; visible needle`,
			base,
		),
		newSearchEventFixture(
			t,
			"event-envelope",
			types.EventKindTranscript,
			workspace.String(),
			`{"blocks":[{"type":"thinking","text":"secret-thinking needle"},{"type":"text","text":"visible needle response"}]}`,
			base.Add(time.Second),
		),
	}
	for _, event := range fixtures {
		if err := sut.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}
	auditEvent, audit := newSearchAuditFixture(
		t,
		"event-audit",
		workspace.String(),
		base.Add(2*time.Second),
	)
	if err := sut.SaveWithAudit(ctx, auditEvent, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "boolean-looking phrase is literal", query: "boolean OR", want: []string{"event-literal"}},
		{name: "quotes are escaped as phrase data", query: `"quoted"`, want: []string{"event-literal"}},
		{name: "LIKE wildcards stay literal", query: "100%_", want: []string{"event-literal"}},
		{name: "ASCII remains case insensitive", query: `C:\TRACEARY`, want: []string{"event-literal"}},
		{name: "matching non-ASCII case", query: "ÄBC", want: []string{"event-literal"}},
		{name: "non-ASCII remains case sensitive", query: "äbc", want: []string{}},
		{name: "thinking text is excluded", query: "secret-thinking", want: []string{}},
		{name: "persisted audit text is indexed", query: "stdout with details", want: []string{"event-audit"}},
		{name: "final timestamp and ID order", query: "visible needle", want: []string{"event-envelope", "event-literal"}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := sut.Search(
				ctx,
				tc.query,
				workspace,
				"",
				"",
				"",
				"",
				time.Time{},
				time.Time{},
				20,
				0,
				false,
			)
			if err != nil {
				t.Fatalf("Search(%q) error = %v", tc.query, err)
			}
			if diff := cmp.Diff(tc.want, eventIDs(got)); diff != "" {
				t.Fatalf("Search(%q) IDs mismatch (-want +got):\n%s", tc.query, diff)
			}
		})
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("visible needle").
		Workspace(workspace).
		Build()
	metadata, err := sut.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(
		[]string{"event-envelope", "event-literal"},
		metadataIDs(metadata),
	); diff != "" {
		t.Fatalf("metadata IDs mismatch (-want +got):\n%s", diff)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE events
		   SET body = ?, body_availability = 'unavailable_retention'
		 WHERE id = ?`,
		types.EventBodyUnavailableRetentionMarker,
		"event-literal",
	); err != nil {
		_ = db.Close()
		t.Fatalf("mark retention-unavailable: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close direct DB: %v", err)
	}
	got, err := sut.Search(
		ctx,
		"boolean OR",
		workspace,
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if err != nil {
		t.Fatalf("Search(retention-unavailable) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Search(retention-unavailable) IDs = %v, want none", eventIDs(got))
	}
}

func TestEventSearchFTS_SynchronizesCommandAuditUpdatesAndDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	workspace := types.Workspace("github.com/duck8823/traceary")
	event, audit := newSearchAuditFixture(
		t,
		"event-audit-mutation",
		workspace.String(),
		time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC),
	)
	if err := sut.SaveWithAudit(ctx, event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	searchIDs := func(query string) []string {
		t.Helper()
		events, err := sut.Search(
			ctx,
			query,
			workspace,
			"",
			"",
			"",
			"",
			time.Time{},
			time.Time{},
			20,
			0,
			false,
		)
		if err != nil {
			t.Fatalf("Search(%q) error = %v", query, err)
		}
		return eventIDs(events)
	}
	if diff := cmp.Diff(
		[]string{"event-audit-mutation"},
		searchIDs("stdout with details"),
	); diff != "" {
		t.Fatalf("initial audit IDs mismatch (-want +got):\n%s", diff)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `
		UPDATE command_audits
		   SET output_text = 'replacement audit marker'
		 WHERE event_id = 'event-audit-mutation'`); err != nil {
		t.Fatalf("update command audit: %v", err)
	}
	if got := searchIDs("stdout with details"); len(got) != 0 {
		t.Fatalf("old audit text IDs after update = %v, want none", got)
	}
	if diff := cmp.Diff(
		[]string{"event-audit-mutation"},
		searchIDs("replacement audit marker"),
	); diff != "" {
		t.Fatalf("updated audit IDs mismatch (-want +got):\n%s", diff)
	}

	if _, err := db.ExecContext(ctx, `
		DELETE FROM command_audits
		 WHERE event_id = 'event-audit-mutation'`); err != nil {
		t.Fatalf("delete command audit: %v", err)
	}
	if got := searchIDs("replacement audit marker"); len(got) != 0 {
		t.Fatalf("deleted audit text IDs = %v, want none", got)
	}
}

func TestEventSearchBackfill_IsBoundedCompleteAndResumable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preMigration := infra.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 32))
	if err := infra.NewStoreManagementDatasource(preMigration).Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-32 store: %v", err)
	}
	seedHistoricalSearchEvents(t, dbPath, 130, func(index int) string {
		if index == 0 || index == 129 {
			return "historical needle"
		}
		return "ordinary historical body"
	})

	current := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := infra.NewStoreManagementDatasource(current)
	sut := infra.NewEventDatasource(current)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("first current Initialize() error = %v", err)
	}
	documentCount, completed := eventSearchProgress(t, dbPath)
	if documentCount != 128 || completed {
		t.Fatalf("first bounded backfill = (documents=%d, completed=%v), want (128, false)", documentCount, completed)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	full, err := sut.Search(
		ctx,
		"historical needle",
		workspace,
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if err != nil {
		t.Fatalf("bounded incomplete Search() error = %v", err)
	}
	wantIDs := []string{"event-00129", "event-00000"}
	if diff := cmp.Diff(wantIDs, eventIDs(full)); diff != "" {
		t.Fatalf("bounded incomplete IDs mismatch (-want +got):\n%s", diff)
	}
	metadata, err := sut.SearchMetadata(
		ctx,
		apptypes.NewEventSearchCriteriaBuilder(20).
			Query("historical needle").
			Workspace(workspace).
			Build(),
	)
	if err != nil {
		t.Fatalf("bounded incomplete SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(wantIDs, metadataIDs(metadata)); diff != "" {
		t.Fatalf("bounded incomplete metadata IDs mismatch (-want +got):\n%s", diff)
	}

	_, err = sut.Search(
		ctx,
		"historical needle",
		"",
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if !errors.Is(err, queryservice.ErrEventSearchIndexIncomplete) {
		t.Fatalf("unbounded incomplete Search() error = %v, want ErrEventSearchIndexIncomplete", err)
	}

	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("resumed Initialize() error = %v", err)
	}
	documentCount, completed = eventSearchProgress(t, dbPath)
	if documentCount != 130 || !completed {
		t.Fatalf("resumed backfill = (documents=%d, completed=%v), want (130, true)", documentCount, completed)
	}
	full, err = sut.Search(
		ctx,
		"historical needle",
		"",
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if err != nil {
		t.Fatalf("complete unbounded Search() error = %v", err)
	}
	if diff := cmp.Diff(wantIDs, eventIDs(full)); diff != "" {
		t.Fatalf("complete IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestEventSearchShortQuery_RejectsCandidateScopeAboveHardCap(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preMigration := infra.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 32))
	if err := infra.NewStoreManagementDatasource(preMigration).Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-32 store: %v", err)
	}
	seedHistoricalSearchEvents(t, dbPath, 10_001, func(int) string { return "xy" })

	current := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := infra.NewStoreManagementDatasource(current).Initialize(ctx); err != nil {
		t.Fatalf("current Initialize() error = %v", err)
	}
	sut := infra.NewEventDatasource(current)
	_, err := sut.Search(
		ctx,
		"xy",
		types.Workspace("github.com/duck8823/traceary"),
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		20,
		0,
		false,
	)
	if !errors.Is(err, queryservice.ErrEventSearchScopeTooBroad) {
		t.Fatalf("Search() error = %v, want ErrEventSearchScopeTooBroad", err)
	}
	var unavailable *queryservice.EventSearchUnavailableError
	if !errors.As(err, &unavailable) || unavailable.CandidateCount != 10_001 {
		t.Fatalf("Search() typed error = %#v, want candidate_count=10001", unavailable)
	}
}

func seedHistoricalSearchEvents(
	t *testing.T,
	dbPath string,
	count int,
	bodyFor func(index int) string,
) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO events(
			id, kind, agent, session_id, body, created_at,
			client, workspace, body_availability
		) VALUES (?, 'note', 'codex', 'session-history', ?, ?, 'cli', ?, 'available')`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare historical event insert: %v", err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index < count; index++ {
		if _, err := statement.Exec(
			fmt.Sprintf("event-%05d", index),
			bodyFor(index),
			base.Add(time.Duration(index)*time.Second).Format(time.RFC3339Nano),
			"github.com/duck8823/traceary",
		); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatalf("insert historical event %d: %v", index, err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close historical insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit historical events: %v", err)
	}
}

func eventSearchProgress(t *testing.T, dbPath string) (documents int, completed bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_search_documents`).Scan(&documents); err != nil {
		t.Fatalf("count event search documents: %v", err)
	}
	var completedValue int
	if err := db.QueryRow(`
		SELECT completed
		  FROM event_search_backfill_state
		 WHERE singleton = 1`,
	).Scan(&completedValue); err != nil {
		t.Fatalf("read event search backfill state: %v", err)
	}
	return documents, completedValue != 0
}

func eventIDs(events []*model.Event) []string {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.EventID().String()
	}
	return ids
}
