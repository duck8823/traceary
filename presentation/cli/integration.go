package cli

import (
	"context"
	"fmt"
	"io"
	"os"
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

func (c *RootCLI) newIntegrationCodexInstallCommand() *cobra.Command {
	var (
		repoRoot        string
		codexHome       string
		marketplaceRoot string
		tracearyBin     string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: Localize("Install the packaged Codex integration into the local Codex runtime", "Codex 向けの連携パッケージをローカルの Codex runtime に組み込む"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runIntegrationCodexInstall(cmd.Context(), cmd.OutOrStdout(), integrationCodexInstallCommandInput{
				repoRoot:        repoRoot,
				codexHome:       codexHome,
				marketplaceRoot: marketplaceRoot,
				tracearyBin:     tracearyBin,
			})
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo-root", "", Localize("repository root that contains plugins/traceary (defaults to the current directory)", "plugins/traceary を含む repository root (既定値: カレントディレクトリ)"))
	cmd.Flags().StringVar(&codexHome, "codex-home", "", Localize("Codex home directory (defaults to ~/.codex)", "Codex home ディレクトリ (既定値: ~/.codex)"))
	cmd.Flags().StringVar(&marketplaceRoot, "marketplace-root", "", Localize("local marketplace root (defaults to ~/.agents/plugins)", "local marketplace root (既定値: ~/.agents/plugins)"))
	cmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", Localize("traceary binary path or command name", "traceary バイナリパス"))
	return cmd
}

func (c *RootCLI) newIntegrationCodexUninstallCommand() *cobra.Command {
	var (
		codexHome       string
		marketplaceRoot string
	)

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: Localize("Remove the packaged Codex integration from the local Codex runtime", "Codex 向けの連携パッケージをローカルの Codex runtime から外す"),
		Args:  noArgsLocalized(),
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

type integrationCodexInstallCommandInput struct {
	repoRoot        string
	codexHome       string
	marketplaceRoot string
	tracearyBin     string
}

type integrationCodexUninstallCommandInput struct {
	codexHome       string
	marketplaceRoot string
}

func (c *RootCLI) runIntegrationCodexInstall(
	ctx context.Context,
	output io.Writer,
	input integrationCodexInstallCommandInput,
) error {
	if c.codexIntegration == nil {
		return xerrors.Errorf("Codex integration usecase is not configured")
	}

	resolvedRepoRoot, err := resolveRepoRoot(input.repoRoot)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve repository root", "repository root の解決に失敗しました"), err)
	}
	resolvedCodexHome, err := resolveCodexHome(input.codexHome)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve Codex home", "Codex home の解決に失敗しました"), err)
	}
	resolvedMarketplaceRoot, err := resolveMarketplaceRoot(input.marketplaceRoot)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve marketplace root", "marketplace root の解決に失敗しました"), err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}

	result, err := c.codexIntegration.Install(
		ctx,
		resolvedRepoRoot,
		resolvedCodexHome,
		resolvedMarketplaceRoot,
		resolvedTracearyBin,
	)
	if err != nil {
		return xerrors.Errorf("failed to install Codex integration: %w", err)
	}

	_, err = fmt.Fprintf(
		output,
		"installed marketplace copy at %s\ninstalled active Codex plugin at %s\nupdated Codex config at %s\nupdated Codex hooks at %s\nenabled plugin id %s\n",
		result.MarketplaceCopyPath(),
		result.ActivePluginPath(),
		result.ConfigPath(),
		result.HooksPath(),
		result.PluginID(),
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print Codex integration install result", "Codex 連携 install 結果の出力に失敗しました"), err)
	}

	return nil
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

func resolveRepoRoot(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get current directory", "カレントディレクトリの取得に失敗しました"), err)
		}
		flagValue = currentDir
	}

	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}
	return resolvedPath, nil
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
