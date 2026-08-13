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

func TestSessionRefineJSON_Goldens(t *testing.T) {
	// Each outcome documents a row the command can actually emit:
	// created always writes generation 1 with distinct covers_from/to;
	// superseded keeps covers_from and advances covers_to with generation 2+;
	// unchanged returns the already-stored row (not a fresh write).
	producedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	existingProducedAt := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)

	mustRefine := func(sessionID types.SessionID, generation int, from, to types.EventID, summary, keywords string, at time.Time) *model.SessionRefinement {
		t.Helper()
		row, err := model.NewSessionRefinement(sessionID, generation, from, to, summary, keywords, "agent", at, false)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}

	cases := []struct {
		name       string
		outcome    model.SessionRefineOutcome
		fixture    string
		refinement *model.SessionRefinement
	}{
		{
			name:    "created",
			outcome: model.SessionRefineOutcomeCreated,
			fixture: "created.golden.json",
			// First write: generation is always 1; covers_from ≠ covers_to.
			refinement: mustRefine(
				"session-refine-created",
				1,
				"evt-from-created",
				"evt-to-created",
				"first refinement summary",
				"kw-created",
				producedAt,
			),
		},
		{
			name:    "superseded",
			outcome: model.SessionRefineOutcomeSuperseded,
			fixture: "superseded.golden.json",
			// Supersede keeps the earlier covers_from and advances covers_to.
			refinement: mustRefine(
				"session-refine-created",
				2,
				"evt-from-created",
				"evt-to-superseded",
				"merged refinement summary",
				"kw-superseded",
				producedAt,
			),
		},
		{
			name:    "unchanged",
			outcome: model.SessionRefineOutcomeUnchanged,
			fixture: "unchanged.golden.json",
			// Existing stored row returned as-is (not a write); distinct ids
			// so this fixture is not a near-copy of superseded.
			refinement: mustRefine(
				"session-refine-existing",
				3,
				"evt-from-existing",
				"evt-to-existing",
				"already stored summary",
				"kw-existing",
				existingProducedAt,
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := model.SessionRefineResultOf(tc.outcome, tc.refinement)
			if err != nil {
				t.Fatal(err)
			}
			stdout := &bytes.Buffer{}
			// Inject refinement stub the same way executeSessionJSONGoldenCommand
			// injects WithSession — keep that helper's signature unchanged.
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSessionRefinement(&sessionRefinementUsecaseStub{result: result}),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{
				"session", "refine", tc.refinement.SessionID().String(),
				"--summary", tc.refinement.Summary(),
				"--covers-to", tc.refinement.CoversToEventID().String(),
				"--keywords", tc.refinement.Keywords(),
				"--produced-by", "agent",
				"--json",
				"--db-path", "/tmp/test-traceary.db",
			})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "session_refine", tc.fixture))
		})
	}
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
