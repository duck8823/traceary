package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// applyCommandDeprecation adds the one notice emitted by a deprecated command.
func applyCommandDeprecation(cmd *cobra.Command, replacement string, removalVersion string) {
	applyDeprecation(cmd, "this command", "このコマンド", replacement, removalVersion)
}

// applyDeprecation adds a localized deprecation notice to a command hook.
func applyDeprecation(cmd *cobra.Command, subject string, japaneseSubject string, replacement string, removalVersion string) {
	previous := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		writeDeprecationNotice(cmd, subject, japaneseSubject, replacement, removalVersion)

		if previous != nil {
			return previous(cmd, args)
		}
		return nil
	}
}

// writeDeprecationNotice emits the notice immediately. Callers that deprecate a
// default behaviour rather than a command path use it directly, at the point
// where the behaviour is about to happen; `apply*` installs a hook instead.
func writeDeprecationNotice(cmd *cobra.Command, subject string, japaneseSubject string, replacement string, removalVersion string) {
	message := Localize(
		fmt.Sprintf("DEPRECATED: %s is deprecated, use `%s` instead. Removal target: %s.", subject, replacement, removalVersion),
		fmt.Sprintf("DEPRECATED: %sは非推奨です。代わりに `%s` を使用してください。削除予定: %s。", japaneseSubject, replacement, removalVersion),
	)
	// The notice is advisory; a broken stderr writer must not change the command contract.
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
}
