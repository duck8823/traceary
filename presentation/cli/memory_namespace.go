package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// newMemoryStoreCommand groups the deliberate write/store surface
// (`remember`, `propose`, `distill`) under `traceary memory store` so
// every command that writes a durable-memory row sits in one
// namespace, regardless of whether the row lands as accepted or
// candidate.
func (c *RootCLI) newMemoryStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: Localize("Record, propose, and distill durable memories", "durable memory の記録・propose・distill を行う"),
	}
	cmd.AddCommand(c.newMemoryRememberCommand())
	cmd.AddCommand(c.newMemoryProposeCommand())
	cmd.AddCommand(c.newMemoryDistillCommand())
	return cmd
}

// newMemoryAdminCommand groups host-side and maintenance commands —
// extraction, import/export, activation, hygiene, graph, and the
// lifecycle verbs (`supersede`, `expire`, `set-validity`) — under
// `traceary memory admin` so operator-facing concerns sit together.
func (c *RootCLI) newMemoryAdminCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: Localize("Run admin and host-side durable-memory operations", "durable memory の admin / host 連携操作を行う"),
	}
	cmd.AddCommand(c.newMemoryExtractCommand())
	cmd.AddCommand(c.newMemoryImportCommand())
	cmd.AddCommand(c.newMemoryExportCommand())
	cmd.AddCommand(c.newMemoryActivateCommand())
	cmd.AddCommand(c.newMemoryHygieneCommand())
	cmd.AddCommand(c.newMemoryGraphCommand())
	cmd.AddCommand(c.newMemorySupersedeCommand())
	cmd.AddCommand(c.newMemoryExpireCommand())
	cmd.AddCommand(c.newMemorySetValidityCommand())
	return cmd
}

// removedMemoryAlias names a former flat `traceary memory <verb>` path
// retired in v0.15.0 and the canonical replacement under
// `memory inbox|store|admin`. The Cobra tree still registers a hidden
// stub for each entry so scripted callers receive a localized
// migration error instead of Cobra's generic "unknown command"
// output. The stub does not register the legacy flags or call the old
// use case — it only emits the removal/migration error.
type removedMemoryAlias struct {
	use          string
	replacement  string
	preserveLeaf bool
}

// addRemovedMemoryAliases registers a hidden migration stub for every
// legacy flat `memory <verb>` path retired in v0.15.0. Each stub
// disables flag parsing so trailing args (including `--help`) flow
// through to the RunE that returns the localized removal error,
// instead of letting Cobra render the legacy command help.
func (c *RootCLI) addRemovedMemoryAliases(memoryCmd *cobra.Command) {
	for _, alias := range []removedMemoryAlias{
		{use: "remember", replacement: "traceary memory store remember"},
		{use: "propose", replacement: "traceary memory store propose"},
		{use: "distill", replacement: "traceary memory store distill"},
		{use: "extract", replacement: "traceary memory admin extract"},
		{use: "accept", replacement: "traceary memory inbox accept"},
		{use: "reject", replacement: "traceary memory inbox reject"},
		{use: "supersede", replacement: "traceary memory admin supersede"},
		{use: "expire", replacement: "traceary memory admin expire"},
		{use: "set-validity", replacement: "traceary memory admin set-validity"},
		{use: "import", replacement: "traceary memory admin import", preserveLeaf: true},
		{use: "export", replacement: "traceary memory admin export"},
		{use: "activate", replacement: "traceary memory admin activate"},
		{use: "hygiene", replacement: "traceary memory admin hygiene", preserveLeaf: true},
		{use: "graph", replacement: "traceary memory admin graph", preserveLeaf: true},
	} {
		memoryCmd.AddCommand(newRemovedMemoryAliasCommand(alias))
	}
}

func newRemovedMemoryAliasCommand(alias removedMemoryAlias) *cobra.Command {
	return &cobra.Command{
		Use:                alias.use,
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			replacement := removedMemoryAliasReplacement(alias, args)
			return xerrors.New(Localizef(
				"`traceary memory %s` was removed in v0.15.0; use `%s` instead",
				"`traceary memory %s` は v0.15.0 で削除されました。代わりに `%s` を使ってください",
				alias.use,
				replacement,
			))
		},
	}
}

func removedMemoryAliasReplacement(alias removedMemoryAlias, args []string) string {
	if !alias.preserveLeaf {
		return alias.replacement
	}
	for _, arg := range args {
		leaf := strings.TrimSpace(arg)
		if leaf == "" {
			continue
		}
		if strings.HasPrefix(leaf, "-") || leaf == "help" {
			break
		}
		return alias.replacement + " " + leaf
	}
	return alias.replacement
}
