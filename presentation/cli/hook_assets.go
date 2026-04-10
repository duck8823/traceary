package cli

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	hookscripts "github.com/duck8823/traceary/scripts/hooks"
)

const hooksScriptsDirEnvKey = "TRACEARY_HOOK_SCRIPTS_DIR"

type hookScriptAsset = hookscripts.ScriptAsset

var hookScriptAssets = mustHookScriptAssets()

func mustHookScriptAssets() []hookScriptAsset {
	assets, err := hookscripts.Assets()
	if err != nil {
		panic(err)
	}

	return assets
}

func ensureHookScriptsInstalled() (string, error) {
	scriptsDir, err := resolveHooksScriptsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to create hook scripts directory", "hook script ディレクトリの作成に失敗しました"), err)
	}

	for _, asset := range hookScriptAssets {
		outputPath := filepath.Join(scriptsDir, asset.Name)
		currentContent, err := os.ReadFile(outputPath)
		if err == nil && string(currentContent) == asset.Content {
			if chmodErr := os.Chmod(outputPath, 0o755); chmodErr != nil {
				return "", xerrors.Errorf("%s: %w", Localize("failed to chmod hook script", "hook script の chmod に失敗しました"), chmodErr)
			}
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return "", xerrors.Errorf("%s: %w", Localize("failed to inspect installed hook script", "既存 hook script の確認に失敗しました"), err)
		}
		if err := os.WriteFile(outputPath, []byte(asset.Content), 0o755); err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to write hook script", "hook script の書き出しに失敗しました"), err)
		}
	}

	return scriptsDir, nil
}

func resolveHooksScriptsDir() (string, error) {
	if envValue := strings.TrimSpace(os.Getenv(hooksScriptsDirEnvKey)); envValue != "" {
		resolvedPath, err := filepath.Abs(envValue)
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute hook scripts path", "hook scripts path の絶対パス化に失敗しました"), err)
		}

		return resolvedPath, nil
	}

	homeDir, err := userHomeDirFunc()
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "hook-scripts"), nil
}
