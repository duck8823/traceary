package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestStoreManagementDatasource_CloseStaleSessions_UsesLatestActivity(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	tests := []struct {
		name          string
		startedAt     time.Time
		latestEventAt *time.Time
		wantClosed    int
	}{
		{name: "old session without recent event closes", startedAt: now.Add(-48 * time.Hour), wantClosed: 1},
		{name: "old session with old event closes", startedAt: now.Add(-48 * time.Hour), latestEventAt: timePtr(now.Add(-25 * time.Hour)), wantClosed: 1},
		{name: "old session with recent event stays active", startedAt: now.Add(-48 * time.Hour), latestEventAt: timePtr(now.Add(-time.Hour)), wantClosed: 0},
		{name: "fresh session stays active", startedAt: now.Add(-time.Hour), wantClosed: 0},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			store := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err := store.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			sessionID := fmt.Sprintf("session-%d", i)
			if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at) VALUES (?, ?)`, sessionID, tt.startedAt.Format(time.RFC3339Nano)); err != nil {
				t.Fatalf("insert session: %v", err)
			}
			if tt.latestEventAt != nil {
				if _, err := db.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, source_hook, created_at) VALUES (?, 'note', 'hook', 'codex', ?, '', '', '', ?)`, "event-"+sessionID, sessionID, tt.latestEventAt.Format(time.RFC3339Nano)); err != nil {
					t.Fatalf("insert event: %v", err)
				}
			}

			dryRunCount, err := store.CloseStaleSessions(context.Background(), 24*time.Hour, true)
			if err != nil {
				t.Fatalf("CloseStaleSessions(dry-run) error = %v", err)
			}
			if dryRunCount != tt.wantClosed {
				t.Fatalf("dry-run count = %d, want %d", dryRunCount, tt.wantClosed)
			}
			closedCount, err := store.CloseStaleSessions(context.Background(), 24*time.Hour, false)
			if err != nil {
				t.Fatalf("CloseStaleSessions() error = %v", err)
			}
			if closedCount != tt.wantClosed {
				t.Fatalf("closed count = %d, want %d", closedCount, tt.wantClosed)
			}
			var endedAt sql.NullString
			if err := db.QueryRow(`SELECT ended_at FROM sessions WHERE session_id = ?`, sessionID).Scan(&endedAt); err != nil {
				t.Fatalf("select ended_at: %v", err)
			}
			if gotClosed := endedAt.Valid; gotClosed != (tt.wantClosed == 1) {
				t.Fatalf("ended_at valid = %v, want %v", gotClosed, tt.wantClosed == 1)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time { return &value }
