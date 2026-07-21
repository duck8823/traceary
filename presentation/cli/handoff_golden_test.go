package cli_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestSessionHandoff_TextGoldens locks the structured handoff text shape
// because `traceary session handoff` does not expose a `--json` flag and
// downstream prompt-injection / resume tooling parses the text directly.
// The fields and ordering ("TRACEARY HANDOFF" header, SESSION_ID,
// WORKSPACE, LABEL, STATUS, TOTAL_EVENTS, COMMAND_COUNT, AGENTS,
// WORKING_STATE, RECENT_COMMANDS, MEMORIES) are part of the public
// contract and must not drift accidentally.
func TestSessionHandoff_TextGoldens(t *testing.T) {
	fixedAt := time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC)

	acceptedSummary, err := apptypes.MemorySummaryOf(
		types.MemoryID("memory-handoff-accepted"),
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"Keep handoff format stable for downstream parsers",
		types.MemoryStatusAccepted,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		fixedAt,
		types.None[time.Time](),
		fixedAt,
		fixedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf(accepted) error = %v", err)
	}
	candidateSummary, err := apptypes.MemorySummaryOf(
		types.MemoryID("memory-handoff-candidate"),
		types.MemoryTypeLesson,
		types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
		"Mark non-accepted entries with a status prefix",
		types.MemoryStatusCandidate,
		types.ConfidenceLow,
		types.MemorySourceExtracted,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		fixedAt,
		types.None[time.Time](),
		fixedAt,
		fixedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf(candidate) error = %v", err)
	}
	extent, err := apptypes.EventBodyExtentOf(types.Some(80), 80, types.Some(false), types.Some(false), types.None[int]())
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	commandItem, err := apptypes.RecentCommandSummaryOf(
		types.EventID("event-command-golden"), "go test ./...", true, extent, fixedAt,
	)
	if err != nil {
		t.Fatalf("RecentCommandSummaryOf() error = %v", err)
	}
	fullPack := apptypes.ContextPackOf(
		types.SessionID("session-handoff-golden"),
		types.Workspace("duck8823/traceary"),
		"v0.14.0",
		"active",
		42,
		9,
		[]string{"claude", "codex"},
		apptypes.WorkingStateOf(
			"Lock CLI/MCP contract surfaces before v1.0.",
			"Add MCP registry snapshot.",
		),
		[]string{"go test ./...", "go tool golangci-lint run"},
		[]apptypes.MemorySummary{acceptedSummary},
	).
		WithRecentCommandItems([]apptypes.RecentCommandSummary{commandItem}).
		WithMemoryNeedsReview([]apptypes.MemorySummary{candidateSummary}, 1).
		WithMemoryCounts(1, 1)

	cases := []struct {
		name    string
		handoff types.Optional[apptypes.ContextPack]
		fixture string
	}{
		{
			name:    "full",
			handoff: types.Some(fullPack),
			fixture: "full.golden",
		},
		{
			name: "empty",
			handoff: types.Some(apptypes.ContextPackOf(
				types.SessionID("session-handoff-empty"),
				types.Workspace(""),
				"",
				"",
				0,
				0,
				nil,
				apptypes.WorkingStateOf("", ""),
				nil,
				nil,
			)),
			fixture: "empty_pack.golden",
		},
		{
			name:    "no_match",
			handoff: types.None[apptypes.ContextPack](),
			fixture: "no_match.golden",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithContext(&contextUsecaseStub{handoff: tc.handoff}),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{
				"session", "handoff",
				"--db-path", "/tmp/test-traceary.db",
				"--session-id", "session-handoff-golden",
			})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			assertGolden(t, stdout.Bytes(), filepath.Join("testdata", "session_handoff", tc.fixture))
		})
	}
}
