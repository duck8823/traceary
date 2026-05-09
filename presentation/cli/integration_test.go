package cli_test

import (
	"bytes"
	"strings"
	"testing"
)

// TestRootCLI_IntegrationCodexInstallCommand_RemovedReportsReplacement
// confirms that the legacy `traceary integration codex install` command
// is no longer a working install path: it must exit with an error that
// names the v0.14.0 removal and points at the Codex official `/plugins`
// flow as the supported replacement (#920).
func TestRootCLI_IntegrationCodexInstallCommand_RemovedReportsReplacement(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{
		"integration",
		"codex",
		"install",
		"--repo-root", "/tmp/traceary",
		"--codex-home", "/tmp/.codex",
		"--marketplace-root", "/tmp/agents/plugins",
		"--traceary-bin", "/tmp/bin/traceary",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want removed-install error")
	}
	msg := err.Error()
	for _, want := range []string{"v0.14.0", "/plugins", "Traceary Plugins", "codex-plugin.md"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

// TestRootCLI_IntegrationCodexUninstallCommand_RemovedReportsReplacement
// confirms that the cleanup-only `traceary integration codex uninstall`
// command was removed in v0.15.0 and now exits with a usage error that
// names the v0.15.0 removal and points at Codex's official `/plugins`
// flow plus the manual cleanup steps documented in
// docs/integrations/codex-plugin.md (#957).
func TestRootCLI_IntegrationCodexUninstallCommand_RemovedReportsReplacement(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{
		"integration",
		"codex",
		"uninstall",
		"--codex-home", "/tmp/.codex",
		"--marketplace-root", "/tmp/agents/plugins",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want removed-uninstall error")
	}
	msg := err.Error()
	for _, want := range []string{"v0.15.0", "/plugins", "codex-plugin.md"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

// TestRootCLI_IntegrationCodexCommandsHiddenFromHelp confirms that the
// `traceary integration codex --help` output advertises neither the
// retired `install` command nor the retired `uninstall` command, so the
// legacy surface is invisible from default help output (#920, #957).
func TestRootCLI_IntegrationCodexCommandsHiddenFromHelp(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"integration", "codex", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(integration codex --help) error = %v", err)
	}

	help := stdout.String()
	available := extractAvailableCommandsBlock(help)
	for _, hidden := range []string{"install", "uninstall"} {
		if strings.Contains(available, hidden) {
			t.Fatalf("traceary integration codex --help still advertises %q:\n%s", hidden, available)
		}
	}
}
