package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_CompactSummaryCommand(t *testing.T) {
	t.Parallel()

	t.Run("prints summary with active session and recent commands", func(t *testing.T) {
		t.Parallel()

		sessionID, _ := types.SessionIDOf("session-abc")

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		contextStub := &contextUsecaseStub{
			handoff: types.Of(apptypes.ContextPackOf(
				sessionID,
				types.Workspace("duck8823/traceary"),
				"v0.2.1 sprint",
				"active",
				12,
				3,
				[]string{"claude"},
				apptypes.WorkingStateOf("Continue docs pass", "Implementing feature X"),
				[]string{"go test ./..."},
				nil,
			)),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(contextStub),
		).Command()
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
		if !strings.Contains(output, "Continue docs pass | Implementing feature X") {
			t.Errorf("output missing combined summary")
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
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{}),
		).Command()
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
		contextStub := &contextUsecaseStub{
			handoff: types.Of(apptypes.ContextPackOf(
				types.SessionID("target-session"),
				types.Workspace("duck8823/traceary"),
				"",
				"active",
				0,
				0,
				nil,
				apptypes.WorkingStateOf("", ""),
				nil,
				nil,
			)),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(contextStub),
		).Command()
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

	t.Run("includes memories when available", func(t *testing.T) {
		t.Parallel()

		sessionID, _ := types.SessionIDOf("session-abc")
		scope := types.WorkspaceScopeOf(types.Workspace("duck8823/traceary"))
		memorySummary, err := apptypes.MemorySummaryOf(
			types.MemoryID("memory-1"),
			types.MemoryTypeDecision,
			scope,
			"Use ContextUsecase for structured handoff output",
			types.MemoryStatusAccepted,
			types.ConfidenceVerified,
			types.MemorySourceManual,
			types.Empty[types.MemoryID](),
			types.Empty[time.Time](),
			time.Now(),
			time.Now(),
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf() error = %v", err)
		}

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		contextStub := &contextUsecaseStub{
			handoff: types.Of(apptypes.ContextPackOf(
				sessionID,
				types.Workspace("duck8823/traceary"),
				"",
				"active",
				0,
				0,
				nil,
				apptypes.WorkingStateOf("", "Implementing feature X"),
				nil,
				[]apptypes.MemorySummary{memorySummary},
			)),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(contextStub),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"compact-summary", "--db-path", dbPath})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "memories:") {
			t.Errorf("output missing memories section, got: %s", output)
		}
		if !strings.Contains(output, "Use ContextUsecase for structured handoff output") {
			t.Errorf("output missing memory fact, got: %s", output)
		}
	})

	t.Run("output stays within token limit", func(t *testing.T) {
		t.Parallel()

		commands := make([]string, 0, 10)
		for i := 0; i < 10; i++ {
			commands = append(commands, strings.Repeat("x", 200))
		}

		dbPath := filepath.Join(t.TempDir(), "traceary.db")
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Of(apptypes.ContextPackOf(
					types.SessionID("s1"),
					types.Workspace("workspace"),
					"",
					"active",
					0,
					0,
					nil,
					apptypes.WorkingStateOf("", strings.Repeat("summary ", 40)),
					commands,
					nil,
				)),
			}),
		).Command()
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
