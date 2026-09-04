package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	usecase "github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

var contentReliabilityCountsPattern = regexp.MustCompile(`duplicate_groups=(\d+) duplicate_records=(\d+)`)

// TestRootCLI_DoctorContentReliability_ExcludesRetentionEmptiedRows pins
// #1701's availability filter on the diagnostic itself: a genuine available
// duplicate pair is reported, and retention-emptied rows that used to hash
// as a phantom group are not. The retired content-dedupe dry-run is gone.
func TestRootCLI_DoctorContentReliability_ExcludesRetentionEmptiedRows(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	storeManagementDS := sqliteinfra.NewStoreManagementDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(storeManagementDS)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sqldb, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })

	insertEvent := func(id, kind, agent, session, workspace, body, createdAt, sourceHook, availability string) {
		t.Helper()
		if _, err := sqldb.ExecContext(ctx,
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_availability)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'hook', ?)`,
			id, kind, agent, session, workspace, body, createdAt, sourceHook, availability,
		); err != nil {
			t.Fatalf("insert %s error = %v", id, err)
		}
	}

	// Genuine duplicate pair: identical identity and body, 2s apart.
	insertEvent("evt-genuine-1", "prompt", "codex", "session-genuine", "workspace-1", "run the tests", "2026-04-10T00:00:00Z", "user_prompt_submit", "available")
	insertEvent("evt-genuine-2", "prompt", "codex", "session-genuine", "workspace-1", "run the tests", "2026-04-10T00:00:02Z", "user_prompt_submit", "available")

	// Retention-emptied rows: same marker body, same identity-defining
	// columns, 2s apart each. Pre-fix these hash to one phantom group; the
	// repair command has always excluded them via dedupeEligibilityFilter.
	marker := types.EventBodyUnavailableRetentionMarker
	insertEvent("evt-retention-1", "prompt", "codex", "session-retention", "workspace-1", marker, "2026-04-10T00:10:00Z", "user_prompt_submit", "unavailable_retention")
	insertEvent("evt-retention-2", "prompt", "codex", "session-retention", "workspace-1", marker, "2026-04-10T00:10:02Z", "user_prompt_submit", "unavailable_retention")
	insertEvent("evt-retention-3", "prompt", "codex", "session-retention", "workspace-1", marker, "2026-04-10T00:10:04Z", "user_prompt_submit", "unavailable_retention")

	// Run the shipped doctor diagnostic.
	rootCmd := newTestRootCLI(cli.WithStoreManagement(storeUC), cli.WithEvent(eventUC)).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--project-dir", t.TempDir(), "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor Execute() error = %v", err)
	}

	var report struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v\noutput: %s", err, stdout.String())
	}

	var diagnosticMessage string
	found := false
	for _, check := range report.Checks {
		if check.Name == "content-event-reliability" {
			diagnosticMessage = check.Message
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("content-event-reliability check missing from report: %s", stdout.String())
	}
	if strings.Contains(diagnosticMessage, "evt-retention") {
		t.Fatalf("diagnostic message leaked a retention-emptied row into a duplicate group: %q", diagnosticMessage)
	}

	matches := contentReliabilityCountsPattern.FindStringSubmatch(diagnosticMessage)
	if matches == nil {
		t.Fatalf("diagnostic message %q did not contain duplicate_groups=N duplicate_records=N", diagnosticMessage)
	}
	diagnosticGroups, err := strconv.Atoi(matches[1])
	if err != nil {
		t.Fatalf("parse duplicate_groups: %v", err)
	}
	diagnosticRecords, err := strconv.Atoi(matches[2])
	if err != nil {
		t.Fatalf("parse duplicate_records: %v", err)
	}

	if diagnosticGroups != 1 {
		t.Fatalf("diagnostic reported %d duplicate group(s), want 1 genuine pair (no phantom retention groups)", diagnosticGroups)
	}
	if diagnosticRecords != 2 {
		t.Fatalf("diagnostic duplicate_records = %d, want 2 (canonical + duplicate of the genuine pair)", diagnosticRecords)
	}
}
