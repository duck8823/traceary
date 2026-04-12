package cli

import (
	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// defaultHooksOrchestrator returns a ready-to-use orchestrator wired against
// the filesystem package. It is used by NewRootCLI when the caller does not
// provide an explicit HooksOrchestrator so that every construction path has a
// working hooks implementation.
func defaultHooksOrchestrator() application.HooksOrchestrator {
	return filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex":  filesystem.NewCodexHooksHandlerWithHomeDirFunc(callUserHomeDirFunc),
		"gemini": filesystem.NewGeminiHooksHandler(),
	})
}

// defaultHookScriptsInstaller returns a HookScriptsInstaller wired against
// the filesystem package. It honors the presentation-layer userHomeDirFunc
// override so existing tests that swap the home directory continue to work.
func defaultHookScriptsInstaller() application.HookScriptsInstaller {
	return filesystem.NewHookScriptsInstallerWithHomeDirFunc(callUserHomeDirFunc)
}

// callUserHomeDirFunc always delegates to the current value of
// userHomeDirFunc so test overrides installed after construction still apply.
func callUserHomeDirFunc() (string, error) {
	return userHomeDirFunc()
}
