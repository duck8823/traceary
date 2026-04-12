package cli

import (
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	event               usecase.EventUsecase
	session             usecase.SessionUsecase
	storeManagement     usecase.StoreManagementUsecase
	mcpServerRunner     MCPServerRunner
	hooksOrchestrator   application.HooksOrchestrator
	hookScripts         application.HookScriptsInstaller
	extraRedactPatterns []string
}

// RootCLIOptions holds the dependencies used by RootCLI.
type RootCLIOptions struct {
	Event                usecase.EventUsecase
	Session              usecase.SessionUsecase
	StoreManagement      usecase.StoreManagementUsecase
	MCPServerRunner      MCPServerRunner
	HooksOrchestrator    application.HooksOrchestrator
	HookScriptsInstaller application.HookScriptsInstaller
	ExtraRedactPatterns  []string
}

// NewRootCLI creates a new RootCLI.
func NewRootCLI(options RootCLIOptions) *RootCLI {
	return &RootCLI{
		event:               options.Event,
		session:             options.Session,
		storeManagement:     options.StoreManagement,
		mcpServerRunner:     options.MCPServerRunner,
		hooksOrchestrator:   options.HooksOrchestrator,
		hookScripts:         options.HookScriptsInstaller,
		extraRedactPatterns: options.ExtraRedactPatterns,
	}
}

// HooksOrchestrator returns the configured HooksOrchestrator, falling back
// to a filesystem-backed default when none was injected via RootCLIOptions.
func (c *RootCLI) HooksOrchestrator() application.HooksOrchestrator {
	if c.hooksOrchestrator != nil {
		return c.hooksOrchestrator
	}

	return defaultHooksOrchestrator()
}

// HookScriptsInstaller returns the configured HookScriptsInstaller, falling
// back to a filesystem-backed default when none was injected via
// RootCLIOptions.
func (c *RootCLI) HookScriptsInstaller() application.HookScriptsInstaller {
	if c.hookScripts != nil {
		return c.hookScripts
	}

	return defaultHookScriptsInstaller()
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
