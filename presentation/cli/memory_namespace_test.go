package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
	"github.com/spf13/cobra"
)

// TestRootCLI_MemoryHelp_HidesDeprecatedFlatVerbs guards the v0.14
// memory namespace reorganization (#922): `traceary memory --help`
// must advertise the grouped surface (inbox / store / admin) plus the
// daily-read commands, and it must NOT advertise the legacy flat
// implementation verbs (remember / accept / hygiene / graph / etc.) at
// the top level — those were removed in v0.15.0 and are no longer
// registered; use the grouped canonical paths instead.
func TestRootCLI_MemoryHelp_HidesDeprecatedFlatVerbs(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(memory --help) error = %v", err)
	}

	available := extractAvailableCommandsBlock(stdout.String())

	for _, want := range []string{"inbox", "store", "admin", "search", "show", "list"} {
		if !strings.Contains(available, "  "+want+" ") && !strings.Contains(available, "  "+want+"\n") {
			t.Errorf("memory --help missing grouped/daily-read entry %q in:\n%s", want, available)
		}
	}

	for _, hidden := range []string{
		"remember",
		"propose",
		"distill",
		"extract",
		"accept",
		"reject",
		"supersede",
		"expire",
		"set-validity",
		"import",
		"export",
		"activate",
		"hygiene",
		"graph",
	} {
		// Each token must not appear as a top-level entry. Because the
		// help block is "  <name>  <short>", a literal "  hygiene "
		// match would also fire on "  admin   ..." text. Restrict the
		// match to the start of an entry line.
		for _, line := range strings.Split(available, "\n") {
			trimmed := strings.TrimLeft(line, " ")
			fields := strings.Fields(trimmed)
			if len(fields) == 0 {
				continue
			}
			if fields[0] == hidden {
				t.Errorf("memory --help still advertises deprecated flat verb %q:\n%s", hidden, available)
				break
			}
		}
	}
}

// TestRootCLI_MemoryGroupedCanonicalPaths_ExecuteSameUseCase exercises
// the canonical grouped paths to confirm they remain wired to the
// underlying use cases and emit no removal/migration error of their
// own.
func TestRootCLI_MemoryGroupedCanonicalPaths_ExecuteSameUseCase(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	cases := []struct {
		name       string
		args       []string
		assertCall func(t *testing.T, stub *memoryUsecaseStub)
	}{
		{
			name: "memory store remember",
			args: []string{
				"memory", "store", "remember",
				"--db-path", "/tmp/test-traceary.db",
				"--type", "decision",
				"--fact", "Grouped store remember",
			},
			assertCall: func(t *testing.T, stub *memoryUsecaseStub) {
				if stub.rememberCall.fact != "Grouped store remember" {
					t.Errorf("rememberCall.fact = %q", stub.rememberCall.fact)
				}
			},
		},
		{
			name: "memory store propose",
			args: []string{
				"memory", "store", "propose",
				"--db-path", "/tmp/test-traceary.db",
				"--type", "decision",
				"--fact", "Grouped store propose",
			},
			assertCall: func(_ *testing.T, _ *memoryUsecaseStub) {
				// Successful Execute() above means Propose ran on the
				// stub; further field assertions live in propose-
				// specific tests.
			},
		},
		{
			name: "memory admin expire",
			args: []string{
				"memory", "admin", "expire", "memory-target",
				"--db-path", "/tmp/test-traceary.db",
			},
			assertCall: func(t *testing.T, stub *memoryUsecaseStub) {
				if stub.expireCallCount != 1 {
					t.Errorf("expireCallCount = %d, want 1", stub.expireCallCount)
				}
				if string(stub.expireCall.memoryID) != "memory-target" {
					t.Errorf("expireCall.memoryID = %q, want %q", stub.expireCall.memoryID, "memory-target")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &memoryUsecaseStub{
				rememberDetails: mustMemoryDetails(t, "memory-remembered-grouped", "Grouped store remember", types.MemoryStatusAccepted),
				proposeDetails:  mustMemoryDetails(t, "memory-proposed-grouped", "Grouped store propose", types.MemoryStatusCandidate),
				expireDetails:   mustMemoryDetails(t, "memory-expired-grouped", "Grouped admin expire", types.MemoryStatusExpired),
			}

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(stub),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs(tc.args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute(%v) error = %v", tc.args, err)
			}

			tc.assertCall(t, stub)

			if strings.Contains(stderr.String(), "DEPRECATED") {
				t.Errorf("canonical grouped path emitted deprecation notice: %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "removed in v0.15.0") {
				t.Errorf("canonical grouped path emitted removal notice: %q", stderr.String())
			}
		})
	}
}

// TestRootCLI_MemoryRemovedFlatVerbs_NotRegistered guards #1109: the v0.15
// flat memory verbs were removed and must not reappear as memory subcommands
// (hidden or visible). The help-hiding test alone would miss a hidden
// re-registration, so this asserts on the actual subcommand set.
func TestRootCLI_MemoryRemovedFlatVerbs_NotRegistered(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	var memoryCmd *cobra.Command
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "memory" {
			memoryCmd = sub
			break
		}
	}
	if memoryCmd == nil {
		t.Fatal("memory command not found")
	}

	removed := map[string]bool{
		"remember": true, "propose": true, "distill": true, "extract": true,
		"accept": true, "reject": true, "supersede": true, "expire": true,
		"set-validity": true, "import": true, "export": true, "activate": true,
		"hygiene": true, "graph": true,
	}
	for _, sub := range memoryCmd.Commands() {
		if removed[sub.Name()] {
			t.Errorf("removed v0.15 flat verb %q is still registered as a memory subcommand", sub.Name())
		}
	}
}
