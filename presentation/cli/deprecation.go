package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// applyDeprecation marks cmd as a deprecated alias and emits the
// notice to stderr via PreRun rather than Cobra's built-in Deprecated
// field. Cobra's `Deprecated` field routes the warning through
// `cmd.Printf` which targets stdout (or whatever `SetOut` binds),
// so legacy consumers of the alias's stdout — scripts that parsed
// v0.8.x `compact-summary` output byte-for-byte, for example — would
// see the warning mixed into their payload.
//
// Emitting to stderr keeps stdout byte-for-byte compatible while
// still surfacing the replacement path to operators running the
// alias interactively. The command is also hidden from the top-level
// `--help` listing (so only canonical entry points are advertised)
// but its own `--help` still shows the deprecation notice via a
// prepended Long-description line, matching what users saw before
// with Cobra's built-in Deprecated field.
func applyDeprecation(cmd *cobra.Command, message string) {
	cmd.Hidden = true
	// Prepend the deprecation notice to Long so `cmd --help` still
	// displays it (Cobra's PreRun does not fire for --help).
	notice := fmt.Sprintf("DEPRECATED: this command is deprecated, %s", message)
	if cmd.Long != "" {
		cmd.Long = notice + "\n\n" + cmd.Long
	} else if cmd.Short != "" {
		cmd.Long = notice + "\n\n" + cmd.Short
	} else {
		cmd.Long = notice
	}
	existing := cmd.PreRun
	cmd.PreRun = func(c *cobra.Command, args []string) {
		// A write failure to stderr is not worth failing the command
		// over (stderr can be closed or redirected to /dev/null in
		// non-interactive pipelines); best-effort notice is enough.
		_, _ = fmt.Fprintf(c.ErrOrStderr(), "Command %q is deprecated, %s\n", c.Name(), message)
		if existing != nil {
			existing(c, args)
		}
	}
}
