package filesystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ClaudePluginDetection reports whether the Traceary Claude Code plugin
// is enabled in the user's global Claude settings.
//
// When a plugin-based install is active the plugin already provides the
// Traceary hooks to Claude Code, and writing the same hooks into a
// settings.json would cause every audit to fire twice — one from the
// plugin and one from settings.json. `hooks install` and `doctor` both
// use this detection to warn about (or skip) that double-registration.
type ClaudePluginDetection struct {
	// Active is true when the user's ~/.claude/settings.json lists a
	// Traceary plugin under enabledPlugins with value true.
	Active bool
	// SettingsPath is the absolute path that was consulted, regardless
	// of whether the plugin was active. Used for diagnostic messages.
	SettingsPath string
	// PluginKey is the enabledPlugins key that matched, e.g.
	// "traceary@traceary-plugins". Empty when Active is false.
	PluginKey string
}

// DetectClaudeTracearyPlugin reads ~/.claude/settings.json and returns
// whether the Traceary plugin is enabled. A missing settings file or
// malformed JSON returns Active=false without raising an error — the
// absence of a global Claude settings file is a normal state for users
// who have not opted into Claude Code plugins at all.
func DetectClaudeTracearyPlugin() ClaudePluginDetection {
	home, err := osUserHomeDir()
	if err != nil {
		return ClaudePluginDetection{}
	}
	return DetectClaudeTracearyPluginIn(home)
}

// DetectClaudeTracearyPluginIn is the home-directory-parameterized
// variant of DetectClaudeTracearyPlugin so callers with their own home
// resolver (e.g. tests or the CLI's override hook) can point detection
// at a custom home directory.
func DetectClaudeTracearyPluginIn(home string) ClaudePluginDetection {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return ClaudePluginDetection{SettingsPath: settingsPath}
	}

	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(content, &settings); err != nil {
		return ClaudePluginDetection{SettingsPath: settingsPath}
	}

	for name, enabled := range settings.EnabledPlugins {
		if !enabled {
			continue
		}
		// The plugin name is `<plugin>@<marketplace>`; Traceary's
		// canonical plugin name is `traceary`, so we match any
		// marketplace that hosts it.
		if strings.HasPrefix(name, "traceary@") {
			return ClaudePluginDetection{
				Active:       true,
				SettingsPath: settingsPath,
				PluginKey:    name,
			}
		}
	}

	return ClaudePluginDetection{SettingsPath: settingsPath}
}
