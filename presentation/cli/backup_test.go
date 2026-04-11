package cli_test

import (
	"strings"
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

type createStoreBackupUsecaseStub struct {
	input  usecase.CreateStoreBackupInput
	called bool
	err    error
}

func (s *createStoreBackupUsecaseStub) Run(_ context.Context, input usecase.CreateStoreBackupInput) error {
	s.called = true
	s.input = input

	return s.err
}

type restoreStoreBackupUsecaseStub struct {
	input  usecase.RestoreStoreBackupInput
	called bool
	err    error
}

func (s *restoreStoreBackupUsecaseStub) Run(_ context.Context, input usecase.RestoreStoreBackupInput) error {
	s.called = true
	s.input = input

	return s.err
}

func TestRootCLI_BackupCreateCommand(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "traceary-backup.db")
	createBackup := &createStoreBackupUsecaseStub{}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		CreateStoreBackupUsecase: createBackup,
	}).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"backup", "create",
		"--db-path",
		"/tmp/test-traceary.db",
		"--output", outputPath,
		"--force",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !createBackup.called {
		t.Fatal("create store backup usecase was not called")
	}
	if createBackup.input.OutputPath != outputPath {
		t.Fatalf("CreateStoreBackupUsecase OutputPath = %q, want %q", createBackup.input.OutputPath, outputPath)
	}
	if !createBackup.input.Overwrite {
		t.Fatal("CreateStoreBackupUsecase Overwrite = false, want true")
	}
	if stdout.String() != "Created backup: "+outputPath+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRootCLI_BackupCreateCommand_MissingOutputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		CreateStoreBackupUsecase: &createStoreBackupUsecaseStub{},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "create"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error for missing output path")
	}
}

func TestRootCLI_BackupCreateCommand_PositionalArgument(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "traceary-backup.db")
	createBackup := &createStoreBackupUsecaseStub{}
	initStub := &initializeStoreUsecaseStub{}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:   initStub,
		CreateStoreBackupUsecase: createBackup,
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "create", "--db-path", "/tmp/test-traceary.db", outputPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if createBackup.input.OutputPath != outputPath {
		t.Fatalf("output = %q, want %q", createBackup.input.OutputPath, outputPath)
	}
}

func TestRootCLI_BackupCreateCommand_DuplicateOutputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		CreateStoreBackupUsecase: &createStoreBackupUsecaseStub{},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "create", "--output", "/tmp/a.db", "/tmp/b.db"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error for duplicate output path")
	}
}

func TestRootCLI_BackupRestoreCommand(t *testing.T) {
	t.Parallel()

	inputPath := filepath.Join(t.TempDir(), "traceary-backup.db")
	restoreBackup := &restoreStoreBackupUsecaseStub{}

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		RestoreStoreBackupUsecase: restoreBackup,
	}).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"backup", "restore",
		"--db-path",
		"/tmp/test-traceary.db",
		"--input", inputPath,
		"--force",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !restoreBackup.called {
		t.Fatal("restore store backup usecase was not called")
	}
	if restoreBackup.input.InputPath != inputPath {
		t.Fatalf("RestoreStoreBackupUsecase InputPath = %q, want %q", restoreBackup.input.InputPath, inputPath)
	}
	if !restoreBackup.input.Overwrite {
		t.Fatal("RestoreStoreBackupUsecase Overwrite = false, want true")
	}
	if !strings.Contains(stdout.String(), "Restored backup to:") {
		t.Fatalf("stdout = %q, want to contain 'Restored backup to:'", stdout.String())
	}
}

func TestRootCLI_BackupRestoreCommand_MissingInputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		RestoreStoreBackupUsecase: &restoreStoreBackupUsecaseStub{},
	}).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "restore"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if err.Error() != `required flag(s) "input" not set` {
		t.Fatalf("Execute() error = %q, want required input flag error", err.Error())
	}
}

func TestRootCLI_BackupHelp(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Create or restore SQLite-backed backups")) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
