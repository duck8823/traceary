package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"

	_ "modernc.org/sqlite"
)

const searchSessionNotReadyNotice = "session tier was not consulted"

// TestRootCLI_SearchParkedProjectionAgreesOnSessionNotReadyNotice drives the
// shipped search command against a real scratch store whose generation is
// parked failed (idle would auto-complete on the first search open). After
// #1822 the leftover two-transaction CLI path was --kind calling
// SearchSessionPage twice; the kind-set call is always not_applicable and
// never opens the store. This pins the remaining contract: with and without
// --kind, one not-ready notice, no kind-suppression, and two runs agree.
func TestRootCLI_SearchParkedProjectionAgreesOnSessionNotReadyNotice(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	defer cli.ResetDetectRepoContextFunc()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(database))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	eventDS := sqliteinfra.NewEventDatasource(database)
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	const token = "1859-idle-needle"
	if err := eventDS.Save(ctx, model.EventOf(
		types.EventID("evt-1859"),
		types.EventKindNote,
		"cli",
		"codex",
		"session-1859",
		"",
		token,
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// Save opens the store while the event is not yet visible to catch-up, so
	// the generation stays idle. Park it as failed so later search opens do
	// not auto-complete and hide the not-ready notice this test pins.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := raw.Exec(`UPDATE search_projection_state SET state='failed', active_generation_id=NULL, failure_class='row_work' WHERE singleton=1`); err != nil {
		_ = raw.Close()
		t.Fatalf("park projection as failed: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close scratch db: %v", err)
	}

	run := func(t *testing.T, args []string) (string, string) {
		t.Helper()
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		root := newTestRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithEvent(eventUC),
			cli.WithProjectionSessionSearch(eventDS),
			cli.WithDatabasePathSetter(database.SetPath),
		).Command()
		root.SetOut(out)
		root.SetErr(errOut)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v\nstderr=%s", args, err, errOut.String())
		}
		return out.String(), errOut.String()
	}

	assertIdleNotices := func(t *testing.T, stdout, stderr string, kindSet bool) {
		t.Helper()
		if !strings.Contains(stdout, token) {
			t.Fatalf("stdout = %q, want token %q", stdout, token)
		}
		if !strings.Contains(stderr, searchSessionNotReadyNotice) {
			t.Fatalf("stderr = %q, want not-ready notice", stderr)
		}
		if got := strings.Count(stderr, "traceary:"); got != 1 {
			t.Fatalf("notice count = %d, want 1; stderr = %q", got, stderr)
		}
		if kindSet && strings.Contains(stderr, "suppressed because --kind") {
			t.Fatalf("idle --kind search must not emit kind-suppression: %q", stderr)
		}
	}

	var firstPlainOut, firstPlainErr, firstKindOut, firstKindErr string
	for runN := 1; runN <= 2; runN++ {
		plainOut, plainErr := run(t, []string{"search", "--db-path", dbPath, token})
		kindOut, kindErr := run(t, []string{"search", "--db-path", dbPath, "--kind", "note", token})
		assertIdleNotices(t, plainOut, plainErr, false)
		assertIdleNotices(t, kindOut, kindErr, true)
		if runN == 1 {
			firstPlainOut, firstPlainErr = plainOut, plainErr
			firstKindOut, firstKindErr = kindOut, kindErr
			continue
		}
		if diff := cmp.Diff(firstPlainOut, plainOut); diff != "" {
			t.Fatalf("plain stdout disagreed across runs (-first +second):\n%s", diff)
		}
		if diff := cmp.Diff(firstPlainErr, plainErr); diff != "" {
			t.Fatalf("plain stderr disagreed across runs (-first +second):\n%s", diff)
		}
		if diff := cmp.Diff(firstKindOut, kindOut); diff != "" {
			t.Fatalf("kind stdout disagreed across runs (-first +second):\n%s", diff)
		}
		if diff := cmp.Diff(firstKindErr, kindErr); diff != "" {
			t.Fatalf("kind stderr disagreed across runs (-first +second):\n%s", diff)
		}
	}
}
