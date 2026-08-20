package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

const (
	grokTracearyPluginName     = "traceary-grok"
	legacyTracearyPluginName   = "traceary"
	grokPluginPathClassNative  = "grok-plugin"
	grokPluginPathClassClaude  = "claude-plugin"
	grokPluginPathClassUnknown = "unknown"
)

// releaseTracearyVersionPattern matches released Traceary versions so
// plugin/binary parity checks can skip development builds. Shared by the
// host plugin checks (grok, kimi).
var releaseTracearyVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

type grokDoctorState struct {
	CLIAvailable    bool
	HostVersion     string
	PluginInstalled bool
	PluginEnabled   bool
	PluginVersion   string
	PluginPath      string
	// PluginSource is the `grok plugin list` Source for the canonical plugin.
	// PluginSourceMissing is true when that Source is a local filesystem path
	// that does not exist on disk; remote git URLs are never "missing".
	PluginSource         string
	PluginSourceMissing  bool
	ResolvedPathClass    string
	LegacyPluginDetected bool
	LocalRepoConflict    bool
	ProjectTrusted       bool
	ProjectHooks         bool
	// UserHooks is true when the user-level Traceary Grok hook file exists as a
	// regular file (primary), or inspect reports source.type=user for a Traceary
	// Grok hook target. Grok merges all hook sources, so this route can fire
	// alongside the native plugin.
	UserHooks     bool
	UserHooksPath string
	// UserHooksInvalid is true when the user-level file exists but is unreadable
	// or not valid JSON. Diagnosis only; doctor does not rewrite the file.
	UserHooksInvalid bool
	// NativeHooksPresent is true when inspect dispatches a plugin-source
	// (source.type=plugin) traceary-grok hook with a native path class. Grok
	// still merges that route even when coverage is incomplete/stale, so
	// duplicate-route detection must use presence rather than verified
	// coverage. A plugin that is merely installed/listed does not count: Grok
	// can list plugin hooks while dispatching only user-level routes.
	NativeHooksPresent bool
	// NativeHooks is true only when the dispatched plugin-source route also
	// passes the exact seven-event verified coverage contract. Used by
	// grok-hooks only.
	NativeHooks bool
	// PluginHookFileVerified is true when the installed plugin's own hook file
	// (PluginPath/hooks/hooks.json) passes the seven-event verified coverage
	// contract, regardless of whether Grok actually dispatches it. Listing or
	// file coverage alone never proves the host executes the plugin route.
	PluginHookFileVerified bool
	MCPServers             int
	Skills                 int
}

type grokPluginListEntry struct {
	Name    string `json:"name"`
	RepoKey string `json:"repo_key"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Source  string `json:"source"`
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
		Path     string `json:"path"`
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
		if plugin.Name == grokTracearyPluginName {
			state.PluginInstalled = true
			state.PluginVersion = plugin.Version
			state.PluginPath = plugin.Path
			state.PluginSource = plugin.Source
			if localSource, ok := grokPluginSourceLocalPath(plugin.Source); ok {
				if _, statErr := os.Stat(localSource); statErr != nil {
					state.PluginSourceMissing = true
				}
			}
		}
		if grokIsLocalRepositoryIdentity(plugin) {
			state.LocalRepoConflict = true
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
	if info, statErr := os.Stat(filepath.Join(projectDir, ".grok", "hooks", "traceary.json")); statErr == nil && info.Mode().IsRegular() {
		state.ProjectHooks = true
	}
	if userPath, resolved, pathErr := resolveHooksGlobalPath("grok"); pathErr == nil && resolved {
		state.UserHooksPath = userPath
		probeGrokUserHooksFile(&state, userPath)
	}
	for _, plugin := range document.Plugins {
		if plugin.Name != grokTracearyPluginName {
			continue
		}
		state.PluginEnabled = plugin.Enabled
		state.MCPServers = plugin.Provides.MCPServers
		state.Skills = plugin.Provides.Skills
	}
	// The installed plugin's own hook file is verified independently of the
	// inspect dispatch list: a listed/enabled plugin whose hooks Grok never
	// dispatches must not read as wired capture.
	if state.PluginInstalled && strings.TrimSpace(state.PluginPath) != "" {
		state.PluginHookFileVerified = grokHookFileHasVerifiedCoverage(filepath.Join(state.PluginPath, "hooks", "hooks.json"))
	}
	for _, hook := range document.Hooks {
		switch hook.Source.Type {
		case "project":
			state.ProjectHooks = true
		case "user":
			if grokIsTracearyUserHookTarget(hook.Target, state.UserHooksPath) {
				state.UserHooks = true
				if state.UserHooksPath == "" && strings.TrimSpace(hook.Target) != "" {
					state.UserHooksPath = hook.Target
				}
			}
		}
		pathClass := grokPluginPathClass(hook.Target)
		if hook.Source.PluginName == grokTracearyPluginName || (hook.Source.PluginName == legacyTracearyPluginName && state.ResolvedPathClass == "") {
			state.ResolvedPathClass = pathClass
		}
		if hook.Source.PluginName == legacyTracearyPluginName {
			state.LegacyPluginDetected = true
		}
		if hook.Source.Type == "plugin" && hook.Source.PluginName == grokTracearyPluginName && pathClass == grokPluginPathClassNative {
			state.NativeHooksPresent = true
			if grokHookFileHasVerifiedCoverage(hook.Target) {
				state.NativeHooks = true
			}
		}
	}
	return state, nil
}

// probeGrokUserHooksFile records whether the resolved user-level Traceary Grok
// hook file exists and whether it is readable JSON. Diagnosis only.
func probeGrokUserHooksFile(state *grokDoctorState, userPath string) {
	info, err := os.Stat(userPath)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	state.UserHooks = true
	data, readErr := os.ReadFile(userPath) // #nosec G304 -- resolved user hook path
	if readErr != nil || !json.Valid(data) {
		state.UserHooksInvalid = true
	}
}

// grokIsTracearyUserHookTarget reports whether an inspect hook target looks like
// the user-level Traceary Grok hook file (not an unrelated user hook).
func grokIsTracearyUserHookTarget(target, knownUserPath string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(target))
	if normalized == "" {
		return false
	}
	if knownUserPath != "" && filepath.Clean(target) == filepath.Clean(knownUserPath) {
		return true
	}
	return strings.HasSuffix(normalized, "/.grok/hooks/traceary.json") ||
		strings.HasSuffix(normalized, "/.grok/hooks/traceary.json/")
}

// grokIsLocalRepositoryIdentity distinguishes Grok's repository-level local
// install identity from a package manifest name. It uses only the plugin list
// inventory fields and never reads installed plugin contents or hook payloads.
func grokIsLocalRepositoryIdentity(plugin grokPluginListEntry) bool {
	if plugin.Name == grokTracearyPluginName || !strings.HasPrefix(plugin.RepoKey, "grok-plugin-") {
		return false
	}
	normalizedSource := filepath.ToSlash(plugin.Source)
	return strings.HasSuffix(normalizedSource, "/integrations/grok-plugin")
}

// grokPluginSourceLocalPath resolves a `grok plugin list` Source value to a
// local filesystem path to stat. Remote git sources (http(s)://, any scheme
// URL, SCP-style git@) and empty values are never "missing" and return false.
// A tilde-prefixed Source is expanded against the user home; when home cannot
// be resolved the Source is left unverified rather than falsely flagged.
func grokPluginSourceLocalPath(source string) (string, bool) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "git@") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, err := userHomeDirFunc()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", false
		}
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), true
	}
	return trimmed, true
}

// grokPluginPathClass classifies only the installation host encoded in a hook
// target path. It deliberately does not read configuration or plugin payloads.
func grokPluginPathClass(path string) string {
	normalized := filepath.ToSlash(path)
	switch {
	case strings.Contains(normalized, "/.grok/plugins/"), strings.Contains(normalized, "/.grok/installed-plugins/"), strings.Contains(normalized, "/integrations/grok-plugin/"):
		return grokPluginPathClassNative
	case strings.Contains(normalized, "/.claude/plugins/"):
		return grokPluginPathClassClaude
	default:
		return grokPluginPathClassUnknown
	}
}

func grokDoctorDisplayPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "unreported"
	}
	return path
}

func grokHookFileHasVerifiedCoverage(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	type command struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type route struct {
		Matcher string    `json:"matcher"`
		Hooks   []command `json:"hooks"`
	}
	var file struct {
		Hooks map[string][]route `json:"hooks"`
	}
	if json.Unmarshal(data, &file) != nil || len(file.Hooks) != 7 {
		return false
	}
	contracts := map[string]struct {
		name   string
		action string
	}{
		"SessionStart":     {name: "traceary-session-start", action: "session-start"},
		"UserPromptSubmit": {name: "traceary-prompt", action: "user-prompt-submit"},
		"PreToolUse":       {name: "traceary-tool-pre", action: "pre-tool-use"},
		"PostToolUse":      {name: "traceary-audit", action: "post-tool-use"},
		"Stop":             {name: "traceary-stop", action: "stop"},
		"PreCompact":       {name: "traceary-compact-pre", action: "pre-compact"},
		"PostCompact":      {name: "traceary-compact-post", action: "post-compact"},
	}
	for event, contract := range contracts {
		routes, ok := file.Hooks[event]
		if !ok || len(routes) != 1 || routes[0].Matcher != "" || len(routes[0].Hooks) != 1 {
			return false
		}
		got := routes[0].Hooks[0]
		wantCommand := `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "` + contract.action + `"`
		if got.Name != contract.name || got.Type != "command" || got.Command != wantCommand || got.Timeout != 10 {
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
		message := Localize("native Traceary Grok plugin traceary-grok is not installed", "native Traceary Grok plugin traceary-grok がインストールされていません")
		hint := Localize("install the native plugin with scripts/install-grok-plugin.sh", "scripts/install-grok-plugin.sh で native plugin をインストールしてください")
		if state.LegacyPluginDetected {
			message = Localize("legacy Traceary Grok plugin traceary is resolved but canonical traceary-grok is not installed", "legacy Traceary Grok plugin traceary が解決されていますが canonical traceary-grok はインストールされていません")
		}
		if state.LocalRepoConflict {
			message = Localize("a local-repository Grok identity conflicts with canonical traceary-grok", "ローカルリポジトリ由来の Grok identity が canonical traceary-grok と競合しています")
			hint = "scripts/install-grok-plugin.sh --migrate-local-repo-identity"
		}
		checks = append(checks, doctorCheck{Name: "grok-plugin", Status: doctorStatusWarn, Message: message, Hint: hint})
	} else {
		pluginStatus := doctorStatusPass
		pluginMessage := localizef("native Traceary Grok plugin traceary-grok %s is installed and enabled", "native Traceary Grok plugin traceary-grok %s はインストール済みで有効です", state.PluginVersion)
		pluginHint := ""
		if state.PluginSourceMissing {
			pluginStatus = doctorStatusFail
			pluginMessage = localizef("native Traceary Grok plugin source %s is a local path that does not exist on disk; the cached version %s does not prove the plugin can load", "native Traceary Grok plugin の source %s はローカルパスですがディスク上に存在しません。cache 上の version %s は plugin を読み込めることの証明になりません", grokDoctorDisplayPath(state.PluginSource), state.PluginVersion)
			pluginHint = Localize("reinstall the native plugin with scripts/install-grok-plugin.sh", "scripts/install-grok-plugin.sh で native plugin を再インストールしてください")
		} else if !state.PluginEnabled || state.LegacyPluginDetected || state.LocalRepoConflict {
			pluginStatus, pluginMessage = doctorStatusWarn, Localize("native Traceary Grok plugin traceary-grok is installed but a legacy traceary route is also resolved, or the canonical route is disabled", "native Traceary Grok plugin traceary-grok はインストール済みですが legacy traceary route も解決されているか、canonical route が無効です")
			pluginHint = "grok plugin enable traceary-grok"
			if state.LocalRepoConflict {
				pluginMessage = Localize("a local-repository Grok identity conflicts with canonical traceary-grok", "ローカルリポジトリ由来の Grok identity が canonical traceary-grok と競合しています")
				pluginHint = "scripts/install-grok-plugin.sh --migrate-local-repo-identity"
			}
		} else if releaseTracearyVersionPattern.MatchString(tracearyVersion) && strings.TrimPrefix(state.PluginVersion, "v") != strings.TrimPrefix(tracearyVersion, "v") {
			pluginStatus = doctorStatusWarn
			pluginMessage = localizef("native Traceary Grok plugin version %s does not match Traceary %s", "native Traceary Grok plugin version %s は Traceary %s と一致しません", state.PluginVersion, tracearyVersion)
			pluginHint = "grok plugin update traceary-grok"
		}
		checks = append(checks, doctorCheck{Name: "grok-plugin", Status: pluginStatus, Message: pluginMessage, Hint: pluginHint})
	}
	resolutionStatus := doctorStatusPass
	resolutionMessage := localizef("native plugin installed path: %s; resolved path class: %s", "native plugin のインストール path: %s、解決された path class: %s", grokDoctorDisplayPath(state.PluginPath), state.ResolvedPathClass)
	resolutionHint := ""
	if state.LocalRepoConflict {
		resolutionStatus = doctorStatusWarn
		resolutionMessage = Localize("Grok has a local-repository identity from the plugin subdirectory; canonical traceary-grok cannot converge until it is explicitly migrated", "Grok は plugin subdirectory 由来のローカルリポジトリ identity を持っています。明示的に移行するまで canonical traceary-grok へ収束できません")
		resolutionHint = "scripts/install-grok-plugin.sh --migrate-local-repo-identity"
	} else if state.ResolvedPathClass != grokPluginPathClassNative || state.LegacyPluginDetected {
		resolutionStatus = doctorStatusWarn
		resolutionMessage = localizef("native plugin installed path: %s; resolved path class: %s; same-name legacy traceary may be shadowed by another host", "native plugin のインストール path: %s、解決された path class: %s。同名の legacy traceary が別 host により shadow されている可能性があります", grokDoctorDisplayPath(state.PluginPath), state.ResolvedPathClass)
		resolutionHint = "scripts/install-grok-plugin.sh"
	}
	checks = append(checks, doctorCheck{Name: "grok-plugin-resolution", Status: resolutionStatus, Message: resolutionMessage, Hint: resolutionHint})
	trustStatus := doctorStatusPass
	trustMessage := Localize("Grok project hooks are trusted or no project hook route is configured", "Grok project hook は信頼済み、または project hook route は未設定です")
	if state.ProjectHooks && !state.ProjectTrusted {
		trustStatus = doctorStatusWarn
		trustMessage = Localize("Grok project hooks are configured but the project is not trusted", "Grok project hook は設定されていますが project が信頼されていません")
	}
	checks = append(checks, doctorCheck{Name: "grok-hook-trust", Status: trustStatus, Message: trustMessage, Hint: Localize("use Grok /hooks-trust for this project when project hooks are intended", "project hook を使用する場合は Grok の /hooks-trust で信頼してください")})
	hookStatus := doctorStatusPass
	hookMessage := Localize("native Grok hooks cover all seven verified events", "native Grok hook は検証済み7 eventをすべてカバーしています")
	hookHint := Localize("update or reinstall the native Traceary Grok plugin", "native Traceary Grok plugin を更新または再インストールしてください")
	switch {
	case state.NativeHooks:
		// PASS: a dispatched plugin-source route with verified coverage.
	case !state.NativeHooksPresent && (state.PluginHookFileVerified || (state.PluginInstalled && state.PluginEnabled)):
		// The plugin is installed/listed (and its own hook file may satisfy the
		// seven-event contract), but inspect dispatches no plugin-source
		// traceary-grok hook — only user-level or other sources. Listing or
		// file coverage is not capture.
		hookStatus = doctorStatusWarn
		hookMessage = Localize("native Traceary Grok plugin hooks are installed but Grok does not dispatch them: inspect lists only user-level hook sources, so no plugin hook route is active", "native Traceary Grok plugin の hook はインストールされていますが Grok が dispatch していません。inspect では user-level の hook source のみが列挙され、plugin の hook 経路は有効ではありません")
		hookHint = Localize("run grok inspect --json to confirm which hook sources Grok dispatches; a listed plugin is not necessarily an active hook route", "grok inspect --json で Grok が dispatch している hook source を確認してください。一覧にある plugin が有効な hook 経路とは限りません")
	case !state.NativeHooks:
		hookStatus, hookMessage = doctorStatusWarn, Localize("native Grok hook coverage is missing or incomplete", "native Grok hook coverage が不足しています")
	}
	checks = append(checks, doctorCheck{Name: "grok-hooks", Status: hookStatus, Message: hookMessage, Hint: hookHint})
	checks = append(checks, buildGrokUserHooksCheck(state), buildGrokHookRoutesSummary(state))
	skillStatus := doctorStatusPass
	skillMessage := Localize("native Grok plugin exposes all four Traceary skills", "native Grok plugin は Traceary skill を4件すべて公開しています")
	if state.Skills != 4 {
		skillStatus = doctorStatusWarn
		skillMessage = localizef("native Grok plugin exposes %d Traceary skills; expected 4", "native Grok plugin の Traceary skill は %d 件です。4件必要です", state.Skills)
	}
	checks = append(checks, doctorCheck{Name: "grok-skills", Status: skillStatus, Message: skillMessage, Hint: Localize("update or reinstall the native Traceary Grok plugin", "native Traceary Grok plugin を更新または再インストールしてください")})
	return checks
}

// grokUserHooksDisplayPath prefers a home-relative tilde form so doctor output
// does not leak host-private temporary path prefixes while still naming the
// canonical user route (~/.grok/hooks/traceary.json).
func grokUserHooksDisplayPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "~/.grok/hooks/traceary.json"
	}
	home, err := userHomeDirFunc()
	if err == nil && strings.TrimSpace(home) != "" {
		home = filepath.Clean(home)
		cleaned := filepath.Clean(path)
		if cleaned == home || strings.HasPrefix(cleaned, home+string(os.PathSeparator)) {
			rel, relErr := filepath.Rel(home, cleaned)
			if relErr == nil {
				return "~/" + filepath.ToSlash(rel)
			}
		}
	}
	return path
}

// buildGrokUserHooksCheck reports the optional user-level hook route. Absent is
// SKIP (optional when the native plugin is used); present is PASS; invalid is
// FAIL because Grok cannot load a malformed hooks document from that path.
func buildGrokUserHooksCheck(state grokDoctorState) doctorCheck {
	displayPath := grokUserHooksDisplayPath(state.UserHooksPath)
	if !state.UserHooks {
		return doctorCheck{
			Name:   "grok-hooks-user",
			Status: doctorStatusSkip,
			Message: localizef(
				"no user-level Grok Traceary hooks at %s (optional when the native plugin or project route is active)",
				"user-level の Grok Traceary hooks (%s) はありません（native plugin または project route が有効なら任意です）",
				displayPath,
			),
		}
	}
	if state.UserHooksInvalid {
		return doctorCheck{
			Name:   "grok-hooks-user",
			Status: doctorStatusFail,
			Message: localizef(
				"invalid user-level Grok Traceary hooks at %s (unreadable or not valid JSON). Fix or remove the file; doctor does not rewrite it",
				"user-level の Grok Traceary hooks (%s) が不正です（読み取れないか有効な JSON ではありません）。修正または削除してください。doctor はファイルを書き換えません",
				displayPath,
			),
			Hint: Localize(
				"remove or fix ~/.grok/hooks/traceary.json; prefer the native plugin traceary-grok",
				"~/.grok/hooks/traceary.json を削除または修正してください。native plugin traceary-grok を優先してください",
			),
		}
	}
	return doctorCheck{
		Name:   "grok-hooks-user",
		Status: doctorStatusPass,
		Message: localizef(
			"user-level Grok Traceary hooks are present at %s",
			"user-level の Grok Traceary hooks が存在します: %s",
			displayPath,
		),
	}
}

// buildGrokHookRoutesSummary warns when more than one Grok hook route is active.
// Grok merges every source, so user-level leftovers next to the native plugin
// (or project route) can fire duplicate handlers — the same class of problem as
// Antigravity multi-route setups. Native route presence (not verified coverage)
// counts, because a stale/partial plugin file is still executed by Grok.
// Diagnosis only; no files are removed.
func buildGrokHookRoutesSummary(state grokDoctorState) doctorCheck {
	active := make([]string, 0, 3)
	if state.NativeHooksPresent {
		active = append(active, "native plugin")
	}
	if state.ProjectHooks {
		active = append(active, "project")
	}
	if state.UserHooks {
		active = append(active, "user-level")
	}
	displayPath := grokUserHooksDisplayPath(state.UserHooksPath)
	if len(active) > 1 {
		return doctorCheck{
			Name:   "grok-hooks-routes",
			Status: doctorStatusWarn,
			Message: localizef(
				"multiple Grok hook routes are active and can register duplicate lifecycle handlers: %s. Retain exactly one route",
				"複数の Grok hook 経路が有効で lifecycle handler が重複登録される可能性があります: %s。経路を 1 つだけ残してください",
				strings.Join(active, ", "),
			),
			Hint: localizef(
				"Retain exactly one Grok hook route. Prefer the native plugin traceary-grok; remove %s if the plugin is installed. Do not copy plugin hooks into the user or project route",
				"Grok hook 経路は 1 つだけ残してください。native plugin traceary-grok を優先し、plugin を導入済みなら %s を削除してください。plugin hooks を user または project route にコピーしないでください",
				displayPath,
			),
		}
	}
	if len(active) == 1 {
		return doctorCheck{
			Name:   "grok-hooks-routes",
			Status: doctorStatusPass,
			Message: localizef(
				"Grok hooks are active via a single route: %s",
				"Grok hooks は単一の経路で有効です: %s",
				active[0],
			),
		}
	}
	return doctorCheck{
		Name:   "grok-hooks-routes",
		Status: doctorStatusSkip,
		Message: Localize(
			"no Grok hook route is active yet (see grok-hooks / grok-hooks-user)",
			"有効な Grok hook 経路はまだありません（grok-hooks / grok-hooks-user を参照）",
		),
	}
}
