package cli

import "github.com/spf13/cobra"

// newStoreCommand groups the SQLite-store administration subcommands
// (init / backup / compact) under a single namespace introduced in
// v0.9.0 so the top-level `traceary` --help stays focused on the
// daily-use commands. The old top-level aliases still work during
// the v0.9 series and are scheduled for removal in v1.0.
func (c *RootCLI) newStoreCommand() *cobra.Command {
	storeCmd := &cobra.Command{
		Use:   "store",
		Short: Localize("Manage the local SQLite store (init / backup / compact)", "ローカル SQLite ストアの管理 (init / backup / compact)"),
	}
	storeCmd.AddCommand(c.newStoreInitCommand())
	storeCmd.AddCommand(c.newStoreBackupCommand())
	storeCmd.AddCommand(c.newStoreArchiveCommand())
	storeCmd.AddCommand(c.newStoreFileRetentionOnlyCommand())
	storeCmd.AddCommand(c.newStoreWorkspaceAliasCommand())
	storeCmd.AddCommand(c.newStoreSearchProjectionCommand())
	storeCmd.AddCommand(c.newStoreCompactionCommand())
	return storeCmd
}
