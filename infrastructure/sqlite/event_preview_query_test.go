package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventPreviewQuery_ReturnsBoundedCommandBodiesWithPersistedExtent(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	largeBody := strings.Repeat("日本語-command-output-", 512*1024)
	command := model.EventOf(
		mustEventIDForSQLite(t, "event-command-large"), types.EventKindCommandExecuted,
		types.Client("cli"), mustAgentForSQLite(t, "codex"),
		mustSessionIDForSQLite(t, "session-1"), types.Workspace("ws"), largeBody, base,
	)
	note := newEventForSQLiteTest(t, "event-note", "cli", "codex", "session-1", "ws", "not a command", base.Add(time.Second))
	otherSession := model.EventOf(
		mustEventIDForSQLite(t, "event-other-session"), types.EventKindCommandExecuted,
		types.Client("cli"), mustAgentForSQLite(t, "codex"),
		mustSessionIDForSQLite(t, "session-2"), types.Workspace("ws"), "other", base.Add(2*time.Second),
	)
	for _, event := range []*model.Event{command, note, otherSession} {
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	const previewRunes = 128
	got, err := sut.ListRecentCommandPreviews(context.Background(), types.SessionID("session-1"), 10, previewRunes)
	if err != nil {
		t.Fatalf("ListRecentCommandPreviews() error = %v", err)
	}
	if len(got) != 1 || got[0].EventID() != types.EventID("event-command-large") {
		t.Fatalf("previews = %#v, want only session-1 command", got)
	}
	if runeCount := len([]rune(got[0].Body())); runeCount != previewRunes {
		t.Fatalf("preview runes = %d, want %d", runeCount, previewRunes)
	}
	if got[0].StoredBytes() != len(largeBody) {
		t.Fatalf("stored bytes = %d, want %d", got[0].StoredBytes(), len(largeBody))
	}
	if len(got[0].Body()) >= len(largeBody) {
		t.Fatalf("preview hydrated full body: preview=%d stored=%d", len(got[0].Body()), len(largeBody))
	}
}

func TestEventPreviewQuery_RejectsUnboundedInputs(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, _ := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	for _, tc := range []struct{ limit, runes int }{{0, 10}, {1, 0}, {-1, 10}, {1, -1}} {
		if _, err := sut.ListRecentCommandPreviews(context.Background(), types.SessionID("session"), tc.limit, tc.runes); err == nil {
			t.Fatalf("ListRecentCommandPreviews(limit=%d, runes=%d) error = nil", tc.limit, tc.runes)
		}
	}
}
