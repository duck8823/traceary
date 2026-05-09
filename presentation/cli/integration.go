package cli

import (
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
// retired `traceary integration codex install` command. v0.14.0 retired
// the install path and v0.15.0 removed the matching uninstall cleanup
// command; both stubs now point at Codex's official `/plugins` flow as
// the supported install/uninstall surface (#920, #957).
func (c *RootCLI) newIntegrationCodexInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "install",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return xerrors.New(Localize(
				"`traceary integration codex install` was removed in v0.14.0. Use Codex's official /plugins flow: run `codex` inside this repository, open `/plugins`, and install `Traceary` from `Traceary Plugins`. See docs/integrations/codex-plugin.md for the full migration guide.",
				"`traceary integration codex install` は v0.14.0 で削除されました。Codex 公式の /plugins flow を使ってください: この repository 上で `codex` を起動し、`/plugins` を開き、`Traceary Plugins` から `Traceary` を install してください。詳細な移行手順は docs/integrations/codex-plugin.ja.md を参照してください。",
			))
		},
	}
}

// newIntegrationCodexUninstallCommand registers a hidden stub for the
// retired `traceary integration codex uninstall` command. v0.14.0 kept
// the command as a hidden cleanup-only escape hatch and announced its
// removal target as v0.15. The stub exits non-zero with a localized
// migration error pointing at Codex's official `/plugins` flow and the
// manual cleanup steps documented in docs/integrations/codex-plugin.md
// (#957).
func (c *RootCLI) newIntegrationCodexUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "uninstall",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return xerrors.New(Localize(
				"`traceary integration codex uninstall` was removed in v0.15.0. Use Codex's official /plugins flow to uninstall the Traceary plugin: run `codex` inside this repository, open `/plugins`, and uninstall `Traceary`. For legacy state left behind by the retired pre-v0.14 install path, follow the manual cleanup steps in docs/integrations/codex-plugin.md.",
				"`traceary integration codex uninstall` は v0.15.0 で削除されました。Codex 公式の /plugins flow で Traceary plugin を uninstall してください: この repository 上で `codex` を起動し、`/plugins` を開き、`Traceary` を uninstall してください。v0.14 以前の廃止された install path が残した legacy state は docs/integrations/codex-plugin.ja.md の手動 cleanup 手順を参照してください。",
			))
		},
	}
}
