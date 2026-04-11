package cli

import (
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application/usecase"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	event            usecase.EventUsecase
	session          usecase.SessionUsecase
	storeMaintenance usecase.StoreMaintenanceUsecase
	mcpServerRunner  MCPServerRunner
}

// RootCLIOptions holds the dependencies used by RootCLI.
type RootCLIOptions struct {
	Event            usecase.EventUsecase
	Session          usecase.SessionUsecase
	StoreMaintenance usecase.StoreMaintenanceUsecase
	MCPServerRunner  MCPServerRunner
}

// NewRootCLI creates a new RootCLI.
func NewRootCLI(options RootCLIOptions) *RootCLI {
	return &RootCLI{
		event:            options.Event,
		session:          options.Session,
		storeMaintenance: options.StoreMaintenance,
		mcpServerRunner:  options.MCPServerRunner,
	}
}

// Command returns the Traceary root command.
func (c *RootCLI) Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "traceary",
		Short:         Localize("Local-first CLI for AI agent work history", "AI エージェントの作業履歴をローカルに記録する CLI"),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	rootCmd.AddCommand(c.newInitCommand())
	rootCmd.AddCommand(c.newBackupCommand())
	rootCmd.AddCommand(c.newLogCommand())
	rootCmd.AddCommand(c.newAuditCommand())
	rootCmd.AddCommand(c.newGCCommand())
	rootCmd.AddCommand(c.newSearchCommand())
	rootCmd.AddCommand(c.newContextCommand())
	rootCmd.AddCommand(c.newListCommand())
	rootCmd.AddCommand(c.newShowCommand())
	rootCmd.AddCommand(c.newSessionCommand())
	rootCmd.AddCommand(c.newCompactSummaryCommand())
	rootCmd.AddCommand(c.newTimelineCommand())
	rootCmd.AddCommand(c.newCompletionCommand(rootCmd))
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newDoctorCommand())
	rootCmd.AddCommand(c.newMCPServerCommand())
	return rootCmd
}
