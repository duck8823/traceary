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
	"runtime/debug"
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
			log.Fatalf("%s", cli.Localizef("invalid LOG_LEVEL: %s", "ログレベルが不正です: %s", logLevel))
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

type buildMetadata struct {
	version string
	commit  string
	date    string
}

var readBuildInfo = debug.ReadBuildInfo

func versionString() string {
	metadata := resolveBuildMetadata(version, commit, date, readBuildInfo)
	return fmt.Sprintf("%s (commit=%s, date=%s, go=%s)", metadata.version, metadata.commit, metadata.date, runtime.Version())
}

func resolveBuildMetadata(
	explicitVersion string,
	explicitCommit string,
	explicitDate string,
	readInfo func() (*debug.BuildInfo, bool),
) buildMetadata {
	metadata := buildMetadata{
		version: explicitVersion,
		commit:  explicitCommit,
		date:    explicitDate,
	}

	if readInfo == nil {
		return metadata
	}
	info, ok := readInfo()
	if !ok || info == nil {
		return metadata
	}

	if (metadata.version == "" || metadata.version == "dev") && info.Main.Version != "" && info.Main.Version != "(devel)" {
		metadata.version = info.Main.Version
	}
	if metadata.commit == "" || metadata.commit == "none" || metadata.commit == "unknown" {
		if value := findBuildSetting(info, "vcs.revision"); value != "" {
			metadata.commit = value
		}
	}
	if metadata.date == "" || metadata.date == "unknown" {
		if value := findBuildSetting(info, "vcs.time"); value != "" {
			metadata.date = value
		}
	}

	return metadata
}

func findBuildSetting(info *debug.BuildInfo, key string) string {
	if info == nil {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func run() error {
	metadata := resolveBuildMetadata(version, commit, date, readBuildInfo)

	migrationsSubFS, err := fs.Sub(migrationsFS, "schema/sqlite/migrations")
	if err != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to read migration files", "マイグレーションファイルの読み込みに失敗しました"), err)
	}

	datasource := sqlite.NewDatasource(migrationsSubFS)
	initializeStoreUsecase := usecase.NewInitializeStoreUsecase(datasource)
	createStoreBackupUsecase := usecase.NewCreateStoreBackupUsecase(datasource)
	restoreStoreBackupUsecase := usecase.NewRestoreStoreBackupUsecase(datasource)
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
		metadata.version,
		initializeStoreUsecase,
		recordLogUsecase,
		recordSessionBoundaryUsecase,
		recordCommandAuditUsecase,
		findLatestSessionQueryService,
		searchEventsQueryService,
		getContextQueryService,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to initialize MCP server", "MCP server の初期化に失敗しました"), err)
	}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase:        initializeStoreUsecase,
		CreateStoreBackupUsecase:      createStoreBackupUsecase,
		RestoreStoreBackupUsecase:     restoreStoreBackupUsecase,
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
			log.Printf("%s: %v", cli.Localize("failed to print CLI error", "CLI error の出力に失敗しました"), writeErr)
		}
		os.Exit(1)
	}
}

func writeCLIError(output io.Writer, err error) error {
	if err == nil {
		return nil
	}

	if _, writeErr := fmt.Fprintf(output, "Error: %v\n", err); writeErr != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to print CLI error", "CLI error の出力に失敗しました"), writeErr)
	}

	return nil
}
