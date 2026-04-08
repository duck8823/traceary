package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newCompletionCommand(rootCmd *cobra.Command) *cobra.Command {
	completionCmd := &cobra.Command{
		Use:   "completion",
		Short: Localize("Generate shell completion scripts", "shell completion script を生成する"),
		Args:  noArgsJP(),
	}

	completionCmd.AddCommand(newCompletionSubcommand(
		"bash",
		Localize("Generate Bash completion", "Bash completion を生成する"),
		func(cmd *cobra.Command, _ []string) error {
			if err := rootCmd.GenBashCompletionV2(cmd.OutOrStdout(), true); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to generate bash completion", "bash completion の生成に失敗しました"), err)
			}

			return nil
		},
	))
	completionCmd.AddCommand(newCompletionSubcommand(
		"zsh",
		Localize("Generate Zsh completion", "Zsh completion を生成する"),
		func(cmd *cobra.Command, _ []string) error {
			if err := rootCmd.GenZshCompletion(cmd.OutOrStdout()); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to generate zsh completion", "zsh completion の生成に失敗しました"), err)
			}

			return nil
		},
	))
	completionCmd.AddCommand(newCompletionSubcommand(
		"fish",
		Localize("Generate Fish completion", "Fish completion を生成する"),
		func(cmd *cobra.Command, _ []string) error {
			if err := rootCmd.GenFishCompletion(cmd.OutOrStdout(), true); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to generate fish completion", "fish completion の生成に失敗しました"), err)
			}

			return nil
		},
	))
	completionCmd.AddCommand(newCompletionSubcommand(
		"powershell",
		Localize("Generate PowerShell completion", "PowerShell completion を生成する"),
		func(cmd *cobra.Command, _ []string) error {
			if err := rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout()); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to generate PowerShell completion", "PowerShell completion の生成に失敗しました"), err)
			}

			return nil
		},
	))

	return completionCmd
}

func newCompletionSubcommand(
	use string,
	short string,
	runE func(cmd *cobra.Command, args []string) error,
) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  noArgsJP(),
		RunE:  runE,
	}
}
