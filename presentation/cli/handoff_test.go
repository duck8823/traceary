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

func TestRootCLI_HandoffCommand(t *testing.T) {
	t.Parallel()

	t.Run("prints structured handoff output", func(t *testing.T) {
		t.Parallel()

		memorySummary, err := apptypes.MemorySummaryOf(
			types.MemoryID("memory-1"),
			types.MemoryTypeDecision,
			types.WorkspaceScopeOf(types.Workspace("duck8823/traceary")),
			"Keep context assembly centralized",
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

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Of(apptypes.ContextPackOf(
					types.SessionID("session-1"),
					types.Workspace("duck8823/traceary"),
					"v0.5.0",
					"active",
					20,
					4,
					[]string{"claude", "codex"},
					apptypes.WorkingStateOf("Finalize context semantics", "Wire CLI handoff to ContextUsecase"),
					[]string{"go test ./...", "go tool golangci-lint run"},
					[]apptypes.MemorySummary{memorySummary},
				)),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		for _, needle := range []string{
			"TRACEARY HANDOFF",
			"SESSION_ID: session-1",
			"WORKSPACE: duck8823/traceary",
			"WORKING_STATE:",
			"Finalize context semantics",
			"Wire CLI handoff to ContextUsecase",
			"RECENT_COMMANDS:",
			"go test ./...",
			"MEMORIES:",
			"Keep context assembly centralized",
		} {
			if !strings.Contains(output, needle) {
				t.Fatalf("output missing %q:\n%s", needle, output)
			}
		}
	})

	t.Run("session handoff subcommand reuses the same structured output", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithContext(&contextUsecaseStub{
				handoff: types.Of(apptypes.ContextPackOf(
					types.SessionID("session-2"),
					types.Workspace("duck8823/traceary"),
					"",
					"ended",
					5,
					1,
					nil,
					apptypes.WorkingStateOf("Done", ""),
					nil,
					nil,
				)),
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"session", "handoff", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "SESSION_ID: session-2") {
			t.Fatalf("output missing session handoff payload:\n%s", stdout.String())
		}
	})
}
