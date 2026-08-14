package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// noReplacement marks a surface that is going away without a successor, so the
// notice says so rather than naming a replacement it does not have.
const noReplacement = ""

// applyCommandDeprecation adds the one notice emitted by a deprecated command.
//
// The notice is attached to the run step, not to PreRunE: cobra validates
// required flags between the two, so a PreRunE notice would fire for
// invocations that never run. See the notice rules in docs/cli-stability.md.
func applyCommandDeprecation(cmd *cobra.Command, replacement string, removalVersion string) {
	annotateDeprecationHelp(cmd, replacement, removalVersion)
	previous := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		writeDeprecationNotice(cmd, "this command", "このコマンド", replacement, removalVersion)

		if previous != nil {
			return previous(cmd, args)
		}
		// Cobra prefers RunE once it is set, so a command that only had Run
		// keeps running through it here.
		if cmd.Run != nil {
			cmd.Run(cmd, args)
		}
		return nil
	}
}

// writeDeprecationNotice emits the notice immediately. Callers that deprecate a
// default behaviour rather than a command path use it directly, at the point
// where the behaviour is about to happen; `apply*` installs a hook instead.
func writeDeprecationNotice(cmd *cobra.Command, subject string, japaneseSubject string, replacement string, removalVersion string) {
	message := Localize(
		fmt.Sprintf("DEPRECATED: %s is deprecated with no replacement. Removal target: %s.", subject, removalVersion),
		fmt.Sprintf("DEPRECATED: %sは非推奨です。置き換え先はありません。削除予定: %s。", japaneseSubject, removalVersion),
	)
	if replacement != noReplacement {
		message = Localize(
			fmt.Sprintf("DEPRECATED: %s is deprecated, use `%s` instead. Removal target: %s.", subject, replacement, removalVersion),
			fmt.Sprintf("DEPRECATED: %sは非推奨です。代わりに `%s` を使用してください。削除予定: %s。", japaneseSubject, replacement, removalVersion),
		)
	}
	// The notice is advisory; a broken stderr writer must not change the command contract.
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
}

// annotateDeprecationHelp puts the replacement and removal target on Short
// (and Long when present). --help never reaches RunE, so the run-step notice
// alone would leave help silent.
func annotateDeprecationHelp(cmd *cobra.Command, replacement string, removalVersion string) {
	suffix := Localize(
		fmt.Sprintf("Deprecated with no replacement. Removal target: %s.", removalVersion),
		fmt.Sprintf("非推奨です。置き換え先はありません。削除予定: %s。", removalVersion),
	)
	if replacement != noReplacement {
		suffix = Localize(
			fmt.Sprintf("Deprecated; use `%s`. Removal target: %s.", replacement, removalVersion),
			fmt.Sprintf("非推奨です。代わりに `%s` を使用してください。削除予定: %s。", replacement, removalVersion),
		)
	}
	if cmd.Short == "" {
		cmd.Short = suffix
	} else {
		cmd.Short = cmd.Short + " (" + suffix + ")"
	}
	if cmd.Long != "" {
		cmd.Long = cmd.Long + "\n\n" + suffix
	}
}
