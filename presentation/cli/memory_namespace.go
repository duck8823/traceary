package cli

import (
	"github.com/spf13/cobra"
)

// newMemoryStoreCommand groups the deliberate write/store surface
// (`propose`, `distill`) under `traceary memory store`. `remember` was
// removed in v0.36.0 (#1870); skill writes land on `propose`.
func (c *RootCLI) newMemoryStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: Localize("Propose and distill durable memories", "durable memory の propose・distill を行う"),
	}
	cmd.AddCommand(c.newMemoryProposeCommand())
	cmd.AddCommand(c.newMemoryDistillCommand())
	return cmd
}

// newMemoryAdminCommand groups host-side and maintenance commands —
// extraction, import/export, activation, hygiene, and the lifecycle
// verbs (`supersede`, `expire`, `set-validity`) — under
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
	cmd.AddCommand(c.newMemorySupersedeCommand())
	cmd.AddCommand(c.newMemoryExpireCommand())
	cmd.AddCommand(c.newMemorySetValidityCommand())
	return cmd
}
