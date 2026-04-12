package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_BackupCreateCommand(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "traceary-backup.db")

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
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
	if stdout.String() != "Created backup: "+outputPath+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRootCLI_BackupCreateCommand_MissingOutputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
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

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"backup", "create", "--db-path", "/tmp/test-traceary.db", outputPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_BackupCreateCommand_DuplicateOutputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
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

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
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
	if !strings.Contains(stdout.String(), "Restored backup to:") {
		t.Fatalf("stdout = %q, want to contain 'Restored backup to:'", stdout.String())
	}
}

func TestRootCLI_BackupRestoreCommand_MissingInputReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
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
	rootCmd := cli.NewRootCLI().Command()
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
