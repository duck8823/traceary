package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type doctorReport struct {
	DBPath   string          `json:"db_path"`
	Clients  []string        `json:"clients"`
	Checks   []doctorCheck   `json:"checks"`
	Sections []doctorSection `json:"sections"`
	Summary  doctorSummary   `json:"summary"`
	ExitCode int             `json:"exit_code"`
	Fixes    []doctorFixLog  `json:"fixes"`
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
	Name             string `json:"name"`
	Status           string `json:"status"`
	Severity         string `json:"severity"`
	Section          string `json:"section"`
	Message          string `json:"message"`
	Hint             string `json:"hint"`
	FixCommand       string `json:"fix_command"`
	AutoFixAvailable bool   `json:"auto_fix_available"`
}

type doctorFixLog struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	Before string `json:"before"`
	After  string `json:"after"`
	Error  string `json:"error"`
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
		t.Setenv("TRACEARY_LANG", "en")
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
			"gemini-compact-coverage":  gotStatuses["gemini-compact-coverage"],
		}
		wantStatuses := map[string]string{
			"config":                   "pass",
			"claude-config":            "warn",
			"codex-config":             "warn",
			"gemini-config":            "warn",
			"claude-host-capabilities": "pass",
			"codex-host-capabilities":  "pass",
			"gemini-host-capabilities": "pass",
			"gemini-compact-coverage":  "pass",
		}
		if diff := cmp.Diff(wantStatuses, gotSubset); diff != "" {
			t.Fatalf("doctor statuses mismatch (-want +got):\n%s", diff)
		}

		gotMessages := map[string]string{}
		for _, check := range report.Checks {
			gotMessages[check.Name] = check.Message
		}
		if msg := gotMessages["gemini-compact-coverage"]; !strings.Contains(msg, "no post-compress hook") || !strings.Contains(msg, "PreCompress") {
			t.Fatalf("gemini-compact-coverage message should explain the missing post-compress hook, got: %q", msg)
		}
		if msg := gotMessages["gemini-host-capabilities"]; !strings.Contains(msg, "BeforeAgent") || !strings.Contains(msg, "PreCompress") {
			t.Fatalf("gemini-host-capabilities message should list the wired BeforeAgent / PreCompress hooks, got: %q", msg)
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
		writeCompleteClaudeProjectHookSettings(t, projectDir)

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
		if !bytes.Contains([]byte(codex.Message), []byte("Stop")) {
			t.Fatalf("expected warn message to mention Stop transcript gap, got %q", codex.Message)
		}
		if !bytes.Contains([]byte(codex.Message), []byte("durable-memory extraction")) {
			t.Fatalf("expected warn message to explain memory extraction impact, got %q", codex.Message)
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
				"Stop": [{"hooks": [
					{"type": "command", "command": "'traceary' 'hook' 'transcript' 'codex'"},
					{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'stop'"}
				]}],
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
				"Stop": [{"hooks": [
					{"name": "traceary-transcript", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'transcript' 'codex'"},
					{"name": "traceary-session-stop", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'session' 'codex' 'stop'"}
				]}],
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

	t.Run("codex plugin enabled but hooks.json lacks Traceary entries warns about plugin_hooks fallback", func(t *testing.T) {
		// Regression for #967: Codex builds with `plugin_hooks=false`
		// (or any plugin host that does not materialize plugin-managed
		// hooks) leave hooks.json without Traceary entries even when
		// the Traceary plugin is enabled in config.toml. The doctor
		// must surface the manual fallback, not the generic "no
		// Traceary-managed hook" warning.
		codexDir := filepath.Join(homeDir, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		configTOML := `[features]
plugin_hooks = false

[plugins."traceary@local-traceary-plugins"]
enabled = true
`
		if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(configTOML), 0o644); err != nil {
			t.Fatalf("WriteFile(codex config.toml) error = %v", err)
		}
		// Non-Traceary user hooks already present in hooks.json.
		nonTraceary := `{
			"hooks": {
				"SessionStart": [{"hooks": [{"type": "command", "command": "'/usr/local/bin/custom' 'hook' 'session' 'start'"}]}],
				"Stop": [{"hooks": [{"type": "command", "command": "'/usr/local/bin/custom' 'hook' 'session' 'stop'"}]}]
			}
		}`
		if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(nonTraceary), 0o644); err != nil {
			t.Fatalf("WriteFile(codex hooks.json) error = %v", err)
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
		if !strings.Contains(codex.Message, "traceary@local-traceary-plugins") {
			t.Fatalf("expected message to identify the enabled plugin key, got %q", codex.Message)
		}
		if !strings.Contains(codex.Message, "plugin_hooks") {
			t.Fatalf("expected message to mention plugin_hooks fallback path, got %q", codex.Message)
		}
		if !strings.Contains(codex.Message, "manual hook install") {
			t.Fatalf("expected message to explain manual hook install fallback, got %q", codex.Message)
		}
		if !strings.Contains(codex.Message, "duplicate event capture") {
			t.Fatalf("expected message to warn about duplicate event capture, got %q", codex.Message)
		}
		if !strings.Contains(codex.Message, "plugin_hooks = false") {
			t.Fatalf("expected message to surface the explicit [features].plugin_hooks=false flag, got %q", codex.Message)
		}
		if codex.Hint == "" {
			t.Fatalf("expected hint to be set, got empty (check=%+v)", codex)
		}
		if !strings.Contains(codex.FixCommand, "traceary hooks install --client codex --upgrade") {
			t.Fatalf("FixCommand = %q, want the manual fallback install command", codex.FixCommand)
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

func TestRootCLI_DoctorCommand_ClaudeHookCancellationDiagnostics(t *testing.T) {
	t.Run("passes when no pending marker exists", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("TRACEARY_LANG", "en")
		setTracearyPathToCurrentExecutable(t)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteClaudeProjectHookSettings(t, projectDir)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json", "--warnings-ok"})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		check := statusByName(report, "claude-hook-cancellations")
		if got, want := check.Status, "pass"; got != want {
			t.Fatalf("claude-hook-cancellations status = %q, want %q (message=%q)", got, want, check.Message)
		}
	})

	t.Run("warns with durable marker evidence", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("TRACEARY_LANG", "en")
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "doctor-cancel-key")
		setTracearyPathToCurrentExecutable(t)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteClaudeProjectHookSettings(t, projectDir)

		stateDir := filepath.Join(homeDir, ".config", "traceary", "hooks")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(state) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "claude-doctor-cancel-key"), []byte("doctor-cancelled-session"), 0o600); err != nil {
			t.Fatalf("WriteFile(session state) error = %v", err)
		}

		hookCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithSession(&sessionUsecaseStub{endErr: errors.New("simulated cancellation")}),
		).Command()
		hookCmd.SetOut(&bytes.Buffer{})
		hookCmd.SetErr(&bytes.Buffer{})
		hookCmd.SetIn(strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, projectDir)))
		hookCmd.SetArgs([]string{"hook", "session", "claude", "end"})
		if err := hookCmd.Execute(); err != nil {
			t.Fatalf("hook Execute() error = %v", err)
		}

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json", "--warnings-ok"})
		if err != nil {
			t.Fatalf("doctor Execute() error = %v", err)
		}
		check := statusByName(report, "claude-hook-cancellations")
		if got, want := check.Status, "warn"; got != want {
			t.Fatalf("claude-hook-cancellations status = %q, want %q (message=%q)", got, want, check.Message)
		}
		for _, want := range []string{
			"SessionEnd",
			"'traceary' 'hook' 'session' 'claude' 'end'",
			"doctor-cancelled-session",
			"started_at=",
			"path=",
			filepath.Join(stateDir, "diagnostics"),
		} {
			if !strings.Contains(check.Message, want) {
				t.Fatalf("claude-hook-cancellations message = %q, want substring %q", check.Message, want)
			}
		}
		if check.Hint == "" || !strings.Contains(check.Hint, "Traceary reached Claude SessionEnd") {
			t.Fatalf("claude-hook-cancellations hint = %q, want actionable cancellation hint", check.Hint)
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

	t.Run("warnings-ok makes warning-only report exit zero", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		setTracearyPathToCurrentExecutable(t)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json", "--warnings-ok"})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if report.ExitCode != 0 || report.Summary.Warn == 0 || report.Summary.Fail != 0 {
			t.Fatalf("report summary = %+v exit_code=%d, want warning-only exit 0", report.Summary, report.ExitCode)
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

	t.Run("warnings-ok does not mask failures", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeTracearyConfig(t, homeDir, `{invalid}`)

		report, err := executeDoctorJSON(t, []string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json", "--warnings-ok"})
		if got := doctorErrExitCode(err); got != 1 {
			t.Fatalf("ExitCode(error) = %d, want 1 (err=%v)", got, err)
		}
		if report.ExitCode != 1 || report.Summary.Fail == 0 {
			t.Fatalf("report summary = %+v exit_code=%d, want fail exit 1", report.Summary, report.ExitCode)
		}
	})
}

func TestRootCLI_DoctorCodexMemoryActivationStatus(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	targetPath := filepath.Join(homeDir, ".codex", "memories", "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 2,
			Message:        "activation target file is missing",
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "codex", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "codex-memory-activation")
	if check.Status != "warn" {
		t.Fatalf("codex-memory-activation status = %q, want warn (message: %q)", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, targetPath) || !strings.Contains(check.Message, "--dry-run --diff") || !strings.Contains(check.FixCommand, "--apply") {
		t.Fatalf("unexpected activation doctor check: %+v", check)
	}
	if len(memoryStub.activationStatusCalls) != 1 {
		t.Fatalf("activation status calls = %d, want 1", len(memoryStub.activationStatusCalls))
	}
	call := memoryStub.activationStatusCalls[0]
	if call.Target != apptypes.MemoryBridgeTargetCodex || !call.IncludeGlobal {
		t.Fatalf("activation status criteria = %+v, want codex include global", call)
	}
	if len(call.Scopes) != 1 || call.Scopes[0].Kind() != types.MemoryScopeKindWorkspace || call.Scopes[0].Key() != projectDir {
		t.Fatalf("activation status scope = %+v, want workspace %s", call.Scopes, projectDir)
	}
}

func TestRootCLI_DoctorClaudeMemoryActivationStatus(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	hostPath := filepath.Join(projectDir, "CLAUDE.md")
	externalPath := filepath.Join(projectDir, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 3,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "claude-memory-activation")
	if check.Status != "warn" {
		t.Fatalf("claude-memory-activation status = %q, want warn (message: %q)", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, hostPath) || !strings.Contains(check.Message, "--dry-run --diff") {
		t.Fatalf("unexpected activation doctor message: %+v", check)
	}
	if !strings.Contains(check.FixCommand, "--target claude") || !strings.Contains(check.FixCommand, "--apply") {
		t.Fatalf("FixCommand = %q, want claude apply remediation", check.FixCommand)
	}
	if len(memoryStub.activationStatusCalls) != 1 {
		t.Fatalf("activation status calls = %d, want 1", len(memoryStub.activationStatusCalls))
	}
	call := memoryStub.activationStatusCalls[0]
	if call.Target != apptypes.MemoryBridgeTargetClaude || !call.IncludeGlobal {
		t.Fatalf("activation status criteria = %+v, want claude include global", call)
	}
	if call.Root != projectDir {
		t.Fatalf("activation status Root = %q, want %q", call.Root, projectDir)
	}
}

func TestRootCLI_DoctorClaudeMemoryActivationInvalid(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	hostPath := filepath.Join(projectDir, "CLAUDE.md")
	externalPath := filepath.Join(projectDir, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusInvalid,
			Existing:       true,
			ActivatedCount: 1,
			Message:        "host context file invalid: refusing to overwrite newer Traceary managed block version v9",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:    hostPath,
				State:   apptypes.MemoryActivationStatusInvalid,
				Message: "refusing to overwrite newer Traceary managed block version v9",
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

	_ = rootCmd.Execute()

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "claude-memory-activation")
	if check.Status != "fail" {
		t.Fatalf("claude-memory-activation status = %q, want fail", check.Status)
	}
	if !strings.Contains(check.Message, "invalid") {
		t.Fatalf("invalid claude activation message must mention invalid: %q", check.Message)
	}
	if check.FixCommand != "" {
		t.Fatalf("FixCommand = %q, want empty for invalid state", check.FixCommand)
	}
}

func TestRootCLI_DoctorGeminiMemoryActivationStatus(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	hostPath := filepath.Join(projectDir, "GEMINI.md")
	externalPath := filepath.Join(projectDir, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 4,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "gemini-memory-activation")
	if check.Status != "warn" {
		t.Fatalf("gemini-memory-activation status = %q, want warn (message: %q)", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, hostPath) || !strings.Contains(check.Message, "--dry-run --diff") {
		t.Fatalf("unexpected activation doctor message: %+v", check)
	}
	if !strings.Contains(check.FixCommand, "--target gemini") || !strings.Contains(check.FixCommand, "--apply") {
		t.Fatalf("FixCommand = %q, want gemini apply remediation", check.FixCommand)
	}
	if len(memoryStub.activationStatusCalls) != 1 {
		t.Fatalf("activation status calls = %d, want 1", len(memoryStub.activationStatusCalls))
	}
	call := memoryStub.activationStatusCalls[0]
	if call.Target != apptypes.MemoryBridgeTargetGemini || !call.IncludeGlobal {
		t.Fatalf("activation status criteria = %+v, want gemini include global", call)
	}
	if call.Root != projectDir {
		t.Fatalf("activation status Root = %q, want %q", call.Root, projectDir)
	}
}

func TestRootCLI_DoctorGeminiMemoryActivationInSync(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	hostPath := filepath.Join(projectDir, "GEMINI.md")
	externalPath := filepath.Join(projectDir, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusInSync,
			Existing:       true,
			ActivatedCount: 5,
			Message:        "activation pair is in sync",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusInSync,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusInSync,
			},
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "gemini-memory-activation")
	if check.Status != "pass" {
		t.Fatalf("gemini-memory-activation status = %q, want pass (message: %q)", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "in sync") {
		t.Fatalf("in_sync gemini activation message must mention in sync: %q", check.Message)
	}
	if check.FixCommand != "" {
		t.Fatalf("FixCommand = %q, want empty for in_sync state", check.FixCommand)
	}
}

func TestRootCLI_DoctorGeminiMemoryActivationInvalid(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("TRACEARY_WORKSPACE", "")
	setTracearyPathToCurrentExecutable(t)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	hostPath := filepath.Join(projectDir, "GEMINI.md")
	externalPath := filepath.Join(projectDir, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusInvalid,
			Existing:       true,
			ActivatedCount: 1,
			Message:        "host context file invalid: refusing to overwrite newer Traceary managed block version v9",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:    hostPath,
				State:   apptypes.MemoryActivationStatusInvalid,
				Message: "refusing to overwrite newer Traceary managed block version v9",
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

	_ = rootCmd.Execute()

	report := decodeDoctorReport(t, stdout.Bytes())
	check := statusByName(report, "gemini-memory-activation")
	if check.Status != "fail" {
		t.Fatalf("gemini-memory-activation status = %q, want fail", check.Status)
	}
	if !strings.Contains(check.Message, "invalid") {
		t.Fatalf("invalid gemini activation message must mention invalid: %q", check.Message)
	}
	if check.FixCommand != "" {
		t.Fatalf("FixCommand = %q, want empty for invalid state", check.FixCommand)
	}
}

func TestRootCLI_DoctorGeminiCoverage(t *testing.T) {
	t.Run("partial gemini config warns with autofix", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeLegacyGeminiProjectHookSettings(t, projectDir)

		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "gemini-config")
		if check.Status != "warn" {
			t.Fatalf("gemini-config status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "prompt") || !strings.Contains(check.Message, "transcript") {
			t.Fatalf("gemini-config message = %q; want prompt/transcript gap", check.Message)
		}
		if !check.AutoFixAvailable {
			t.Fatalf("gemini-config AutoFixAvailable = false, want true")
		}
		if !strings.Contains(check.FixCommand, "doctor --fix --dry-run") {
			t.Fatalf("gemini-config FixCommand = %q, want doctor dry-run preview", check.FixCommand)
		}
	})

	t.Run("boundary only recent sessions warn when above threshold", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteGeminiProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: geminiCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "gemini-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("gemini-event-coverage status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "prompt/transcript") || !strings.Contains(check.Message, "67%") {
			t.Fatalf("gemini-event-coverage message = %q; want ratio evidence", check.Message)
		}
		if eventStub.listCriteria.Agent() != types.Agent("gemini") {
			t.Fatalf("List criteria agent = %q, want gemini", eventStub.listCriteria.Agent())
		}
		if wantWorkspace := types.Workspace(filepath.ToSlash(filepath.Clean(projectDir))); eventStub.listCriteria.Workspace() != wantWorkspace {
			t.Fatalf("List criteria workspace = %q, want %q", eventStub.listCriteria.Workspace(), wantWorkspace)
		}
		if eventStub.listCriteria.Limit() != 500 {
			t.Fatalf("List criteria limit = %d, want 500", eventStub.listCriteria.Limit())
		}
	})

	t.Run("coverage threshold flag can suppress expected warning", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteGeminiProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: geminiCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--coverage-threshold", "0.75", "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "gemini-event-coverage")
		if check.Status != "pass" {
			t.Fatalf("gemini-event-coverage status = %q, want pass (message=%q)", check.Status, check.Message)
		}
	})

	t.Run("audit only sessions still warn because prompt transcript coverage is missing", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteGeminiProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: geminiAuditOnlyCoverageEvents(3)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "gemini-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("gemini-event-coverage status = %q, want warn for audit-only sessions (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "prompt/transcript") || !strings.Contains(check.Message, "with_command=3") {
			t.Fatalf("gemini-event-coverage message = %q; want prompt/transcript gap and command evidence", check.Message)
		}
	})

	t.Run("small samples are reported as pass", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteGeminiProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: geminiCoverageEvents(2, 0)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "gemini-event-coverage")
		if check.Status != "pass" {
			t.Fatalf("gemini-event-coverage status = %q, want pass for small sample (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "minimum sample") {
			t.Fatalf("gemini-event-coverage message = %q; want minimum sample explanation", check.Message)
		}
	})

	t.Run("invalid threshold is rejected", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "gemini", "--project-dir", projectDir, "--coverage-threshold", "1.5", "--json"})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want invalid threshold error")
		}
		if !strings.Contains(err.Error(), "--coverage-threshold") {
			t.Fatalf("Execute() error = %v, want --coverage-threshold", err)
		}
	})
}

func TestRootCLI_DoctorClaudeCoverage(t *testing.T) {
	t.Run("boundary only recent claude sessions warn in current workspace", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteClaudeProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "claude-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("claude-event-coverage status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "prompt/transcript") || !strings.Contains(check.Message, "67%") {
			t.Fatalf("claude-event-coverage message = %q; want ratio evidence", check.Message)
		}
		if !strings.Contains(check.Hint, "hook cancellations") {
			t.Fatalf("claude-event-coverage hint = %q; want hook cancellation diagnostic hint", check.Hint)
		}
		if eventStub.listCriteria.Agent() != types.Agent("claude") {
			t.Fatalf("List criteria agent = %q, want claude", eventStub.listCriteria.Agent())
		}
		if wantWorkspace := types.Workspace(filepath.ToSlash(filepath.Clean(projectDir))); eventStub.listCriteria.Workspace() != wantWorkspace {
			t.Fatalf("List criteria workspace = %q, want %q", eventStub.listCriteria.Workspace(), wantWorkspace)
		}
	})

	t.Run("prompt only recent claude sessions warn about transcript gap", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeCompleteClaudeProjectHookSettings(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: claudePromptOnlyCoverageEvents(3)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "claude-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("claude-event-coverage status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "100%") || !strings.Contains(check.Message, "with_prompt=3") || !strings.Contains(check.Message, "with_transcript=0") {
			t.Fatalf("claude-event-coverage message = %q; want prompt-only transcript gap evidence", check.Message)
		}
		if !strings.Contains(check.Hint, "hook cancellations") {
			t.Fatalf("claude-event-coverage hint = %q; want hook cancellation diagnostic hint", check.Hint)
		}
	})

	t.Run("plugin-managed hooks do not suggest writing project settings", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writePluginEnabledSettings(t, homeDir)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "claude-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("claude-event-coverage status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Hint, "plugin") {
			t.Fatalf("claude-event-coverage hint = %q; want plugin-managed diagnostic", check.Hint)
		}
		if strings.Contains(check.Hint, "first fix claude-config") || check.FixCommand != "" {
			t.Fatalf("claude-event-coverage remediation = hint %q fix %q; want no project-settings fix", check.Hint, check.FixCommand)
		}
	})

	t.Run("plugin-managed cached hook gaps suggest plugin update", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writePluginEnabledSettings(t, homeDir)
		writeClaudePluginCacheHooks(t, homeDir, `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'claude' 'start'"}]}
    ]
  }
}`)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "claude-event-coverage")
		if check.Status != "warn" {
			t.Fatalf("claude-event-coverage status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Hint, "plugin-managed") || !strings.Contains(check.FixCommand, "claude plugins update traceary@traceary-plugins") {
			t.Fatalf("claude-event-coverage remediation = hint %q fix %q; want plugin update", check.Hint, check.FixCommand)
		}
		if strings.Contains(check.FixCommand, "traceary doctor") {
			t.Fatalf("claude-event-coverage FixCommand = %q; want no project settings fix", check.FixCommand)
		}
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.AutoFixAvailable {
			t.Fatalf("claude-config AutoFixAvailable = true; plugin-managed remediation must not write project settings hooks")
		}
	})

	t.Run("plugin active with stale settings hooks does not suggest settings fix", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writePluginEnabledSettings(t, homeDir)
		writeClaudeProjectSessionOnlyHook(t, projectDir)
		writeClaudePluginCacheHooks(t, homeDir, `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'claude' 'start'"}]}
    ]
  }
}`)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(2, 1)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		configCheck := statusByName(report, "claude-config")
		if configCheck.Status != "warn" || !strings.Contains(configCheck.Message, "twice") {
			t.Fatalf("claude-config status/message = %q/%q; want double-registration warning", configCheck.Status, configCheck.Message)
		}
		coverage := statusByName(report, "claude-event-coverage")
		if coverage.Status != "warn" {
			t.Fatalf("claude-event-coverage status = %q, want warn (message=%q)", coverage.Status, coverage.Message)
		}
		if strings.Contains(coverage.FixCommand, "traceary doctor") {
			t.Fatalf("claude-event-coverage remediation = hint %q fix %q; want plugin/double-registration path, not settings fix", coverage.Hint, coverage.FixCommand)
		}
		if !strings.Contains(coverage.FixCommand, "claude plugins update traceary@traceary-plugins") {
			t.Fatalf("claude-event-coverage FixCommand = %q; want plugin update", coverage.FixCommand)
		}
	})

	t.Run("plugin-managed cached hook gaps warn before sample gate", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writePluginEnabledSettings(t, homeDir)
		writeClaudePluginCacheHooks(t, homeDir, `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'claude' 'start'"}]}
    ]
  }
}`)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(1, 0)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "warn" {
			t.Fatalf("claude-config status = %q, want warn (message=%q)", claudeCfg.Status, claudeCfg.Message)
		}
		if !strings.Contains(claudeCfg.Message, "installed plugin hook config") || !strings.Contains(claudeCfg.FixCommand, "claude plugins update traceary@traceary-plugins") {
			t.Fatalf("claude-config message/fix = %q / %q; want plugin cache remediation", claudeCfg.Message, claudeCfg.FixCommand)
		}
		coverage := statusByName(report, "claude-event-coverage")
		if coverage.Status != "pass" || !strings.Contains(coverage.Message, "not judged yet") {
			t.Fatalf("claude-event-coverage status/message = %q/%q; want sample gate pass while plugin config still warns", coverage.Status, coverage.Message)
		}
	})

	t.Run("settings-managed hook gaps warn before sample gate", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)
		writeClaudeProjectSessionOnlyHook(t, projectDir)
		eventStub := &eventUsecaseStub{listEvents: claudeCoverageEvents(1, 0)}

		rootCmd := newTestRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(eventStub),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "warn" {
			t.Fatalf("claude-config status = %q, want warn (message=%q)", claudeCfg.Status, claudeCfg.Message)
		}
		if !strings.Contains(claudeCfg.Message, "missing enrichment coverage") || !strings.Contains(claudeCfg.FixCommand, "--client claude") {
			t.Fatalf("claude-config message/fix = %q / %q; want static Claude enrichment remediation", claudeCfg.Message, claudeCfg.FixCommand)
		}
		coverage := statusByName(report, "claude-event-coverage")
		if coverage.Status != "pass" || !strings.Contains(coverage.Message, "not judged yet") {
			t.Fatalf("claude-event-coverage status/message = %q/%q; want sample gate pass while config still warns", coverage.Status, coverage.Message)
		}
	})
}

func TestRootCLI_DoctorStaleActiveSessions(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("no stale active sessions passes", func(t *testing.T) {
		initStub := &storeManagementUsecaseStub{
			staleResult: apptypes.CloseStaleSessionsResultOf(0),
		}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "stale-active-sessions")
		if check.Status != "pass" {
			t.Fatalf("stale-active-sessions status = %q, want pass (message: %q)", check.Status, check.Message)
		}
		if len(initStub.staleCalls) != 1 {
			t.Fatalf("CloseStaleSessions calls = %d, want 1", len(initStub.staleCalls))
		}
		if !initStub.staleCalls[0].dryRun {
			t.Fatalf("CloseStaleSessions dryRun = false, want true (doctor must never mutate)")
		}
	})

	t.Run("stale active sessions warn with actionable hint", func(t *testing.T) {
		initStub := &storeManagementUsecaseStub{
			staleResult: apptypes.CloseStaleSessionsResultOf(3),
		}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		check := statusByName(report, "stale-active-sessions")
		if check.Status != "warn" {
			t.Fatalf("stale-active-sessions status = %q, want warn (message: %q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "3") {
			t.Fatalf("expected message to surface the stale count, got %q", check.Message)
		}
		if !strings.Contains(check.FixCommand, "session gc") {
			t.Fatalf("FixCommand = %q, want a `session gc` remediation", check.FixCommand)
		}
		if !strings.Contains(check.Hint, "--dry-run") {
			t.Fatalf("expected hint to mention --dry-run preview, got %q", check.Hint)
		}
		if check.Section != "Database" {
			t.Fatalf("section = %q, want Database", check.Section)
		}
		if !initStub.staleCalls[0].dryRun {
			t.Fatalf("CloseStaleSessions dryRun = false, want true (doctor must never mutate)")
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
            "name": "traceary-session-start",
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-prompt",
            "type": "command",
            "command": "'traceary' 'hook' 'prompt' 'claude'"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-transcript",
            "type": "command",
            "command": "'traceary' 'hook' 'transcript' 'claude'"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "name": "traceary-audit",
            "type": "command",
            "command": "'traceary' 'hook' 'audit' 'claude'"
          }
        ]
      }
    ],
    "PostToolUseFailure": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "name": "traceary-audit",
            "type": "command",
            "command": "'traceary' 'hook' 'audit' 'claude'"
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

func writeClaudeProjectSessionOnlyHook(t *testing.T, projectDir string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
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
`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeCompleteClaudeProjectHookSettings(t *testing.T, projectDir string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'claude' 'start'"}]}
    ],
    "SessionEnd": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-end", "type": "command", "command": "'traceary' 'hook' 'session' 'claude' 'end'"}]}
    ],
    "UserPromptSubmit": [
      {"matcher": "*", "hooks": [{"name": "traceary-prompt", "type": "command", "command": "'traceary' 'hook' 'prompt' 'claude'"}]}
    ],
    "Stop": [
      {"matcher": "*", "hooks": [{"name": "traceary-transcript", "type": "command", "command": "'traceary' 'hook' 'transcript' 'claude'"}]}
    ],
    "PostToolUse": [
      {"matcher": "Bash", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'claude'"}]}
    ],
    "PostToolUseFailure": [
      {"matcher": "Bash", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'claude'"}]}
    ],
    "PreCompact": [
      {"matcher": "*", "hooks": [{"name": "traceary-compact-pre-compact", "type": "command", "command": "'traceary' 'hook' 'compact' 'claude' 'pre-compact'"}]}
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(claude settings) error = %v", err)
	}
}

func writeClaudePluginCacheHooks(t *testing.T, homeDir, content string) {
	t.Helper()
	cacheDir := writeClaudePluginCacheVersionDir(t, homeDir)
	hooksPath := filepath.Join(cacheDir, "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(hooksPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(plugin cache hooks) error = %v", err)
	}
}

func writeClaudePluginCacheVersionDir(t *testing.T, homeDir string) string {
	t.Helper()
	cacheDir := filepath.Join(homeDir, ".claude", "plugins", "cache", "traceary-plugins", "traceary", "0.20.1")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin cache version) error = %v", err)
	}
	return cacheDir
}

func writeClaudePluginMarketplaceHooks(t *testing.T, homeDir, content string) {
	t.Helper()
	hooksPath := filepath.Join(homeDir, ".claude", "plugins", "marketplaces", "traceary-plugins", "integrations", "claude-plugin", "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin marketplace hooks) error = %v", err)
	}
	if err := os.WriteFile(hooksPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(plugin marketplace hooks) error = %v", err)
	}
}

func writeLegacyGeminiProjectHookSettings(t *testing.T, projectDir string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'start'"}]}
    ],
    "SessionEnd": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-end", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'end'"}]}
    ],
    "AfterTool": [
      {"matcher": "run_shell_command", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'gemini'"}]}
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(gemini legacy settings) error = %v", err)
	}
}

func writeCompleteGeminiProjectHookSettings(t *testing.T, projectDir string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "mcpServers": {
    "traceary": {
      "command": "traceary",
      "args": ["mcp-server"]
    }
  },
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'start'"}]}
    ],
    "SessionEnd": [
      {"matcher": "*", "hooks": [{"name": "traceary-session-end", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'end'"}]}
    ],
    "BeforeAgent": [
      {"matcher": "*", "hooks": [{"name": "traceary-prompt", "type": "command", "command": "'traceary' 'hook' 'prompt' 'gemini'"}]}
    ],
    "AfterAgent": [
      {"matcher": "*", "hooks": [{"name": "traceary-transcript", "type": "command", "command": "'traceary' 'hook' 'transcript' 'gemini'"}]}
    ],
    "AfterTool": [
      {"matcher": "run_shell_command", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'gemini'"}]}
    ],
    "PreCompress": [
      {"matcher": "*", "hooks": [{"name": "traceary-pre-compress", "type": "command", "command": "'traceary' 'hook' 'compact' 'gemini' 'pre-compact'"}]}
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(gemini settings) error = %v", err)
	}
}

func geminiCoverageEvents(boundaryOnly, enriched int) []*model.Event {
	return clientCoverageEvents(types.Agent("gemini"), boundaryOnly, enriched)
}

func claudeCoverageEvents(boundaryOnly, enriched int) []*model.Event {
	return clientCoverageEvents(types.Agent("claude"), boundaryOnly, enriched)
}

func claudePromptOnlyCoverageEvents(count int) []*model.Event {
	events := make([]*model.Event, 0, count*2)
	createdAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		sessionID := fmt.Sprintf("prompt-only-%d", i)
		events = append(events,
			model.EventOf(
				types.EventID(fmt.Sprintf("prompt-only-%d-start", i)),
				types.EventKindSessionStarted,
				types.Client("hook"),
				types.Agent("claude"),
				types.SessionID(sessionID),
				types.Workspace("duck8823/traceary"),
				"body",
				createdAt,
			),
			model.EventOf(
				types.EventID(fmt.Sprintf("prompt-only-%d-prompt", i)),
				types.EventKindPrompt,
				types.Client("hook"),
				types.Agent("claude"),
				types.SessionID(sessionID),
				types.Workspace("duck8823/traceary"),
				"body",
				createdAt,
			),
		)
	}
	return events
}

func clientCoverageEvents(agent types.Agent, boundaryOnly, enriched int) []*model.Event {
	events := make([]*model.Event, 0, boundaryOnly*2+enriched*3)
	createdAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	appendEvent := func(id string, kind types.EventKind, sessionID string) {
		events = append(events, model.EventOf(
			types.EventID(id),
			kind,
			types.Client("hook"),
			agent,
			types.SessionID(sessionID),
			types.Workspace("duck8823/traceary"),
			"body",
			createdAt,
		))
	}
	for i := 0; i < boundaryOnly; i++ {
		sessionID := fmt.Sprintf("boundary-%d", i)
		appendEvent(fmt.Sprintf("boundary-%d-start", i), types.EventKindSessionStarted, sessionID)
		appendEvent(fmt.Sprintf("boundary-%d-end", i), types.EventKindSessionEnded, sessionID)
	}
	for i := 0; i < enriched; i++ {
		sessionID := fmt.Sprintf("enriched-%d", i)
		appendEvent(fmt.Sprintf("enriched-%d-start", i), types.EventKindSessionStarted, sessionID)
		appendEvent(fmt.Sprintf("enriched-%d-prompt", i), types.EventKindPrompt, sessionID)
		appendEvent(fmt.Sprintf("enriched-%d-transcript", i), types.EventKindTranscript, sessionID)
	}
	return events
}

func geminiAuditOnlyCoverageEvents(count int) []*model.Event {
	events := make([]*model.Event, 0, count*2)
	createdAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		sessionID := fmt.Sprintf("audit-only-%d", i)
		events = append(events,
			model.EventOf(
				types.EventID(fmt.Sprintf("audit-only-%d-start", i)),
				types.EventKindSessionStarted,
				types.Client("hook"),
				types.Agent("gemini"),
				types.SessionID(sessionID),
				types.Workspace("duck8823/traceary"),
				"body",
				createdAt,
			),
			model.EventOf(
				types.EventID(fmt.Sprintf("audit-only-%d-command", i)),
				types.EventKindCommandExecuted,
				types.Client("hook"),
				types.Agent("gemini"),
				types.SessionID(sessionID),
				types.Workspace("duck8823/traceary"),
				"body",
				createdAt,
			),
		)
	}
	return events
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

func TestRootCLI_DoctorAntigravity(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// Ensure antigravity is not resolvable on PATH so the detector uses
	// bundle-path probing only (injected via package-level execLookPathFunc
	// through the production inspectAntigravityCapability path).
	t.Setenv("PATH", t.TempDir())

	initStub := &storeManagementUsecaseStub{}

	t.Run("doctor --client antigravity reports capability check and correct clients", func(t *testing.T) {
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "antigravity", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())

		if diff := cmp.Diff([]string{"antigravity"}, report.Clients); diff != "" {
			t.Fatalf("report.Clients mismatch (-want +got):\n%s", diff)
		}

		checkNames := make([]string, 0, len(report.Checks))
		for _, check := range report.Checks {
			checkNames = append(checkNames, check.Name)
		}

		// Must include the capability check.
		foundCapability := false
		for _, name := range checkNames {
			if name == "antigravity-capability" {
				foundCapability = true
			}
		}
		if !foundCapability {
			t.Fatalf("antigravity-capability check not found in report; checks: %v", checkNames)
		}

		// Must NOT include hook-config or coverage checks for OTHER clients.
		// The Antigravity route checks (antigravity-hooks-workspace /
		// antigravity-hooks-user / antigravity-cli-plugin / antigravity-hooks)
		// are expected instead (v0.21.4).
		forbidden := []string{"claude-config", "codex-config", "gemini-config"}
		for _, name := range checkNames {
			for _, f := range forbidden {
				if name == f {
					t.Fatalf("unexpected check %q found in antigravity doctor report; checks: %v", name, checkNames)
				}
			}
		}

		// The capability check is pass when an Antigravity install is detected
		// (the supported hooks/plugin contract) and warn otherwise; both are
		// valid depending on the test environment.
		for _, check := range report.Checks {
			if check.Name == "antigravity-capability" {
				if check.Status != "warn" && check.Status != "pass" {
					t.Fatalf("antigravity-capability status = %q, want warn or pass", check.Status)
				}
			}
		}
	})

	t.Run("doctor default clients unchanged by antigravity addition", func(t *testing.T) {
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		if diff := cmp.Diff([]string{"claude", "codex", "gemini"}, report.Clients); diff != "" {
			t.Fatalf("default clients mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("not_installed path warns with non-empty capability message", func(t *testing.T) {
		// PATH is cleared (so no agy/antigravity CLI resolves) and the bundle
		// probe is forced to report "missing" so this subtest deterministically
		// exercises the not_installed path regardless of whether the host
		// machine actually has Antigravity installed.
		// The pure detector state assertions live in the internal unit tests;
		// this E2E subtest only validates the overall warn shape and message presence.
		cli.SetAntigravityBundleExistsFunc(func(string) bool { return false })
		t.Cleanup(cli.ResetAntigravityBundleExistsFunc)

		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "antigravity", "--project-dir", projectDir, "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		for _, check := range report.Checks {
			if check.Name != "antigravity-capability" {
				continue
			}
			if check.Status != "warn" {
				t.Fatalf("antigravity-capability status = %q, want warn", check.Status)
			}
			if check.Message == "" {
				t.Fatalf("antigravity-capability message is empty")
			}
		}
	})

	t.Run("no route installed warns once via the antigravity-hooks summary", func(t *testing.T) {
		// Fresh home with no user-level hooks file and a project dir with no
		// workspace .agents/hooks.json, so every route is absent.
		freshHome := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return freshHome, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "antigravity", "--project-dir", t.TempDir(), "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		byName := indexDoctorChecks(report.Checks)

		if got := byName["antigravity-hooks-workspace"].Status; got != "skip" {
			t.Fatalf("workspace route status = %q, want skip (absent route must not warn)", got)
		}
		if got := byName["antigravity-hooks-user"].Status; got != "skip" {
			t.Fatalf("user route status = %q, want skip", got)
		}
		summary, ok := byName["antigravity-hooks"]
		if !ok {
			t.Fatalf("antigravity-hooks summary check missing; checks: %v", report.Checks)
		}
		if summary.Status != "warn" {
			t.Fatalf("summary status = %q, want warn", summary.Status)
		}
		if !strings.Contains(summary.Message, "no supported Antigravity hook route") {
			t.Fatalf("summary message missing actionable text: %q", summary.Message)
		}
	})

	t.Run("healthy user-level route makes workspace optional (no warn)", func(t *testing.T) {
		// A healthy user-level ~/.gemini/config/hooks.json with no workspace
		// file must NOT warn about the missing workspace route — this is the
		// regression #1236 fixes.
		freshHome := t.TempDir()
		cli.SetUserHomeDirFunc(func() (string, error) { return freshHome, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		userHooks := filepath.Join(freshHome, ".gemini", "config", "hooks.json")
		if err := os.MkdirAll(filepath.Dir(userHooks), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		const healthyUserHooks = `{
  "traceary": {
    "PreInvocation": [
      {"type": "command", "command": "'traceary' 'hook' 'antigravity' 'pre-invocation'", "timeout": 10}
    ]
  }
}`
		if err := os.WriteFile(userHooks, []byte(healthyUserHooks), 0o600); err != nil {
			t.Fatalf("write user hooks: %v", err)
		}

		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "antigravity", "--project-dir", t.TempDir(), "--json"})

		executeDoctorAllowWarnings(t, rootCmd)

		report := decodeDoctorReport(t, stdout.Bytes())
		byName := indexDoctorChecks(report.Checks)

		if got := byName["antigravity-hooks-user"].Status; got != "pass" {
			t.Fatalf("user route status = %q, want pass", got)
		}
		if got := byName["antigravity-hooks-workspace"].Status; got != "skip" {
			t.Fatalf("workspace route status = %q, want skip (must not warn when user route healthy)", got)
		}
		summary := byName["antigravity-hooks"]
		if summary.Status != "pass" {
			t.Fatalf("summary status = %q, want pass; message: %q", summary.Status, summary.Message)
		}
		if !strings.Contains(summary.Message, "optional") {
			t.Fatalf("summary should note workspace hooks are optional: %q", summary.Message)
		}
	})
}

func indexDoctorChecks(checks []doctorCheck) map[string]doctorCheck {
	byName := make(map[string]doctorCheck, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}
	return byName
}
