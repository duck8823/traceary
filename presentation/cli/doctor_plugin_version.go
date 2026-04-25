package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/duck8823/traceary/application/marketplace"
)

type doctorPluginInstall struct {
	Client           string
	ManifestPath     string
	InstalledVersion string
	UpdateHint       string
}

func (c *RootCLI) inspectPluginVersionChecks(currentVersion string) []doctorCheck {
	checks := []doctorCheck{}
	for _, install := range c.detectPluginInstalls() {
		checks = append(checks, inspectPluginVersion(install, currentVersion))
	}
	return checks
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
	installs = append(installs, detectManifestInstalls(filepath.Join(home, ".codex", "plugins", "cache", "*", "traceary", "*", ".codex-plugin", "plugin.json"), "codex", "reinstall plugin to align")...)
	installs = append(installs, detectManifestInstalls(filepath.Join(home, ".gemini", "extensions", "traceary", "gemini-extension.json"), "gemini", "gemini extensions update traceary")...)
	return installs
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
	if current == "" || current == "dev" {
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
	if installed != current {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s plugin version %s does not match running traceary version %s (%s)", "%s plugin version %s は実行中 traceary version %s と一致しません (%s)", install.Client, installedVersion, currentVersion, install.ManifestPath), Hint: "reinstall plugin to align", FixCommand: install.UpdateHint}
	}
	return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("%s plugin version matches running traceary version %s (%s)", "%s plugin version は実行中 traceary version %s と一致しています (%s)", install.Client, currentVersion, install.ManifestPath)}
}

func normalizeDoctorVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}
