// Command repo-tooling is the maintainer-only repository tooling entrypoint
// for Traceary. It is intentionally separate from the shipped `traceary`
// binary: these subcommands assume a Git checkout, validate repository-only
// files, and are used in CI / release preparation rather than the installed
// product. See docs/operations/repo-tooling.md.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "repo-tooling",
		Short: "Maintainer-only repository tooling for Traceary",
		Long: "repo-tooling hosts maintainer-only repository helpers (integration " +
			"verification, docs/release checks) that are not part of the shipped " +
			"traceary runtime. See docs/operations/repo-tooling.md.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newIntegrationsCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newReleaseCommand())
	return root
}
