package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation/cli"
)

type doctorReport struct {
	DBPath   string          `json:"db_path"`
	Clients  []string        `json:"clients"`
	Checks   []doctorCheck   `json:"checks"`
	Sections []doctorSection `json:"sections"`
	Summary  doctorSummary   `json:"summary"`
	ExitCode int             `json:"exit_code"`
}

type doctorSection struct {
	Name   string        `json:"name"`
	Checks []doctorCheck `json:"checks"`
}

type doctorSummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type doctorCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Severity   string `json:"severity"`
	Section    string `json:"section"`
	Message    string `json:"message"`
	Hint       string `json:"hint"`
	FixCommand string `json:"fix_command"`
}

func executeDoctorAllowWarnings(t *testing.T, cmd interface{ Execute() error }) {
	t.Helper()
	if err := cmd.Execute(); err != nil && !strings.Contains(err.Error(), "warning checks") && !strings.Contains(err.Error(), "failing checks") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_DoctorCommand(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("all clients without config only warns", func(t *testing.T) {
		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		if diff := cmp.Diff([]string{"claude", "codex", "gemini"}, report.Clients); diff != "" {
			t.Fatalf("report.Clients mismatch (-want +got):\n%s", diff)
		}
		if !initStub.initCalled {
			t.Fatalf("initCalled = false, want true")
		}
		gotStatuses := map[string]string{}
		for _, check := range report.Checks {
			gotStatuses[check.Name] = check.Status
		}
		gotSubset := map[string]string{
			"config":                   gotStatuses["config"],
			"claude-config":            gotStatuses["claude-config"],
			"codex-config":             gotStatuses["codex-config"],
			"gemini-config":            gotStatuses["gemini-config"],
			"claude-host-capabilities": gotStatuses["claude-host-capabilities"],
			"codex-host-capabilities":  gotStatuses["codex-host-capabilities"],
			"gemini-host-capabilities": gotStatuses["gemini-host-capabilities"],
		}
		wantStatuses := map[string]string{
			"config":                   "pass",
			"claude-config":            "warn",
			"codex-config":             "warn",
			"gemini-config":            "warn",
			"claude-host-capabilities": "pass",
			"codex-host-capabilities":  "pass",
			"gemini-host-capabilities": "pass",
		}
		if diff := cmp.Diff(wantStatuses, gotSubset); diff != "" {
			t.Fatalf("doctor statuses mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("specific client without config warns", func(t *testing.T) {
		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		gotStatuses := map[string]string{}
		for _, check := range report.Checks {
			gotStatuses[check.Name] = check.Status
		}
		gotSubset := map[string]string{
			"config":        gotStatuses["config"],
			"claude-config": gotStatuses["claude-config"],
		}
		wantStatuses := map[string]string{
			"config":        "pass",
			"claude-config": "warn",
		}
		if diff := cmp.Diff(wantStatuses, gotSubset); diff != "" {
			t.Fatalf("doctor statuses mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("existing Traceary config passes", func(t *testing.T) {
		configDir := filepath.Join(homeDir, ".config", "traceary")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"redact":{"extra_patterns":["internal_token"]}}`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}
`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		gotStatuses := map[string]string{}
		for _, check := range report.Checks {
			gotStatuses[check.Name] = check.Status
		}
		gotSubset := map[string]string{
			"config":        gotStatuses["config"],
			"claude-config": gotStatuses["claude-config"],
		}
		wantStatuses := map[string]string{
			"config":        "pass",
			"claude-config": "pass",
		}
		if diff := cmp.Diff(wantStatuses, gotSubset); diff != "" {
			t.Fatalf("doctor statuses mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("codex config missing UserPromptSubmit warns", func(t *testing.T) {
		codexDir := filepath.Join(homeDir, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		// Legacy v0.6.x surface: Traceary-managed SessionStart / Stop /
		// PostToolUse exist, but UserPromptSubmit is missing.
		legacy := `{
			"hooks": {
				"SessionStart": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}]}],
				"Stop": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'stop'"}]}],
				"PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"}]}]
			}
		}`
		if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(legacy), 0o644); err != nil {
			t.Fatalf("WriteFile(legacy codex hooks) error = %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(codexDir) })

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "codex", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		var codex doctorCheck
		for _, check := range report.Checks {
			if check.Name == "codex-config" {
				codex = check
			}
		}
		if codex.Status != "warn" {
			t.Fatalf("codex-config status = %q, want warn", codex.Status)
		}
		if !bytes.Contains([]byte(codex.Message), []byte("UserPromptSubmit")) {
			t.Fatalf("expected warn message to mention UserPromptSubmit, got %q", codex.Message)
		}
	})

	t.Run("codex config with user-managed UserPromptSubmit warns", func(t *testing.T) {
		codexDir := filepath.Join(homeDir, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		// User has a custom UserPromptSubmit hook that coincidentally
		// carries "hook" and "codex" tokens. Doctor must still warn: a
		// substring heuristic would misclassify this as a Traceary install.
		userManaged := `{
			"hooks": {
				"SessionStart": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}]}],
				"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "'/usr/local/bin/custom-cli' 'hook' 'prompt' 'codex'"}]}],
				"Stop": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'stop'"}]}],
				"PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"}]}]
			}
		}`
		if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(userManaged), 0o644); err != nil {
			t.Fatalf("WriteFile(user-managed codex hooks) error = %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(codexDir) })

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "codex", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		var codex doctorCheck
		for _, check := range report.Checks {
			if check.Name == "codex-config" {
				codex = check
			}
		}
		if codex.Status != "warn" {
			t.Fatalf("codex-config status = %q, want warn (message: %q)", codex.Status, codex.Message)
		}
		if !bytes.Contains([]byte(codex.Message), []byte("UserPromptSubmit")) {
			t.Fatalf("expected warn message to flag UserPromptSubmit, got %q", codex.Message)
		}
	})

	t.Run("codex config with UserPromptSubmit passes", func(t *testing.T) {
		codexDir := filepath.Join(homeDir, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		complete := `{
			"hooks": {
				"SessionStart": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}]}],
				"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'prompt' 'codex'"}]}],
				"Stop": [{"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'stop'"}]}],
				"PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"}]}]
			}
		}`
		if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(complete), 0o644); err != nil {
			t.Fatalf("WriteFile(complete codex hooks) error = %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(codexDir) })

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "codex", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		for _, check := range report.Checks {
			if check.Name == "codex-config" && check.Status != "pass" {
				t.Fatalf("codex-config status = %q, want pass (message: %q)", check.Status, check.Message)
			}
		}
	})

	t.Run("codex config installed with non-canonical traceary-bin still passes", func(t *testing.T) {
		// Regression guard for #667: an operator running a dev build
		// via `--traceary-bin /tmp/traceary-qa` ends up with hook
		// commands whose binary basename is not `traceary`. Before the
		// fix, doctor's codex-config check rejected every such entry
		// because it only parsed the binary token, producing a
		// misleading "missing Traceary-managed events" warning. The
		// Name-aware extractor added in #667 treats the `traceary-*`
		// name prefix as a sufficient signal.
		codexDir := filepath.Join(homeDir, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		complete := `{
			"hooks": {
				"SessionStart": [{"hooks": [{"name": "traceary-session-start", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'session' 'codex' 'start'"}]}],
				"UserPromptSubmit": [{"hooks": [{"name": "traceary-prompt", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'prompt' 'codex'"}]}],
				"Stop": [{"hooks": [{"name": "traceary-session-stop", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'session' 'codex' 'stop'"}]}],
				"PostToolUse": [{"matcher": "", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'audit' 'codex'"}]}]
			}
		}`
		if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(complete), 0o644); err != nil {
			t.Fatalf("WriteFile(non-canonical codex hooks) error = %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(codexDir) })

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "codex", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		for _, check := range report.Checks {
			if check.Name == "codex-config" && check.Status != "pass" {
				t.Fatalf("codex-config status = %q, want pass for non-canonical --traceary-bin install (message: %q)", check.Status, check.Message)
			}
		}
	})

	t.Run("invalid Traceary config fails doctor", func(t *testing.T) {
		configDir := filepath.Join(homeDir, ".config", "traceary")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid}"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want non-nil")
		}

		report := decodeDoctorReport(t, stdout.Bytes())
		gotStatuses := map[string]string{}
		for _, check := range report.Checks {
			gotStatuses[check.Name] = check.Status
		}
		if diff := cmp.Diff(map[string]string{"config": "fail"}, map[string]string{"config": gotStatuses["config"]}); diff != "" {
			t.Fatalf("doctor statuses mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestRootCLI_DoctorExitCodeMatrixAndJSONSections(t *testing.T) {
	t.Run("all pass exits zero", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		setTracearyPathToCurrentExecutable(t)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeTracearyConfig(t, homeDir, `{"redact":{"extra_patterns":["internal_token"]}}`)
		writeClaudeHookSettings(t, projectDir)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if report.ExitCode != 0 {
			t.Fatalf("exit_code = %d, want 0", report.ExitCode)
		}
	})

	t.Run("warn without fail exits two", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		setTracearyPathToCurrentExecutable(t)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})
		if got := doctorErrExitCode(err); got != 2 {
			t.Fatalf("ExitCode(error) = %d, want 2 (err=%v)", got, err)
		}
		if report.ExitCode != 2 || report.Summary.Warn == 0 || report.Summary.Fail != 0 {
			t.Fatalf("report summary = %+v exit_code=%d, want warn-only exit 2", report.Summary, report.ExitCode)
		}
		assertDoctorSectionShape(t, report)
	})

	t.Run("fail exits one", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeTracearyConfig(t, homeDir, `{invalid}`)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})
		if got := doctorErrExitCode(err); got != 1 {
			t.Fatalf("ExitCode(error) = %d, want 1 (err=%v)", got, err)
		}
		if report.ExitCode != 1 || report.Summary.Fail == 0 {
			t.Fatalf("report summary = %+v exit_code=%d, want fail exit 1", report.Summary, report.ExitCode)
		}
	})
}

func executeDoctorJSON(t *testing.T, args []string) (doctorReport, error) {
	t.Helper()
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return decodeDoctorReport(t, stdout.Bytes()), err
}

func doctorErrExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitCoder, ok := err.(interface{ ExitCode() int }); ok {
		return exitCoder.ExitCode()
	}
	return -1
}

func assertDoctorSectionShape(t *testing.T, report doctorReport) {
	t.Helper()
	gotNames := make([]string, 0, len(report.Sections))
	for _, section := range report.Sections {
		gotNames = append(gotNames, section.Name)
		for _, check := range section.Checks {
			if check.Name == "" || check.Severity == "" || check.Message == "" {
				t.Fatalf("section %q contains incomplete check: %+v", section.Name, check)
			}
			switch check.Severity {
			case "PASS", "WARN", "FAIL":
			default:
				t.Fatalf("check %q severity = %q, want PASS/WARN/FAIL", check.Name, check.Severity)
			}
		}
	}
	wantNames := []string{"Environment", "Database", "Plugins", "MCP", "Hooks"}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Fatalf("section names mismatch (-want +got):\n%s", diff)
	}
}

func writeTracearyConfig(t *testing.T, homeDir, content string) {
	t.Helper()
	configDir := filepath.Join(homeDir, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeClaudeHookSettings(t *testing.T, projectDir string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
  "mcpServers": {
    "traceary": {
      "command": "traceary",
      "args": ["mcp-server"]
    }
  },
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func setTracearyPathToCurrentExecutable(t *testing.T) {
	t.Helper()
	current, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "traceary")
	if err := os.Symlink(current, link); err != nil {
		if err := os.WriteFile(link, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("failed to create traceary test executable: %v", err)
		}
	}
	t.Setenv("PATH", dir)
}

func decodeDoctorReport(t *testing.T, data []byte) doctorReport {
	t.Helper()

	var report doctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return report
}
