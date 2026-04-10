package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_CompactSummaryCommand(t *testing.T) {
	t.Parallel()

	t.Run("prints summary with active session and recent commands", func(t *testing.T) {
		t.Parallel()

		eventID, _ := types.EventIDOf("e1")
		agent, _ := types.AgentOf("claude")
		sessionID, _ := types.SessionIDOf("session-abc")

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		listEventsStub := &listEventsQueryServiceStub{
			events: []*model.Event{
				model.EventOf(eventID, types.EventKindCommandExecuted, "hook", agent, sessionID, "duck8823/traceary", "go test ./...", time.Now()),
			},
		}
		listSessionsStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID: "session-abc",
					Repo:      "duck8823/traceary",
					Label:     "v0.2.1 sprint",
					StartedAt: time.Now().Add(-time.Hour),
				},
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   initStub,
			ListEventsQueryService:   listEventsStub,
			ListSessionsQueryService: listSessionsStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "[Traceary]") {
			t.Errorf("output missing [Traceary] header")
		}
		if !strings.Contains(output, "session-abc") {
			t.Errorf("output missing session ID")
		}
		if !strings.Contains(output, "duck8823/traceary") {
			t.Errorf("output missing repo")
		}
		if !strings.Contains(output, "v0.2.1 sprint") {
			t.Errorf("output missing label")
		}
		if !strings.Contains(output, "go test ./...") {
			t.Errorf("output missing recent command")
		}
		if !strings.Contains(output, "list_events") {
			t.Errorf("output missing MCP tool reference")
		}
	})

	t.Run("prints no active session when empty", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   &initializeStoreUsecaseStub{},
			ListEventsQueryService:   &listEventsQueryServiceStub{},
			ListSessionsQueryService: &listSessionsQueryServiceStub{},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "No active session") {
			t.Errorf("output missing 'No active session', got: %s", output)
		}
	})

	t.Run("--session-id flag is passed to session query service", func(t *testing.T) {
		t.Parallel()

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		initStub := &initializeStoreUsecaseStub{}
		listEventsStub := &listEventsQueryServiceStub{}
		listSessionsStub := &listSessionsQueryServiceStub{
			summaries: []*port.SessionSummary{
				{
					SessionID: "target-session",
					Repo:      "duck8823/traceary",
					StartedAt: time.Now().Add(-time.Hour),
				},
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase:   initStub,
			ListEventsQueryService:   listEventsStub,
			ListSessionsQueryService: listSessionsStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath, "--session-id", "target-session"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if listSessionsStub.receivedInput.SessionID != "target-session" {
			t.Errorf("session query received SessionID = %q, want %q", listSessionsStub.receivedInput.SessionID, "target-session")
		}
		if !strings.Contains(stdout.String(), "target-session") {
			t.Errorf("output missing session ID, got: %s", stdout.String())
		}
	})

	t.Run("output stays within token limit", func(t *testing.T) {
		t.Parallel()

		// Generate events with long command names
		events := make([]*model.Event, 0, 10)
		for i := 0; i < 10; i++ {
			eid, _ := types.EventIDOf("e" + string(rune('0'+i)))
			agent, _ := types.AgentOf("claude")
			sid, _ := types.SessionIDOf("s1")
			events = append(events, model.EventOf(eid, types.EventKindCommandExecuted, "hook", agent, sid, "repo", strings.Repeat("x", 200), time.Now()))
		}

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: &initializeStoreUsecaseStub{},
			ListEventsQueryService: &listEventsQueryServiceStub{events: events},
			ListSessionsQueryService: &listSessionsQueryServiceStub{
				summaries: []*port.SessionSummary{{SessionID: "s1", Repo: "repo"}},
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath, "--recent", "3"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		// Rough token estimate: ~4 chars per token, 120 tokens = 480 chars
		output := stdout.String()
		if len(output) > 600 {
			t.Errorf("output too long for context injection: %d chars (target < 600)", len(output))
		}
	})
}
