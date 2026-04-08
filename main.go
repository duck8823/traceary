// Package main は traceary の起動関数を定義するパッケージです。
// 環境変数の読み込みと依存関係の組み立てはこのパッケージのみで行います。
package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
	"github.com/duck8823/traceary/presentation/mcpserver"
)

//go:embed schema/sqlite/migrations/*.sql
var migrationsFS embed.FS

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type cliCommandError struct {
	err error
}

func (e cliCommandError) Error() string {
	return e.err.Error()
}

func (e cliCommandError) Unwrap() error {
	return e.err
}

func init() {
	level := slog.LevelInfo
	if logLevel, exists := os.LookupEnv("LOG_LEVEL"); exists {
		switch strings.ToLower(logLevel) {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn", "warning":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			log.Fatalf("ログレベルが不正です: %s", logLevel)
		}
	}

	var handler slog.Handler
	switch os.Getenv("LOG_OPTION") {
	case "development":
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		})
	default:
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})
	}

	slog.SetDefault(slog.New(handler))
}

func versionString() string {
	return fmt.Sprintf("%s (commit=%s, date=%s, go=%s)", version, commit, date, runtime.Version())
}

func run() error {
	migrationsSubFS, err := fs.Sub(migrationsFS, "schema/sqlite/migrations")
	if err != nil {
		return xerrors.Errorf("マイグレーションファイルの読み込みに失敗しました: %w", err)
	}

	datasource := sqlite.NewDatasource(migrationsSubFS)
	initializeStoreUsecase := usecase.NewInitializeStoreUsecase(datasource)
	recordLogUsecase := usecase.NewRecordLogUsecase(datasource)
	recordSessionBoundaryUsecase := usecase.NewRecordSessionBoundaryUsecase(datasource, datasource)
	recordCommandAuditUsecase := usecase.NewRecordCommandAuditUsecase(datasource)
	collectGarbageUsecase := usecase.NewCollectGarbageUsecase(datasource)
	searchEventsQueryService := queryservice.NewSearchEventsQueryService(datasource)
	listRecentEventsQueryService := queryservice.NewListRecentEventsQueryService(datasource)
	getContextQueryService := queryservice.NewGetContextQueryService(datasource)
	getEventDetailsQueryService := queryservice.NewGetEventDetailsQueryService(datasource)
	findLatestSessionQueryService := queryservice.NewFindLatestSessionQueryService(datasource)
	mcpServer, err := mcpserver.NewServer(
		version,
		initializeStoreUsecase,
		recordLogUsecase,
		recordCommandAuditUsecase,
		searchEventsQueryService,
		getContextQueryService,
	)
	if err != nil {
		return xerrors.Errorf("MCP server の初期化に失敗しました: %w", err)
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initializeStoreUsecase,
		RecordLogUsecase:              recordLogUsecase,
		RecordSessionBoundaryUsecase:  recordSessionBoundaryUsecase,
		RecordCommandAuditUsecase:     recordCommandAuditUsecase,
		CollectGarbageUsecase:         collectGarbageUsecase,
		SearchEventsQueryService:      searchEventsQueryService,
		ListEventsQueryService:        listRecentEventsQueryService,
		GetContextQueryService:        getContextQueryService,
		GetEventDetailsQueryService:   getEventDetailsQueryService,
		FindLatestSessionQueryService: findLatestSessionQueryService,
		MCPServerRunner:               mcpServer,
	}).Command()
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	if err := rootCmd.Execute(); err != nil {
		return cliCommandError{err: err}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		if writeErr := writeCLIError(os.Stderr, err); writeErr != nil {
			log.Printf("CLI error の出力に失敗しました: %v", writeErr)
		}
		os.Exit(1)
	}
}

func writeCLIError(output io.Writer, err error) error {
	if err == nil {
		return nil
	}

	if _, writeErr := fmt.Fprintf(output, "Error: %v\n", err); writeErr != nil {
		return xerrors.Errorf("CLI error の出力に失敗しました: %w", writeErr)
	}

	return nil
}
