package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
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
		eventStub := &eventUsecaseStub{
			listEvents: []*model.Event{
				model.EventOf(eventID, types.EventKindCommandExecuted, "hook", agent, sessionID, "duck8823/traceary", "go test ./...", time.Now()),
			},
		}
		sessionStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-abc"),
					types.Workspace("duck8823/traceary"),
					time.Now().Add(-time.Hour),
					types.Empty[time.Time](),
					"active",
					0,
					0,
					nil,
					"v0.2.1 sprint",
					"",
					types.SessionID(""),
				),
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event: eventStub,
			Session: sessionStub,
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
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event:            &eventUsecaseStub{},
			Session:          &sessionUsecaseStub{},
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
		eventStub := &eventUsecaseStub{}
		sessionStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("target-session"),
					types.Workspace("duck8823/traceary"),
					time.Now().Add(-time.Hour),
					types.Empty[time.Time](),
					"active",
					0,
					0,
					nil,
					"",
					"",
					types.SessionID(""),
				),
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event: eventStub,
			Session: sessionStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath, "--session-id", "target-session"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "target-session") {
			t.Errorf("output missing session ID, got: %s", stdout.String())
		}
	})

	t.Run("includes summary from compact_summary event", func(t *testing.T) {
		t.Parallel()

		eventID, _ := types.EventIDOf("e1")
		agent, _ := types.AgentOf("claude")
		sessionID, _ := types.SessionIDOf("session-abc")

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		eventStub := &eventUsecaseStub{
			listEvents: []*model.Event{
				model.EventOf(eventID, types.EventKindCompactSummary, "hook", agent, sessionID, "duck8823/traceary",
					"<summary>\n8. Current Work:\n   Implementing feature X\n9. Optional Next Step:\n   Deploy\n</summary>",
					time.Now()),
			},
		}
		sessionStub := &sessionUsecaseStub{
			listResult: []apptypes.SessionSummary{
				apptypes.SessionSummaryOf(
					types.SessionID("session-abc"),
					types.Workspace("duck8823/traceary"),
					time.Now(),
					types.Empty[time.Time](),
					"active",
					0,
					0,
					nil,
					"",
					"",
					types.SessionID(""),
				),
			},
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event:            eventStub,
			Session:          sessionStub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "summary:") {
			t.Errorf("output missing summary section, got: %s", output)
		}
		if !strings.Contains(output, "Implementing feature X") {
			t.Errorf("output missing current work content, got: %s", output)
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
			events = append(events, model.EventOf(eid, types.EventKindCommandExecuted, "hook", agent, sid, "workspace", strings.Repeat("x", 200), time.Now()))
		}

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event:            &eventUsecaseStub{listEvents: events},
			Session: &sessionUsecaseStub{
				listResult: []apptypes.SessionSummary{
					apptypes.SessionSummaryOf(
						types.SessionID("s1"),
						types.Workspace("workspace"),
						time.Now(),
						types.Empty[time.Time](),
						"active",
						0,
						0,
						nil,
						"",
						"",
						types.SessionID(""),
					),
				},
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
