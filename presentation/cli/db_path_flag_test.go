package cli_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_DBPathFlagPropagates guards the regression Codex flagged
// in the --db-path review: the flag was declared on every subcommand
// but the composition root in main.go fixed the sqlite path at startup,
// so the flag had no effect. The fix wires a DatabasePathSetter that
// each subcommand invokes after resolveDBPath. This test pins the
// behaviour across the four cases:
//   - --db-path flag passed at the subcommand position
//   - --db-path flag passed at the root position
//   - TRACEARY_DB_PATH environment variable without an explicit flag
//   - an explicit --db-path flag overriding TRACEARY_DB_PATH
func TestRootCLI_DBPathFlagPropagates(t *testing.T) {
	// Not t.Parallel(): each sub-test manipulates process-wide environ.

	sessionID, _ := types.SessionIDOf("flag-propagates")
	agent, _ := types.AgentOf("smoke")
	startEvent := model.EventOf(
		types.EventID("evt-start"),
		types.EventKindSessionStarted,
		types.Client("cli"),
		agent,
		sessionID,
		types.Workspace("ws"),
		"session started",
		time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
	)

	runCase := func(t *testing.T, args []string, envPath string, wantPath string) {
		t.Helper()

		if envPath != "" {
			t.Setenv("TRACEARY_DB_PATH", envPath)
		} else {
			t.Setenv("TRACEARY_DB_PATH", "")
		}

		var observed string
		recordPath := func(resolved string) { observed = resolved }

		storeStub := &storeManagementUsecaseStub{}
		sessionStub := &sessionUsecaseStub{startEvent: startEvent}
		root := newTestRootCLI(
			cli.WithStoreManagement(storeStub),
			cli.WithSession(sessionStub),
			cli.WithDatabasePathSetter(recordPath),
		).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(args)

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !storeStub.initCalled {
			t.Fatalf("storeManagement.Initialize was not called")
		}
		if observed != wantPath {
			t.Fatalf("DatabasePathSetter received %q, want %q", observed, wantPath)
		}
	}

	t.Run("subcommand-position --db-path", func(t *testing.T) {
		runCase(t,
			[]string{"session", "start",
				"--db-path", "/tmp/traceary-sub.db",
				"--client", "cli",
				"--agent", "smoke",
				"--workspace", "ws",
				"--session-id", "flag-propagates",
			},
			"",
			"/tmp/traceary-sub.db",
		)
	})

	t.Run("root-position --db-path", func(t *testing.T) {
		runCase(t,
			[]string{"--db-path", "/tmp/traceary-root.db",
				"session", "start",
				"--client", "cli",
				"--agent", "smoke",
				"--workspace", "ws",
				"--session-id", "flag-propagates",
			},
			"",
			"/tmp/traceary-root.db",
		)
	})

	t.Run("TRACEARY_DB_PATH env variable", func(t *testing.T) {
		runCase(t,
			[]string{"session", "start",
				"--client", "cli",
				"--agent", "smoke",
				"--workspace", "ws",
				"--session-id", "flag-propagates",
			},
			"/tmp/traceary-env.db",
			"/tmp/traceary-env.db",
		)
	})

	t.Run("--db-path flag wins over env", func(t *testing.T) {
		runCase(t,
			[]string{"session", "start",
				"--db-path", "/tmp/traceary-flag-wins.db",
				"--client", "cli",
				"--agent", "smoke",
				"--workspace", "ws",
				"--session-id", "flag-propagates",
			},
			"/tmp/traceary-env.db",
			"/tmp/traceary-flag-wins.db",
		)
	})
}
