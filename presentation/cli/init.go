package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/xerrors"
)

const dbPathEnvKey = "TRACEARY_DB_PATH"

func dbPathFlagUsage() string {
	return Localize("SQLite DB path (env: TRACEARY_DB_PATH)", "SQLite DB パス (env: TRACEARY_DB_PATH)")
}

// ResolveDefaultDBPath resolves the default database path from environment or conventions.
func ResolveDefaultDBPath() (string, error) {
	return resolveDBPath("")
}

func resolveDBPath(dbPath string) (string, error) {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		trimmedPath = strings.TrimSpace(os.Getenv(dbPathEnvKey))
	}
	if trimmedPath == "" {
		homeDir, err := userHomeDirFunc()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
		}
		trimmedPath = filepath.Join(homeDir, ".config", "traceary", "traceary.db")
	}

	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パスへの変換に失敗しました"), err)
	}

	return absolutePath, nil
}

func formatMigrationVersions(versions []int64) string {
	parts := make([]string, 0, len(versions))
	for _, version := range versions {
		parts = append(parts, strconv.FormatInt(version, 10))
	}
	return strings.Join(parts, ", ")
}
