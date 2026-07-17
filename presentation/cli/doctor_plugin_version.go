package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/duck8823/traceary/application/marketplace"
)

type doctorPluginInstall struct {
	Client           string
	ManifestPath     string
	InstalledVersion string
	UpdateHint       string
}

var (
	doctorVersionPrefixPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}(?:[-+][0-9A-Za-z.-]+)?`)
	doctorPseudoVersionPattern = regexp.MustCompile(`-0\.\d{14}-[a-f0-9]{12,}$`)
)

func (c *RootCLI) inspectPluginVersionChecks(currentVersion string) []doctorCheck {
	checks := []doctorCheck{}
	for _, install := range c.detectPluginInstalls() {
		checks = append(checks, inspectPluginVersion(install, currentVersion))
	}
	return coalesceAntigravityPluginVersionChecks(checks, currentVersion)
}

func (c *RootCLI) detectPluginInstalls() []doctorPluginInstall {
	home, err := userHomeDirFunc()
	if err != nil {
		return nil
	}
	installs := []doctorPluginInstall{}
	if detection := c.detectClaudeTracearyPluginForCLI(); detection.Active {
		if status := c.pluginCacheStatusForDetection(home, detection.PluginKey); status.CachedVersion != "" {
			installs = append(installs, doctorPluginInstall{Client: "claude", ManifestPath: fmt.Sprintf("claude plugin cache %s", detection.PluginKey), InstalledVersion: status.CachedVersion, UpdateHint: "claude plugins update " + detection.PluginKey})
		} else if status.MarketplacePath != "" {
			installs = append(installs, doctorPluginInstall{Client: "claude", ManifestPath: status.MarketplacePath, UpdateHint: "claude plugins update " + detection.PluginKey})
		}
	}
	installs = append(installs, detectManifestInstalls(
		filepath.Join(home, ".codex", "plugins", "cache", "*", "traceary", "*", ".codex-plugin", "plugin.json"),
		"codex",
		"reinstall the Traceary Codex plugin from a matching release tag (see docs/release/post-upgrade-plugins.md)",
	)...)
	// Antigravity can materialize the packaged plugin under either the CLI
	// import root or the shared Gemini config plugins root. Prefer whichever
	// copy is present; when both exist, doctor reports each install so a
	// stale partial copy under antigravity-cli is still visible, then
	// coalesceAntigravityPluginVersionChecks softens a missing-version twin
	// when the other path already matches the running binary.
	agyHint := "cd <traceary-repository> && agy plugin install integrations/antigravity-plugin"
	installs = append(installs, detectManifestInstalls(filepath.Join(home, ".gemini", "config", "plugins", "traceary", "plugin.json"), "antigravity", agyHint)...)
	installs = append(installs, detectManifestInstalls(filepath.Join(home, ".gemini", "antigravity-cli", "plugins", "traceary", "plugin.json"), "antigravity", agyHint)...)
	installs = append(installs, detectManifestInstalls(filepath.Join(home, ".gemini", "extensions", "traceary", "gemini-extension.json"), "gemini", "gemini extensions update traceary")...)
	installs = append(installs, detectManifestInstalls(
		filepath.Join(home, ".grok", "plugins", "traceary", "plugin.json"),
		"grok",
		"./scripts/install-grok-plugin.sh  # from a matching release tag checkout",
	)...)
	return installs
}

// coalesceAntigravityPluginVersionChecks avoids permanent WARN noise when one
// Antigravity install path is healthy/matching while a twin path is incomplete
// (missing version) or only a leftover shell of an old import.
func coalesceAntigravityPluginVersionChecks(checks []doctorCheck, currentVersion string) []doctorCheck {
	current := normalizeDoctorVersion(currentVersion)
	if current == "" || isDevBuild(currentVersion) {
		return checks
	}
	hasMatching := false
	for _, check := range checks {
		if check.Name != "antigravity-plugin-version" {
			continue
		}
		if check.Status == doctorStatusPass {
			hasMatching = true
			break
		}
	}
	if !hasMatching {
		return checks
	}
	out := make([]doctorCheck, 0, len(checks))
	for _, check := range checks {
		if check.Name == "antigravity-plugin-version" &&
			check.Status == doctorStatusWarn &&
			(strings.Contains(check.Message, "has no version") || strings.Contains(check.Message, "version がありません")) {
			check.Status = doctorStatusSkip
			check.Hint = Localize(
				"another Antigravity plugin path already matches the running binary; remove or reinstall this incomplete install if it is unused",
				"別の Antigravity plugin path が実行中 binary と一致しています。未使用ならこの不完全な install を削除するか再インストールしてください",
			)
			check.FixCommand = ""
			check.Message = localizef(
				"antigravity plugin path is incomplete but a healthy install already matches Traceary %s; skipped permanent WARN",
				"antigravity plugin path は不完全ですが、健全な install が既に Traceary %s と一致しているため恒久 WARN を skip しました",
				current,
			)
		}
		out = append(out, check)
	}
	return out
}

func (c *RootCLI) pluginCacheStatusForDetection(home, pluginKey string) pluginVersionCacheStatus {
	if c.pluginCacheInspector == nil || pluginKey == "" {
		return pluginVersionCacheStatus{}
	}
	status := c.pluginCacheInspector.DetectClaudePluginCacheStatus(home, pluginKey)
	return pluginVersionCacheStatus{CachedVersion: status.CachedVersion, MarketplacePath: status.MarketplacePath}
}

type pluginVersionCacheStatus struct {
	CachedVersion   string
	MarketplacePath string
}

func detectManifestInstalls(pattern, client, hint string) []doctorPluginInstall {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	installs := make([]doctorPluginInstall, 0, len(matches))
	for _, match := range matches {
		installs = append(installs, doctorPluginInstall{Client: client, ManifestPath: match, UpdateHint: hint})
	}
	return installs
}

func inspectPluginVersion(install doctorPluginInstall, currentVersion string) doctorCheck {
	name := install.Client + "-plugin-version"
	current := normalizeDoctorVersion(currentVersion)
	if current == "" {
		return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("running traceary version is %q; skipped plugin version comparison for %s", "実行中 traceary version は %q のため %s plugin version 比較を skip しました", currentVersion, install.Client)}
	}
	installedVersion := ""
	if install.InstalledVersion != "" {
		installedVersion = strings.TrimSpace(install.InstalledVersion)
	} else {
		version, err := marketplace.ReadManifestVersion(install.ManifestPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("%s plugin manifest is absent; skipped version comparison: %s", "%s plugin manifest がないため version 比較を skip しました: %s", install.Client, install.ManifestPath)}
			}
			return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to read %s plugin manifest version: %s (%v)", "%s plugin manifest version の読み込みに失敗しました: %s (%v)", install.Client, install.ManifestPath, err)}
		}
		installedVersion = version
	}
	installed := normalizeDoctorVersion(installedVersion)
	if installed == "" {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s plugin manifest has no version: %s", "%s plugin manifest に version がありません: %s", install.Client, install.ManifestPath), Hint: "reinstall plugin to align", FixCommand: install.UpdateHint}
	}
	if isDevBuild(currentVersion) {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusPass,
			Message: localizef(
				"%s plugin version comparison soft-passed because traceary is running a dev build: %s (plugin %s at %s)",
				"traceary が dev build として実行中のため %s plugin version 比較を soft-pass しました: %s (plugin %s at %s)",
				install.Client, currentVersion, installedVersion, install.ManifestPath,
			),
			Hint: "running a dev build (rebuild + reinstall plugin to verify version alignment)",
		}
	}
	if installed != current {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s plugin version %s does not match running traceary version %s (%s)", "%s plugin version %s は実行中 traceary version %s と一致しません (%s)", install.Client, installedVersion, currentVersion, install.ManifestPath), Hint: "reinstall plugin to align", FixCommand: install.UpdateHint}
	}
	return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("%s plugin version matches running traceary version %s (%s)", "%s plugin version は実行中 traceary version %s と一致しています (%s)", install.Client, currentVersion, install.ManifestPath)}
}

func normalizeDoctorVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return ""
	}
	if match := doctorVersionPrefixPattern.FindString(version); match != "" {
		return match
	}
	if index := strings.IndexAny(version, " \t\r\n("); index >= 0 {
		return version[:index]
	}
	return version
}

func isDevBuild(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	lower := strings.ToLower(version)
	if lower == "dev" || strings.Contains(lower, "devel") || strings.Contains(lower, "dirty") {
		return true
	}
	return doctorPseudoVersionPattern.MatchString(normalizeDoctorVersion(version))
}
