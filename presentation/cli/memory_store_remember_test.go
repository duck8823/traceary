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

// TestRootCLI_MemoryStoreRememberGoneProposeStillWritesCandidate drives the
// shipped cobra tree against a real scratch store. remember is unknown with
// no DEPRECATED line; propose still writes a candidate.
func TestRootCLI_MemoryStoreRememberGoneProposeStillWritesCandidate(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(database))
	memoryDS := sqliteinfra.NewMemoryDatasource(database)
	memoryUC := usecase.NewMemoryUsecase(memoryDS, memoryDS, nil)
	newRoot := func() *cli.RootCLI {
		return cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithMemory(memoryUC),
			cli.WithDatabasePathSetter(database.SetPath),
		)
	}

	rememberOut := &bytes.Buffer{}
	rememberErr := &bytes.Buffer{}
	rememberCmd := newRoot().Command()
	rememberCmd.SetOut(rememberOut)
	rememberCmd.SetErr(rememberErr)
	rememberCmd.SetArgs([]string{"memory", "store", "remember"})
	err := rememberCmd.Execute()
	if err == nil {
		t.Fatal("memory store remember error = nil, want unknown subcommand")
	}
	if !strings.Contains(err.Error(), `unknown subcommand "remember"`) {
		t.Fatalf("memory store remember error = %q, want unknown subcommand", err.Error())
	}
	combined := rememberOut.String() + rememberErr.String() + err.Error()
	if strings.Contains(combined, "DEPRECATED") {
		t.Fatalf("memory store remember emitted DEPRECATED:\n%s", combined)
	}

	proposeOut := &bytes.Buffer{}
	proposeErr := &bytes.Buffer{}
	proposeCmd := newRoot().Command()
	proposeCmd.SetOut(proposeOut)
	proposeCmd.SetErr(proposeErr)
	proposeCmd.SetArgs([]string{
		"memory", "store", "propose",
		"--db-path", dbPath,
		"--type", "decision",
		"--workspace", "github.com/duck8823/traceary",
		"--fact", "one-line fact",
		"--json",
	})
	if err := proposeCmd.Execute(); err != nil {
		t.Fatalf("memory store propose error = %v\nstderr=%s", err, proposeErr.String())
	}
	got := proposeOut.String()
	if !strings.Contains(got, `"status": "candidate"`) {
		t.Fatalf("propose stdout = %s, want status=candidate", got)
	}
	if !strings.Contains(got, "one-line fact") {
		t.Fatalf("propose stdout = %s, want the proposed fact", got)
	}
}
