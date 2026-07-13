package cli_test

import (
	"bytes"
	"fmt"
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

	t.Run("duplicate codex hooks dry-run does not mutate user hooks", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))
		writeCodexDuplicateAuditHook(t, homeDir)
		codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
		before, err := os.ReadFile(codexPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--dry-run", "--client", "codex", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		report := decodeDoctorReport(t, stdout.Bytes())

		foundDryRun := false
		for _, fix := range report.Fixes {
			if fix.Name == "codex-config" && strings.HasPrefix(fix.Action, "would:") {
				foundDryRun = true
			}
		}
		if !foundDryRun {
			t.Fatalf("fixes = %#v, want dry-run preview for codex-config", report.Fixes)
		}
		after, err := os.ReadFile(codexPath)
		if err != nil {
			t.Fatalf("ReadFile(after) error = %v", err)
		}
		if string(before) != string(after) {
			t.Fatalf("doctor --fix --dry-run mutated codex hooks\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if !strings.Contains(string(after), "echo user-hook") {
			t.Fatalf("user hook disappeared from codex hooks:\n%s", after)
		}
	})

	t.Run("confirmed codex plugin hooks remove only manual Traceary entries", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		binDir := filepath.Join(t.TempDir(), "bin")
		setTracearyPathToCurrentExecutableAt(t, binDir)
		writeTrustedCodexAppServer(t, binDir, projectDir, "traceary@local-traceary-plugins")
		writeCodexDuplicateAuditHook(t, homeDir)
		writeCodexPluginHookFeature(t, homeDir, "true")

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "codex", "--project-dir", projectDir, "--json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v\n%s", err, stdout.String())
		}
		report := decodeDoctorReport(t, stdout.Bytes())
		if got := doctorStatuses(report)["codex-config"]; got != "pass" {
			t.Fatalf("codex-config status = %q, want pass; report=%s", got, stdout.String())
		}
		foundRemoval := false
		for _, fix := range report.Fixes {
			if fix.Name == "codex-config" && strings.Contains(fix.Action, "remove manual Traceary hooks") {
				foundRemoval = true
			}
		}
		if !foundRemoval {
			t.Fatalf("fixes = %#v, want manual hook removal", report.Fixes)
		}
		content, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if strings.Contains(string(content), "traceary-") || strings.Contains(string(content), "'traceary'") {
			t.Fatalf("manual Traceary hooks remain after fix:\n%s", content)
		}
		if !strings.Contains(string(content), "echo user-hook") {
			t.Fatalf("user hook disappeared after fix:\n%s", content)
		}
		if !strings.Contains(string(content), `"custom"`) || !strings.Contains(string(content), `"keep": true`) {
			t.Fatalf("top-level user configuration disappeared after fix:\n%s", content)
		}
		if !strings.Contains(string(content), `"matcherExtension"`) || !strings.Contains(string(content), `"futureCommandField"`) {
			t.Fatalf("unknown user hook fields disappeared after fix:\n%s", content)
		}
	})

	t.Run("incomplete trusted codex plugin hooks retain manual fallback entries", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		binDir := filepath.Join(t.TempDir(), "bin")
		setTracearyPathToCurrentExecutableAt(t, binDir)
		writeCodexAppServerWithTrustedHookCount(t, binDir, projectDir, "traceary@local-traceary-plugins", cli.ExpectedCodexPluginHookCount()-1)
		codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
		installCmd := newTestRootCLI().Command()
		installCmd.SetOut(&bytes.Buffer{})
		installCmd.SetErr(&bytes.Buffer{})
		installCmd.SetArgs([]string{"hooks", "install", "--client", "codex", "--output", codexPath, "--traceary-bin", "traceary", "--force"})
		if err := installCmd.Execute(); err != nil {
			t.Fatalf("install current Codex fallback: %v", err)
		}
		writeCodexPluginHookFeature(t, homeDir, "true")

		before, err := os.ReadFile(codexPath)
		if err != nil {
			t.Fatalf("ReadFile(before) error = %v", err)
		}
		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "codex", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		report := decodeDoctorReport(t, stdout.Bytes())
		if got := doctorStatuses(report)["codex-plugin-hooks"]; got != "warn" {
			t.Fatalf("codex-plugin-hooks status = %q, want warn; report=%s", got, stdout.String())
		}
		for _, fix := range report.Fixes {
			if fix.Name == "codex-config" && strings.Contains(fix.Action, "remove manual Traceary hooks") {
				t.Fatalf("fixes = %#v, must not remove fallback for incomplete plugin hooks", report.Fixes)
			}
		}
		after, err := os.ReadFile(codexPath)
		if err != nil {
			t.Fatalf("ReadFile(after) error = %v", err)
		}
		if string(after) != string(before) {
			t.Fatalf("doctor --fix mutated fallback for incomplete plugin hooks\nbefore:\n%s\nafter:\n%s", before, after)
		}
		for _, event := range []string{"SubagentStart", "SubagentStop", "PreCompact", "PostCompact"} {
			if !strings.Contains(string(after), `"`+event+`"`) {
				t.Fatalf("manual fallback lost %s for incomplete plugin hooks:\n%s", event, after)
			}
		}
	})

	t.Run("disabled codex plugin hooks retain manual fallback entries", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))
		writeCodexDuplicateAuditHook(t, homeDir)
		writeCodexPluginHookFeature(t, homeDir, "false")

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "codex", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		content, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if !strings.Contains(string(content), "traceary-session-start") || !strings.Contains(string(content), "traceary-audit") {
			t.Fatalf("manual fallback hooks were removed while plugin_hooks=false:\n%s", content)
		}
	})

	t.Run("unspecified codex plugin hooks retain manual fallback entries", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		setDoctorFixHome(t, homeDir)
		setTracearyPathToCurrentExecutableAt(t, filepath.Join(t.TempDir(), "bin"))
		writeCodexDuplicateAuditHook(t, homeDir)
		codexDir := filepath.Join(homeDir, ".codex")
		config := "[plugins.\"traceary@local-traceary-plugins\"]\nenabled = true\n"
		if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(config), 0o644); err != nil {
			t.Fatalf("WriteFile(config.toml) error = %v", err)
		}

		stdout := &bytes.Buffer{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--fix", "--client", "codex", "--project-dir", projectDir, "--json"})
		executeDoctorAllowWarnings(t, rootCmd)
		content, err := os.ReadFile(filepath.Join(codexDir, "hooks.json"))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if !strings.Contains(string(content), "traceary-session-start") || !strings.Contains(string(content), "traceary-audit") {
			t.Fatalf("manual fallback hooks were removed while plugin_hooks was unspecified:\n%s", content)
		}
	})
}

func writeTrustedCodexAppServer(t *testing.T, binDir, projectDir, pluginKey string) {
	t.Helper()
	writeCodexAppServerWithTrustedHookCount(t, binDir, projectDir, pluginKey, cli.ExpectedCodexPluginHookCount())
}

func writeCodexAppServerWithTrustedHookCount(t *testing.T, binDir, projectDir, pluginKey string, hookCount int) {
	t.Helper()
	script := `#!/usr/bin/python3
import json
import sys

initialize = json.loads(sys.stdin.readline())
assert initialize["id"] == 0 and initialize["method"] == "initialize"
print(json.dumps({"id": 0, "result": {"userAgent": "synthetic"}}), flush=True)

hooks_list = json.loads(sys.stdin.readline())
assert hooks_list["id"] == 1 and hooks_list["method"] == "hooks/list"
assert hooks_list["params"]["cwds"] == [` + fmt.Sprintf("%q", projectDir) + `]
print(json.dumps({"id": 1, "result": {"data": [{
    "cwd": ` + fmt.Sprintf("%q", projectDir) + `,
    "hooks": [{"pluginId": ` + fmt.Sprintf("%q", pluginKey) + `, "enabled": True, "trustStatus": "trusted"}] * ` + fmt.Sprintf("%d", hookCount) + `,
    "warnings": [],
    "errors": []
}]}}), flush=True)
`
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}
}

func writeCodexPluginHookFeature(t *testing.T, homeDir, value string) {
	t.Helper()
	codexDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "[features]\nplugin_hooks = " + value + "\n\n[plugins.\"traceary@local-traceary-plugins\"]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}
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
