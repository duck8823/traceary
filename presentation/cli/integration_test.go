package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_IntegrationCodexInstallCommand_RemovedReportsReplacement
// confirms that the legacy `traceary integration codex install` command
// is no longer a working install path: it must exit with an error that
// names the v0.14.0 removal and points at the Codex official `/plugins`
// flow as the supported replacement (#920).
func TestRootCLI_IntegrationCodexInstallCommand_RemovedReportsReplacement(t *testing.T) {
	t.Parallel()

	stub := &codexIntegrationUsecaseStub{}

	rootCmd := newTestRootCLI(cli.WithCodexIntegration(stub)).Command()
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
	for _, want := range []string{"v0.14.0", "/plugins", "Traceary Plugins", "uninstall"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	if stub.installCall.repoRoot != "" || stub.installCall.codexHome != "" {
		t.Fatalf("install usecase unexpectedly invoked: %+v", stub.installCall)
	}
}

// TestRootCLI_IntegrationCodexCommandsHiddenFromHelp confirms that the
// `traceary integration codex --help` output advertises neither the
// retired `install` command nor the cleanup-only `uninstall` command, so
// the legacy surface is invisible from default help output (#920).
func TestRootCLI_IntegrationCodexCommandsHiddenFromHelp(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI(cli.WithCodexIntegration(&codexIntegrationUsecaseStub{})).Command()
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

// TestRootCLI_IntegrationCodexUninstallCommand confirms the cleanup-only
// `traceary integration codex uninstall` command still works for users
// migrating off the retired install path. The command stays Hidden but
// remains executable until v0.15.
func TestRootCLI_IntegrationCodexUninstallCommand(t *testing.T) {
	t.Parallel()

	stub := &codexIntegrationUsecaseStub{
		uninstallResult: apptypes.CodexIntegrationUninstallResultOf(
			"/tmp/agents/plugins/plugins/traceary",
			true,
			"/tmp/agents/plugins/marketplace.json",
			"/tmp/.codex/plugins/cache/local-traceary-plugins/traceary",
			false,
			"/tmp/.codex/config.toml",
			"/tmp/.codex/hooks.json",
			true,
		),
	}

	rootCmd := newTestRootCLI(cli.WithCodexIntegration(stub)).Command()
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

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if diff := cmp.Diff("/tmp/.codex", stub.uninstallCall.codexHome); diff != "" {
		t.Fatalf("codexHome mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("/tmp/agents/plugins", stub.uninstallCall.marketplaceRoot); diff != "" {
		t.Fatalf("marketplaceRoot mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(stdout.String(), "removed marketplace copy /tmp/agents/plugins/plugins/traceary")); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(stdout.String(), "plugin cache already absent")); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(stdout.String(), "removed Traceary Codex hooks from /tmp/.codex/hooks.json")); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
	// The cleanup-only uninstall path must not leak stale install
	// deprecation banners that referenced the retired install command.
	if strings.Contains(stderr.String(), "DEPRECATED") {
		t.Fatalf("uninstall unexpectedly printed a deprecation banner on stderr: %q", stderr.String())
	}
}
