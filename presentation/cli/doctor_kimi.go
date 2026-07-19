package cli

import (
	"context"
	"encoding/json"
	"errors"
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
// installed managed copy against it, order-independently.
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

// kimiExpectedSkills are the three shared skills every host package ships.
var kimiExpectedSkills = []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history"}

type kimiDoctorState struct {
	CLIAvailable    bool
	HostVersion     string
	PluginInstalled bool
	PluginEnabled   bool
	// PluginRecordKnown is false when the install record cannot be read or
	// parsed, so the enabled state is undetermined rather than "disabled".
	PluginRecordKnown bool
	PluginVersion     string
	NativeHooks       bool
	PluginMCP         bool
	UserMCP           bool
	Skills            int
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

// kimiProbeError renders a path-free probe error. Filesystem errors carry
// the failing path inside *os.PathError, and doctor messages must never
// expose home-directory layouts.
func kimiProbeError(operation string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return xerrors.Errorf("%s: %s", operation, pathErr.Err)
	}
	return xerrors.Errorf("%s: %v", operation, err)
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
			return state, kimiProbeError("failed to read the Kimi plugin manifest", err)
		}
	} else {
		state.PluginInstalled = true
		var manifest kimiPluginManifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return state, kimiProbeError("failed to parse the Kimi plugin manifest", err)
		}
		state.PluginVersion = manifest.Version
		state.NativeHooks = kimiManifestHasVerifiedHooks(manifest)
		state.PluginMCP = kimiServerDeclaresTraceary(manifest.MCPServers)
		state.Skills = countKimiPluginSkills(kimiHome)
		state.PluginEnabled, state.PluginRecordKnown = kimiPluginRecordEnabled(kimiHome)
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
	type rule struct {
		event   string
		matcher string
		command string
	}
	counts := map[rule]int{}
	for _, want := range kimiExpectedHooks {
		counts[rule{want.event, want.matcher, "traceary hook kimi " + want.action}]++
	}
	for _, hook := range manifest.Hooks {
		key := rule{hook.Event, hook.Matcher, hook.Command}
		if _, ok := counts[key]; !ok || hook.Timeout != 5 {
			return false
		}
		counts[key]--
	}
	for _, remaining := range counts {
		if remaining != 0 {
			return false
		}
	}
	return true
}

func countKimiPluginSkills(kimiHome string) int {
	count := 0
	for _, skill := range kimiExpectedSkills {
		if info, err := os.Stat(filepath.Join(kimiHome, "plugins", "managed", "traceary", "skills", skill, "SKILL.md")); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	return count
}

// kimiPluginRecordEnabled reads the install record: enabled=true and
// state=ok. known=false means the record is missing or unreadable, so the
// enabled state cannot be determined.
func kimiPluginRecordEnabled(kimiHome string) (enabled, known bool) {
	data, err := os.ReadFile(filepath.Join(kimiHome, "plugins", "installed.json")) // #nosec G304 -- fixed path under the Kimi home
	if err != nil {
		return false, false
	}
	var record struct {
		Plugins []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
			State   string `json:"state"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &record) != nil {
		return false, false
	}
	for _, entry := range record.Plugins {
		if entry.ID == "traceary" {
			return entry.Enabled && entry.State == "ok", true
		}
	}
	return false, true
}

// kimiServerDeclaresTraceary validates a traceary MCP server declaration:
// the command must be traceary launching mcp-server.
func kimiServerDeclaresTraceary(servers map[string]struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}) bool {
	server, ok := servers["traceary"]
	if !ok {
		return false
	}
	return server.Command == "traceary" && len(server.Args) == 1 && server.Args[0] == "mcp-server"
}

// kimiMCPJSONRegisters checks the user-level and project-level mcp.json
// files for a valid traceary server declaration (the plugin-declared server
// is checked separately from the manifest).
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
			MCPServers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		if kimiServerDeclaresTraceary(doc.MCPServers) {
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
	switch {
	case !state.PluginRecordKnown:
		pluginStatus = doctorStatusWarn
		pluginMessage = Localize("native Traceary Kimi plugin is installed, but its activation record cannot be confirmed", "native Traceary Kimi plugin はインストール済みですが、有効化の記録を確認できません")
		pluginHint = Localize("reinstall the native Traceary Kimi plugin with scripts/install-kimi-plugin.sh", "scripts/install-kimi-plugin.sh で native Traceary Kimi plugin を再インストールしてください")
	case !state.PluginEnabled:
		pluginStatus = doctorStatusWarn
		pluginMessage = Localize("native Traceary Kimi plugin is installed but not enabled", "native Traceary Kimi plugin はインストール済みですが有効ではありません")
		pluginHint = Localize("enable the plugin with /plugins, or reinstall with scripts/install-kimi-plugin.sh", "/plugins で有効化するか、scripts/install-kimi-plugin.sh で再インストールしてください")
	case releaseTracearyVersionPattern.MatchString(tracearyVersion) && strings.TrimPrefix(state.PluginVersion, "v") != strings.TrimPrefix(tracearyVersion, "v"):
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
			mcpHint = Localize("reinstall the native Traceary Kimi plugin", "native Traceary Kimi plugin を再インストールしてください")
		}
	}
	checks = append(checks, doctorCheck{Name: "kimi-mcp", Status: mcpStatus, Message: mcpMessage, Hint: mcpHint})

	skillStatus := doctorStatusPass
	skillMessage := Localize("native Kimi plugin exposes all three Traceary skills", "native Kimi plugin は Traceary skill を3件すべて公開しています")
	if state.Skills != len(kimiExpectedSkills) {
		skillStatus = doctorStatusWarn
		skillMessage = localizef("native Kimi plugin exposes %d Traceary skills; expected %d", "native Kimi plugin の Traceary skill は %d 件です。%d 件必要です", state.Skills, len(kimiExpectedSkills))
	}
	checks = append(checks, doctorCheck{Name: "kimi-skills", Status: skillStatus, Message: skillMessage, Hint: Localize("reinstall the native Traceary Kimi plugin", "native Traceary Kimi plugin を再インストールしてください")})
	return checks
}
