package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// codexPluginHookFallbackState captures the subset of ~/.codex/config.toml
// that doctor needs to surface the v0.15.1 plugin_hooks fallback warning.
// PluginKey is the first matching `traceary@<marketplace>` table key that
// is enabled; PluginHooksFeature is the literal value of `[features].plugin_hooks`
// (nil when the key is absent). ConfigTOMLPath is always populated so callers
// can reference the file in user-facing messages even when the read fails.
type codexPluginHookFallbackState struct {
	ConfigTOMLPath     string
	PluginEnabled      bool
	PluginKey          string
	PluginHooksFeature *bool
}

// detectCodexPluginHookFallback returns the state of the Codex plugin
// entry and the `[features].plugin_hooks` flag in `~/.codex/config.toml`.
// Missing file, unreadable file, and invalid TOML all return a zero-value
// state so the doctor falls back to the existing generic warning.
func (c *RootCLI) detectCodexPluginHookFallback() codexPluginHookFallbackState {
	home, err := userHomeDirFunc()
	if err != nil {
		return codexPluginHookFallbackState{}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	state := codexPluginHookFallbackState{ConfigTOMLPath: path}
	content, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	var root struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
		Features struct {
			PluginHooks *bool `toml:"plugin_hooks"`
		} `toml:"features"`
	}
	if err := toml.Unmarshal(content, &root); err != nil {
		return state
	}
	state.PluginHooksFeature = root.Features.PluginHooks
	for key, plugin := range root.Plugins {
		if strings.HasPrefix(key, "traceary@") && plugin.Enabled {
			state.PluginEnabled = true
			state.PluginKey = key
			break
		}
	}
	return state
}

// codexPluginHookFallbackCheck builds the actionable doctor warning that
// fires when the Traceary Codex plugin is enabled in config.toml but the
// effective hooks.json does not register any Traceary-managed hook entry.
// The reason argument explains the observed shape of hooks.json (missing,
// no hooks field, or no Traceary entry) so users can correlate the warning
// with what they see on disk.
func codexPluginHookFallbackCheck(state codexPluginHookFallbackState, hooksPath, reason string) doctorCheck {
	pluginKey := state.PluginKey
	if pluginKey == "" {
		pluginKey = "traceary@<marketplace>"
	}
	featureNote := ""
	if state.PluginHooksFeature != nil && !*state.PluginHooksFeature {
		featureNote = localizef(
			" `[features].plugin_hooks = false` in %s confirms the Codex build is not delivering plugin-managed hooks for this install.",
			" %s では `[features].plugin_hooks = false` が明示されており、この install では plugin-managed hook が配信されません。",
			state.ConfigTOMLPath,
		)
	}
	message := localizef(
		"codex plugin %q is enabled in %s but %s %s; Codex builds where plugin-managed hooks are unavailable (e.g. `plugin_hooks=false` or `codex features list` showing `plugin_hooks` under development) need a manual hook install to record events.%s Run the fallback below; if you later enable plugin-managed hooks, remove the manual entries first to avoid duplicate event capture.",
		"codex plugin %q は %s で有効になっていますが %s %s。Codex 側で plugin-managed hook が配信されないビルド (`plugin_hooks=false` や `codex features list` で `plugin_hooks` が development 状態など) では、event を記録するために fallback の hook install が必要です。%s 下記の fallback を実行してください。後で plugin-managed hook を有効化する場合は、二重記録を避けるために事前に手動 hook を削除してください。",
		pluginKey,
		state.ConfigTOMLPath,
		hooksPath,
		reason,
		featureNote,
	)
	return doctorCheck{
		Name:       "codex-config",
		Status:     doctorStatusWarn,
		Hint:       "fall back to a manual hook install while plugin-managed hooks are unavailable; remove the manual entries before re-enabling plugin hooks to avoid duplicate capture",
		Message:    message,
		FixCommand: "traceary hooks install --client codex --upgrade --traceary-bin $(command -v traceary)",
	}
}
