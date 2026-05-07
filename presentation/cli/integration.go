package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newIntegrationCommand() *cobra.Command {
	integrationCmd := &cobra.Command{
		Use:   "integration",
		Short: Localize("Manage packaged local agent integrations", "ローカル agent 連携パッケージを管理する"),
	}
	integrationCmd.AddCommand(c.newIntegrationCodexCommand())
	return integrationCmd
}

func (c *RootCLI) newIntegrationCodexCommand() *cobra.Command {
	codexCmd := &cobra.Command{
		Use:   "codex",
		Short: Localize("Manage the packaged Codex integration", "Codex 向けの連携パッケージを管理する"),
	}
	codexCmd.AddCommand(c.newIntegrationCodexInstallCommand())
	codexCmd.AddCommand(c.newIntegrationCodexUninstallCommand())
	return codexCmd
}

// newIntegrationCodexInstallCommand registers a hidden stub for the
// retired `traceary integration codex install` command. The command is
// no longer a working install path; invoking it returns a usage error
// pointing at the Codex official `/plugins` flow. The hidden registration
// keeps the replacement hint reachable for users on automation that still
// types the old command (#920).
func (c *RootCLI) newIntegrationCodexInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "install",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return xerrors.New(Localize(
				"`traceary integration codex install` was removed in v0.14.0. Use Codex's official /plugins flow: run `codex` inside this repository, open `/plugins`, and install `Traceary` from `Traceary Plugins`. The hidden `traceary integration codex uninstall` remains as cleanup-only until v0.15.",
				"`traceary integration codex install` は v0.14.0 で削除されました。Codex 公式の /plugins flow を使ってください: この repository 上で `codex` を起動し、`/plugins` を開き、`Traceary Plugins` から `Traceary` を install してください。hidden な `traceary integration codex uninstall` は v0.15 までの cleanup 用途として残します。",
			))
		},
	}
}

// newIntegrationCodexUninstallCommand registers the cleanup-only
// `traceary integration codex uninstall` command. v0.14.0 hides the
// command from default help so it stays available for users migrating
// off the retired install path without advertising it as a normal flow.
// The command is scheduled for removal in v0.15 once migration is
// complete (#920).
func (c *RootCLI) newIntegrationCodexUninstallCommand() *cobra.Command {
	var (
		codexHome       string
		marketplaceRoot string
	)

	cmd := &cobra.Command{
		Use:    "uninstall",
		Hidden: true,
		Short:  Localize("Remove legacy Codex integration state (hidden cleanup-only command, scheduled for removal in v0.15)", "旧 Codex 連携 state を取り除く (非表示の cleanup 専用 command、v0.15 で削除予定)"),
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runIntegrationCodexUninstall(cmd.Context(), cmd.OutOrStdout(), integrationCodexUninstallCommandInput{
				codexHome:       codexHome,
				marketplaceRoot: marketplaceRoot,
			})
		},
	}
	cmd.Flags().StringVar(&codexHome, "codex-home", "", Localize("Codex home directory (defaults to ~/.codex)", "Codex home ディレクトリ (既定値: ~/.codex)"))
	cmd.Flags().StringVar(&marketplaceRoot, "marketplace-root", "", Localize("local marketplace root (defaults to ~/.agents/plugins)", "local marketplace root (既定値: ~/.agents/plugins)"))
	return cmd
}

type integrationCodexUninstallCommandInput struct {
	codexHome       string
	marketplaceRoot string
}

func (c *RootCLI) runIntegrationCodexUninstall(
	ctx context.Context,
	output io.Writer,
	input integrationCodexUninstallCommandInput,
) error {
	if c.codexIntegration == nil {
		return xerrors.Errorf("Codex integration usecase is not configured")
	}

	resolvedCodexHome, err := resolveCodexHome(input.codexHome)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve Codex home", "Codex home の解決に失敗しました"), err)
	}
	resolvedMarketplaceRoot, err := resolveMarketplaceRoot(input.marketplaceRoot)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve marketplace root", "marketplace root の解決に失敗しました"), err)
	}

	result, err := c.codexIntegration.Uninstall(ctx, resolvedCodexHome, resolvedMarketplaceRoot)
	if err != nil {
		return xerrors.Errorf("failed to uninstall Codex integration: %w", err)
	}

	marketplaceCopyLine := fmt.Sprintf("marketplace copy already absent: %s\n", result.MarketplaceCopyPath())
	if result.MarketplaceCopyRemoved() {
		marketplaceCopyLine = fmt.Sprintf("removed marketplace copy %s\n", result.MarketplaceCopyPath())
	}

	pluginCacheLine := fmt.Sprintf("plugin cache already absent: %s\n", result.ActivePluginCachePath())
	if result.ActivePluginCacheRemoved() {
		pluginCacheLine = fmt.Sprintf("removed active Codex plugin cache %s\n", result.ActivePluginCachePath())
	}

	hooksLine := fmt.Sprintf("Codex hooks already absent: %s\n", result.HooksPath())
	if result.HooksRemoved() {
		hooksLine = fmt.Sprintf("removed Traceary Codex hooks from %s\n", result.HooksPath())
	}

	_, err = fmt.Fprintf(
		output,
		"%supdated marketplace manifest at %s\n%supdated Codex config at %s\n%sleft [features].codex_hooks unchanged so other local hook workflows keep working\n",
		marketplaceCopyLine,
		result.MarketplaceManifestPath(),
		pluginCacheLine,
		result.ConfigPath(),
		hooksLine,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print Codex integration uninstall result", "Codex 連携 uninstall 結果の出力に失敗しました"), err)
	}

	return nil
}

func resolveCodexHome(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) == "" {
		homeDir, err := userHomeDirFunc()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
		}
		flagValue = filepath.Join(homeDir, ".codex")
	}
	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}
	return resolvedPath, nil
}

func resolveMarketplaceRoot(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) == "" {
		homeDir, err := userHomeDirFunc()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
		}
		flagValue = filepath.Join(homeDir, ".agents", "plugins")
	}
	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}
	return resolvedPath, nil
}
