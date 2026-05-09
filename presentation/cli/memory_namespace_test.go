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
// the top level — those are now hidden removed-alias migration stubs
// and only reachable via their grouped canonical paths.
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

// TestRootCLI_MemoryRemovedAliases_ReportReplacement covers the v0.15
// behavior contract for the retired flat memory paths: each old verb
// (remember / propose / distill / accept / reject / hygiene / graph /
// extract / supersede / expire / set-validity / import / export /
// activate) exits non-zero with a localized migration error that
// names the canonical replacement under
// `memory inbox|store|admin`. The stubs do not parse legacy flags or
// call the old use case — including `--help`, which must surface the
// removal error rather than the legacy command help.
func TestRootCLI_MemoryRemovedAliases_ReportReplacement(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		args        []string
		alias       string
		replacement string
	}{
		{
			name:        "memory remember",
			args:        []string{"memory", "remember", "--type", "decision", "--fact", "ignored"},
			alias:       "remember",
			replacement: "traceary memory store remember",
		},
		{
			name:        "memory propose",
			args:        []string{"memory", "propose", "--type", "decision", "--fact", "ignored"},
			alias:       "propose",
			replacement: "traceary memory store propose",
		},
		{
			name:        "memory distill",
			args:        []string{"memory", "distill", "--from", "memory-x", "--type", "lesson", "--fact", "ignored"},
			alias:       "distill",
			replacement: "traceary memory store distill",
		},
		{
			name:        "memory accept",
			args:        []string{"memory", "accept", "memory-x"},
			alias:       "accept",
			replacement: "traceary memory inbox accept",
		},
		{
			name:        "memory reject",
			args:        []string{"memory", "reject", "memory-x"},
			alias:       "reject",
			replacement: "traceary memory inbox reject",
		},
		{
			name:        "memory extract",
			args:        []string{"memory", "extract"},
			alias:       "extract",
			replacement: "traceary memory admin extract",
		},
		{
			name:        "memory supersede",
			args:        []string{"memory", "supersede", "memory-x", "--fact", "ignored"},
			alias:       "supersede",
			replacement: "traceary memory admin supersede",
		},
		{
			name:        "memory expire",
			args:        []string{"memory", "expire", "memory-x"},
			alias:       "expire",
			replacement: "traceary memory admin expire",
		},
		{
			name:        "memory set-validity",
			args:        []string{"memory", "set-validity", "memory-x", "--from", "2026-04-20"},
			alias:       "set-validity",
			replacement: "traceary memory admin set-validity",
		},
		{
			name:        "memory import codex",
			args:        []string{"memory", "import", "codex"},
			alias:       "import",
			replacement: "traceary memory admin import codex",
		},
		{
			name:        "memory export",
			args:        []string{"memory", "export", "--target", "codex"},
			alias:       "export",
			replacement: "traceary memory admin export",
		},
		{
			name:        "memory import bare parent",
			args:        []string{"memory", "import", "--help"},
			alias:       "import",
			replacement: "traceary memory admin import",
		},
		{
			name:        "memory activate",
			args:        []string{"memory", "activate", "--target", "codex", "--apply"},
			alias:       "activate",
			replacement: "traceary memory admin activate",
		},
		{
			name:        "memory hygiene scan",
			args:        []string{"memory", "hygiene", "scan"},
			alias:       "hygiene",
			replacement: "traceary memory admin hygiene scan",
		},
		{
			name:        "memory graph list",
			args:        []string{"memory", "graph", "list"},
			alias:       "graph",
			replacement: "traceary memory admin graph list",
		},
		// `--help` on every retired path must surface the removal
		// error too — never the legacy command help. DisableFlagParsing
		// on the stub is what makes this work.
		{
			name:        "memory remember --help",
			args:        []string{"memory", "remember", "--help"},
			alias:       "remember",
			replacement: "traceary memory store remember",
		},
		{
			name:        "memory hygiene scan --help",
			args:        []string{"memory", "hygiene", "scan", "--help"},
			alias:       "hygiene",
			replacement: "traceary memory admin hygiene scan",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// memoryUsecaseStub is wired so that any accidental fall-
			// through to the legacy use case shows up as an
			// unexpected stub call below.
			stub := &memoryUsecaseStub{}
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(stub),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs(tc.args)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want removed-alias error", tc.args)
			}
			msg := err.Error()
			if !strings.Contains(msg, "removed in v0.15.0") {
				t.Errorf("error %q missing removal version", msg)
			}
			if !strings.Contains(msg, "traceary memory "+tc.alias) {
				t.Errorf("error %q missing legacy alias name %q", msg, tc.alias)
			}
			if !strings.Contains(msg, tc.replacement) {
				t.Errorf("error %q missing replacement %q", msg, tc.replacement)
			}
			// The stub uses zero counters; if any field tracking a
			// legacy call has incremented, the guard accidentally let
			// the old use case run.
			if stub.acceptCallCount != 0 || stub.rejectCallCount != 0 || stub.expireCallCount != 0 || stub.setValidityCallCount != 0 {
				t.Errorf("legacy use case unexpectedly invoked: %+v", stub)
			}
		})
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
