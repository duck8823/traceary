package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// antigravityPluginSchema is the $schema value the packaged Antigravity CLI
// plugin (integrations/antigravity-plugin/plugin.json) declares.
const antigravityPluginSchema = "https://antigravity.google/schemas/v1/plugin.json"

// antigravityCLIPluginShape classifies the package found in the Antigravity CLI
// plugin directory that `agy plugin install` imports into.
type antigravityCLIPluginShape int

const (
	// antigravityCLIPluginAbsent means the CLI plugin directory does not exist.
	// This is normal for users who wire hooks via `traceary hooks install`
	// instead of the CLI plugin, so doctor skips rather than warns.
	antigravityCLIPluginAbsent antigravityCLIPluginShape = iota
	// antigravityCLIPluginHealthy means the directory holds a supported
	// Antigravity top-level hook-group document with a `traceary` group.
	antigravityCLIPluginHealthy
	// antigravityCLIPluginStaleGemini means the directory still holds a
	// legacy Gemini-shaped package: a top-level {"hooks": ...} document or
	// commands that call `traceary hook ... gemini`. `agy plugin install`
	// can report success without replacing this, leaving Antigravity sessions
	// wired to the Gemini hook runtime.
	antigravityCLIPluginStaleGemini
	// antigravityCLIPluginUnknown means the directory exists but neither the
	// supported nor the legacy shape was recognized.
	antigravityCLIPluginUnknown
)

// antigravityCLIPluginDir returns the directory `agy plugin install` imports
// the Traceary plugin into under the user's home directory.
func antigravityCLIPluginDir(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "plugins", "traceary")
}

// antigravityCLIPluginProbe captures the plugin-config files doctor reads to
// classify the CLI plugin package. Only plugin.json and hooks files are read;
// no transcripts or credentials are touched.
type antigravityCLIPluginProbe struct {
	DirExists       bool
	PluginSchema    string // $schema value from plugin.json ("" if absent/unreadable)
	HooksJSON       []byte // contents of hooks.json (nil if absent)
	LegacyHooksJSON []byte // contents of hooks/hooks.json (nil if absent)
}

// probeAntigravityCLIPlugin reads the plugin config files under dir. It only
// reads plugin.json, hooks.json, and hooks/hooks.json — never transcripts or
// credentials.
func probeAntigravityCLIPlugin(dir string) antigravityCLIPluginProbe {
	probe := antigravityCLIPluginProbe{}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return probe
	}
	probe.DirExists = true
	if data, err := os.ReadFile(filepath.Join(dir, "plugin.json")); err == nil { // #nosec G304 -- resolved plugin path
		var manifest struct {
			Schema string `json:"$schema"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			probe.PluginSchema = manifest.Schema
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "hooks.json")); err == nil { // #nosec G304 -- resolved plugin path
		probe.HooksJSON = data
	}
	if data, err := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); err == nil { // #nosec G304 -- resolved plugin path
		probe.LegacyHooksJSON = data
	}
	return probe
}

// hooksContentIsStaleGemini reports whether a hooks document carries the legacy
// Gemini shape: a top-level {"hooks": ...} envelope, or a command that targets
// the gemini hook host (`traceary hook ... gemini`). A supported Antigravity
// document is a top-level hook-group map with no "hooks" key and commands that
// target the antigravity host, so the gemini substring is a reliable signal.
func hooksContentIsStaleGemini(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err == nil {
		if _, ok := top["hooks"]; ok {
			return true
		}
	}
	return strings.Contains(string(data), "gemini")
}

// hooksContentIsAntigravityGroup reports whether a hooks document is the
// supported Antigravity top-level hook-group format: a "traceary" group whose
// commands invoke `traceary hook antigravity ...`.
func hooksContentIsAntigravityGroup(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return false
	}
	group, ok := top["traceary"]
	if !ok {
		return false
	}
	return strings.Contains(string(group), "antigravity")
}

// classifyAntigravityCLIPluginProbe maps an observed probe to a shape. It is
// pure: no I/O, no side effects.
func classifyAntigravityCLIPluginProbe(p antigravityCLIPluginProbe) antigravityCLIPluginShape {
	if !p.DirExists {
		return antigravityCLIPluginAbsent
	}
	for _, data := range [][]byte{p.HooksJSON, p.LegacyHooksJSON} {
		if hooksContentIsStaleGemini(data) {
			return antigravityCLIPluginStaleGemini
		}
	}
	if hooksContentIsAntigravityGroup(p.HooksJSON) || hooksContentIsAntigravityGroup(p.LegacyHooksJSON) {
		return antigravityCLIPluginHealthy
	}
	return antigravityCLIPluginUnknown
}

// buildAntigravityCLIPluginCheck returns the doctor check for
// "antigravity-cli-plugin" based on the classified shape. The dir argument is
// used only to reference the path in user-facing messages.
func buildAntigravityCLIPluginCheck(shape antigravityCLIPluginShape, dir string) doctorCheck {
	const checkName = "antigravity-cli-plugin"
	switch shape {
	case antigravityCLIPluginHealthy:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"Antigravity CLI plugin at %s uses the supported top-level hook-group format (traceary group invoking `traceary hook antigravity ...`).",
				"%s の Antigravity CLI plugin はサポートされた top-level hook-group 形式（`traceary hook antigravity ...` を呼び出す traceary グループ）です。",
				dir,
			),
		}
	case antigravityCLIPluginStaleGemini:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint:   "remove the stale Gemini-imported plugin directory and reinstall the Antigravity CLI plugin",
			Message: localizef(
				"stale Gemini-shaped Antigravity CLI plugin detected at %s. Its hooks use the legacy Gemini {\"hooks\": ...} shape or call `traceary hook ... gemini`, so Antigravity sessions are wired to the Gemini hook runtime instead of the Antigravity one. The supported package uses a top-level hook-group document with a `traceary` group invoking `traceary hook antigravity ...`. Remove the stale directory and reinstall: rm -rf %s && agy plugin install integrations/antigravity-plugin (or run `traceary hooks install --client antigravity --upgrade`).",
				"%s に古い Gemini 形式の Antigravity CLI plugin が検出されました。hook が legacy な Gemini の {\"hooks\": ...} 形式、または `traceary hook ... gemini` を呼び出しているため、Antigravity セッションが Antigravity ではなく Gemini の hook runtime に配線されています。サポートされるパッケージは `traceary hook antigravity ...` を呼ぶ `traceary` グループを持つ top-level hook-group 形式です。古いディレクトリを削除して再インストールしてください: rm -rf %s && agy plugin install integrations/antigravity-plugin（または `traceary hooks install --client antigravity --upgrade` を実行）。",
				dir, dir,
			),
			FixCommand: "rm -rf " + dir + " && agy plugin install integrations/antigravity-plugin",
		}
	case antigravityCLIPluginUnknown:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: localizef(
				"Antigravity CLI plugin at %s exists but its hooks are neither the supported top-level hook-group format nor a recognized legacy shape. Reinstall: agy plugin install integrations/antigravity-plugin (or run `traceary hooks install --client antigravity --upgrade`).",
				"%s の Antigravity CLI plugin は存在しますが、その hook はサポートされた top-level hook-group 形式でも既知の legacy 形式でもありません。再インストール: agy plugin install integrations/antigravity-plugin（または `traceary hooks install --client antigravity --upgrade`）。",
				dir,
			),
		}
	default: // antigravityCLIPluginAbsent
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: localizef(
				"no Antigravity CLI plugin directory at %s (using `traceary hooks install` instead of the CLI plugin is fine).",
				"%s に Antigravity CLI plugin ディレクトリはありません（CLI plugin の代わりに `traceary hooks install` を使う場合は問題ありません）。",
				dir,
			),
		}
	}
}

// inspectAntigravityCLIPlugin is the doctor-facing entry point. It resolves the
// CLI plugin directory under the user's home, reads only its plugin config
// files, and returns the classified check.
func inspectAntigravityCLIPlugin() doctorCheck {
	home, err := userHomeDirFunc()
	if err != nil {
		return doctorCheck{
			Name:    "antigravity-cli-plugin",
			Status:  doctorStatusSkip,
			Message: localizef("could not resolve home directory: %v", "ホームディレクトリを解決できませんでした: %v", err),
		}
	}
	dir := antigravityCLIPluginDir(home)
	probe := probeAntigravityCLIPlugin(dir)
	return buildAntigravityCLIPluginCheck(classifyAntigravityCLIPluginProbe(probe), dir)
}
