package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_MemoryHelp_HidesDeprecatedFlatVerbs guards the v0.14
// memory namespace reorganization (#922): `traceary memory --help`
// must advertise the grouped surface (inbox / store / admin) plus the
// daily-read commands, and it must NOT advertise the legacy flat
// implementation verbs (remember / accept / hygiene / graph / etc.) at
// the top level — those are now only reachable through their grouped
// canonical paths or as hidden deprecated aliases.
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

// TestRootCLI_MemoryDeprecatedAliases_StillExecute_AndWarn covers the
// behavior contract for the v0.14 hidden deprecated aliases: each old
// flat path keeps executing the same use case AND emits a single
// stderr line that names the canonical replacement plus the v0.15
// removal target. JSON / stdout output stays unchanged.
func TestRootCLI_MemoryDeprecatedAliases_StillExecute_AndWarn(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	cases := []struct {
		name        string
		args        []string
		replacement string
		assertCall  func(t *testing.T, stub *memoryUsecaseStub)
	}{
		{
			name: "memory remember alias (store)",
			args: []string{
				"memory", "remember",
				"--db-path", "/tmp/test-traceary.db",
				"--type", "decision",
				"--fact", "Alias still records",
			},
			replacement: "traceary memory store remember",
			assertCall: func(t *testing.T, stub *memoryUsecaseStub) {
				if stub.rememberCall.fact != "Alias still records" {
					t.Errorf("rememberCall.fact = %q", stub.rememberCall.fact)
				}
			},
		},
		{
			name: "memory accept alias (inbox)",
			args: []string{
				"memory", "accept", "memory-target",
				"--db-path", "/tmp/test-traceary.db",
			},
			replacement: "traceary memory inbox accept",
			assertCall: func(t *testing.T, stub *memoryUsecaseStub) {
				if stub.acceptCallCount != 1 {
					t.Errorf("acceptCallCount = %d, want 1", stub.acceptCallCount)
				}
			},
		},
		{
			name: "memory reject alias (inbox)",
			args: []string{
				"memory", "reject", "memory-target",
				"--db-path", "/tmp/test-traceary.db",
			},
			replacement: "traceary memory inbox reject",
			assertCall: func(t *testing.T, stub *memoryUsecaseStub) {
				if stub.rejectCallCount != 1 {
					t.Errorf("rejectCallCount = %d, want 1", stub.rejectCallCount)
				}
			},
		},
		{
			name: "memory hygiene scan alias (subcommand-aware)",
			args: []string{
				"memory", "hygiene", "scan",
				"--db-path", "/tmp/test-traceary.db",
			},
			replacement: "traceary memory admin hygiene scan",
			assertCall: func(_ *testing.T, _ *memoryUsecaseStub) {
				// Successful Execute() above means Scan ran on the stub
				// through the deprecated alias parent. The replacement
				// assertion below proves the runtime notice names the
				// exact subcommand canonical path, not just the parent.
			},
		},
		{
			name: "memory expire alias (admin)",
			args: []string{
				"memory", "expire", "memory-target",
				"--db-path", "/tmp/test-traceary.db",
			},
			replacement: "traceary memory admin expire",
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
				rememberDetails: mustMemoryDetails(t, "memory-remembered", "Alias still records", types.MemoryStatusAccepted),
				acceptDetails:   mustMemoryDetails(t, "memory-accepted", "Accepted via alias", types.MemoryStatusAccepted),
				rejectDetails:   mustMemoryDetails(t, "memory-rejected", "Rejected via alias", types.MemoryStatusRejected),
				expireDetails:   mustMemoryDetails(t, "memory-expired", "Expired via alias", types.MemoryStatusExpired),
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

			if !strings.Contains(stderr.String(), "DEPRECATED") {
				t.Errorf("stderr missing DEPRECATED notice; got %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.replacement) {
				t.Errorf("stderr missing replacement %q; got %q", tc.replacement, stderr.String())
			}
			if !strings.Contains(stderr.String(), "v0.15") {
				t.Errorf("stderr missing v0.15 removal target; got %q", stderr.String())
			}
			// Deprecation notice must not leak into stdout (scripted
			// callers must keep parsing the response unchanged).
			if strings.Contains(stdout.String(), "DEPRECATED") {
				t.Errorf("stdout leaked deprecation notice: %q", stdout.String())
			}
		})
	}
}

// TestRootCLI_MemoryGroupedCanonicalPaths_ExecuteSameUseCase exercises
// the new canonical grouped paths and confirms they invoke the same
// underlying use cases as the legacy flat paths — without emitting a
// deprecation notice.
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
		})
	}
}
