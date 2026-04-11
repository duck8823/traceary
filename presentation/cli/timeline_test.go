package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_TimelineCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays work blocks in text format", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event: &eventUsecaseStub{
				timelineBlocks: []*usecase.TimelineBlock{
					{
						BlockStart: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
						BlockEnd:   time.Date(2026, 4, 10, 12, 30, 0, 0, time.UTC),
						EventCount: 42,
						Workspaces: []string{"github.com/duck8823/traceary"},
						Agents:     []string{"claude"},
						KindCounts: map[string]int{"command_executed": 30, "note": 10, "prompt": 2},
					},
				},
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db", "--from", "2026-04-10"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "3h30m") {
			t.Errorf("output missing duration, got: %s", output)
		}
		if !strings.Contains(output, "github.com/duck8823/traceary") {
			t.Errorf("output missing workspace, got: %s", output)
		}
		if !strings.Contains(output, "command_executed: 30") {
			t.Errorf("output missing kind counts, got: %s", output)
		}
	})

	t.Run("displays empty message when no blocks", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event:            &eventUsecaseStub{},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(stdout.String(), "No work blocks found.") {
			t.Errorf("output missing empty message, got: %s", stdout.String())
		}
	})

	t.Run("outputs JSON format", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event: &eventUsecaseStub{
				timelineBlocks: []*usecase.TimelineBlock{
					{
						BlockStart: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
						BlockEnd:   time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC),
						EventCount: 5,
						Workspaces: []string{"ws"},
						Agents:     []string{"claude"},
						KindCounts: map[string]int{"note": 5},
					},
				},
			},
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db", "--json"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, `"event_count": 5`) {
			t.Errorf("output missing event_count, got: %s", output)
		}
		if !strings.Contains(output, `"duration": "1h0m"`) {
			t.Errorf("output missing duration, got: %s", output)
		}
	})

	t.Run("rejects invalid --from value", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: &storeMaintenanceUsecaseStub{},
			Event:            &eventUsecaseStub{},
		}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db", "--from", "not-a-date"})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error for invalid date")
		}
	})
}
