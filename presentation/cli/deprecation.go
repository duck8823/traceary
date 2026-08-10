package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// applyCommandDeprecation adds the one notice emitted by a deprecated command.
func applyCommandDeprecation(cmd *cobra.Command, replacement string, removalVersion string) {
	previous := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		message := Localize(
			fmt.Sprintf("DEPRECATED: this command is deprecated, use `%s` instead. Removal target: %s.", replacement, removalVersion),
			fmt.Sprintf("DEPRECATED: このコマンドは非推奨です。代わりに `%s` を使用してください。削除予定: %s。", replacement, removalVersion),
		)
		// The notice is advisory; a broken stderr writer must not change the command contract.
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)

		if previous != nil {
			return previous(cmd, args)
		}
		return nil
	}
}
