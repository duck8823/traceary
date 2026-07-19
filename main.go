// Package main defines the Traceary entrypoint.
// Environment loading and dependency wiring happen only in this package.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	structuredRedactRules := cfg.StructuredRedactRules
	auditMaxInputBytes := cfg.AuditMaxInputBytes
	auditMaxOutputBytes := cfg.AuditMaxOutputBytes
	defaultReadFields := cfg.ReadFields
	readPresets := cfg.ReadPresets
	defaultReadColor := cfg.ReadColor

	eventUsecase := usecase.NewEventUsecase(eventDatasource, eventDatasource)
	sessionUsecase := usecase.NewSessionUsecase(eventDatasource, sessionDatasource, sessionDatasource, eventDatasource)
	codexMemorySource := filesystem.NewCodexMemorySource()
	memoryUsecase := usecase.NewMemoryUsecase(memoryDatasource, memoryDatasource, extraRedactPatterns, usecase.MemoryUsecaseDependencies{
		SessionQuery: sessionDatasource,
		EventQuery:   eventDatasource,
		CodexSource:  codexMemorySource,
	})
	memoryEdgeDatasource := sqlite.NewMemoryEdgeDatasource(db)
	memoryEdgeUsecase := usecase.NewMemoryEdgeUsecase(memoryEdgeDatasource, memoryEdgeDatasource, nil)
	bundleDatasource := sqlite.NewBundleDatasource(db, eventDatasource)
	bundleUsecase := usecase.NewBundleUsecase(eventDatasource, bundleDatasource, nil)
	contextUsecase := usecase.NewContextUsecase(sessionDatasource, eventDatasource, memoryDatasource)
	replayUsecase := usecase.NewReplayUsecase(sessionDatasource, eventDatasource, memoryDatasource)
	storeManagementUsecase := usecase.NewStoreManagementUsecase(storeManagementDatasource)

	mcpServer, err := mcpserver.NewServer(
		resolvedVersion,
		extraRedactPatterns,
		structuredRedactRules,
		auditMaxInputBytes,
		auditMaxOutputBytes,
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
		"claude":      filesystem.NewClaudeHooksHandler(),
		"codex":       filesystem.NewCodexHooksHandler(),
		"gemini":      filesystem.NewGeminiHooksHandler(),
		"antigravity": filesystem.NewAntigravityHooksHandler(),
		"grok":        filesystem.NewGrokHooksHandler(),
		"kimi":        filesystem.NewKimiHooksHandler(),
	})
	hooksInspector := filesystem.NewHooksInspector()
	pluginCacheInspector := filesystem.NewPluginCacheInspector()
	pluginDetector := filesystem.NewClaudePluginDetectorAdapter()

	rootCmd := cli.NewRootCLI(
		cli.WithEvent(eventUsecase),
		cli.WithSession(sessionUsecase),
		cli.WithMemory(memoryUsecase),
		cli.WithMemoryEdge(memoryEdgeUsecase),
		cli.WithBundle(bundleUsecase),
		cli.WithContext(contextUsecase),
		cli.WithReplay(replayUsecase),
		cli.WithStoreManagement(storeManagementUsecase),
		cli.WithMCPServerRunner(mcpServer),
		cli.WithHooksOrchestrator(hooksOrchestrator),
		cli.WithHooksInspector(hooksInspector),
		cli.WithPluginCacheInspector(pluginCacheInspector),
		cli.WithClaudePluginDetector(pluginDetector),
		cli.WithCockpitStateReader(cli.NewFileCockpitStateStore()),
		cli.WithExtraRedactPatterns(extraRedactPatterns),
		cli.WithStructuredRedactRules(structuredRedactRules),
		cli.WithDefaultAuditPayloadLimits(auditMaxInputBytes, auditMaxOutputBytes),
		cli.WithDefaultReadFields(defaultReadFields),
		cli.WithReadPresets(readPresets),
		cli.WithDefaultReadColor(defaultReadColor),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	commandCtx, stopSignals := commandContext(os.Args)
	defer stopSignals()
	rootCmd.SetContext(commandCtx)

	if err := rootCmd.Execute(); err != nil {
		return xerrors.Errorf("failed to execute CLI command: %w", err)
	}

	return nil
}

// defaultHookSoftDeadline is slightly below the smallest packaged host hook
// budget (10s for codex/claude/gemini). Canceling ourselves first keeps the
// spool / fail-soft path deterministic instead of racing a mid-commit host kill.
const defaultHookSoftDeadline = 8 * time.Second

// hookSoftDeadlineEnvKey overrides the soft deadline (Go duration, e.g. "8s").
// Empty / invalid / non-positive values disable the soft deadline (signal-only).
const hookSoftDeadlineEnvKey = "TRACEARY_HOOK_SOFT_DEADLINE"

func commandContext(args []string) (context.Context, context.CancelFunc) {
	if isHookCommandArgs(args) {
		ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		// Detached workers are not bound by host hook budgets; keep signal-only.
		if isDetachedHookWorkerArgs(args) {
			return ctx, stopSignals
		}
		if deadline := resolveHookSoftDeadline(); deadline > 0 {
			timedCtx, cancelTimeout := context.WithTimeout(ctx, deadline)
			return timedCtx, func() {
				cancelTimeout()
				stopSignals()
			}
		}
		return ctx, stopSignals
	}
	return context.WithCancel(context.Background())
}

func resolveHookSoftDeadline() time.Duration {
	raw, ok := os.LookupEnv(hookSoftDeadlineEnvKey)
	if !ok {
		return defaultHookSoftDeadline
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" || strings.EqualFold(raw, "off") || strings.EqualFold(raw, "none") {
		return 0
	}
	// Accept Go durations ("8s") and plain seconds ("8") for operator convenience.
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0
		}
		return d
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	return defaultHookSoftDeadline
}

func isHookCommandArgs(args []string) bool {
	for _, arg := range args[1:] {
		if arg == "hook" {
			return true
		}
	}
	return false
}

// isDetachedHookWorkerArgs reports hidden workers that run outside host
// timeout budgets (memory-extract / grok-transcript).
func isDetachedHookWorkerArgs(args []string) bool {
	for _, arg := range args[1:] {
		switch arg {
		case "memory-extract-worker", "grok-transcript-worker":
			return true
		}
	}
	return false
}

type cliExitCoder interface {
	ExitCode() int
}

func main() {
	configureBrokenPipeSignalHandling()
	if err := run(); err != nil {
		if isSilentCLIExitError(err) {
			return
		}
		if writeErr := writeCLIError(os.Stderr, err); writeErr != nil {
			log.Printf("%s: %v", cli.Localize("failed to print CLI error", "CLI error の出力に失敗しました"), writeErr)
		}
		exitCode := 1
		var exitCoder cliExitCoder
		if errors.As(err, &exitCoder) && exitCoder.ExitCode() != 0 {
			exitCode = exitCoder.ExitCode()
		}
		os.Exit(exitCode)
	}
}

func isSilentCLIExitError(err error) bool {
	return cli.IsBrokenPipeError(err)
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
