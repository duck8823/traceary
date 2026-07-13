package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/xerrors"
)

var (
	grokDoctorLookPath = exec.LookPath
	grokDoctorOutput   = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "grok", args...).Output()
	}
)

var grokVersionPattern = regexp.MustCompile(`grok\s+([^\s]+)`)
var grokTracearyVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

type grokDoctorState struct {
	CLIAvailable    bool
	HostVersion     string
	PluginInstalled bool
	PluginEnabled   bool
	PluginVersion   string
	ProjectTrusted  bool
	ProjectHooks    bool
	NativeHooks     bool
	MCPServers      int
	Skills          int
}

type grokPluginListEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type grokInspectDocument struct {
	ProjectTrusted bool `json:"projectTrusted"`
	Hooks          []struct {
		Target string `json:"target"`
		Source struct {
			Type       string `json:"type"`
			PluginName string `json:"plugin_name"`
		} `json:"source"`
	} `json:"hooks"`
	Plugins []struct {
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
		Provides struct {
			Skills     int `json:"skills"`
			MCPServers int `json:"mcpServers"`
		} `json:"provides"`
	} `json:"plugins"`
}

func probeGrokDoctorState(ctx context.Context, projectDir string) (grokDoctorState, error) {
	state := grokDoctorState{}
	if _, err := grokDoctorLookPath("grok"); err != nil {
		return state, nil
	}
	state.CLIAvailable = true
	versionOutput, err := grokDoctorOutput(ctx, "--version")
	if err != nil {
		return state, xerrors.Errorf("failed to read Grok version: %w", err)
	}
	if match := grokVersionPattern.FindStringSubmatch(string(versionOutput)); len(match) == 2 {
		state.HostVersion = match[1]
	}
	listOutput, err := grokDoctorOutput(ctx, "plugin", "list", "--json")
	if err != nil {
		return state, xerrors.Errorf("failed to list Grok plugins: %w", err)
	}
	var plugins []grokPluginListEntry
	if err := json.Unmarshal(listOutput, &plugins); err != nil {
		return state, xerrors.Errorf("failed to decode Grok plugin list: %w", err)
	}
	for _, plugin := range plugins {
		if plugin.Name == "traceary" {
			state.PluginInstalled = true
			state.PluginVersion = plugin.Version
			break
		}
	}
	inspectOutput, err := grokDoctorOutput(ctx, "--cwd", projectDir, "inspect", "--json")
	if err != nil {
		return state, xerrors.Errorf("failed to inspect Grok configuration: %w", err)
	}
	var document grokInspectDocument
	if err := json.Unmarshal(inspectOutput, &document); err != nil {
		return state, xerrors.Errorf("failed to decode Grok inspection: %w", err)
	}
	state.ProjectTrusted = document.ProjectTrusted
	for _, plugin := range document.Plugins {
		if plugin.Name != "traceary" {
			continue
		}
		state.PluginEnabled = plugin.Enabled
		state.MCPServers = plugin.Provides.MCPServers
		state.Skills = plugin.Provides.Skills
	}
	for _, hook := range document.Hooks {
		if hook.Source.Type == "project" {
			state.ProjectHooks = true
		}
		if hook.Source.PluginName == "traceary" && grokHookFileHasVerifiedCoverage(hook.Target) {
			state.NativeHooks = true
		}
	}
	return state, nil
}

func grokHookFileHasVerifiedCoverage(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var file struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(data, &file) != nil || len(file.Hooks) != 7 {
		return false
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		if _, ok := file.Hooks[event]; !ok {
			return false
		}
	}
	return true
}

func buildGrokDoctorChecks(state grokDoctorState, tracearyVersion string) []doctorCheck {
	if !state.CLIAvailable {
		return []doctorCheck{{Name: "grok-cli", Status: doctorStatusFail, Message: Localize("Grok CLI is not installed", "Grok CLI がインストールされていません"), Hint: Localize("install Grok Build, then rerun doctor", "Grok Build をインストールして doctor を再実行してください")}}
	}
	checks := []doctorCheck{{Name: "grok-cli", Status: doctorStatusPass, Message: localizef("detected Grok CLI %s", "Grok CLI %s を検出しました", state.HostVersion)}}
	if !state.PluginInstalled {
		checks = append(checks, doctorCheck{Name: "grok-plugin", Status: doctorStatusWarn, Message: Localize("native Traceary Grok plugin is not installed", "native Traceary Grok plugin がインストールされていません"), Hint: Localize("install the native plugin with scripts/install-grok-plugin.sh", "scripts/install-grok-plugin.sh で native plugin をインストールしてください")})
		return checks
	}
	pluginStatus := doctorStatusPass
	pluginMessage := localizef("native Traceary Grok plugin %s is installed and enabled", "native Traceary Grok plugin %s はインストール済みで有効です", state.PluginVersion)
	pluginHint := ""
	if !state.PluginEnabled {
		pluginStatus, pluginMessage = doctorStatusWarn, Localize("native Traceary Grok plugin is installed but disabled", "native Traceary Grok plugin はインストール済みですが無効です")
		pluginHint = "grok plugin enable traceary"
	} else if grokTracearyVersionPattern.MatchString(tracearyVersion) && strings.TrimPrefix(state.PluginVersion, "v") != strings.TrimPrefix(tracearyVersion, "v") {
		pluginStatus = doctorStatusWarn
		pluginMessage = localizef("native Traceary Grok plugin version %s does not match Traceary %s", "native Traceary Grok plugin version %s は Traceary %s と一致しません", state.PluginVersion, tracearyVersion)
		pluginHint = "grok plugin update traceary"
	}
	checks = append(checks, doctorCheck{Name: "grok-plugin", Status: pluginStatus, Message: pluginMessage, Hint: pluginHint})
	trustStatus := doctorStatusPass
	trustMessage := Localize("Grok project hooks are trusted or no project hook route is configured", "Grok project hook は信頼済み、または project hook route は未設定です")
	if state.ProjectHooks && !state.ProjectTrusted {
		trustStatus = doctorStatusWarn
		trustMessage = Localize("Grok project hooks are configured but the project is not trusted", "Grok project hook は設定されていますが project が信頼されていません")
	}
	checks = append(checks, doctorCheck{Name: "grok-hook-trust", Status: trustStatus, Message: trustMessage, Hint: Localize("use Grok /hooks-trust for this project when project hooks are intended", "project hook を使用する場合は Grok の /hooks-trust で信頼してください")})
	hookStatus := doctorStatusPass
	hookMessage := Localize("native Grok hooks cover all seven verified events", "native Grok hook は検証済み7 eventをすべてカバーしています")
	if !state.NativeHooks {
		hookStatus, hookMessage = doctorStatusWarn, Localize("native Grok hook coverage is missing or incomplete", "native Grok hook coverage が不足しています")
	}
	checks = append(checks, doctorCheck{Name: "grok-hooks", Status: hookStatus, Message: hookMessage, Hint: Localize("update or reinstall the native Traceary Grok plugin", "native Traceary Grok plugin を更新または再インストールしてください")})
	mcpStatus := doctorStatusPass
	mcpMessage := Localize("native Grok plugin exposes one Traceary MCP server", "native Grok plugin は Traceary MCP server を1件公開しています")
	if state.MCPServers != 1 {
		mcpStatus = doctorStatusWarn
		mcpMessage = localizef("native Grok plugin exposes %d Traceary MCP server entries; expected 1", "native Grok plugin の Traceary MCP server は %d 件です。1件必要です", state.MCPServers)
	}
	checks = append(checks, doctorCheck{Name: "grok-mcp", Status: mcpStatus, Message: mcpMessage, Hint: Localize("update or reinstall the native Traceary Grok plugin", "native Traceary Grok plugin を更新または再インストールしてください")})
	skillStatus := doctorStatusPass
	skillMessage := Localize("native Grok plugin exposes all three Traceary skills", "native Grok plugin は Traceary skill を3件すべて公開しています")
	if state.Skills != 3 {
		skillStatus = doctorStatusWarn
		skillMessage = localizef("native Grok plugin exposes %d Traceary skills; expected 3", "native Grok plugin の Traceary skill は %d 件です。3件必要です", state.Skills)
	}
	checks = append(checks, doctorCheck{Name: "grok-skills", Status: skillStatus, Message: skillMessage, Hint: Localize("update or reinstall the native Traceary Grok plugin", "native Traceary Grok plugin を更新または再インストールしてください")})
	return checks
}
