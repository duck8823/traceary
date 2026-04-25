package cli_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestSessionListJSON_Goldens(t *testing.T) {
	startedAt := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	endedAt := time.Date(2026, 4, 9, 13, 30, 0, 0, time.UTC)

	cases := []struct {
		name      string
		summaries []apptypes.SessionSummary
	}{
		{
			name: "flat_without_ongoing",
			summaries: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-list-ended-a"),
					types.Workspace("duck8823/traceary"),
					startedAt,
					types.Some(endedAt),
					"ended",
					42,
					30,
					[]string{"claude", "codex"},
					"docs",
					"Document session JSON contracts.",
					types.SessionID(""),
				),
				apptypes.SessionSummaryOf(
					types.SessionID("session-list-ended-b"),
					types.Workspace("duck8823/traceary"),
					startedAt.Add(2*time.Hour),
					types.Some(startedAt.Add(3*time.Hour+15*time.Minute)),
					"ended",
					11,
					7,
					[]string{"codex"},
					"tests",
					"Lock flat session list output.",
					types.SessionID(""),
				),
			},
		},
		{
			name: "flat_with_ongoing",
			summaries: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-list-active"),
					types.Workspace("duck8823/traceary"),
					startedAt,
					types.None[time.Time](),
					"active",
					9,
					4,
					[]string{"codex"},
					"implementation",
					"Record active session list output.",
					types.SessionID("parent-session-list"),
				),
				apptypes.SessionSummaryOf(
					types.SessionID("session-list-ended"),
					types.Workspace("duck8823/traceary"),
					startedAt.Add(-2*time.Hour),
					types.Some(startedAt.Add(-30*time.Minute)),
					"ended",
					18,
					12,
					[]string{"claude"},
					"review",
					"Review existing session list behavior.",
					types.SessionID(""),
				),
			},
		},
		{
			name:      "empty",
			summaries: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := executeSessionJSONGoldenCommand(t, &sessionUsecaseStub{listResult: tc.summaries}, "session", "list", "--json")
			assertJSONGolden(t, stdout, filepath.Join("testdata", "session_list", tc.name+".golden.json"))
		})
	}
}

func TestSessionTreeJSON_Goldens(t *testing.T) {
	startedAt := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(90 * time.Minute)

	flatSummaries := []apptypes.SessionSummary{
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-flat-a"),
			types.Workspace("duck8823/traceary"),
			startedAt,
			types.Some(endedAt),
			"ended",
			15,
			8,
			[]string{"claude"},
			"planning",
			"Plan the golden coverage.",
			types.SessionID(""),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-flat-b"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(2*time.Hour),
			types.None[time.Time](),
			"active",
			6,
			2,
			[]string{"codex"},
			"implementation",
			"Implement session golden tests.",
			types.SessionID(""),
		),
	}
	nestedSummaries := []apptypes.SessionSummary{
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-root"),
			types.Workspace("duck8823/traceary"),
			startedAt,
			types.Some(endedAt),
			"ended",
			21,
			10,
			[]string{"claude"},
			"sprint",
			"Coordinate the implementation session.",
			types.SessionID(""),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-child"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(10*time.Minute),
			types.None[time.Time](),
			"active",
			7,
			5,
			[]string{"claude/explore"},
			"research",
			"Explore existing session command tests.",
			types.SessionID("session-tree-root"),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-grandchild"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(20*time.Minute),
			types.Some(startedAt.Add(35*time.Minute)),
			"ended",
			3,
			1,
			[]string{"codex/qa"},
			"verification",
			"Verify generated fixtures.",
			types.SessionID("session-tree-child"),
		),
	}
	ongoingOnlySummaries := []apptypes.SessionSummary{
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-live-root"),
			types.Workspace("duck8823/traceary"),
			startedAt,
			types.None[time.Time](),
			"active",
			5,
			2,
			[]string{"codex"},
			"live",
			"Keep active root sessions.",
			types.SessionID(""),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-ended-root"),
			types.Workspace("duck8823/traceary"),
			startedAt,
			types.Some(endedAt),
			"ended",
			12,
			8,
			[]string{"claude"},
			"done",
			"Pruned by ongoing-only.",
			types.SessionID(""),
		),
		apptypes.SessionSummaryOf(
			types.SessionID("session-tree-live-child"),
			types.Workspace("duck8823/traceary"),
			startedAt.Add(5*time.Minute),
			types.None[time.Time](),
			"active",
			4,
			3,
			[]string{"claude/planner"},
			"child-live",
			"Keep ended ancestors when a child is active.",
			types.SessionID("session-tree-ended-root"),
		),
	}

	cases := []struct {
		name      string
		summaries []apptypes.SessionSummary
		args      []string
	}{
		{name: "flat", summaries: flatSummaries},
		{name: "nested", summaries: nestedSummaries},
		{name: "ongoing_only", summaries: ongoingOnlySummaries, args: []string{"--ongoing-only"}},
		{name: "empty", summaries: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"session", "tree", "--json"}, tc.args...)
			stdout := executeSessionJSONGoldenCommand(t, &sessionUsecaseStub{listResult: tc.summaries}, args...)
			assertJSONGolden(t, stdout, filepath.Join("testdata", "session_tree", tc.name+".golden.json"))
		})
	}
}

func TestSessionBoundaryAndLookupJSON_Goldens(t *testing.T) {
	cases := []struct {
		name        string
		fixtureDir  string
		sessionStub *sessionUsecaseStub
		args        []string
	}{
		{
			name:        "single_result",
			fixtureDir:  "session_start",
			sessionStub: &sessionUsecaseStub{startEvent: sessionGoldenEvent(t, "event-session-start-golden", types.EventKindSessionStarted, "session-start-golden", time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC))},
			args:        []string{"session", "start", "--json", "--client", "cli", "--agent", "codex", "--workspace", "duck8823/traceary", "--session-id", "session-start-golden"},
		},
		{
			name:        "single_result",
			fixtureDir:  "session_end",
			sessionStub: &sessionUsecaseStub{endEvent: sessionGoldenEvent(t, "event-session-end-golden", types.EventKindSessionEnded, "session-end-golden", time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC))},
			args:        []string{"session", "end", "--json", "--client", "cli", "--agent", "codex", "--workspace", "duck8823/traceary", "--session-id", "session-end-golden"},
		},
		{
			name:        "single_result",
			fixtureDir:  "session_active",
			sessionStub: &sessionUsecaseStub{activeEvent: sessionGoldenEvent(t, "event-session-active-golden", types.EventKindSessionStarted, "session-active-golden", time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC))},
			args:        []string{"session", "active", "--json", "--allow-stale", "--client", "cli", "--agent", "codex", "--workspace", "duck8823/traceary"},
		},
		{
			name:        "single_result",
			fixtureDir:  "session_latest",
			sessionStub: &sessionUsecaseStub{latestEvent: sessionGoldenEvent(t, "event-session-latest-golden", types.EventKindSessionStarted, "session-latest-golden", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC))},
			args:        []string{"session", "latest", "--json", "--client", "cli", "--agent", "codex", "--workspace", "duck8823/traceary"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixtureDir, func(t *testing.T) {
			stdout := executeSessionJSONGoldenCommand(t, tc.sessionStub, tc.args...)
			assertJSONGolden(t, stdout, filepath.Join("testdata", tc.fixtureDir, tc.name+".golden.json"))
		})
	}
}

func executeSessionJSONGoldenCommand(t *testing.T, sessionStub *sessionUsecaseStub, args ...string) []byte {
	t.Helper()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append(args, "--db-path", "/tmp/test-traceary.db"))

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	return stdout.Bytes()
}

func sessionGoldenEvent(t *testing.T, eventIDValue string, kind types.EventKind, sessionIDValue string, createdAt time.Time) *model.Event {
	t.Helper()

	return model.EventOf(
		mustEventID(t, eventIDValue),
		kind,
		"cli",
		mustAgent(t, "codex"),
		mustSessionID(t, sessionIDValue),
		"duck8823/traceary",
		kind.String(),
		createdAt,
	)
}
