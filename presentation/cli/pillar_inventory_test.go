package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_MemoryStoreRememberIsUnknown(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"memory", "store", "remember"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown subcommand remember")
	}
	if !strings.Contains(err.Error(), `unknown subcommand "remember"`) {
		t.Fatalf("Execute() error = %q, want unknown subcommand remember", err.Error())
	}
	if strings.Contains(err.Error(), "DEPRECATED:") || strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
		t.Fatalf("remember must not emit DEPRECATED\nerr=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
}

func TestRootCLI_SurfacesWithoutV036Notice(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "propose", args: []string{"memory", "store", "propose", "--db-path", "/tmp/test-traceary.db", "--type", "decision", "--fact", "Candidate"}},
		{name: "list follow", args: []string{"list", "--follow", "--help"}},
		{name: "list blocks", args: []string{"list", "--blocks", "--help"}},
		{name: "context handoff", args: []string{"context", "--handoff", "--help"}},
		{name: "hooks install dry-run", args: []string{"hooks", "install", "--dry-run", "--help"}},
		{name: "memory search all", args: []string{"memory", "search", "--all", "--help"}},
		{name: "replay", args: []string{"replay", "--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(&memoryUsecaseStub{
					proposeDetails: mustMemoryDetails(t, "memory-proposed", "Candidate", types.MemoryStatusCandidate),
				}),
				cli.WithSession(&sessionUsecaseStub{}),
			).Command()
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if strings.Contains(stderr.String(), "DEPRECATED:") || strings.Contains(stdout.String(), "DEPRECATED:") {
				t.Errorf("unexpected deprecation notice\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
			}
		})
	}
}
