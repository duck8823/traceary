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

// NewRootCLI は新しい RootCLI を生成します。
func NewRootCLI(
	initializeStoreUsecase usecase.InitializeStoreUsecase,
	recordLogUsecase usecase.RecordLogUsecase,
	recordSessionBoundaryUsecase usecase.RecordSessionBoundaryUsecase,
	recordCommandAuditUsecase usecase.RecordCommandAuditUsecase,
	collectGarbageUsecase usecase.CollectGarbageUsecase,
	searchEventsQueryService queryservice.SearchEventsQueryService,
	listEventsQueryService queryservice.ListRecentEventsQueryService,
	getEventDetailsQueryService queryservice.GetEventDetailsQueryService,
	findLatestSessionQueryService queryservice.FindLatestSessionQueryService,
	mcpServerRunner MCPServerRunner,
) *RootCLI {
	return &RootCLI{
		initializeStoreUsecase:        initializeStoreUsecase,
		recordLogUsecase:              recordLogUsecase,
		recordSessionBoundaryUsecase:  recordSessionBoundaryUsecase,
		recordCommandAuditUsecase:     recordCommandAuditUsecase,
		collectGarbageUsecase:         collectGarbageUsecase,
		searchEventsQueryService:      searchEventsQueryService,
		listEventsQueryService:        listEventsQueryService,
		getEventDetailsQueryService:   getEventDetailsQueryService,
		findLatestSessionQueryService: findLatestSessionQueryService,
		mcpServerRunner:               mcpServerRunner,
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
	rootCmd.AddCommand(c.newMCPServerCommand())
	return rootCmd
}
