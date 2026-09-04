package cli

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestSkippedOneOffRepairsCheckIsSkipNotWarn(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	check := skippedOneOffRepairsCheck()
	if check.Name != "one-off-repairs" || check.Status != doctorStatusSkip {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "filesystem-metadata-only") {
		t.Fatalf("message = %q", check.Message)
	}
	if check.AutoFixAvailable {
		t.Fatal("skip check must not advertise auto-fix")
	}
	if check.FixCommand != "traceary doctor --fix" {
		t.Fatalf("FixCommand = %q", check.FixCommand)
	}
}

func TestInspectOneOffRepairsPendingWorkMatrix(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	ctx := context.Background()
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		setup          func(t *testing.T, path string)
		wantStatus     string
		wantEpoch      string
		wantWorkspace  string
		wantFixCommand bool
	}{
		{
			name: "pending-078+dirty → epoch outstanding",
			setup: func(t *testing.T, path string) {
				seedPre078Store(t, path, true, false)
			},
			wantStatus:     doctorStatusWarn,
			wantEpoch:      apptypes.OneOffRepairOutstanding,
			wantWorkspace:  apptypes.OneOffRepairOutstanding,
			wantFixCommand: true,
		},
		{
			name: "pending-078+clean → epoch never-ran",
			setup: func(t *testing.T, path string) {
				seedPre078Store(t, path, false, true)
			},
			wantStatus:     doctorStatusWarn,
			wantEpoch:      apptypes.OneOffRepairNeverRan,
			wantWorkspace:  apptypes.OneOffRepairRetired,
			wantFixCommand: true,
		},
		{
			name: "ledger-078+clean+exhausted → retired",
			setup: func(t *testing.T, path string) {
				seedPre078Store(t, path, false, true)
				if err := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, migrations)).InitializeAuthorized(ctx); err != nil {
					t.Fatalf("InitializeAuthorized: %v", err)
				}
			},
			wantStatus:     doctorStatusPass,
			wantEpoch:      apptypes.OneOffRepairRetired,
			wantWorkspace:  apptypes.OneOffRepairRetired,
			wantFixCommand: false,
		},
		{
			name: "exhausted=0 → workspace outstanding",
			setup: func(t *testing.T, path string) {
				seedPre078Store(t, path, false, false)
				if err := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, migrations)).InitializeAuthorized(ctx); err != nil {
					t.Fatalf("InitializeAuthorized: %v", err)
				}
				conn, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				if _, err := conn.Exec(`UPDATE workspace_observation_catchup_state SET exhausted = 0 WHERE singleton = 1`); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus:     doctorStatusWarn,
			wantEpoch:      apptypes.OneOffRepairRetired,
			wantWorkspace:  apptypes.OneOffRepairOutstanding,
			wantFixCommand: true,
		},
		{
			name: "exhausted=1 → workspace retired",
			setup: func(t *testing.T, path string) {
				seedPre078Store(t, path, false, true)
				if err := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, migrations)).InitializeAuthorized(ctx); err != nil {
					t.Fatalf("InitializeAuthorized: %v", err)
				}
			},
			wantStatus:     doctorStatusPass,
			wantEpoch:      apptypes.OneOffRepairRetired,
			wantWorkspace:  apptypes.OneOffRepairRetired,
			wantFixCommand: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.db")
			tt.setup(t, path)
			store := sqlite.NewDatabase(path, migrations)
			root := NewRootCLI(WithStoreManagement(usecase.NewStoreManagementUsecase(sqlite.NewStoreManagementDatasource(store))))
			check := root.inspectOneOffRepairs(ctx)
			if check.Name != "one-off-repairs" {
				t.Fatalf("name = %q", check.Name)
			}
			if check.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (message=%q)", check.Status, tt.wantStatus, check.Message)
			}
			if !strings.Contains(check.Message, "epoch-zero-hook-usage="+tt.wantEpoch) {
				t.Fatalf("message = %q, want epoch %s", check.Message, tt.wantEpoch)
			}
			if !strings.Contains(check.Message, "workspace-observations="+tt.wantWorkspace) {
				t.Fatalf("message = %q, want workspace %s", check.Message, tt.wantWorkspace)
			}
			if tt.wantFixCommand && check.FixCommand != "traceary doctor --fix" {
				t.Fatalf("FixCommand = %q", check.FixCommand)
			}
			if !tt.wantFixCommand && check.FixCommand != "" {
				t.Fatalf("FixCommand = %q, want empty", check.FixCommand)
			}
		})
	}
}

func seedPre078Store(t *testing.T, path string, dirty, exhausted bool) {
	t.Helper()
	if err := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(path, cliMigrationsBefore(t, 78))).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(pre-078): %v", err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sessionTime := time.Date(2026, 8, 15, 18, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, workspace)
VALUES ('session-doctor', ?, ?, 'test', 'test', 'ws')`, sessionTime, sessionTime); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := conn.Exec(`
INSERT INTO events (id, kind, agent, session_id, body, created_at, client, workspace)
VALUES ('event-doctor', 'session_ended', 'test', 'session-doctor', 'ended', ?, 'test', 'ws')`, sessionTime); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if exhausted {
		if _, err := conn.Exec(`UPDATE workspace_observation_catchup_state SET exhausted = 1 WHERE singleton = 1`); err != nil {
			t.Fatalf("mark exhausted: %v", err)
		}
	}
	if !dirty {
		return
	}
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
INSERT INTO usage_observations (
    observation_id, session_id, host, source_name, source_version,
    scope, accounting, status, observed_at, finalized_at, terminal_code,
    input_state, cached_input_state, cache_write_input_state,
    output_state, reasoning_output_state, total_state, cost_state
) VALUES ('grok:stop_hook:doctor', 'session-doctor', 'grok', 'stop_hook', 'schema-v1',
    'call', 'excluded', 'finalized', ?, ?, 'unknown',
    'unavailable', 'unavailable', 'unavailable',
    'unavailable', 'unavailable', 'unavailable', 'unavailable')`, epoch, epoch); err != nil {
		t.Fatalf("insert dirty usage: %v", err)
	}
}

func cliMigrationsBefore(t *testing.T, beforeVersion int) fs.FS {
	t.Helper()
	dir := filepath.Join("..", "..", "schema", "sqlite", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	migrations := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			t.Fatalf("migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			t.Fatal(err)
		}
		if version >= beforeVersion {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return migrations
}
