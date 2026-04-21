package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_TimelineCommand(t *testing.T) {
	t.Parallel()

	t.Run("displays work blocks in text format", func(t *testing.T) {
		t.Parallel()

		// Kinds for the single workspace: command_executed:30, note:10, prompt:2
		var kinds []string
		for i := 0; i < 30; i++ {
			kinds = append(kinds, "command_executed")
		}
		for i := 0; i < 10; i++ {
			kinds = append(kinds, "note")
		}
		for i := 0; i < 2; i++ {
			kinds = append(kinds, "prompt")
		}
		breakdown := []apptypes.TimelineWorkspaceBreakdown{
			apptypes.TimelineWorkspaceBreakdownOf(
				"github.com/duck8823/traceary",
				42,
				kinds,
				[]string{"claude"},
				"",
				apptypes.TimelineSummarySourceKindCounts,
			),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
				timelineBlocks: []apptypes.TimelineBlock{
					apptypes.TimelineBlockOf(
						time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
						time.Date(2026, 4, 10, 12, 30, 0, 0, time.UTC),
						42,
						[]string{"claude"},
						breakdown,
					),
				},
			}),
		).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db", "--from", "2026-04-10", "--utc"})

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
		if !strings.Contains(output, "total events: 42") {
			t.Errorf("output missing total event count, got: %s", output)
		}
	})

	t.Run("displays empty message when no blocks", func(t *testing.T) {
		t.Parallel()

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
		).Command()
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

		breakdown := []apptypes.TimelineWorkspaceBreakdown{
			apptypes.TimelineWorkspaceBreakdownOf(
				"ws",
				5,
				[]string{"note", "note", "note", "note", "note"},
				nil,
				"",
				apptypes.TimelineSummarySourceKindCounts,
			),
		}

		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{
				timelineBlocks: []apptypes.TimelineBlock{
					apptypes.TimelineBlockOf(
						time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
						time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC),
						5,
						[]string{"claude"},
						breakdown,
					),
				},
			}),
		).Command()
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

		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(&eventUsecaseStub{}),
		).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test.db", "--from", "not-a-date"})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error for invalid date")
		}
	})
}
