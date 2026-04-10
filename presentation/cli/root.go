package cli

import (
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application/usecase"
)

// RootCLI provides the Traceary root command.
type RootCLI struct {
	initializeStoreUsecase        usecase.InitializeStoreUsecase
	createStoreBackupUsecase      usecase.CreateStoreBackupUsecase
	restoreStoreBackupUsecase     usecase.RestoreStoreBackupUsecase
	recordLogUsecase              usecase.RecordLogUsecase
	recordSessionBoundaryUsecase  usecase.RecordSessionBoundaryUsecase
	recordCommandAuditUsecase     usecase.RecordCommandAuditUsecase
	collectGarbageUsecase         usecase.CollectGarbageUsecase
	searchEventsQueryService      queryservice.SearchEventsQueryService
	listEventsQueryService        queryservice.ListRecentEventsQueryService
	getContextQueryService        queryservice.GetContextQueryService
	getEventDetailsQueryService   queryservice.GetEventDetailsQueryService
	findLatestSessionQueryService queryservice.FindLatestSessionQueryService
	listSessionsQueryService      queryservice.ListSessionsQueryService
	updateSessionLabelUsecase     usecase.UpdateSessionLabelUsecase
	mcpServerRunner               MCPServerRunner
}

// RootCLIOptions holds the dependencies used by RootCLI.
type RootCLIOptions struct {
	InitializeStoreUsecase        usecase.InitializeStoreUsecase
	CreateStoreBackupUsecase      usecase.CreateStoreBackupUsecase
	RestoreStoreBackupUsecase     usecase.RestoreStoreBackupUsecase
	RecordLogUsecase              usecase.RecordLogUsecase
	RecordSessionBoundaryUsecase  usecase.RecordSessionBoundaryUsecase
	RecordCommandAuditUsecase     usecase.RecordCommandAuditUsecase
	CollectGarbageUsecase         usecase.CollectGarbageUsecase
	SearchEventsQueryService      queryservice.SearchEventsQueryService
	ListEventsQueryService        queryservice.ListRecentEventsQueryService
	GetContextQueryService        queryservice.GetContextQueryService
	GetEventDetailsQueryService   queryservice.GetEventDetailsQueryService
	FindLatestSessionQueryService queryservice.FindLatestSessionQueryService
	ListSessionsQueryService      queryservice.ListSessionsQueryService
	UpdateSessionLabelUsecase     usecase.UpdateSessionLabelUsecase
	MCPServerRunner               MCPServerRunner
}

// NewRootCLI creates a new RootCLI.
func NewRootCLI(options RootCLIOptions) *RootCLI {
	return &RootCLI{
		initializeStoreUsecase:        options.InitializeStoreUsecase,
		createStoreBackupUsecase:      options.CreateStoreBackupUsecase,
		restoreStoreBackupUsecase:     options.RestoreStoreBackupUsecase,
		recordLogUsecase:              options.RecordLogUsecase,
		recordSessionBoundaryUsecase:  options.RecordSessionBoundaryUsecase,
		recordCommandAuditUsecase:     options.RecordCommandAuditUsecase,
		collectGarbageUsecase:         options.CollectGarbageUsecase,
		searchEventsQueryService:      options.SearchEventsQueryService,
		listEventsQueryService:        options.ListEventsQueryService,
		getContextQueryService:        options.GetContextQueryService,
		getEventDetailsQueryService:   options.GetEventDetailsQueryService,
		findLatestSessionQueryService: options.FindLatestSessionQueryService,
		listSessionsQueryService:      options.ListSessionsQueryService,
		updateSessionLabelUsecase:     options.UpdateSessionLabelUsecase,
		mcpServerRunner:               options.MCPServerRunner,
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
	rootCmd.AddCommand(c.newCompletionCommand(rootCmd))
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newDoctorCommand())
	rootCmd.AddCommand(c.newMCPServerCommand())
	return rootCmd
}
