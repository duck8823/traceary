package cli_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

var updateGolden = flag.Bool("update", false, "update golden test fixtures")

func assertJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertNDJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatalf("create golden fixture directory %q: %v", filepath.Dir(fixturePath), err)
		}
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatalf("update golden fixture %q: %v", fixturePath, err)
		}
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %q: %v", fixturePath, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("golden fixture mismatch %q (-want +got):\n%s", fixturePath, diff)
	}
}

func TestGoldenHarness_NDJSONHelper(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "events.ndjson.golden")
	got := []byte("{\"event_id\":\"event-1\"}\n")
	if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
		t.Fatalf("write NDJSON fixture: %v", err)
	}

	assertNDJSONGolden(t, got, fixturePath)
}

func TestEventShow_JSON_Golden(t *testing.T) {
	eventID, err := types.EventIDFrom("event-golden-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-golden-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	eventDetails, err := apptypes.EventDetailsOf(
		model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./presentation/cli",
			time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC),
		),
		types.Some(model.CommandAuditOf(
			eventID,
			"go test ./presentation/cli",
			"stdin",
			"stdout",
			false,
			false,
			types.Some(0),
		)),
	)
	if err != nil {
		t.Fatalf("EventDetailsOf() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "--json", "event-golden-1"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "show", "with_audit.golden.json"))
}

func TestCoreReadJSONGoldens(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")

	events := []*model.Event{
		mustGoldenEvent(t, "event-golden-list-1", types.EventKindNote, "cli", "codex", "session-golden-list", "duck8823/traceary", "first golden event", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC), ""),
		mustGoldenEvent(t, "event-golden-list-2", types.EventKindPrompt, "hook", "codex", "session-golden-list", "duck8823/traceary", "prompt with hook provenance", time.Date(2026, 4, 8, 13, 5, 0, 0, time.UTC), "user_prompt_submit"),
	}

	t.Run("list empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"list", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "list", "empty.golden.json"))
	})

	t.Run("list multi event", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{listEvents: events}, nil, []string{
			"list", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "list", "multi_event.golden.json"))
	})

	t.Run("search empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json", "missing",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "search", "empty.golden.json"))
	})

	t.Run("search single event", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{searchEvents: events[:1]}, nil, []string{
			"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json", "golden",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "search", "single_event.golden.json"))
	})

	t.Run("log single event", func(t *testing.T) {
		logEvent := mustGoldenEvent(t, "event-golden-log-1", types.EventKindTranscript, "cli", "codex", "session-golden-log", "duck8823/traceary", "transcript token=[REDACTED]", time.Date(2026, 4, 8, 14, 0, 0, 0, time.UTC), "")
		stdout := executeGoldenCommand(t, &eventUsecaseStub{logEvent: logEvent}, nil, []string{
			"log", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-log", "--kind", "transcript", "--json", "transcript token=secret",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "log", "single_event.golden.json"))
	})

	t.Run("audit with redacted payload", func(t *testing.T) {
		eventID, err := types.EventIDFrom("event-golden-audit-1")
		if err != nil {
			t.Fatalf("EventIDFrom() error = %v", err)
		}
		audit := model.CommandAuditOf(
			eventID,
			"curl https://example.test",
			`{"access_token":"[REDACTED]"}`,
			"Authorization: Bearer [REDACTED]",
			false,
			true,
			types.Some(1),
		)
		audit.SetRedaction(true, true)
		auditEvent := mustGoldenEvent(t, eventID.String(), types.EventKindCommandExecuted, "cli", "codex", "session-golden-audit", "duck8823/traceary", "curl https://example.test", time.Date(2026, 4, 8, 14, 30, 0, 0, time.UTC), "")
		stdout := executeGoldenCommand(t, &eventUsecaseStub{auditEvent: auditEvent, auditAudit: audit}, nil, []string{
			"audit", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-audit", "--exit-code", "1", "--json",
			"curl https://example.test", `{"access_token":"top-secret"}`, "Authorization: Bearer token-value",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "audit", "redacted_with_audit.golden.json"))
	})

	t.Run("context empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"context", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-context", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "context", "empty.golden.json"))
	})

	t.Run("context multi event", func(t *testing.T) {
		contextEvents := []*model.Event{
			mustGoldenEvent(t, "event-golden-context-1", types.EventKindSessionStarted, "cli", "codex", "session-golden-context", "duck8823/traceary", "session started", time.Date(2026, 4, 8, 15, 0, 0, 0, time.UTC), ""),
			mustGoldenEvent(t, "event-golden-context-2", types.EventKindNote, "cli", "codex", "session-golden-context", "duck8823/traceary", "next: verify goldens", time.Date(2026, 4, 8, 15, 5, 0, 0, time.UTC), "stop"),
		}
		stdout := executeGoldenCommand(t, &eventUsecaseStub{contextEvents: contextEvents}, nil, []string{
			"context", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-context", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "context", "multi_event.golden.json"))
	})
}

func executeGoldenCommand(t *testing.T, eventStub *eventUsecaseStub, sessionStub *sessionUsecaseStub, args []string) []byte {
	t.Helper()

	options := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	}
	if sessionStub != nil {
		options = append(options, cli.WithSession(sessionStub))
	}

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(options...).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}

	return stdout.Bytes()
}

func mustGoldenEvent(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	client string,
	agentValue string,
	sessionIDValue string,
	workspace string,
	body string,
	createdAt time.Time,
	sourceHook string,
) *model.Event {
	t.Helper()

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom(%q) error = %v", eventIDValue, err)
	}
	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		t.Fatalf("AgentFrom(%q) error = %v", agentValue, err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDFrom(%q) error = %v", sessionIDValue, err)
	}

	if sourceHook != "" {
		return model.EventOfWithSourceHook(
			eventID,
			kind,
			types.Client(client),
			agent,
			sessionID,
			types.Workspace(workspace),
			body,
			createdAt,
			sourceHook,
		)
	}

	return model.EventOf(
		eventID,
		kind,
		types.Client(client),
		agent,
		sessionID,
		types.Workspace(workspace),
		body,
		createdAt,
	)
}
