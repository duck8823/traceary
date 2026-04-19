// Package main defines the Traceary entrypoint.
// Environment loading and dependency wiring happen only in this package.
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

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation"
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

func setupLogger() error {
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
			return xerrors.Errorf("%s", cli.Localizef("invalid LOG_LEVEL: %s", "ログレベルが不正です: %s", logLevel))
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
	return nil
}

var readBuildInfo = debug.ReadBuildInfo

func versionString() string {
	resolvedVersion, resolvedCommit, resolvedDate := resolveBuildMetadata(version, commit, date, readBuildInfo)
	return fmt.Sprintf("%s (commit=%s, date=%s, go=%s)", resolvedVersion, resolvedCommit, resolvedDate, runtime.Version())
}

func resolveBuildMetadata(
	explicitVersion string,
	explicitCommit string,
	explicitDate string,
	readInfo func() (*debug.BuildInfo, bool),
) (resolvedVersion string, resolvedCommit string, resolvedDate string) {
	resolvedVersion = explicitVersion
	resolvedCommit = explicitCommit
	resolvedDate = explicitDate

	if readInfo == nil {
		return resolvedVersion, resolvedCommit, resolvedDate
	}
	info, ok := readInfo()
	if !ok || info == nil {
		return resolvedVersion, resolvedCommit, resolvedDate
	}

	if (resolvedVersion == "" || resolvedVersion == "dev") && info.Main.Version != "" && info.Main.Version != "(devel)" {
		resolvedVersion = info.Main.Version
	}
	if resolvedCommit == "" || resolvedCommit == "none" || resolvedCommit == "unknown" {
		if value := findBuildSetting(info, "vcs.revision"); value != "" {
			resolvedCommit = value
		}
	}
	if resolvedDate == "" || resolvedDate == "unknown" {
		if value := findBuildSetting(info, "vcs.time"); value != "" {
			resolvedDate = value
		}
	}

	return resolvedVersion, resolvedCommit, resolvedDate
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
	if err := setupLogger(); err != nil {
		return err
	}

	resolvedVersion, _, _ := resolveBuildMetadata(version, commit, date, readBuildInfo)

	migrationsSubFS, err := fs.Sub(migrationsFS, "schema/sqlite/migrations")
	if err != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to read migration files", "マイグレーションファイルの読み込みに失敗しました"), err)
	}

	dbPath, err := cli.ResolveDefaultDBPath()
	if err != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to resolve DB path", "DBパスの解決に失敗しました"), err)
	}
	db := sqlite.NewDatabase(dbPath, migrationsSubFS)
	eventDatasource := sqlite.NewEventDatasource(db)
	sessionDatasource := sqlite.NewSessionDatasource(db)
	memoryDatasource := sqlite.NewMemoryDatasource(db)
	storeManagementDatasource := sqlite.NewStoreManagementDatasource(db)

	cfg := presentation.LoadConfig()
	extraRedactPatterns := cfg.ExtraRedactPatterns
	defaultReadFields := cfg.ReadFields

	eventUsecase := usecase.NewEventUsecase(eventDatasource, eventDatasource)
	sessionUsecase := usecase.NewSessionUsecase(eventDatasource, sessionDatasource, sessionDatasource, eventDatasource)
	memoryUsecase := usecase.NewMemoryUsecase(memoryDatasource, memoryDatasource, extraRedactPatterns)
	memoryExtractionUsecase := usecase.NewMemoryExtractionUsecase(sessionDatasource, eventDatasource, memoryUsecase, extraRedactPatterns)
	contextUsecase := usecase.NewContextUsecase(sessionDatasource, eventDatasource, memoryDatasource)
	storeManagementUsecase := usecase.NewStoreManagementUsecase(storeManagementDatasource)

	mcpServer, err := mcpserver.NewServer(
		resolvedVersion,
		extraRedactPatterns,
		eventUsecase,
		sessionUsecase,
		memoryUsecase,
		contextUsecase,
		storeManagementUsecase,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", cli.Localize("failed to initialize MCP server", "MCP server の初期化に失敗しました"), err)
	}

	hooksOrchestrator := filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandler(),
		"gemini": filesystem.NewGeminiHooksHandler(),
	})
	codexIntegrationUsecase := usecase.NewCodexIntegrationUsecase(filesystem.NewCodexIntegrationManager(hooksOrchestrator))
	hooksInspector := filesystem.NewHooksInspector()

	rootCmd := cli.NewRootCLI(
		cli.WithEvent(eventUsecase),
		cli.WithSession(sessionUsecase),
		cli.WithMemory(memoryUsecase),
		cli.WithMemoryExtraction(memoryExtractionUsecase),
		cli.WithContext(contextUsecase),
		cli.WithCodexIntegration(codexIntegrationUsecase),
		cli.WithStoreManagement(storeManagementUsecase),
		cli.WithMCPServerRunner(mcpServer),
		cli.WithHooksOrchestrator(hooksOrchestrator),
		cli.WithHooksInspector(hooksInspector),
		cli.WithExtraRedactPatterns(extraRedactPatterns),
		cli.WithDefaultReadFields(defaultReadFields),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	if err := rootCmd.Execute(); err != nil {
		return xerrors.Errorf("failed to execute CLI command: %w", err)
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
