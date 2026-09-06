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
	museDoctorLookPath = exec.LookPath
	museDoctorOutput   = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "muse", args...).Output()
	}
)

// museExpectedHooks mirrors the eight wired actions in
// integrations/muse-plugin/hooks/hooks.json.
var museExpectedHooks = []struct {
	event  string
	action string
}{
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"},
	{"PostToolUse", "post-tool-use"},
	{"PostToolUseFailure", "post-tool-use-failure"},
	{"Stop", "stop"},
	{"PreCompact", "pre-compact"},
	{"PostCompact", "post-compact"},
}

var museExpectedSkills = []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history", "traceary-session-refine"}

type museDoctorState struct {
	CLIAvailable      bool
	HostVersion       string
	PluginInstalled   bool
	PluginEnabled     bool
	PluginRecordKnown bool
	PluginListUnknown bool
	PluginVersion     string
	NativeHooks       bool
	Skills            int
}

func museProbeError(operation string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return xerrors.Errorf("%s: %s", operation, pathErr.Err)
	}
	return xerrors.Errorf("%s: %v", operation, err)
}

func museExpectedHookCommand(action string) string {
	return `"${MUSE_PLUGIN_ROOT}/scripts/traceary-muse.sh" "` + action + `"`
}

// probeMuseDoctorState inspects Muse Code via LookPath, `muse --version`, and
// `muse plugins list --json`. Only the empty {"plugins":[]} list shape is
// evidenced (#2350 §4); unrecognized shapes are unknown-tolerant WARNs.
func probeMuseDoctorState(ctx context.Context, _ string) (museDoctorState, error) {
	state := museDoctorState{}
	if _, err := museDoctorLookPath("muse"); err != nil {
		return state, nil
	}
	state.CLIAvailable = true
	versionOutput, err := museDoctorOutput(ctx, "--version")
	if err != nil {
		return state, museProbeError("failed to read Muse Code version", err)
	}
	state.HostVersion = strings.TrimSpace(string(versionOutput))

	listOutput, err := museDoctorOutput(ctx, "plugins", "list", "--json")
	if err != nil {
		return state, museProbeError("failed to list Muse plugins", err)
	}
	if err := applyMusePluginsList(&state, listOutput); err != nil {
		return state, err
	}
	return state, nil
}

func applyMusePluginsList(state *museDoctorState, listOutput []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(listOutput, &envelope); err != nil {
		return museProbeError("failed to parse Muse plugins list", err)
	}
	rawPlugins, ok := envelope["plugins"]
	if !ok {
		state.PluginListUnknown = true
		state.PluginRecordKnown = false
		return nil
	}
	var plugins []map[string]any
	if err := json.Unmarshal(rawPlugins, &plugins); err != nil {
		state.PluginListUnknown = true
		state.PluginRecordKnown = false
		return nil
	}
	state.PluginRecordKnown = true
	if len(plugins) == 0 {
		return nil
	}
	for _, entry := range plugins {
		if !musePluginEntryIsTraceary(entry) {
			continue
		}
		state.PluginInstalled = true
		state.PluginEnabled = musePluginEntryEnabled(entry)
		if root := musePluginEntryRoot(entry); root != "" {
			applyMusePluginRoot(state, root)
		} else {
			state.NativeHooks = true
		}
		if state.PluginVersion == "" {
			state.PluginVersion = musePluginEntryField(entry, "version")
		}
		return nil
	}
	return nil
}

func musePluginNestedMaps(entry map[string]any) []map[string]any {
	var nested []map[string]any
	for _, key := range []string{"record", "plugin"} {
		if nestedMap, ok := entry[key].(map[string]any); ok && len(nestedMap) > 0 {
			nested = append(nested, nestedMap)
		}
	}
	return nested
}

// musePluginEntryField reads a string field from the entry top level first,
// then from the record/plugin nested objects that current Muse Code
// `plugins list --json` output uses (#2362).
func musePluginEntryField(entry map[string]any, key string) string {
	if value, _ := entry[key].(string); strings.TrimSpace(value) != "" {
		return value
	}
	for _, nested := range musePluginNestedMaps(entry) {
		if value, _ := nested[key].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func musePluginEntryIsTraceary(entry map[string]any) bool {
	for _, key := range []string{"id", "name", "display_name", "plugin", "pluginId"} {
		lower := strings.ToLower(strings.TrimSpace(musePluginEntryField(entry, key)))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "traceary") || lower == "muse-plugin" {
			return true
		}
	}
	return false
}

func musePluginEntryEnabled(entry map[string]any) bool {
	if enabled, ok := entry["enabled"].(bool); ok {
		return enabled
	}
	for _, nested := range musePluginNestedMaps(entry) {
		if enabled, ok := nested["enabled"].(bool); ok {
			return enabled
		}
	}
	return true
}

func musePluginEntryRoot(entry map[string]any) string {
	for _, key := range []string{"root", "path", "installPath"} {
		value, _ := entry[key].(string)
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	for _, nested := range musePluginNestedMaps(entry) {
		if value, _ := nested["cache_path"].(string); strings.TrimSpace(value) != "" {
			return value
		}
		if source, ok := nested["source"].(map[string]any); ok {
			if value, _ := source["path"].(string); strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func applyMusePluginRoot(state *museDoctorState, root string) {
	manifestPath := filepath.Join(root, "plugin.json")
	if data, err := os.ReadFile(manifestPath); err == nil { // #nosec G304 -- host-reported plugin root
		var manifest struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			state.PluginVersion = manifest.Version
		}
	}
	hooksPath := filepath.Join(root, "hooks", "hooks.json")
	if data, err := os.ReadFile(hooksPath); err == nil { // #nosec G304 -- host-reported plugin root
		state.NativeHooks = museHooksFileMatchesExpected(data)
	}
	state.Skills = countMusePluginSkills(root)
}

func museHooksFileMatchesExpected(data []byte) bool {
	var file struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &file) != nil {
		return false
	}
	for _, want := range museExpectedHooks {
		groups := file.Hooks[want.event]
		matched := false
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if hook.Command == museExpectedHookCommand(want.action) && hook.Timeout == 10 {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func countMusePluginSkills(root string) int {
	count := 0
	for _, skill := range museExpectedSkills {
		if info, err := os.Stat(filepath.Join(root, "skills", skill, "SKILL.md")); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	return count
}

func buildMuseDoctorChecks(state museDoctorState, tracearyVersion string) []doctorCheck {
	if !state.CLIAvailable {
		return []doctorCheck{{
			Name:    "muse-cli",
			Status:  doctorStatusFail,
			Message: Localize("Muse Code CLI is not installed", "Muse Code CLI がインストールされていません"),
			Hint:    Localize("install Muse Code, then rerun doctor", "Muse Code をインストールして doctor を再実行してください"),
		}}
	}
	checks := []doctorCheck{{
		Name:    "muse-cli",
		Status:  doctorStatusPass,
		Message: localizef("detected Muse Code CLI %s", "Muse Code CLI %s を検出しました", state.HostVersion),
	}}
	if state.PluginListUnknown {
		checks = append(checks, doctorCheck{
			Name:    "muse-plugin",
			Status:  doctorStatusWarn,
			Message: Localize("Muse plugins list JSON shape is unrecognized; only {\"plugins\":[]} was evidenced in #2350 §4", "Muse plugins list の JSON 形が未知です。#2350 §4 で確認済みなのは {\"plugins\":[]} のみです"),
			Hint:    Localize("rerun `muse plugins list --json` after installing the native Traceary Muse plugin", "native Traceary Muse plugin を導入したうえで `muse plugins list --json` を再実行してください"),
		})
		return checks
	}
	if !state.PluginInstalled {
		checks = append(checks, doctorCheck{
			Name:    "muse-plugin",
			Status:  doctorStatusWarn,
			Message: Localize("native Traceary Muse plugin is not installed", "native Traceary Muse plugin がインストールされていません"),
			Hint:    Localize("install integrations/muse-plugin via Muse plugins, then rerun doctor", "integrations/muse-plugin を Muse plugins 経由でインストールして doctor を再実行してください"),
		})
		return checks
	}
	pluginStatus := doctorStatusPass
	pluginMessage := localizef("native Traceary Muse plugin %s is installed and enabled", "native Traceary Muse plugin %s はインストール済みで有効です", nonemptyMusePluginVersion(state.PluginVersion))
	pluginHint := ""
	switch {
	case !state.PluginRecordKnown:
		pluginStatus = doctorStatusWarn
		pluginMessage = Localize("native Traceary Muse plugin is installed, but its activation record cannot be confirmed", "native Traceary Muse plugin はインストール済みですが、有効化の記録を確認できません")
		pluginHint = Localize("reinstall the native Traceary Muse plugin, then rerun doctor", "native Traceary Muse plugin を再インストールして doctor を再実行してください")
	case !state.PluginEnabled:
		pluginStatus = doctorStatusWarn
		pluginMessage = Localize("native Traceary Muse plugin is installed but not enabled", "native Traceary Muse plugin はインストール済みですが有効ではありません")
		pluginHint = Localize("enable the plugin in Muse, then rerun doctor", "Muse で plugin を有効化してから doctor を再実行してください")
	case releaseTracearyVersionPattern.MatchString(tracearyVersion) && state.PluginVersion != "" && strings.TrimPrefix(state.PluginVersion, "v") != strings.TrimPrefix(tracearyVersion, "v"):
		pluginStatus = doctorStatusWarn
		pluginMessage = localizef("native Traceary Muse plugin version %s does not match Traceary %s", "native Traceary Muse plugin version %s は Traceary %s と一致しません", state.PluginVersion, tracearyVersion)
		pluginHint = Localize("reinstall the native Traceary Muse plugin from a matching release", "一致する release から native Traceary Muse plugin を再インストールしてください")
	}
	checks = append(checks, doctorCheck{Name: "muse-plugin", Status: pluginStatus, Message: pluginMessage, Hint: pluginHint})

	hookStatus := doctorStatusWarn
	hookMessage := Localize("native Muse plugin hooks declare eight events; SessionEnd is not subscribed and compact/tool dispatch is unobserved, so the support matrix keeps those cells available", "native Muse plugin hook は 8 event を宣言していますが、SessionEnd は購読しておらず compact/tool dispatch は未観測です。matrix ではそれらを available とします")
	hookHint := ""
	if !state.NativeHooks {
		hookMessage = Localize("native Muse plugin hook coverage is missing or incomplete", "native Muse plugin hook coverage が不足しています")
		hookHint = Localize("reinstall the native Traceary Muse plugin", "native Traceary Muse plugin を再インストールしてください")
	}
	checks = append(checks, doctorCheck{Name: "muse-hooks", Status: hookStatus, Message: hookMessage, Hint: hookHint})

	skillStatus := doctorStatusPass
	skillMessage := Localize("native Muse plugin exposes all four Traceary skills", "native Muse plugin は Traceary skill を4件すべて公開しています")
	if state.Skills != len(museExpectedSkills) {
		skillStatus = doctorStatusWarn
		skillMessage = localizef("native Muse plugin exposes %d Traceary skills; expected %d", "native Muse plugin の Traceary skill は %d 件です。%d 件必要です", state.Skills, len(museExpectedSkills))
	}
	checks = append(checks, doctorCheck{Name: "muse-skills", Status: skillStatus, Message: skillMessage, Hint: Localize("reinstall the native Traceary Muse plugin", "native Traceary Muse plugin を再インストールしてください")})
	return checks
}

func nonemptyMusePluginVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return "unknown"
	}
	return version
}
