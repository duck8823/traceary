package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_IntegrationCodexInstallCommand(t *testing.T) {
	stub := &codexIntegrationUsecaseStub{
		installResult: apptypes.CodexIntegrationInstallResultOf(
			"/tmp/agents/plugins/plugins/traceary",
			"/tmp/.codex/plugins/cache/local-traceary-plugins/traceary/local",
			"/tmp/.codex/config.toml",
			"/tmp/.codex/hooks.json",
			"traceary@local-traceary-plugins",
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
		"install",
		"--repo-root", "/tmp/traceary",
		"--codex-home", "/tmp/.codex",
		"--marketplace-root", "/tmp/agents/plugins",
		"--traceary-bin", "/tmp/bin/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if diff := cmp.Diff("/tmp/traceary", stub.installCall.repoRoot); diff != "" {
		t.Fatalf("repoRoot mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("/tmp/.codex", stub.installCall.codexHome); diff != "" {
		t.Fatalf("codexHome mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("/tmp/agents/plugins", stub.installCall.marketplaceRoot); diff != "" {
		t.Fatalf("marketplaceRoot mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("/tmp/bin/traceary", stub.installCall.tracearyBin); diff != "" {
		t.Fatalf("tracearyBin mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, strings.Contains(stdout.String(), "enabled plugin id traceary@local-traceary-plugins")); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
	// Deprecation banner goes to stderr so scripts that parse stdout for
	// install result paths keep working.
	if !strings.Contains(stderr.String(), "DEPRECATED") {
		t.Fatalf("stderr missing deprecation banner, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "/plugins") {
		t.Fatalf("stderr deprecation banner should point at /plugins flow, got %q", stderr.String())
	}
}

func TestRootCLI_IntegrationCodexUninstallCommand_NoDeprecationBanner(t *testing.T) {
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

	// Uninstall stays migration-friendly: it is the cleanup path legacy
	// users actually need, so it must not print a deprecation banner.
	if strings.Contains(stderr.String(), "DEPRECATED") {
		t.Fatalf("uninstall unexpectedly printed a deprecation banner on stderr: %q", stderr.String())
	}
}

func TestRootCLI_IntegrationCodexInstallCommand_DefaultPaths(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stub := &codexIntegrationUsecaseStub{
		installResult: apptypes.CodexIntegrationInstallResultOf("", "", "", "", ""),
	}

	rootCmd := newTestRootCLI(cli.WithCodexIntegration(stub)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"integration",
		"codex",
		"install",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("filepath.Abs(.) error = %v", err)
	}
	if diff := cmp.Diff(cwd, stub.installCall.repoRoot); diff != "" {
		t.Fatalf("repoRoot mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(filepath.Join(homeDir, ".codex"), stub.installCall.codexHome); diff != "" {
		t.Fatalf("codexHome mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(filepath.Join(homeDir, ".agents", "plugins"), stub.installCall.marketplaceRoot); diff != "" {
		t.Fatalf("marketplaceRoot mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("traceary", stub.installCall.tracearyBin); diff != "" {
		t.Fatalf("tracearyBin mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_IntegrationCodexUninstallCommand(t *testing.T) {
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
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
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
}
