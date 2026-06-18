package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	usecase "github.com/duck8823/traceary/application/usecase"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_HookContent_SuppressesDuplicateCodexWriteWithinWindow drives the
// real `hook prompt codex` / `hook transcript codex` commands end-to-end against
// a temp SQLite DB, reproducing the #1167 bug: a host re-firing the same hook in
// immediate succession recorded duplicate rows. After the fix, the second
// identical fire is suppressed, while a distinct body is preserved. It also
// asserts the persisted client/source_hook the hook stamps — the exact inputs
// the datasource eligibility gate keys on.
func TestRootCLI_HookContent_SuppressesDuplicateCodexWriteWithinWindow(t *testing.T) {
	tests := []struct {
		name         string
		subcommand   string
		kind         string
		sourceHook   string
		sessionID    string
		payloadSame  string
		payloadOther string
	}{
		{
			name:         "prompt",
			subcommand:   "prompt",
			kind:         "prompt",
			sourceHook:   "user_prompt_submit",
			sessionID:    "codex-e2e-prompt",
			payloadSame:  `{"prompt":"summarize the diff and run the tests","session_id":"codex-e2e-prompt","cwd":"/tmp"}`,
			payloadOther: `{"prompt":"now open a draft PR","session_id":"codex-e2e-prompt","cwd":"/tmp"}`,
		},
		{
			name:         "transcript",
			subcommand:   "transcript",
			kind:         "transcript",
			sourceHook:   "stop",
			sessionID:    "codex-e2e-transcript",
			payloadSame:  `{"last_assistant_message":"I ran the tests and they pass.","session_id":"codex-e2e-transcript","cwd":"/tmp"}`,
			payloadOther: `{"last_assistant_message":"I opened the draft PR.","session_id":"codex-e2e-transcript","cwd":"/tmp"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate hook state and pin the workspace so resolution is
			// deterministic and never reads a developer's real hook state.
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			eventDS := sqliteinfra.NewEventDatasource(db)
			storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			eventUC := usecase.NewEventUsecase(eventDS, eventDS)
			if err := storeUC.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}

			fire := func(payload string) {
				t.Helper()
				rootCmd := cli.NewRootCLI(
					cli.WithStoreManagement(storeUC),
					cli.WithEvent(eventUC),
					cli.WithDatabasePathSetter(db.SetPath),
				).Command()
				rootCmd.SetIn(strings.NewReader(payload))
				rootCmd.SetOut(&bytes.Buffer{})
				rootCmd.SetErr(&bytes.Buffer{})
				rootCmd.SetArgs([]string{"hook", tt.subcommand, "codex", "--db-path", dbPath})
				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("Execute(%s) error = %v", tt.subcommand, err)
				}
			}

			fire(tt.payloadSame)  // original
			fire(tt.payloadSame)  // identical re-fire within window -> suppressed
			fire(tt.payloadOther) // distinct body -> preserved

			sqldb, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			defer func() { _ = sqldb.Close() }()

			var count int
			if err := sqldb.QueryRow(
				`SELECT COUNT(*) FROM events WHERE kind = ? AND session_id = ?`,
				tt.kind, tt.sessionID,
			).Scan(&count); err != nil {
				t.Fatalf("count query error = %v", err)
			}
			if count != 2 {
				t.Fatalf("%s rows = %d, want 2 (identical re-fire suppressed, distinct body kept)", tt.kind, count)
			}

			var client, sourceHook string
			if err := sqldb.QueryRow(
				`SELECT client, COALESCE(source_hook, '') FROM events WHERE kind = ? AND session_id = ? LIMIT 1`,
				tt.kind, tt.sessionID,
			).Scan(&client, &sourceHook); err != nil {
				t.Fatalf("metadata query error = %v", err)
			}
			if client != "hook" {
				t.Fatalf("persisted client = %q, want hook (eligibility-gate input)", client)
			}
			if sourceHook != tt.sourceHook {
				t.Fatalf("persisted source_hook = %q, want %q", sourceHook, tt.sourceHook)
			}
		})
	}
}
