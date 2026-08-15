package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
		newSearchEventFixture(
			t,
			"event-envelope-tool",
			types.EventKindTranscript,
			workspace.String(),
			`{"blocks":[{"type":"thinking","text":"tool-secret-thinking"},{"type":"tool_use","text":"","id":"call-1","name":"Read"},{"type":"text","text":"visible tool response"}]}`,
			base.Add(2*time.Second),
		),
		newSearchEventFixture(
			t,
			"event-foreign-blocks",
			types.EventKindNote,
			workspace.String(),
			`{"blocks":[{"foo":"foreign-json-marker"}]}`,
			base.Add(3*time.Second),
		),
		newSearchEventFixture(
			t,
			"event-foreign-custom",
			types.EventKindNote,
			workspace.String(),
			`{"blocks":[{"type":"custom","payload":"foreign-custom-marker"}]}`,
			base.Add(4*time.Second),
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
		{name: "thinking beside unknown block is excluded", query: "tool-secret-thinking", want: []string{}},
		{name: "visible text beside unknown block is indexed", query: "visible tool response", want: []string{"event-envelope-tool"}},
		{name: "foreign blocks JSON remains raw-searchable", query: "foreign-json-marker", want: []string{"event-foreign-blocks"}},
		{name: "textless custom block remains raw-searchable", query: "foreign-custom-marker", want: []string{"event-foreign-custom"}},
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
	normalPage, err := sut.Search(ctx, "visible needle", workspace, "", "", "", "", time.Time{}, time.Time{}, 1, 1, false)
	if err != nil {
		t.Fatalf("Search(offset) error = %v", err)
	}
	if diff := cmp.Diff([]string{"event-literal"}, eventIDs(normalPage)); diff != "" {
		t.Fatalf("offset page mismatch (-want +got):\n%s", diff)
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

// A store seeded before migration 32 and then upgraded has no legacy index and
// no projection inventory for any of its history. The legacy path used to
// refuse a short query over it as too broad; the tiered walk answers from the
// canonical rows instead. This is the upgrade case #1718 makes universal.
func TestEventSearchShortQuery_AnswersUpgradedHistoryWithoutInventory(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	preMigration := infra.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 32))
	if err := infra.NewStoreManagementDatasource(preMigration).Initialize(ctx); err != nil {
		t.Fatalf("initialize pre-32 store: %v", err)
	}
	seedHistoricalSearchEvents(t, dbPath, 10_001, func(int) string { return "xy" })

	current := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := infra.NewStoreManagementDatasource(current).InitializeAuthorized(ctx); err != nil {
		t.Fatalf("current Initialize() error = %v", err)
	}
	sut := infra.NewEventDatasource(current)
	got, err := sut.Search(
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
	if err != nil {
		t.Fatalf("Search() error = %v, want a filled page", err)
	}
	if len(got) != 20 {
		t.Fatalf("Search() returned %d events, want 20", len(got))
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

func eventIDs(events []*model.Event) []string {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.EventID().String()
	}
	return ids
}
