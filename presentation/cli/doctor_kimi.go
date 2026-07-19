package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

var (
	kimiDoctorLookPath = exec.LookPath
	kimiDoctorOutput   = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "kimi", args...).Output()
	}
)

// kimiExpectedHooks mirrors the verified hook plan (10 rules) the packaged
// plugin and the TOML print path declare. The doctor check validates the
// installed managed copy against it.
var kimiExpectedHooks = []struct {
	event   string
	matcher string
	action  string
}{
	{"SessionStart", "", "session-start"},
	{"SessionEnd", "", "session-end"},
	{"UserPromptSubmit", "", "user-prompt-submit"},
	{"PreToolUse", "Agent", "pre-tool-use"},
	{"PostToolUse", "", "post-tool-use"},
	{"PostToolUseFailure", "", "post-tool-use-failure"},
	{"Stop", "", "stop"},
	{"SubagentStop", "", "subagent-stop"},
	{"PreCompact", "", "pre-compact"},
	{"PostCompact", "", "post-compact"},
}

type kimiDoctorState struct {
	CLIAvailable    bool
	HostVersion     string
	PluginInstalled bool
	PluginEnabled   bool
	PluginVersion   string
	NativeHooks     bool
	PluginMCP       bool
	UserMCP         bool
	Skills          int
}

type kimiPluginManifest struct {
	Version    string `json:"version"`
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
	Hooks []struct {
		Event   string `json:"event"`
		Matcher string `json:"matcher"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	} `json:"hooks"`
}

// probeKimiDoctorState inspects the local Kimi Code installation. Kimi Code
// has no CLI inspect command, so state is probed from the filesystem: the
// managed plugin copy under $KIMI_CODE_HOME/plugins/managed/traceary (a
// symlink into a generation dir) and the install record.
func probeKimiDoctorState(ctx context.Context, projectDir string) (kimiDoctorState, error) {
	state := kimiDoctorState{}
	if _, err := kimiDoctorLookPath("kimi"); err != nil {
		return state, nil
	}
	state.CLIAvailable = true
	versionOutput, err := kimiDoctorOutput(ctx, "--version")
	if err != nil {
		return state, xerrors.Errorf("failed to read Kimi Code version: %w", err)
	}
	state.HostVersion = strings.TrimSpace(string(versionOutput))

	kimiHome := kimiDoctorCodeHome()
	manifestPath := filepath.Join(kimiHome, "plugins", "managed", "traceary", "kimi.plugin.json")
	manifestBytes, err := os.ReadFile(manifestPath) // #nosec G304 -- fixed path under the Kimi home
	if err != nil {
		if !os.IsNotExist(err) {
			return state, xerrors.Errorf("failed to read Kimi plugin manifest: %w", err)
		}
	} else {
		state.PluginInstalled = true
		var manifest kimiPluginManifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return state, xerrors.Errorf("failed to decode Kimi plugin manifest: %w", err)
		}
		state.PluginVersion = manifest.Version
		state.NativeHooks = kimiManifestHasVerifiedHooks(manifest)
		if server, ok := manifest.MCPServers["traceary"]; ok && server.Command == "traceary" {
			state.PluginMCP = true
		}
		state.Skills = countKimiPluginSkills(kimiHome)
		state.PluginEnabled = kimiPluginRecordEnabled(kimiHome)
	}
	state.UserMCP = kimiMCPJSONRegisters(kimiHome, projectDir)
	return state, nil
}

func kimiDoctorCodeHome() string {
	if home := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); home != "" {
		return home
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kimi-code")
}

func kimiManifestHasVerifiedHooks(manifest kimiPluginManifest) bool {
	if len(manifest.Hooks) != len(kimiExpectedHooks) {
		return false
	}
	for i, want := range kimiExpectedHooks {
		hook := manifest.Hooks[i]
		if hook.Event != want.event || hook.Matcher != want.matcher ||
			hook.Command != "traceary hook kimi "+want.action || hook.Timeout != 5 {
			return false
		}
	}
	return true
}

func countKimiPluginSkills(kimiHome string) int {
	entries, err := os.ReadDir(filepath.Join(kimiHome, "plugins", "managed", "traceary", "skills"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if info, err := os.Stat(filepath.Join(kimiHome, "plugins", "managed", "traceary", "skills", entry.Name(), "SKILL.md")); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	return count
}

// kimiPluginRecordEnabled reads the install record: enabled=true and
// state=ok. A missing record means the plugin is not activated (mirroring the
// official installer behavior).
func kimiPluginRecordEnabled(kimiHome string) bool {
	data, err := os.ReadFile(filepath.Join(kimiHome, "plugins", "installed.json")) // #nosec G304 -- fixed path under the Kimi home
	if err != nil {
		return false
	}
	var record struct {
		Plugins []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
			State   string `json:"state"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &record) != nil {
		return false
	}
	for _, entry := range record.Plugins {
		if entry.ID == "traceary" {
			return entry.Enabled && entry.State == "ok"
		}
	}
	return false
}

// kimiMCPJSONRegisters checks the user-level and project-level mcp.json
// files for a traceary server entry (the plugin-declared server is checked
// separately from the manifest).
func kimiMCPJSONRegisters(kimiHome, projectDir string) bool {
	candidates := []string{filepath.Join(kimiHome, "mcp.json")}
	if projectDir != "" {
		candidates = append(candidates, filepath.Join(projectDir, ".kimi-code", "mcp.json"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path) // #nosec G304 -- fixed name under the Kimi home / project dir
		if err != nil {
			continue
		}
		var doc struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		if _, ok := doc.MCPServers["traceary"]; ok {
			return true
		}
	}
	return false
}

func buildKimiDoctorChecks(state kimiDoctorState, tracearyVersion string) []doctorCheck {
	if !state.CLIAvailable {
		return []doctorCheck{{Name: "kimi-cli", Status: doctorStatusFail, Message: Localize("Kimi Code CLI is not installed", "Kimi Code CLI がインストールされていません"), Hint: Localize("install Kimi Code, then rerun doctor", "Kimi Code をインストールして doctor を再実行してください")}}
	}
	checks := []doctorCheck{{Name: "kimi-cli", Status: doctorStatusPass, Message: localizef("detected Kimi Code CLI %s", "Kimi Code CLI %s を検出しました", state.HostVersion)}}
	if !state.PluginInstalled {
		checks = append(checks, doctorCheck{Name: "kimi-plugin", Status: doctorStatusWarn, Message: Localize("native Traceary Kimi plugin is not installed", "native Traceary Kimi plugin がインストールされていません"), Hint: Localize("install the native plugin with scripts/install-kimi-plugin.sh", "scripts/install-kimi-plugin.sh で native plugin をインストールしてください")})
		return checks
	}
	pluginStatus := doctorStatusPass
	pluginMessage := localizef("native Traceary Kimi plugin %s is installed and enabled", "native Traceary Kimi plugin %s はインストール済みで有効です", state.PluginVersion)
	pluginHint := ""
	if !state.PluginEnabled {
		pluginStatus, pluginMessage = doctorStatusWarn, Localize("native Traceary Kimi plugin is installed but not enabled", "native Traceary Kimi plugin はインストール済みですが有効ではありません")
		pluginHint = "enable the plugin with /plugins, or reinstall with scripts/install-kimi-plugin.sh"
	} else if grokTracearyVersionPattern.MatchString(tracearyVersion) && strings.TrimPrefix(state.PluginVersion, "v") != strings.TrimPrefix(tracearyVersion, "v") {
		pluginStatus = doctorStatusWarn
		pluginMessage = localizef("native Traceary Kimi plugin version %s does not match Traceary %s", "native Traceary Kimi plugin version %s は Traceary %s と一致しません", state.PluginVersion, tracearyVersion)
		pluginHint = "./scripts/install-kimi-plugin.sh  # from a matching release tag checkout"
	}
	checks = append(checks, doctorCheck{Name: "kimi-plugin", Status: pluginStatus, Message: pluginMessage, Hint: pluginHint})

	hookStatus := doctorStatusPass
	hookMessage := Localize("native Kimi plugin hooks cover all ten verified events", "native Kimi plugin hook は検証済み 10 event をすべてカバーしています")
	if !state.NativeHooks {
		hookStatus, hookMessage = doctorStatusWarn, Localize("native Kimi plugin hook coverage is missing or incomplete", "native Kimi plugin hook coverage が不足しています")
	}
	checks = append(checks, doctorCheck{Name: "kimi-hooks", Status: hookStatus, Message: hookMessage, Hint: Localize("reinstall the native Traceary Kimi plugin", "native Traceary Kimi plugin を再インストールしてください")})

	mcpStatus := doctorStatusPass
	mcpMessage := Localize("native Kimi plugin declares the traceary MCP server", "native Kimi plugin は traceary MCP server を宣言しています")
	mcpHint := ""
	if !state.PluginMCP {
		if state.UserMCP {
			mcpMessage = Localize("traceary MCP server is registered in mcp.json (plugin does not declare one)", "traceary MCP server は mcp.json に登録されています (plugin 側の宣言はありません)")
		} else {
			mcpStatus = doctorStatusWarn
			mcpMessage = Localize("no traceary MCP server registration found in the Kimi plugin or mcp.json", "Kimi plugin と mcp.json のどちらにも traceary MCP server の登録がありません")
			mcpHint = "reinstall the native Traceary Kimi plugin"
		}
	}
	checks = append(checks, doctorCheck{Name: "kimi-mcp", Status: mcpStatus, Message: mcpMessage, Hint: mcpHint})

	skillStatus := doctorStatusPass
	skillMessage := Localize("native Kimi plugin exposes all three Traceary skills", "native Kimi plugin は Traceary skill を3件すべて公開しています")
	if state.Skills != 3 {
		skillStatus = doctorStatusWarn
		skillMessage = localizef("native Kimi plugin exposes %d Traceary skills; expected 3", "native Kimi plugin の Traceary skill は %d 件です。3件必要です", state.Skills)
	}
	checks = append(checks, doctorCheck{Name: "kimi-skills", Status: skillStatus, Message: skillMessage, Hint: Localize("reinstall the native Traceary Kimi plugin", "native Traceary Kimi plugin を再インストールしてください")})
	return checks
}
