package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation/cli"
)

type doctorReport struct {
	DBPath  string        `json:"db_path"`
	Clients []string      `json:"clients"`
	Checks  []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

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
			"config":                     gotStatuses["config"],
			"claude-config":              gotStatuses["claude-config"],
			"codex-config":               gotStatuses["codex-config"],
			"gemini-config":              gotStatuses["gemini-config"],
			"claude-host-capabilities":   gotStatuses["claude-host-capabilities"],
			"codex-host-capabilities":    gotStatuses["codex-host-capabilities"],
			"gemini-host-capabilities":   gotStatuses["gemini-host-capabilities"],
		}
		wantStatuses := map[string]string{
			"config":                     "pass",
			"claude-config":              "warn",
			"codex-config":               "warn",
			"gemini-config":              "warn",
			"claude-host-capabilities":   "pass",
			"codex-host-capabilities":    "pass",
			"gemini-host-capabilities":   "pass",
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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

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

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		report := decodeDoctorReport(t, stdout.Bytes())
		for _, check := range report.Checks {
			if check.Name == "codex-config" && check.Status != "pass" {
				t.Fatalf("codex-config status = %q, want pass (message: %q)", check.Status, check.Message)
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

func decodeDoctorReport(t *testing.T, data []byte) doctorReport {
	t.Helper()

	var report doctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return report
}
