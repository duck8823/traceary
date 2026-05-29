package cli

import (
	"github.com/spf13/cobra"
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
