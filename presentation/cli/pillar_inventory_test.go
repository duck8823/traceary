package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
	"github.com/google/go-cmp/cmp"
)

func TestRootCLI_MemoryStoreRememberEmitsV036Notice(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &memoryUsecaseStub{
		rememberDetails: mustMemoryDetails(t, "memory-remembered", "Remember release discipline", types.MemoryStatusAccepted),
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{
		"memory", "store", "remember",
		"--db-path", "/tmp/test-traceary.db",
		"--type", "decision",
		"--fact", "Remember release discipline",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantNotice := "DEPRECATED: this command is deprecated, use `traceary memory store propose` instead. Removal target: v0.36.0.\n"
	if diff := cmp.Diff(wantNotice, stderr.String()); diff != "" {
		t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(stdout.String(), "Remember release discipline") {
		t.Errorf("stdout = %q, want remembered fact", stdout.String())
	}
}

func TestRootCLI_MemoryStoreRememberHelpNamesDeprecation(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root := cli.NewRootCLI().Command()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"memory", "store", "remember", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(stderr.String(), "DEPRECATED:") {
		t.Errorf("help must not emit the run-step notice:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Deprecated; use `traceary memory store propose`") {
		t.Errorf("help stdout missing Short annotation:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Removal target: v0.36.0") {
		t.Errorf("help stdout missing removal target:\n%s", stdout.String())
	}
}

func TestRootCLI_SurfacesWithoutV036Notice(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name string
		args []string
	}{
		{name: "propose", args: []string{"memory", "store", "propose", "--db-path", "/tmp/test-traceary.db", "--type", "decision", "--fact", "Candidate"}},
		{name: "tail", args: []string{"tail", "--help"}},
		{name: "timeline", args: []string{"timeline", "--help"}},
		{name: "handoff", args: []string{"session", "handoff", "--help"}},
		{name: "hooks print", args: []string{"hooks", "print", "--help"}},
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
