package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_DoctorFixAppliesAuthorizedOfflineMigrations(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	stub := &storeManagementUsecaseStub{previewOffline: []int64{35, 45}}
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--fix", "--warnings-ok", "--json", "--db-path", dbPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.authorizedCalled {
		t.Fatal("InitializeAuthorized was not called")
	}
	if !strings.Contains(stdout.String(), `"applied data-dependent migrations: 35, 45"`) && !strings.Contains(stdout.String(), "applied data-dependent migrations: 35, 45") {
		t.Fatalf("stdout = %q, want applied versions", stdout.String())
	}
}

func TestRootCLI_DoctorFixDryRunDoesNotApplyOfflineMigrations(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	stub := &storeManagementUsecaseStub{previewOffline: []int64{35, 45}}
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--fix", "--dry-run", "--warnings-ok", "--json", "--db-path", dbPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.authorizedCalled {
		t.Fatal("dry-run must not call InitializeAuthorized")
	}
	if !strings.Contains(stdout.String(), "would apply data-dependent migrations 35, 45") {
		t.Fatalf("stdout = %q, want dry-run versions", stdout.String())
	}
}

func TestRootCLI_DoctorCreatesFreshEmptyStore(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: store must not exist yet: %v", err)
	}
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--warnings-ok", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s", err, stdout.String())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("doctor must create the empty store: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"name": "db-write"`) || !strings.Contains(got, "initialized SQLite store") {
		t.Fatalf("stdout = %s, want db-write initialized", got)
	}
	if !strings.Contains(got, `"name": "offline-migrations"`) || !strings.Contains(got, "no pending data-dependent migrations") {
		t.Fatalf("stdout = %s, want offline-migrations pass", got)
	}
}

func TestResolveDBPath(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		userHome   string
		wantSuffix string
		wantErr    bool
	}{
		{
			name:       "未指定時はホーム配下の .config を返す",
			input:      "",
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join(".config", "traceary", "traceary.db"),
			wantErr:    false,
		},
		{
			name:       "指定時は指定パスを絶対パス化する",
			input:      filepath.Join(".", "tmp", "traceary.db"),
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join("tmp", "traceary.db"),
			wantErr:    false,
		},
		{
			name:       "TRACEARY_DB_PATH があればそれを使う",
			input:      "",
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join("env", "traceary.db"),
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "TRACEARY_DB_PATH があればそれを使う" {
				t.Setenv("TRACEARY_DB_PATH", filepath.Join(tt.userHome, "env", "traceary.db"))
			}
			cli.SetUserHomeDirFunc(func() (string, error) {
				return tt.userHome, nil
			})
			defer cli.ResetUserHomeDirFunc()

			got, err := cli.ResolveDBPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveDBPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Fatalf("ResolveDBPath() path = %q, want suffix %q", got, tt.wantSuffix)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("ResolveDBPath() path = %q, want absolute path", got)
			}
		})
	}
}
