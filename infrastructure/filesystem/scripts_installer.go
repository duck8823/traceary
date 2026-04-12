package filesystem

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	hookscripts "github.com/duck8823/traceary/scripts/hooks"
)

const hookScriptsDirEnvKey = "TRACEARY_HOOK_SCRIPTS_DIR"

// HookScriptsInstaller materializes the bundled hook scripts to disk under a
// well-known directory so generated hook configurations can reference them.
type HookScriptsInstaller struct {
	userHomeDir func() (string, error)
}

// NewHookScriptsInstaller creates an installer using os.UserHomeDir. Callers
// that need to override the home directory (for example in tests) should
// construct the installer manually with a custom lookup function.
func NewHookScriptsInstaller() *HookScriptsInstaller {
	return &HookScriptsInstaller{userHomeDir: os.UserHomeDir}
}

// NewHookScriptsInstallerWithHomeDirFunc constructs an installer using a
// custom home-directory lookup function.
func NewHookScriptsInstallerWithHomeDirFunc(userHomeDir func() (string, error)) *HookScriptsInstaller {
	return &HookScriptsInstaller{userHomeDir: userHomeDir}
}

// Ensure installs the bundled scripts to disk and returns the resolved
// scripts directory. Existing files with matching content are left alone and
// are only chmod'd to make sure they stay executable.
func (i *HookScriptsInstaller) Ensure() (string, error) {
	scriptsDir, err := i.ResolveDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return "", xerrors.Errorf("failed to create hook scripts directory: %w", err)
	}

	assets, err := hookscripts.Assets()
	if err != nil {
		return "", xerrors.Errorf("failed to load hook script assets: %w", err)
	}

	for _, asset := range assets {
		outputPath := filepath.Join(scriptsDir, asset.Name())
		currentContent, err := os.ReadFile(outputPath)
		if err == nil && string(currentContent) == asset.Content() {
			if chmodErr := os.Chmod(outputPath, 0o755); chmodErr != nil {
				return "", xerrors.Errorf("failed to chmod hook script: %w", chmodErr)
			}
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return "", xerrors.Errorf("failed to inspect installed hook script: %w", err)
		}
		if err := os.WriteFile(outputPath, []byte(asset.Content()), 0o755); err != nil {
			return "", xerrors.Errorf("failed to write hook script: %w", err)
		}
	}

	return scriptsDir, nil
}

// ResolveDir resolves the hook scripts directory without creating it. It
// honors the TRACEARY_HOOK_SCRIPTS_DIR environment variable when set and
// falls back to ~/.config/traceary/hook-scripts otherwise.
func (i *HookScriptsInstaller) ResolveDir() (string, error) {
	if envValue := strings.TrimSpace(os.Getenv(hookScriptsDirEnvKey)); envValue != "" {
		resolvedPath, err := filepath.Abs(envValue)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve absolute hook scripts path: %w", err)
		}

		return resolvedPath, nil
	}

	homeDir, err := i.userHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "hook-scripts"), nil
}
