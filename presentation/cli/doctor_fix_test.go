package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_DoctorFix(t *testing.T) {
	t.Run("fresh claude config installs hooks and mcp then second run is idempotent", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "claude", "--project-dir", projectDir, "--json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("first Execute() error = %v\n%s", err, stdout.String())
		}
		report := decodeDoctorReport(t, stdout.Bytes())
		if len(report.Fixes) == 0 {
			t.Fatalf("first --fix produced no fix logs")
		}
		statuses := doctorStatuses(report)
		if statuses["claude-config"] != "pass" || statuses["claude-mcp"] != "pass" {
			t.Fatalf("after fix statuses = %#v, want claude-config and claude-mcp pass", statuses)
		}
		settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
		content, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatalf("ReadFile(settings) error = %v", err)
		}
		if !strings.Contains(string(content), `"hooks"`) || !strings.Contains(string(content), `"mcpServers"`) {
			t.Fatalf("settings should contain hooks and mcpServers, got:\n%s", content)
		}

		stdout.Reset()
		rootCmd = newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "claude", "--project-dir", projectDir, "--json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("second Execute() error = %v\n%s", err, stdout.String())
		}
		report = decodeDoctorReport(t, stdout.Bytes())
		if len(report.Fixes) != 0 {
			t.Fatalf("second --fix fixes = %#v, want zero", report.Fixes)
		}
	})

	t.Run("dry-run previews without writing", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--dry-run", "--client", "claude", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		report := decodeDoctorReport(t, stdout.Bytes())
		if len(report.Fixes) == 0 || !strings.HasPrefix(report.Fixes[0].Action, "would:") {
			t.Fatalf("dry-run fixes = %#v, want would: action", report.Fixes)
		}
		settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
		if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote settings file or stat failed: %v", err)
		}
	})

	t.Run("non fixable checks are logged as guided skips", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "claude", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		report := decodeDoctorReport(t, stdout.Bytes())
		foundPathSkip := false
		for _, fix := range report.Fixes {
			if fix.Name == "path" && strings.HasPrefix(fix.Action, "skip:") {
				foundPathSkip = true
			}
		}
		if !foundPathSkip {
			t.Fatalf("fixes = %#v, want guided skip for path", report.Fixes)
		}
		if got := doctorStatuses(report)["path"]; got != "fail" && got != "warn" {
			t.Fatalf("path status after fix = %q, want fail or warn", got)
		}
	})

	t.Run("claude plugin and project hooks duplicate registration is guided only", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))
		writePluginEnabledSettings(t, homeDir)
		writeClaudeProjectHook(t, projectDir)

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "claude", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		report := decodeDoctorReport(t, stdout.Bytes())

		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "warn" {
			t.Fatalf("claude-config status = %q, want warn; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
		if claudeCfg.AutoFixAvailable {
			t.Fatalf("claude-config AutoFixAvailable = true, want false")
		}
		foundGuidedSkip := false
		for _, fix := range report.Fixes {
			if fix.Name == "claude-config" {
				if !strings.HasPrefix(fix.Action, "skip:") {
					t.Fatalf("claude-config fix action = %q, want guided skip", fix.Action)
				}
				foundGuidedSkip = true
			}
		}
		if !foundGuidedSkip {
			t.Fatalf("fixes = %#v, want guided skip for claude-config", report.Fixes)
		}
		content, err := os.ReadFile(filepath.Join(projectDir, ".claude", "settings.json"))
		if err != nil {
			t.Fatalf("ReadFile(settings) error = %v", err)
		}
		if !strings.Contains(string(content), "traceary") {
			t.Fatalf("--fix unexpectedly removed project hooks:\n%s", content)
		}
	})
}

func setDoctorFixHome(t *testing.T, homeDir string) {
	t.Helper()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
}

func doctorStatuses(report doctorReport) map[string]string {
	statuses := map[string]string{}
	for _, check := range report.Checks {
		statuses[check.Name] = check.Status
	}
	return statuses
}
