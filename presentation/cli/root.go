package cli

import (
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/application/usecase"
)

// RootCLI は traceary のルートコマンドを提供します。
type RootCLI struct {
	initializeStoreUsecase        usecase.InitializeStoreUsecase
	recordLogUsecase              usecase.RecordLogUsecase
	recordSessionBoundaryUsecase  usecase.RecordSessionBoundaryUsecase
	recordCommandAuditUsecase     usecase.RecordCommandAuditUsecase
	collectGarbageUsecase         usecase.CollectGarbageUsecase
	searchEventsQueryService      queryservice.SearchEventsQueryService
	listEventsQueryService        queryservice.ListRecentEventsQueryService
	getEventDetailsQueryService   queryservice.GetEventDetailsQueryService
	findLatestSessionQueryService queryservice.FindLatestSessionQueryService
	mcpServerRunner               MCPServerRunner
}

// RootCLIOptions は RootCLI の依存関係をまとめた設定です。
type RootCLIOptions struct {
	InitializeStoreUsecase        usecase.InitializeStoreUsecase
	RecordLogUsecase              usecase.RecordLogUsecase
	RecordSessionBoundaryUsecase  usecase.RecordSessionBoundaryUsecase
	RecordCommandAuditUsecase     usecase.RecordCommandAuditUsecase
	CollectGarbageUsecase         usecase.CollectGarbageUsecase
	SearchEventsQueryService      queryservice.SearchEventsQueryService
	ListEventsQueryService        queryservice.ListRecentEventsQueryService
	GetEventDetailsQueryService   queryservice.GetEventDetailsQueryService
	FindLatestSessionQueryService queryservice.FindLatestSessionQueryService
	MCPServerRunner               MCPServerRunner
}

// NewRootCLI は新しい RootCLI を生成します。
func NewRootCLI(options RootCLIOptions) *RootCLI {
	return &RootCLI{
		initializeStoreUsecase:        options.InitializeStoreUsecase,
		recordLogUsecase:              options.RecordLogUsecase,
		recordSessionBoundaryUsecase:  options.RecordSessionBoundaryUsecase,
		recordCommandAuditUsecase:     options.RecordCommandAuditUsecase,
		collectGarbageUsecase:         options.CollectGarbageUsecase,
		searchEventsQueryService:      options.SearchEventsQueryService,
		listEventsQueryService:        options.ListEventsQueryService,
		getEventDetailsQueryService:   options.GetEventDetailsQueryService,
		findLatestSessionQueryService: options.FindLatestSessionQueryService,
		mcpServerRunner:               options.MCPServerRunner,
	}
}

// Command は traceary のルートコマンドを返します。
func (c *RootCLI) Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "traceary",
		Short:        "AI エージェントの作業履歴をローカルに記録する CLI",
		SilenceUsage: true,
	}
	rootCmd.AddCommand(c.newInitCommand())
	rootCmd.AddCommand(c.newLogCommand())
	rootCmd.AddCommand(c.newAuditCommand())
	rootCmd.AddCommand(c.newGCCommand())
	rootCmd.AddCommand(c.newSearchCommand())
	rootCmd.AddCommand(c.newListCommand())
	rootCmd.AddCommand(c.newShowCommand())
	rootCmd.AddCommand(c.newSessionCommand())
	rootCmd.AddCommand(c.newHooksCommand())
	rootCmd.AddCommand(c.newMCPServerCommand())
	return rootCmd
}
