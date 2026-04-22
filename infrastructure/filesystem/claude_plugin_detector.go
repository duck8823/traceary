package filesystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
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

// ClaudePluginCacheStatus describes the cached Traceary plugin version
// alongside the version the marketplace currently advertises. When the
// two drift (typically because the operator ran `brew upgrade traceary`
// without `claude plugins update`), the host continues to run an older
// hook set than the CLI supports — exactly the gotcha v0.8.0 dogfooding
// surfaced for #606 (transcript hook) and #605 (matcher expansion).
type ClaudePluginCacheStatus struct {
	// CachePath is the directory scanned for cached plugin versions.
	// Empty when detection could not resolve it.
	CachePath string
	// CachedVersion is the highest version found under CachePath. Empty
	// when no versioned directory exists (plugin never cached yet).
	CachedVersion string
	// CachedVersions lists every non-orphaned versioned cache directory
	// found under CachePath, sorted from highest to lowest by semver.
	// Diagnostics use the full list to detect the session-snapshot
	// ambiguity reported in #670: when more than one version coexists,
	// a long-lived Claude Code session may still be running hooks
	// registered against an older subdir even though CachedVersion
	// (the highest) matches the marketplace.
	CachedVersions []string
	// MarketplaceVersion is the plugin.json "version" Claude Code sees
	// via `~/.claude/plugins/marketplaces/<marketplace>/...`. Empty when
	// the marketplace clone is missing.
	MarketplaceVersion string
	// MarketplacePath is the plugin.json path that was read (empty if
	// the read failed).
	MarketplacePath string
}

// HasMultipleCachedVersions reports whether the plugin's cache directory
// holds more than one non-orphaned version subdirectory. When true, a
// resumed Claude Code session could have snapshotted its hook registry
// against any of them — not necessarily the highest — so the operator
// should be told to restart without `--continue` to guarantee the
// newest hooks are live.
func (s ClaudePluginCacheStatus) HasMultipleCachedVersions() bool {
	return len(s.CachedVersions) > 1
}

// Stale reports whether the cached plugin is at an older semver than
// the marketplace currently offers. Returns false when either version
// is unresolved — a missing cache or missing marketplace is surfaced to
// the caller through the struct fields rather than as "stale".
func (s ClaudePluginCacheStatus) Stale() bool {
	if s.CachedVersion == "" || s.MarketplaceVersion == "" {
		return false
	}
	return semver.Compare("v"+s.CachedVersion, "v"+s.MarketplaceVersion) < 0
}

// DetectClaudePluginCacheStatus resolves the cached plugin version and
// the marketplace plugin.json version for the given PluginKey (of the
// form "<plugin>@<marketplace>"). Missing caches and missing marketplace
// clones are reported via empty fields rather than as errors; only
// structural IO errors surface. Returns an empty status when pluginKey
// lacks the expected "plugin@marketplace" shape.
func DetectClaudePluginCacheStatus(home, pluginKey string) ClaudePluginCacheStatus {
	parts := strings.SplitN(pluginKey, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ClaudePluginCacheStatus{}
	}
	plugin, marketplace := parts[0], parts[1]

	status := ClaudePluginCacheStatus{
		CachePath: filepath.Join(home, ".claude", "plugins", "cache", marketplace, plugin),
		MarketplacePath: filepath.Join(
			home, ".claude", "plugins", "marketplaces", marketplace,
			"integrations", "claude-plugin", ".claude-plugin", "plugin.json",
		),
	}

	if entries, err := os.ReadDir(status.CachePath); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if name == "" || strings.HasPrefix(name, ".") {
				continue
			}
			// Orphaned caches leave a sibling marker file that should
			// not participate in the "highest cached version" choice.
			if _, err := os.Stat(filepath.Join(status.CachePath, name, ".orphaned_at")); err == nil {
				continue
			}
			status.CachedVersions = append(status.CachedVersions, name)
			if status.CachedVersion == "" || semver.Compare("v"+name, "v"+status.CachedVersion) > 0 {
				status.CachedVersion = name
			}
		}
		// Sort CachedVersions descending so diagnostics can print
		// "cached N, N-1, ..." without additional sorting. semver
		// comparator ordering; fall back to string order when one
		// side isn't parseable (defensive — directory names are
		// expected to be plain semver).
		sort.SliceStable(status.CachedVersions, func(i, j int) bool {
			return semver.Compare("v"+status.CachedVersions[i], "v"+status.CachedVersions[j]) > 0
		})
	}

	if content, err := os.ReadFile(status.MarketplacePath); err == nil {
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(content, &manifest); err == nil {
			status.MarketplaceVersion = strings.TrimSpace(manifest.Version)
		}
	}

	return status
}
