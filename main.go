// Package main は traceary の起動関数を定義するパッケージです。
// 環境変数の読み込みと依存関係の組み立てはこのパッケージのみで行います。
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

//go:embed schema/sqlite/migrations/*.sql
var migrationsFS embed.FS

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
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
	rootCmd := cli.NewRootCLI(initializeStoreUsecase).Command()
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	if err := rootCmd.Execute(); err != nil {
		return xerrors.Errorf("CLI の実行に失敗しました: %w", err)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("traceary の実行に失敗しました", slog.Any("error", err))
		os.Exit(1)
	}
}
