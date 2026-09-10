package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestHooksInstall_CodexPluginOwnershipUsesEffectiveTrustedContract(t *testing.T) {
	for _, tt := range []struct {
		name      string
		trust     cli.CodexPluginHookTrustResult
		plugin    bool
		manual    bool
		upgrade   bool
		force     bool
		wantSkip  bool
		wantUsage bool
	}{
		{name: "complete trusted install without manual file", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustTrusted}, wantSkip: true},
		{name: "complete trusted install preserves manual file", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustTrusted}, manual: true, wantSkip: true},
		{name: "complete trusted upgrade preserves manual file", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustTrusted}, manual: true, upgrade: true, wantSkip: true},
		{name: "complete trusted force install preserves manual file", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustTrusted}, manual: true, force: true, wantSkip: true},
		{name: "plugin missing falls back", trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustAbsent}, wantUsage: true},
		{name: "incomplete including missing usage falls back", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustIncomplete}, wantUsage: true},
		{name: "untrusted falls back", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustUntrusted}, wantUsage: true},
		{name: "disabled falls back", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustDisabled}, wantUsage: true},
		{name: "probe failure falls back", plugin: true, trust: cli.CodexPluginHookTrustResult{Status: cli.CodexPluginHookTrustUndetectable}, wantUsage: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			projectDir := t.TempDir()
			if tt.plugin {
				writeCodexPluginConfig(t, homeDir)
			}
			cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
			t.Cleanup(cli.ResetUserHomeDirFunc)
			cli.SetCodexPluginHookTrustProbeFunc(func(context.Context, string, string) cli.CodexPluginHookTrustResult {
				result := tt.trust
				result.PluginKey = "traceary@traceary-marketplace"
				result.HookCount = cli.ExpectedCodexPluginHookCount()
				return result
			})
			t.Cleanup(cli.ResetCodexPluginHookTrustProbeFunc)

			hooksPath := filepath.Join(homeDir, ".codex", "hooks.json")
			original := []byte(`{"hooks":{"SessionStart":[{"hooks":[{"name":"user-hook","type":"command","command":"echo preserved"}]}]}}`)
			if tt.manual {
				if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
					t.Fatalf("MkdirAll() error = %v", err)
				}
				if err := os.WriteFile(hooksPath, original, 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			}

			rootCmd := newTestRootCLI().Command()
			stdout := &bytes.Buffer{}
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			args := []string{"hooks", "install", "--client", "codex", "--project-dir", projectDir, "--traceary-bin", "traceary"}
			if tt.upgrade {
				args = append(args, "--upgrade")
			}
			if tt.force {
				args = append(args, "--force")
			}
			rootCmd.SetArgs(args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if tt.wantSkip {
				if !strings.Contains(stdout.String(), "owns all current enabled and trusted hooks") || !strings.Contains(stdout.String(), "doctor --fix") {
					t.Fatalf("stdout = %q, want ownership and explicit reconciliation notice", stdout.String())
				}
				content, err := os.ReadFile(hooksPath)
				if tt.manual {
					if err != nil || string(content) != string(original) {
						t.Fatalf("trusted plugin changed existing manual hooks: content %q, err %v", content, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("trusted plugin created manual hooks: stat err = %v", err)
				}
				return
			}

			content, err := os.ReadFile(hooksPath)
			if err != nil {
				t.Fatalf("fallback manual hooks missing: %v", err)
			}
			if !strings.Contains(string(content), "traceary-usage") && !strings.Contains(string(content), "'usage' 'codex'") {
				t.Errorf("fallback hooks = %q; want current usage hook", content)
			}
			if tt.wantUsage && !strings.Contains(string(content), "'usage' 'codex'") {
				t.Errorf("fallback hooks = %q; want usage command", content)
			}
		})
	}
}

func writeCodexPluginConfig(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[plugins.\"traceary@traceary-marketplace\"]\nenabled = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
