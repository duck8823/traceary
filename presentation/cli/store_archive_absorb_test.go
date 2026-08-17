package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_StoreCompactArchiveDryRunDispatchesToCreate(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &storeManagementUsecaseStub{}
	root := cli.NewRootCLI(cli.WithStoreManagement(stub)).Command()
	root.SetArgs([]string{"store", "compact", "--archive", "--dry-run", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	var stdout strings.Builder
	root.SetOut(&stdout)
	root.SetErr(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.archiveCreateParams.DryRun {
		t.Fatal("CreateStoreArchive DryRun = false, want true")
	}
	if stub.archiveCreateParams.DeleteAfterVerify {
		t.Fatal("dry-run must not request delete-after-verify")
	}
	if !strings.Contains(stdout.String(), "Archive candidates") {
		t.Fatalf("stdout = %q, want dry-run counts", stdout.String())
	}
}

func TestRootCLI_StoreCompactArchiveDeleteAfterVerify(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		archiveCreateResult: apptypes.StoreArchiveResult{Path: "/tmp/out.trcaryar", TotalRows: 2, DeletedAfterVerify: true, DeletedCount: 2},
	}
	root := cli.NewRootCLI(cli.WithStoreManagement(stub)).Command()
	out := filepath.Join(t.TempDir(), "out.trcaryar")
	root.SetArgs([]string{
		"store", "compact", "--archive",
		"--output", out,
		"--delete-after-verify",
		"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
	})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.archiveCreateParams.DeleteAfterVerify {
		t.Fatal("DeleteAfterVerify = false, want true")
	}
	if stub.archiveCreateParams.OutputPath != out {
		t.Fatalf("OutputPath = %q, want %q", stub.archiveCreateParams.OutputPath, out)
	}
}

func TestRootCLI_StoreCompactArchiveVerifyAndRestore(t *testing.T) {
	stub := &storeManagementUsecaseStub{}
	pkg := filepath.Join(t.TempDir(), "pack.trcaryar")
	root := cli.NewRootCLI(cli.WithStoreManagement(stub)).Command()
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"store", "compact", "--archive-verify", pkg, "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	if err := root.Execute(); err != nil {
		t.Fatalf("verify Execute() error = %v", err)
	}
	if stub.archiveVerifyPath != pkg {
		t.Fatalf("verify path = %q, want %q", stub.archiveVerifyPath, pkg)
	}

	root = cli.NewRootCLI(cli.WithStoreManagement(stub)).Command()
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"store", "compact", "--archive-restore", pkg, "--dry-run", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})
	if err := root.Execute(); err != nil {
		t.Fatalf("restore Execute() error = %v", err)
	}
	if stub.archiveRestorePath != pkg || !stub.archiveRestoreDry {
		t.Fatalf("restore path/dry = %q/%t", stub.archiveRestorePath, stub.archiveRestoreDry)
	}
}
