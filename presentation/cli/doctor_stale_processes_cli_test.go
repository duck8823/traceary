package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_DoctorWarnsOnSyntheticStaleTracearyProcess(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	now := time.Now()
	cli.SetListTracearyProcessSnapshotsForTest(func() ([]cli.TracearyProcessSnapshot, error) {
		return []cli.TracearyProcessSnapshot{{
			PID:        7766,
			Executable: "/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary",
			Args:       []string{"/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary", "mcp-server"},
			StartedAt:  now.Add(-13 * 24 * time.Hour),
		}}, nil
	})
	t.Cleanup(cli.ResetListTracearyProcessSnapshotsForTest)

	dbPath := t.TempDir() + "/traceary.db"
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.Version = "0.44.1"
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--client", "codex", "--project-dir", projectDir, "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s", err, stdout.String())
	}
	report := decodeDoctorReport(t, stdout.Bytes())
	var found *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "stale-processes" {
			found = &report.Checks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("stale-processes check missing from report: %s", stdout.String())
	}
	if found.Status != "warn" {
		t.Fatalf("status = %q, want warn; msg=%q", found.Status, found.Message)
	}
	if !strings.Contains(found.Message, "pid=7766") || !strings.Contains(found.Message, "0.33.0") {
		t.Fatalf("message should name pid and version, got %q", found.Message)
	}
	if found.Section != "Environment" {
		t.Fatalf("section = %q", found.Section)
	}
}

func TestRootCLI_DoctorPassesWhenNoStaleTracearyProcesses(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	cli.SetEmptyTracearyProcessSnapshotsForTest()
	t.Cleanup(cli.ResetListTracearyProcessSnapshotsForTest)

	dbPath := t.TempDir() + "/traceary.db"
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--client", "codex", "--project-dir", projectDir, "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s", err, stdout.String())
	}
	report := decodeDoctorReport(t, stdout.Bytes())
	var found *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "stale-processes" {
			found = &report.Checks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("stale-processes check missing from report")
	}
	if found.Status != "pass" {
		t.Fatalf("status = %q, want pass; msg=%q", found.Status, found.Message)
	}
}
