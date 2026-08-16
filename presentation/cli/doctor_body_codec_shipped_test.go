package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/google/go-cmp/cmp"

	usecase "github.com/duck8823/traceary/application/usecase"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_DoctorBodyCodec_ReportsUnknownFromRealStore drives the shipped
// doctor command against a temp SQLite store that mixes supported codecs,
// a NULL (legacy identity) row, and two unknown values. The check must
// warn on the unknowns only.
func TestRootCLI_DoctorBodyCodec_ReportsUnknownFromRealStore(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sqldb, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	insert := func(id string, codec any) {
		t.Helper()
		if _, err := sqldb.ExecContext(ctx,
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_codec)
			 VALUES (?, 'prompt', 'codex', 's1', 'w1', 'body', '2026-04-10T00:00:00Z', 'user_prompt_submit', 'hook', ?)`,
			id, codec,
		); err != nil {
			t.Fatalf("insert %s error = %v", id, err)
		}
	}
	insert("evt-identity", "identity")
	insert("evt-zstd", "zstd")
	insert("evt-null", nil)
	insert("evt-gzip", "gzip")
	insert("evt-zstd19", "zstd:19")

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithBodyCodecChecker(sqliteinfra.NewBodyCodecChecker(db)),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--project-dir", t.TempDir(), "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor Execute() error = %v\n%s", err, stdout.String())
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
	var found bool
	for _, check := range report.Checks {
		if check.Name != "body-codec" {
			continue
		}
		found = true
		if diff := cmp.Diff("warn", check.Status); diff != "" {
			t.Fatalf("body-codec status mismatch (-want +got):\n%s\nmessage=%q", diff, check.Message)
		}
		if !strings.Contains(check.Message, "gzip") || !strings.Contains(check.Message, "zstd:19") {
			t.Fatalf("body-codec message %q missing unknown codecs", check.Message)
		}
		if strings.Contains(check.Message, "identity") || strings.Contains(check.Message, "evt-null") {
			t.Fatalf("body-codec message %q leaked a supported or NULL row", check.Message)
		}
	}
	if !found {
		t.Fatalf("body-codec check missing from report: %s", stdout.String())
	}
}

func TestRootCLI_DoctorBodyCodec_PassesOnSupportedOnly(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	ctx := context.Background()
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithBodyCodecChecker(sqliteinfra.NewBodyCodecChecker(db)),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--project-dir", t.TempDir(), "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor Execute() error = %v\n%s", err, stdout.String())
	}

	var report struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v\noutput: %s", err, stdout.String())
	}
	for _, check := range report.Checks {
		if check.Name == "body-codec" {
			if diff := cmp.Diff("pass", check.Status); diff != "" {
				t.Fatalf("body-codec status mismatch (-want +got):\n%s", diff)
			}
			return
		}
	}
	t.Fatalf("body-codec check missing from report: %s", stdout.String())
}
