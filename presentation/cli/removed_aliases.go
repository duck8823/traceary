package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// removedAlias describes a former top-level command name that was
// retired in v0.14.0. The Cobra tree still registers a hidden stub for
// each entry so operators who type the old name receive a concrete
// migration hint instead of Cobra's generic "unknown command" output
// (#918). The stub is hidden, so `traceary --help` no longer advertises
// the legacy surface.
type removedAlias struct {
	use         string
	replacement string
}

func (c *RootCLI) addRemovedAliases(rootCmd *cobra.Command) {
	for _, alias := range []removedAlias{
		{use: "init", replacement: "traceary store init"},
		{use: "backup", replacement: "traceary store backup"},
		{use: "gc", replacement: "traceary store gc"},
		{use: "handoff", replacement: "traceary session handoff"},
		{use: "compact-summary", replacement: "traceary session handoff --compact-only"},
	} {
		rootCmd.AddCommand(newRemovedAliasCommand(alias))
	}
}

func newRemovedAliasCommand(alias removedAlias) *cobra.Command {
	return &cobra.Command{
		Use:                alias.use,
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return xerrors.New(Localizef(
				"`traceary %s` was removed in v0.14.0; use `%s` instead",
				"`traceary %s` は v0.14.0 で削除されました。代わりに `%s` を使ってください",
				alias.use,
				alias.replacement,
			))
		},
	}
}
